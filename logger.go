package gfsLogger

import (
	"os"

	"gfsdeliver.com/go/gfs-go-logging/datadog"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/writer"
)

type Options struct {
	ApiKey string
	Level  logrus.Level
}

func New(options Options) (*logrus.Logger, error) {
	logger := logrus.New()
	logger.AddHook(&writer.Hook{
		Writer:    os.Stdout,
		LogLevels: logrus.AllLevels,
	})

	if options.ApiKey == "" {
		logger.Warn("apiKey is not provided, logs will not be sent to datadog")
	} else {
		hook, err := datadog.New(&options.ApiKey, options.Level, nil)
		if err != nil {
			return nil, err
		}
		logger.AddHook(hook)
		logrus.DeferExitHandler(hook.Close)
	}

	return logger, nil
}
