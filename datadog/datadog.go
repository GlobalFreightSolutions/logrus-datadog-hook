package datadog

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	// The maximum content size per request is 5MB
	maxContentSize = 5 * 1024 * 1024

	// The maximum size for a single log is 256kB
	maxLogSize = 256 * 1024

	// The maximum amount of logs that can be sent in a single request is 1000
	maxLogCount = 1000

	basePath          = "/v1/input"
	datadogHostUS     = "https://http-intake.logs.datadoghq.com"
	datadogHostEU     = "https://http-intake.logs.datadoghq.eu"
	apiKeyHeader      = "DD-API-KEY"
	defaultMaxRetries = 5
)

// These options if provided will be added as default tags to each log sent
type GlobalTags struct {
	Service     string
	Environment string
	Maintainer  string
	Application string
	Hostname    string
}

type DatadogHook struct {
	ApiKey          string
	Tags            *GlobalTags
	MinLevel        logrus.Level
	MaxRetry        int
	datadogEndpoint string
	entryC          chan logrus.Entry
	errorC          chan error
	ticker          *time.Ticker
	formatter       logrus.Formatter
	wg              sync.WaitGroup
}

func getenvOrDefault(key string, defaultString string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}

	return defaultString
}

func defaultErrorhandler(err error) {
	logrus.Errorf("The datadog logger hook has encountered an error: %s", err.Error())
}

func New(apiKey *string, minLevel logrus.Level, errorHandler *func(error)) (*DatadogHook, error) {
	globalTags := &GlobalTags{
		Service:     getenvOrDefault("SERVICE", "unknown"),
		Environment: getenvOrDefault("ENVIRONMENT", "unknown"),
		Maintainer:  getenvOrDefault("MAINTAINER", "unknown"),
		Application: getenvOrDefault("APPLICATION", "unknown"),
		Hostname:    getenvOrDefault("HOST", "0.0.0.0"),
	}

	region := os.Getenv("DATADOG_REGION")

	if apiKey == nil {
		envApiKey := os.Getenv("DATADOG_API_KEY")
		apiKey = &envApiKey
	}

	if apiKey == nil || *apiKey == "" {
		return nil, errors.New("apiKey not provided, cannot create datadog hook")
	}

	maxRetry, err := strconv.ParseInt(getenvOrDefault("DATADOG_MAX_RETRIES", "5"), 0, 32)
	if err != nil {
		logrus.Errorf("The provided variable DATADOG_MAX_RETRIES was invalid using a default of %d: %s", defaultMaxRetries, err.Error())
		maxRetry = defaultMaxRetries
	}

	endpoint := datadogHostUS
	if strings.ToLower(region) == "eu" {
		endpoint = datadogHostEU
	}

	hook := &DatadogHook{
		ApiKey:          *apiKey,
		Tags:            globalTags,
		MinLevel:        minLevel,
		datadogEndpoint: endpoint,
		MaxRetry:        int(maxRetry),
		entryC:          make(chan logrus.Entry),
		errorC:          make(chan error),
		formatter: &logrus.JSONFormatter{
			FieldMap: logrus.FieldMap{
				logrus.FieldKeyMsg: "message",
			},
		},
	}

	go func() {
		for err := range hook.errorC {
			if errorHandler != nil {
				(*errorHandler)(err)
			} else {
				defaultErrorhandler(err)
			}
		}
	}()
	hook.ticker = time.NewTicker(5 * time.Second)
	go hook.batch(hook.ticker.C)

	return hook, nil
}

// Closes the Datadog hook and shuts down gracefully after sending all
func (h *DatadogHook) Close() {
	close(h.entryC)
	h.ticker.Stop()
	h.wg.Wait()
	close(h.errorC)
}

// Levels - implement Hook interface supporting all levels
func (h *DatadogHook) Levels() []logrus.Level {
	return logrus.AllLevels[:h.MinLevel+1]
}

// Fire - implement Hook interface fire the entry
func (h *DatadogHook) Fire(entry *logrus.Entry) error {
	h.entryC <- *entry
	return nil
}

func (h *DatadogHook) datadogURL() (string, error) {
	u, err := url.Parse(h.datadogEndpoint + basePath)
	if err != nil {
		return "", err
	}
	parameters := url.Values{}
	o := h.Tags
	parameters.Add("ddsource", "golang")
	parameters.Add("service", o.Service)
	parameters.Add("hostname", o.Hostname)
	var tags []string
	tags = append(tags, fmt.Sprintf("environment:%s", o.Environment))
	tags = append(tags, fmt.Sprintf("application:%s", o.Application))
	tags = append(tags, fmt.Sprintf("service:%s", o.Service))
	tags = append(tags, fmt.Sprintf("maintainer:%s", o.Maintainer))
	tags = append(tags, fmt.Sprintf("reference:%s.%s.%s.%s", o.Maintainer, o.Application, o.Service, o.Environment))
	parameters.Add("ddtags", strings.Join(tags, ","))
	u.RawQuery = parameters.Encode()
	return u.String(), nil
}

// This function listens to the log entry channel and the ticker channel and drives the batching of logs
func (h *DatadogHook) batch(ticker <-chan time.Time) {
	var batch [][]byte
	size := 0
	h.wg.Add(1)
	go func() {
		for entry := range h.entryC {
			formatted, err := h.formatter.Format(&entry)
			if err != nil {
				h.errorC <- err
				return
			}

			if size+len(formatted) >= maxContentSize || len(batch) == maxLogCount {
				h.send(batch)
				batch = make([][]byte, 0, maxLogCount)
				size = 0
			}

			if len(formatted) > maxLogSize {
				h.errorC <- fmt.Errorf("could not send log as it was too large! Maximum size for a single log is %d bytes, this log is %d bytes", maxLogSize, len(formatted))
				return
			}

			batch = append(batch, formatted)
			size += len(formatted)
		}
		h.wg.Done()
	}()
	h.wg.Add(1)
	go func() {
		for range ticker {
			h.send(batch)
			batch = make([][]byte, 0, maxLogCount)
			size = 0
		}
		h.wg.Done()
	}()
}

// Sends the Batch of logs to datadog
func (h *DatadogHook) send(batch [][]byte) {
	if h.ApiKey == "" || len(batch) == 0 {
		return
	}

	buf := make([]byte, 0)
	for i, line := range batch {
		// First character is the opening bracket of the array
		if i == 0 {
			buf = append(buf, '[')
		}
		buf = append(buf, line...)
		// Last character is the closing bracket of the array and doesn't get a ','
		if i == len(batch)-1 {
			buf = append(buf, ']')
			continue
		}
		buf = append(buf, ',')
	}

	if len(buf) == 0 {
		return
	}

	url, err := h.datadogURL()
	if err != nil {
		h.errorC <- err
		return
	}
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(buf))
	if err != nil {
		h.errorC <- err
		return
	}

	header := http.Header{}
	header.Add(apiKeyHeader, h.ApiKey)
	header.Add("Content-Type", "application/json")
	req.Header = header
	i := 0
	for {
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode > 399 {
			i++
			if h.MaxRetry < 0 || i >= h.MaxRetry {
				h.errorC <- fmt.Errorf("failed to send after %d retries", h.MaxRetry)
				return
			}
			continue
		}

		return
	}
}
