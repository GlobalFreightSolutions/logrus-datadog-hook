# Introduction

This package contains a ready to use hook for the[logrus](https://github.com/sirupsen/logrus) logging package that will collect and send logs to datadog via the http intake. It will batch logs up on a timer or to the maximum amount per request before sending to datadog, Which ever is sooner.

It also modifies the log format to ensure that datadog can properly read the message attribute.

It allows for the basic `service`, `hostname` and `source` datadog tags to be provided as well as any custom global tags you want to add via a `map[string]string`.

# Installing the module

```
> go get github.com/GlobalFreightSolutions/logrus-datadog-hook
```

# Using the Module

```go
package main

import (
  "time"

  datadog "github.com/GlobalFreightSolutions/logrus-datadog-hook"
  "github.com/sirupsen/logrus"
)

func main() {
  apiKey := "YOUR_API_KEY_HERE"
  options := &datadog.Options{
    ApiKey: &apiKey
  }
  hook, err := datadog.New(options)
  if err != nil {
    panic(err.Error())
  }

  logger := logrus.New()
  logger.AddHook(hook)
  
  // This ensures that the logger exits gracefully and all buffered logs are sent before closing down
	logrus.DeferExitHandler(hook.Close)

  for {
    logger.Info("This is a log sent to datadog")
    time.Sleep(30 * time.Second)
  }
}
```
