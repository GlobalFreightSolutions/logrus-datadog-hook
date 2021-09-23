package datadog

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"gotest.tools/assert"
)

func TestNewShouldReturnErrorIfNoApiKey(t *testing.T) {
	// Arrange
	options := &Options{}

	// Act
	_, err := New(options)

	// Assert
	if err == nil {
		t.FailNow()
	}
}

func TestNewShouldSetMaxRetriesToDefault(t *testing.T) {
	// Arrange
	apiKey := "apikey"

	// Act
	hook, err := New(&Options{ApiKey: &apiKey})
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	assert.Equal(t, hook.MaxRetry, defaultMaxRetries)
}

func TestNewGivenNotProvidedShouldSetOptionsToDefault(t *testing.T) {
	// Arrange
	apiKey := "apikey"

	// Act
	hook, err := New(&Options{ApiKey: &apiKey})
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	assert.Equal(t, hook.DatadogEndpoint, DatadogHostUS)
	assert.Equal(t, hook.MinLevel, defaultLevel)
	assert.Equal(t, hook.Service, defaultService)
	assert.Equal(t, hook.Hostname, defaultHost)
	assert.Equal(t, hook.Source, defaultSource)
}

func TestNewShouldSetHookPropertiesAccordingToProvidedOptions(t *testing.T) {
	// Arrange
	apiKey := "apiKey"
	minLevel := logrus.InfoLevel
	endpoint := DatadogHostEU
	service := "Service"
	host := "Host"
	source := "Source"
	options := &Options{
		ApiKey:              &apiKey,
		MinimumLoggingLevel: &minLevel,
		DatadogEndpoint:     &endpoint,
		Service:             &service,
		Host:                &host,
		Source:              &source,
		GlobalTags: &map[string]string{
			"Tag": "Value",
		},
	}

	// Act
	hook, err := New(options)
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	assert.Equal(t, hook.ApiKey, apiKey)
	assert.Equal(t, hook.MinLevel, minLevel)
	assert.Equal(t, hook.DatadogEndpoint, endpoint)
	assert.Equal(t, hook.Service, service)
	assert.Equal(t, hook.Hostname, host)
	assert.Equal(t, hook.Source, source)
	tag, ok := (*hook.Tags)["Tag"]
	assert.Assert(t, ok, "Tags should have tag `Tag`")
	assert.Equal(t, tag, "Value")
}

func TestSendShouldSendLogsToConfiguredDatadogEndpoint(t *testing.T) {
	// Arrange
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Write([]byte("Ok"))

		decoder := json.NewDecoder(r.Body)
		var logs []map[string]string
		decoder.Decode(&logs)
		assert.Assert(t, len(logs) == 3)
		for _, log := range logs {
			message, ok := log["message"]
			assert.Assert(t, ok, "log should have `message` property")
			assert.Assert(t, len(message) > 0, "message should have length greater than 0")
			level, ok := log["level"]
			assert.Assert(t, ok, "log should have `level` property")
			assert.Assert(t, len(level) > 0, "level should have length greater than 0")
			assert.Assert(t, level == "info" || level == "warning" || level == "error", "level should be either info, warning or error")
		}
	}))
	defer server.Close()
	logger := logrus.New()

	// Provide a dummy apikey so it doesn't fall over
	apiKey := "apikey"
	hook, err := New(&Options{ApiKey: &apiKey})
	if err != nil {
		t.Fatal(err)
	}

	hook.DatadogEndpoint = endpoint(server.URL)
	logger.AddHook(hook)
	logrus.DeferExitHandler(hook.Close)

	// Act
	logger.WithFields(logrus.Fields{
		"Field": "Value",
	}).Info("This is a log message!")
	logger.WithFields(logrus.Fields{
		"Field": "Value",
	}).Warn("This is another log message!")
	logger.WithField("error", errors.New("This is the message from an error").Error()).Error("Oh lawd something went wrong")
	// Wait a hot second for the goroutine to send the log batch
	time.Sleep(10 * time.Second)
}

func TestSendShouldSendLogsToConfiguredDatadogEndpointNoBatching(t *testing.T) {

	// Arrange

	sentLogCounter := 3

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Write([]byte("Ok"))

		decoder := json.NewDecoder(r.Body)
		var logs []map[string]string
		decoder.Decode(&logs)
		assert.Assert(t, len(logs) == 1)
		for _, log := range logs {
			message, ok := log["message"]
			assert.Assert(t, ok, "log should have `message` property")
			assert.Assert(t, len(message) > 0, "message should have length greater than 0")
			level, ok := log["level"]
			assert.Assert(t, ok, "log should have `level` property")
			assert.Assert(t, len(level) > 0, "level should have length greater than 0")
			assert.Assert(t, level == "info" || level == "warning" || level == "error", "level should be either info, warning or error")
		}
		sentLogCounter = sentLogCounter - 1
	}))
	defer server.Close()
	logger := logrus.New()

	// Provide a dummy apikey so it doesn't fall over
	apiKey := "apikey"
	hook, err := New(&Options{
		ApiKey:                &apiKey,
		ClientBatchingEnabled: &[]bool{false}[0],
	})
	if err != nil {
		t.Fatal(err)
	}

	hook.DatadogEndpoint = endpoint(server.URL)
	logger.AddHook(hook)
	logrus.DeferExitHandler(hook.Close)

	// Act
	logger.WithFields(logrus.Fields{
		"Field": "Value",
	}).Info("This is a log message!")
	logger.WithFields(logrus.Fields{
		"Field": "Value",
	}).Warn("This is another log message!")
	logger.WithField("error", errors.New("This is the message from an error").Error()).Error("Oh lawd something went wrong")

	assert.Assert(t, sentLogCounter == 0, "Logs were not sent unbatched as 3 seperate occurances")
}
