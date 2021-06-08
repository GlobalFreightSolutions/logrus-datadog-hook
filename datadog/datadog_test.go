package datadog

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"gotest.tools/assert"
)

func TestNewShouldGatherOptionsFromEnv(t *testing.T) {
	// Arrange
	service := "service"
	environment := "environment"
	maintainer := "maintainer"
	application := "application"
	host := "host"
	apiKey := "apikey"
	maxRetries := 11
	os.Setenv("SERVICE", service)
	os.Setenv("ENVIRONMENT", environment)
	os.Setenv("MAINTAINER", maintainer)
	os.Setenv("APPLICATION", application)
	os.Setenv("HOST", host)
	os.Setenv("DATADOG_REGION", "eu")
	os.Setenv("DATADOG_API_KEY", apiKey)
	os.Setenv("DATADOG_MAX_RETRIES", fmt.Sprint(maxRetries))

	// Act
	hook, err := New(nil, logrus.InfoLevel, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	assert.Equal(t, hook.Tags.Service, service)
	assert.Equal(t, hook.Tags.Environment, environment)
	assert.Equal(t, hook.Tags.Maintainer, maintainer)
	assert.Equal(t, hook.Tags.Application, application)
	assert.Equal(t, hook.Tags.Hostname, host)
	assert.Equal(t, hook.datadogEndpoint, datadogHostEU)
	assert.Equal(t, hook.ApiKey, apiKey)
	assert.Equal(t, hook.MaxRetry, maxRetries)
}

func TestNewShouldUseProvidedApiKeyOverEnv(t *testing.T) {
	// Arrange
	apiKey := "apiKey"
	envApiKey := "env apiKey"
	os.Setenv("DATADOG_API_KEY", envApiKey)
	// Act
	hook, err := New(&apiKey, logrus.InfoLevel, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	assert.Equal(t, hook.ApiKey, apiKey)
	assert.Assert(t, hook.ApiKey != envApiKey)
}

func TestNewShouldReturnErrorIfNoApiKey(t *testing.T) {
	// Arrange
	os.Setenv("DATADOG_API_KEY", "")

	// Act
	_, err := New(nil, logrus.InfoLevel, nil)

	// Assert
	if err == nil {
		t.FailNow()
	}
}

func TestNewShouldSetMaxRetriesToDefaultIfInvalid(t *testing.T) {
	// Arrange
	os.Setenv("DATADOG_MAX_RETRIES", "INVALID VALUE!!!")
	apiKey := "apikey"

	// Act
	hook, err := New(&apiKey, logrus.InfoLevel, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	assert.Equal(t, hook.MaxRetry, defaultMaxRetries)
}

func TestNewGivenRegionNotProvidedShouldSetDatadogEndpointToUS(t *testing.T) {
	// Arrange
	region := ""
	os.Setenv("DATADOG_REGION", region)
	apiKey := "apikey"

	// Act
	hook, err := New(&apiKey, logrus.InfoLevel, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	assert.Equal(t, hook.datadogEndpoint, datadogHostUS)
}

func TestGetenvOrDefaultShouldReturnEnvIfExists(t *testing.T) {
	// Arrange
	envKey := "KEY"
	envValue := "value"
	os.Setenv(envKey, envValue)

	// Act
	res := getenvOrDefault(envKey, "default")

	// Assert
	assert.Equal(t, res, envValue)
}

func TestGetenvOrDefaultShouldReturnDefaultIfNotExists(t *testing.T) {
	// Arrange
	envKey := "KEY"
	os.Setenv(envKey, "")
	defaultValue := "defaultValue"

	// Act
	res := getenvOrDefault(envKey, defaultValue)

	// Assert
	assert.Equal(t, res, defaultValue)
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
	hook, err := New(&apiKey, logrus.InfoLevel, nil)
	if err != nil {
		t.Fatal(err)
	}

	hook.datadogEndpoint = server.URL
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
