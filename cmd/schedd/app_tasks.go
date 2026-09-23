package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/sched"
)

const appTaskDispatchConcurrencyEnv = "FAAS_SCHEDD_APP_TASK_DISPATCH_CONCURRENCY"

// appTaskDispatchEnabled is an exact opt-in because enabling it starts
// claiming durable commands and booting deployment-attached VMs.
func appTaskDispatchEnabled(value string) bool {
	return strings.TrimSpace(value) == "1"
}

func appTaskDispatchConcurrencyFromEnv(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return sched.DefaultAppTaskDispatchConcurrency, nil
	}
	concurrency, err := strconv.Atoi(value)
	if err != nil || concurrency < 1 || concurrency > sched.MaxAppTaskDispatchConcurrency {
		return 0, fmt.Errorf("%s must be an integer between 1 and %d: %q", appTaskDispatchConcurrencyEnv, sched.MaxAppTaskDispatchConcurrency, value)
	}
	return concurrency, nil
}
