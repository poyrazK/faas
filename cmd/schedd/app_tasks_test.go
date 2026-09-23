package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
)

func TestAppTaskDispatchGateIsExactOptIn(t *testing.T) {
	for _, value := range []string{"", "0", "true", "yes"} {
		if appTaskDispatchEnabled(value) {
			t.Fatalf("appTaskDispatchEnabled(%q) = true", value)
		}
	}
	if !appTaskDispatchEnabled(" 1 ") {
		t.Fatal("appTaskDispatchEnabled did not accept exact opt-in")
	}
}

func TestAppTaskDispatchConcurrencyBounds(t *testing.T) {
	if got, err := appTaskDispatchConcurrencyFromEnv(""); err != nil || got != sched.DefaultAppTaskDispatchConcurrency {
		t.Fatalf("default = %d, %v", got, err)
	}
	if got, err := appTaskDispatchConcurrencyFromEnv("4"); err != nil || got != 4 {
		t.Fatalf("configured = %d, %v", got, err)
	}
	for _, value := range []string{"0", "33", "many"} {
		if _, err := appTaskDispatchConcurrencyFromEnv(value); err == nil {
			t.Fatalf("value %q was accepted", value)
		}
	}
}
