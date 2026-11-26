package alerts_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

type handoffDispatchFunc func(context.Context, webhookout.Target, webhookout.Event) webhookout.Result

func (f handoffDispatchFunc) Dispatch(ctx context.Context, target webhookout.Target, event webhookout.Event) webhookout.Result {
	return f(ctx, target, event)
}

type fireClaimHook struct {
	MemStore *state.MemStore
	after    func()
}

func (s fireClaimHook) ClaimAlertFire(ctx context.Context, ruleID, key string, payload []byte, observed float64, at time.Time) (string, bool, state.AlertFireRecipients, error) {
	id, won, recipients, err := s.MemStore.ClaimAlertFire(ctx, ruleID, key, payload, observed, at)
	if won && err == nil {
		s.after()
	}
	return id, won, recipients, err
}

func updateHandoffRule(t *testing.T, store *state.MemStore, ruleID string, change state.UpdateAlertRuleParams) state.AlertRule {
	t.Helper()
	rule, err := store.UpdateAlertRule(t.Context(), ruleID, change)
	if err != nil {
		t.Fatal(err)
	}
	return rule
}

type unavailableHandoffLookup struct{ MemStore *state.MemStore }

func (unavailableHandoffLookup) AlertDeferredFireRecipients(context.Context, string, time.Time) (state.AlertDeferredRecipients, error) {
	return state.AlertDeferredRecipients{}, errors.New("recipient lookup unavailable")
}

func TestAlertFireDispatchUsesCommittedRecipients(t *testing.T) {
	store := state.NewMemStore()
	rule, ident, _ := seedRule(t, store, state.AlertMetricRequestCount, state.AlertAboveBaseline, 3)
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200}}
	ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store:      statetest.NewMem(unavailableHandoffLookup{MemStore: store}),
		Identity:   func() *age.X25519Identity { return ident },
		PromQL:     &anomalyProm{currentSamples: 400, pastSamples: 100, trafficDays: 7},
		Dispatcher: dispatch, Now: func() time.Time { return rule.UpdatedAt.Add(time.Minute) }, Log: discardLog(),
	})
	stats, err := ev.RunOnce(t.Context())
	if err != nil || stats.Fired != 1 || dispatch.callCount() != 1 {
		t.Fatalf("claimed fire must dispatch during a recipient lookup failure: stats=%+v calls=%d err=%v", stats, dispatch.callCount(), err)
	}
}

func TestObserveOnlySettlesFireClaimedDuringModeChange(t *testing.T) {
	store := state.NewMemStore()
	rule, ident, _ := seedRule(t, store, state.AlertMetricRequestCount, state.AlertAboveBaseline, 3)
	rule = updateHandoffRule(t, store, rule.ID, state.UpdateAlertRuleParams{NotifyResolved: new(true)})
	now := rule.UpdatedAt.Add(time.Minute)
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200}}
	ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store: statetest.NewMem(fireClaimHook{MemStore: store, after: func() {
			_, err := store.UpdateAlertRule(t.Context(), rule.ID, state.UpdateAlertRuleParams{ObserveOnly: new(true)})
			if err != nil {
				t.Error(err)
			}
		}}),
		Identity:   func() *age.X25519Identity { return ident },
		PromQL:     &anomalyProm{currentSamples: 400, pastSamples: 100, trafficDays: 7},
		Dispatcher: dispatch, Now: func() time.Time { return now }, Log: discardLog(),
	})
	for range 2 {
		if _, err := ev.RunOnce(t.Context()); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Minute)
	}
	if dispatch.callCount() != 2 || dispatch.calls[0].Payload["event"] != "alert.fired" || dispatch.calls[1].Payload["event"] != "alert.resolved" {
		t.Fatalf("deliveries=%v, want a terminal event for the claimed fire", dispatch.calls)
	}
}

func TestObserveOnlyWebhookResolveRetryDoesNotHoldOtherAccounts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := state.NewMemStore()
		rule, ident, _ := seedRule(t, store, state.AlertMetricRequestCount, state.AlertAboveBaseline, 3)
		rule = updateHandoffRule(t, store, rule.ID, state.UpdateAlertRuleParams{NotifyResolved: new(true)})
		now := rule.UpdatedAt.Add(time.Minute)
		var mu sync.Mutex
		var received []string
		recoverReceiver := make(chan struct{})
		blocked := make(chan struct{})
		otherFired := make(chan struct{}, 1)
		failResolve := true
		dispatch := handoffDispatchFunc(func(ctx context.Context, _ webhookout.Target, event webhookout.Event) webhookout.Result {
			if event.Payload["rule_id"] != rule.ID {
				otherFired <- struct{}{}
				return webhookout.Result{StatusCode: 200}
			}
			kind := event.Payload["event"].(string)
			if kind == "alert.resolved" && failResolve {
				failResolve = false
				close(blocked)
				select {
				case <-recoverReceiver:
				case <-ctx.Done():
				}
				return webhookout.Result{StatusCode: 503, Err: errors.New("temporarily unavailable")}
			}
			mu.Lock()
			defer mu.Unlock()
			received = append(received, kind)
			return webhookout.Result{StatusCode: 200}
		})
		idents := []*age.X25519Identity{ident}
		newEvaluator := func() *alerts.Evaluator {
			return alerts.NewEvaluator(alerts.EvaluatorOptions{
				Store: store, Identities: func() []*age.X25519Identity { return idents },
				PromQL:     &anomalyProm{currentSamples: 400, pastSamples: 100, trafficDays: 7},
				Dispatcher: dispatch, Now: func() time.Time { return now }, Log: discardLog(),
			})
		}
		ev := newEvaluator()
		if _, err := ev.RunOnce(t.Context()); err != nil {
			t.Fatal(err)
		}
		updateHandoffRule(t, store, rule.ID, state.UpdateAlertRuleParams{ObserveOnly: new(true)})
		updateHandoffRule(t, store, rule.ID, state.UpdateAlertRuleParams{ObserveOnly: new(false)})
		time.Sleep(time.Nanosecond) // The fixture names accounts with the current nanosecond.
		_, otherIdent, _ := seedRuleForApp(t, store, "other-alert", state.AlertMetricRequestCount, state.AlertAboveBaseline, 3)
		idents = append(idents, otherIdent)
		now = now.Add(time.Minute)
		done := make(chan error, 1)
		go func() {
			_, err := ev.RunOnce(t.Context())
			done <- err
		}()
		<-blocked
		synctest.Wait()
		select {
		case <-otherFired:
		default:
			t.Error("an unrelated account's fire waited for the stuck resolve")
		}
		close(recoverReceiver)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(received, []string{"alert.fired"}) {
			t.Fatalf("fire overtook the failed resolve: %v", received)
		}
		now = now.Add(time.Minute)
		if _, err := newEvaluator().RunOnce(t.Context()); err != nil {
			t.Fatal(err)
		}
		if want := []string{"alert.fired", "alert.resolved", "alert.fired"}; !slices.Equal(received, want) {
			t.Fatalf("after restart, receiver got %v, want %v", received, want)
		}
	})
}

type contextAwareDeliveryStore struct {
	MemStore *state.MemStore
}

func (s contextAwareDeliveryStore) UpdateAlertDeliveryStatus(ctx context.Context, id string, status state.AlertDeliveryStatus, attempts, code int, detail string, deliveredAt *time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.MemStore.UpdateAlertDeliveryStatus(ctx, id, status, attempts, code, detail, deliveredAt)
}

func TestObserveOnlySettlesTimedOutDeferredWebhook(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := state.NewMemStore()
		rule, ident, _ := seedRule(t, base, state.AlertMetricRequestCount, state.AlertAboveBaseline, 3)
		rule = updateHandoffRule(t, base, rule.ID, state.UpdateAlertRuleParams{NotifyResolved: new(true)})
		now := rule.UpdatedAt.Add(time.Minute)
		tickNumber, timeouts := 0, 0
		dispatch := handoffDispatchFunc(func(ctx context.Context, _ webhookout.Target, event webhookout.Event) webhookout.Result {
			if tickNumber == 1 && event.Payload["event"] == "alert.resolved" {
				return webhookout.Result{StatusCode: 503, Err: errors.New("temporarily unavailable")}
			}
			if tickNumber >= 2 && event.Payload["event"] == "alert.fired" {
				timeouts++
				<-ctx.Done()
				time.Sleep(time.Nanosecond) // Both the batch and the transport deadline have elapsed.
				return webhookout.Result{Err: ctx.Err()}
			}
			return webhookout.Result{StatusCode: 200}
		})
		ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
			Store:      statetest.NewMem(contextAwareDeliveryStore{MemStore: base}),
			PromQL:     &anomalyProm{currentSamples: 400, pastSamples: 100, trafficDays: 7},
			Identity:   func() *age.X25519Identity { return ident },
			Dispatcher: dispatch, Now: func() time.Time { return now }, Log: discardLog(),
		})
		tick := func() {
			t.Helper()
			if _, err := ev.RunOnce(t.Context()); err != nil {
				t.Fatal(err)
			}
			tickNumber++
			now = now.Add(time.Minute)
		}
		tick()
		updateHandoffRule(t, base, rule.ID, state.UpdateAlertRuleParams{ObserveOnly: new(true)})
		updateHandoffRule(t, base, rule.ID, state.UpdateAlertRuleParams{ObserveOnly: new(false)})
		tick()
		tick()
		tick()

		deliveries, err := base.ListAlertDeliveriesForRule(t.Context(), rule.ID, 2, false)
		if err != nil || len(deliveries) != 2 || deliveries[0].Status != state.AlertDeliveryFailed {
			t.Fatalf("the timed-out delivery should be settled: deliveries=%+v err=%v", deliveries, err)
		}
		if timeouts != 1 {
			t.Fatalf("the same transport timeout was attempted %d times", timeouts)
		}
	})
}
