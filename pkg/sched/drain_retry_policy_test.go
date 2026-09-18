package sched

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 134
func TestDrainRetryAfterForUsesInvocationPolicy(t *testing.T) {
	d := &Drain{retryAfterSeconds: 5}
	legacy := state.Invocation{Attempts: 1}
	if got := d.retryAfterFor(legacy); got != 5*time.Second {
		t.Fatalf("legacy retry delay = %s, want 5s", got)
	}
	configured := state.Invocation{
		Attempts:        2,
		RetryPolicyJSON: []byte(`{"base_seconds":3,"max_seconds":10}`),
	}
	if got := d.retryAfterFor(configured); got != 6*time.Second {
		t.Fatalf("configured retry delay = %s, want 6s", got)
	}
}
