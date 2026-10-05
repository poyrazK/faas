// adr: 603
package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestEventDeliveryCapacitySchedulerRecovery(t *testing.T) {
	for _, adopted := range []bool{false, true} {
		t.Run(fmt.Sprint(adopted), func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			account, err := store.CreateAccount(ctx, "scheduler-capacity@example.com", api.PlanFree)
			if err != nil {
				t.Fatal(err)
			}
			accountID := mustCanonicalEventAccountID(t, account.ID)
			analytics, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountID, Slug: "analytics", Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			analyticsSub, _, err := store.UpsertEventSubscription(ctx, accountID, analytics.ID, "orders", "order.created", nil)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			ops := wire.NewOpsMetrics("sched")
			loop := (&Loop{engine: &Engine{store: store}, now: func() time.Time { return now }, ops: ops}).WithEventRecipientClaims(adopted)
			publish := func(id string) {
				t.Helper()
				e, err := (events.Envelope{ID: id, Source: "orders", Type: "order.created", Data: json.RawMessage(`{}`)}).Normalize(accountID, time.Now().UTC())
				if err != nil {
					t.Fatal(err)
				}
				p, _ := json.Marshal(e)
				if err := store.AppendEvent(ctx, "apid", "event.published", &accountID, p); err != nil {
					t.Fatal(err)
				}
			}
			cap := api.MustLimitsFor(api.PlanFree).EventDeliveries.PerConsumer
			for i := 0; i < cap; i++ {
				publish(fmt.Sprint(i))
			}
			now = time.Now().Add(time.Millisecond)
			loop.runEventFanoutSweep(ctx)
			billing, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountID, Slug: "billing", Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.UpsertEventSubscription(ctx, accountID, billing.ID, "orders", "order.created", nil); err != nil {
				t.Fatal(err)
			}
			publish("shared")
			now = time.Now().Add(time.Millisecond)
			loop.runEventFanoutSweep(ctx)
			receipt, err := store.EventReceipt(ctx, accountID, "orders", "shared", state.EventReceiptCursor{}, 100)
			if err != nil {
				t.Fatal(err)
			}
			var routing state.EventReceiptRouting
			for _, entry := range receipt.Recipients {
				if entry.SubscriptionID == analyticsSub.ID {
					routing = entry.Routing
				}
			}
			if routing.State != "pending" || routing.CapacityDeferrals != 1 || receipt.RoutingSettledAt != nil {
				t.Fatalf("blocked routing=%+v receipt=%+v", routing, receipt)
			}
			billed, err := store.ListInvocationsForApp(ctx, billing.ID)
			if err != nil || len(billed) != 1 {
				t.Fatalf("billing=%d,%v", len(billed), err)
			}
			analyticsInvs, err := store.ListInvocationsForApp(ctx, analytics.ID)
			if err != nil || len(analyticsInvs) != cap {
				t.Fatalf("analytics=%d,%v", len(analyticsInvs), err)
			}
			// Drive more than twelve durable waits, then inject a real storage
			// failure. The first actual failure must retain eleven retries.
			now = routing.NextAttemptAt.Add(time.Minute)
			loop.runEventFanoutSweep(ctx)
			fault := &eventCapacityFaultStore{MemStore: store}
			faulty := (&Loop{engine: &Engine{store: fault}, now: func() time.Time { return now }}).WithEventRecipientClaims(adopted)
			if adopted {
				faulty.runEventRecipientSweep(ctx)
			} else {
				work, err := store.ClaimDuePublishedEvent(ctx, now)
				if err != nil {
					t.Fatal(err)
				}
				routeErr := faulty.routePublishedEventSnapshot(ctx, work)
				if routeErr == nil {
					t.Fatal("injected routing failure was ignored")
				}
				if err := store.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, routeErr); err != nil {
					t.Fatal(err)
				}
			}
			failedRead, err := store.EventReceipt(ctx, accountID, "orders", "shared", state.EventReceiptCursor{}, 100)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range failedRead.Recipients {
				if entry.SubscriptionID == analyticsSub.ID {
					routing = entry.Routing
				}
			}
			if routing.State != "pending" || routing.CapacityDeferrals <= 12 {
				t.Fatalf("capacity spent failure budget=%+v", routing)
			}
			used := routing.Attempts - routing.CapacityDeferrals
			if adopted {
				used = *routing.GenerationAttempts - *routing.GenerationCapacityDeferrals
			}
			if used != 1 {
				t.Fatalf("first actual failure consumed %d attempts: %+v", used, routing)
			}
			if err := store.CancelInvocation(ctx, analyticsInvs[0].ID); err != nil {
				t.Fatal(err)
			}
			// A new scheduler drains the durable wait with adoption disabled as well.
			now = routing.NextAttemptAt.Add(time.Millisecond)
			restarted := (&Loop{engine: &Engine{store: store}, now: func() time.Time { return now }, ops: ops}).WithEventRecipientClaims(false)
			restarted.runEventFanoutSweep(ctx)
			receipt, err = store.EventReceipt(ctx, accountID, "orders", "shared", state.EventReceiptCursor{}, 100)
			if err != nil || receipt.RoutingSettledAt == nil {
				t.Fatalf("recovered receipt=%+v,%v", receipt, err)
			}
			analyticsInvs, err = store.ListInvocationsForApp(ctx, analytics.ID)
			if err != nil || len(analyticsInvs) != cap+1 {
				t.Fatalf("recovered analytics=%d,%v", len(analyticsInvs), err)
			}
			restarted.runEventFanoutSweep(ctx)
			billed, err = store.ListInvocationsForApp(ctx, billing.ID)
			if err != nil || len(billed) != 1 {
				t.Fatalf("duplicate billing=%d,%v", len(billed), err)
			}
		})
	}
}

// Capacity is already full, so this faults the next route before admission.
type eventCapacityFaultStore struct{ *state.MemStore }

func (s *eventCapacityFaultStore) AdmitPublishedEventRecipient(_ context.Context, _ state.PublishedEventRoutingClaim) (state.PublishedEventRoutingResult, error) {
	return state.PublishedEventRoutingResult{Matched: true}, &state.EventRecipientAdmissionError{FailureCode: state.EventFanoutFailureCodeInvocationEnqueueFailed, Retryable: true, Err: errors.New("temporary admission outage")}
}

func TestEventDeliveryCapacityWarningClassification(t *testing.T) {
	for _, tc := range []struct {
		err  error
		wait bool
	}{
		{nil, false},
		{fmt.Errorf("routing: %w", state.ErrEventDeliveryCapacity), true},
		{errors.Join(state.ErrEventDeliveryCapacity, state.ErrEventDeliveryCapacity), true},
		{errors.Join(state.ErrEventDeliveryCapacity, errors.New("storage unavailable")), false},
		{fmt.Errorf("routing: %w", errors.Join(state.ErrEventDeliveryCapacity, errors.New("storage unavailable"))), false},
	} {
		if got := eventFanoutCapacityWait(tc.err); got != tc.wait {
			t.Fatalf("capacity-only(%v)=%t want %t", tc.err, got, tc.wait)
		}
	}
}
