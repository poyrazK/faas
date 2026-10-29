package sched_test

// adr: 20261029190000

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// A failed event delivery backs off (5 s doubling to 5 minutes, ±20 %)
// instead of retrying every 5 s, takes its app's retry policy when the app
// sets one, and stays capped by the plan; other invocations keep the fixed
// producer default.
func TestEventDeliveryRetriesBackOffAndHonourTheAppPolicy(t *testing.T) {
	ctx := context.Background()
	d, store, _, _, _ := sched.NewDrainHarness(t, api.PlanPro, false)
	delivery := sched.SeedDrainInvocation(t, store, state.InvocationAsyncInvoke)
	delivery.Headers = json.RawMessage(`{"x-gregale-event-subscription-id":"` + uuid.NewString() + `"}`)

	within := func(got, want time.Duration) bool { return got >= want*8/10 && got <= want*12/10 }
	for _, source := range []state.InvocationSource{state.InvocationAsyncInvoke, state.InvocationReplay} {
		delivery.Source = source
		for attempts, want := range map[int]time.Duration{1: 5 * time.Second, 3: 20 * time.Second, 12: 5 * time.Minute} {
			delivery.Attempts = attempts
			if got := d.XInvocationRetryDelay(ctx, delivery); !within(got, want) {
				t.Errorf("%s delivery after %d attempts waits %s, want %s ±20 %%", source, attempts, got, want)
			}
		}
	}
	delivery.Source = state.InvocationAsyncInvoke
	if got, want := d.XInvocationAttemptBudget(ctx, delivery), api.MustLimitsFor(api.PlanPro).MaxQueueAttempts; got != want {
		t.Errorf("delivery attempt budget = %d, want Pro's %d", got, want)
	}

	policy := []byte(`{"max_attempts":4,"base_seconds":2,"max_seconds":10}`)
	if _, err := store.UpdateApp(ctx, delivery.AppID, state.UpdateAppParams{RetryPolicyJSON: &policy, SetRetryPolicy: true}); err != nil {
		t.Fatal(err)
	}
	delivery.Attempts = 3
	if got := d.XInvocationRetryDelay(ctx, delivery); got != 8*time.Second {
		t.Errorf("delivery under the app's policy waits %s, want 8s", got)
	}
	if got := d.XInvocationAttemptBudget(ctx, delivery); got != 4 {
		t.Errorf("delivery under the app's policy has %d attempts, want 4", got)
	}

	plain := sched.SeedDrainInvocation(t, store, state.InvocationAsyncInvoke)
	plain.Attempts = 3
	if got := d.XInvocationRetryDelay(ctx, plain); got != 5*time.Second {
		t.Errorf("a plain async invocation waits %s, want the fixed 5s default", got)
	}
}
