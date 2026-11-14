package main

import (
	"errors"
	"time"
)

// Zero selects the SDK defaults. run restores this invocation-only wiring.
var httpRequestTimeout time.Duration

func setHTTPRequestTimeout(value string) error {
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return errors.New("--http-timeout requires a positive duration, such as 90s or 2m")
	}
	httpRequestTimeout = duration
	return nil
}
