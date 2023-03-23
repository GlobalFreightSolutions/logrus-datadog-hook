package datadog

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

type endpoint string

const (
	// The maximum content size per request is 5MB
	maxContentSize = 5 * 1024 * 1024

	// The maximum size for a single log is 256kB
	maxLogSize = 256 * 1024

	// The maximum amount of logs that can be sent in a single request is 1000
	maxLogCount = 1000

	basePath                   = "/v1/input"
	DatadogHostUS     endpoint = "https://http-intake.logs.datadoghq.com"
	DatadogHostEU     endpoint = "https://http-intake.logs.datadoghq.eu"
	DatadogHostUSGOV  endpoint = "http-intake.logs.ddog-gov.com"
	apiKeyHeader               = "DD-API-KEY"
	defaultMaxRetries          = 5
)

var (
	defaultLevel          = logrus.InfoLevel
	defaultService        = "unknown"
	defaultHost           = "unknown"
	defaultSource         = "golang"
	defaultClientBatching = true
)

type DatadogHook struct {
	ApiKey                string
	Service               string
	Hostname              string
	Source                string
	Tags                  *map[string]string
	ClientBatchingEnabled bool
	MinLevel              logrus.Level
	MaxRetry              int
	DatadogEndpoint       endpoint
	Formatter             logrus.Formatter
	entryC                chan logrus.Entry
	ticker                *time.Ticker
	wg                    sync.WaitGroup
	done                  chan bool
}

type Options struct {
	// The Datadog Api Key needed to authenticate
	ApiKey *string
	// The Minimum level of log to send to datadog, default is logrus.InfoLevel
	MinimumLoggingLevel *logrus.Level
	// The datadog endpoint to send logs to, default is DatadogHostUS
	DatadogEndpoint *endpoint
	// The Service tag to add to all logs
	Service *string
	// The Host tag to add to all logs
	Host *string
	// The source tag to add to all logs
	Source *string
	// A map of custom tags to add to every log
	GlobalTags *map[string]string
	// Controls whether logs are batched locally before sending to Datadog; Defaults to true
	ClientBatchingEnabled *bool
}

// Creates and Starts a new DatadogHook
func New(options *Options) (*DatadogHook, error) {
	if options == nil {
		options = &Options{}
	}

	if options.ClientBatchingEnabled == nil {
		options.ClientBatchingEnabled = &defaultClientBatching
	}

	if options.MinimumLoggingLevel == nil {
		options.MinimumLoggingLevel = &defaultLevel
	}

	if options.ApiKey == nil || *options.ApiKey == "" {
		return nil, errors.New("apiKey not provided, cannot create datadog hook")
	}

	if options.DatadogEndpoint == nil || *options.DatadogEndpoint == "" {
		endpoint := DatadogHostUS
		options.DatadogEndpoint = &endpoint
	}

	if options.Service == nil {
		options.Service = &defaultService
	}

	if options.Host == nil {
		options.Host = &defaultHost
	}

	if options.Source == nil {
		options.Source = &defaultSource
	}

	hook := &DatadogHook{
		ApiKey:                *options.ApiKey,
		Service:               *options.Service,
		Hostname:              *options.Host,
		Source:                *options.Source,
		Tags:                  options.GlobalTags,
		ClientBatchingEnabled: *options.ClientBatchingEnabled,
		MinLevel:              *options.MinimumLoggingLevel,
		DatadogEndpoint:       *options.DatadogEndpoint,
		MaxRetry:              5,
		Formatter: &logrus.JSONFormatter{
			FieldMap: logrus.FieldMap{
				logrus.FieldKeyMsg: "message",
			},
		},
		ticker: time.NewTicker(5 * time.Second),
		entryC: make(chan logrus.Entry),
		wg:     sync.WaitGroup{},
		done:   make(chan bool),
	}

	if hook.ClientBatchingEnabled {
		go hook.batch(hook.ticker.C)
	}

	return hook, nil
}

// Closes the Datadog hook and shuts down gracefully after sending all
func (h *DatadogHook) Close() {
	close(h.entryC)
	h.ticker.Stop()
	go func() { h.done <- true }()
	h.wg.Wait()
	close(h.done)
}

// Levels - implement Hook interface supporting all levels
func (h *DatadogHook) Levels() []logrus.Level {
	return logrus.AllLevels[:h.MinLevel+1]
}

// Fire - implement Hook interface fire the entry
func (h *DatadogHook) Fire(entry *logrus.Entry) error {
	if h.ClientBatchingEnabled {
		h.entryC <- *entry
	} else {
		formatted, err := h.Formatter.Format(entry)
		if err != nil {
			return err
		}

		// Batching is disabled, just send the single log now
		h.send([][]byte{formatted})
	}

	return nil
}

// This function creates the request url
func (h *DatadogHook) buildUrl() (string, error) {
	u, err := url.Parse(string(h.DatadogEndpoint) + basePath)
	if err != nil {
		return "", err
	}
	parameters := url.Values{}
	parameters.Add("ddsource", "golang")
	parameters.Add("service", h.Service)
	parameters.Add("hostname", h.Hostname)
	var tags []string
	if h.Tags != nil {
		for key, value := range *h.Tags {
			tags = append(tags, fmt.Sprintf("%v:%v", key, value))
		}
		parameters.Add("ddtags", strings.Join(tags, ","))
	}
	u.RawQuery = parameters.Encode()
	return u.String(), nil
}

// This function listens to the log entry channel and the ticker channel and drives the batching of logs
func (h *DatadogHook) batch(ticker <-chan time.Time) {
	var batch [][]byte
	size := 0
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		for entry := range h.entryC {
			formatted, err := h.Formatter.Format(&entry)
			if err != nil {
				fmt.Println(err.Error())
				return
			}

			if size+len(formatted) >= maxContentSize || len(batch) == maxLogCount {
				h.send(batch)
				batch = make([][]byte, 0, maxLogCount)
				size = 0
			}

			if len(formatted) > maxLogSize {
				err := fmt.Errorf("could not send log as it was too large! Maximum size for a single log is %d bytes, this log is %d bytes", maxLogSize, len(formatted))
				fmt.Println(err.Error())
				return
			}

			batch = append(batch, formatted)
			size += len(formatted)
		}
	}()
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		for {
			select {
			case <-ticker:
				{
					h.send(batch)
					batch = make([][]byte, 0, maxLogCount)
					size = 0
				}
			case <-h.done:
				{
					// Stopping the hook, try and send any buffered entries
					h.send(batch)
					return
				}
			}
		}
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

	url, err := h.buildUrl()
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(buf))
	if err != nil {
		fmt.Println(err.Error())
		return
	}

	header := http.Header{}
	header.Add(apiKeyHeader, h.ApiKey)
	header.Add("Content-Type", "application/json")
	req.Header = header
	i := 0
	for {
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode >= 400 {
			i++
			if h.MaxRetry < 0 || i >= h.MaxRetry {
				err := fmt.Errorf("failed to send after %d retries", h.MaxRetry)
				fmt.Println(err.Error())
				return
			}
			continue
		}

		return
	}
}
