package state

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestEnvironmentQueueDefaultRetryWaitsAndStillExhausts(t *testing.T) {
	now := time.Now()
	inv := Invocation{Attempts: 1}
	got, err := environmentQueueRetry(inv, api.PlanScale, now, "unavailable")
	if err != nil || got.State != InvocationPending || !got.DueAt.After(now) || got.DueAt.Sub(now) > api.EventDeliveryRetryBaseSeconds*time.Second {
		t.Fatalf("default retry spun or exceeded its first delay: %+v, %v", got, err)
	}
	inv.Attempts = 20
	got, err = environmentQueueRetry(inv, api.PlanScale, now, "unavailable")
	if err != nil || got.State != InvocationPending || !got.DueAt.After(now) || got.DueAt.Sub(now) > api.EventDeliveryRetryMaxSeconds*time.Second {
		t.Fatalf("default retry exceeded its cap: %+v, %v", got, err)
	}
	inv.Attempts = api.DurableRetryMaxAttempts
	got, err = environmentQueueRetry(inv, api.PlanScale, now, "unavailable")
	if err != nil || got.State != InvocationDeadLetter {
		t.Fatalf("default retry bypassed the attempt budget: %+v, %v", got, err)
	}
}
