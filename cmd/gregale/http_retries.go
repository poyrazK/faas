package main

import (
	"errors"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

var httpReadRetries int // invocation-only wiring; run restores it on return

func setHTTPReadRetries(value string) error {
	count, err := strconv.Atoi(value)
	if err != nil || count < 0 || count > api.MaxReadRetries {
		return errors.New("--http-retries requires an integer from 0 to 5")
	}
	httpReadRetries = count
	return nil
}
