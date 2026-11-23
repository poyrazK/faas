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

func (s fireClaimHook) ClaimAlertFire(ctx context.Context, ruleID, key string, payload []byte, observed float64, at time.Time) (string, bool, error) {
	id, won, err := s.MemStore.ClaimAlertFire(ctx, ruleID, key, payload, observed, at)
	if won && err == nil {
		s.after()
	}
	return id, won, err
}

func updateHandoffRule(t *testing.T, store *state.MemStore, ruleID string, change state.UpdateAlertRuleParams) state.AlertRule {
	t.Helper()
	rule, err := store.UpdateAlertRule(t.Context(), ruleID, change)
	if err != nil {
		t.Fatal(err)
	}
	return rule
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
