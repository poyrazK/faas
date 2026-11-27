package alerts_test

// adr: 20261029203000 — a rule with notify_resolved sends its webhook a signed
// alert.resolved event when it recovers; every fire says alert.fired; a rule
// without it gets fires only, as receivers written before it expect.

import (
	"context"
	"errors"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

func resolveEvaluator(store *state.MemStore, prom *stubPromQL, dispatch alerts.Dispatcher, ident *age.X25519Identity) *alerts.Evaluator {
	return alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store:      store,
		PromQL:     prom,
		Audit:      audit.New(store, discardLog(), nil, "meterd"),
		Identities: func() []*age.X25519Identity { return []*age.X25519Identity{ident} },
		Dispatcher: dispatch,
		Now:        func() time.Time { return time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC) },
		Log:        discardLog(),
	})
}

func TestEvaluator_NotifyResolvedSendsResolveToWebhook(t *testing.T) {
	for _, tc := range []struct {
		name           string
		notifyResolved bool
		wantCalls      int
	}{
		{name: "opted in: fire then resolve", notifyResolved: true, wantCalls: 2},
		{name: "default: fires only", notifyResolved: false, wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			rule, ident, _ := seedRule(t, store, state.AlertMetricErrorRate, state.AlertGt, 5)
			if _, err := store.UpdateAlertRule(context.Background(), rule.ID, state.UpdateAlertRuleParams{NotifyResolved: &tc.notifyResolved}); err != nil {
				t.Fatal(err)
			}
			prom := &stubPromQL{value: 10}
			dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
			ev := resolveEvaluator(store, prom, dispatch, ident)
			if stats, err := ev.RunOnce(context.Background()); err != nil || stats.Fired != 1 {
				t.Fatalf("firing tick = %+v, %v", stats, err)
			}
			prom.value = 0
			stats, err := ev.RunOnce(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got := dispatch.callCount(); got != tc.wantCalls {
				t.Fatalf("webhook calls = %d, want %d", got, tc.wantCalls)
			}
			if fire := dispatch.calls[0].Payload["event"]; fire != "alert.fired" {
				t.Fatalf("fire payload event = %v, want alert.fired", fire)
			}
			if !tc.notifyResolved {
				return
			}
			resolve := dispatch.calls[1]
			if resolve.Payload["event"] != "alert.resolved" || resolve.Payload["rule_id"] != rule.ID || resolve.Signer == nil || resolve.EventID == "" {
				t.Fatalf("resolve call = %+v, want a signed alert.resolved event for the rule", resolve)
			}
			if stats.ResolvesDelivered != 1 {
				t.Fatalf("stats = %+v, want one resolve delivered", stats)
			}
		})
	}
}

func TestEvaluator_ObserveOnlySendsSignedStandDownToOriginalWebhook(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	rule, ident, secret := seedRule(t, store, state.AlertMetricRequestCount, state.AlertAboveBaseline, 3)
	observe, err := store.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{
		ObserveOnly: new(true), EnvironmentScope: new("production"), NotifyResolved: new(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	rule, err = store.UpdateAlertRule(ctx, observe.ID, state.UpdateAlertRuleParams{ObserveOnly: new(false)})
	if err != nil {
		t.Fatal(err)
	}
	now := rule.UpdatedAt.Add(time.Minute)
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200}}
	eval := func() *alerts.Evaluator {
		return alerts.NewEvaluator(alerts.EvaluatorOptions{
			Store: store, Identity: func() *age.X25519Identity { return ident },
			PromQL:     &anomalyProm{currentSamples: 400, pastSamples: 100, trafficDays: 7},
			Dispatcher: dispatch, Now: func() time.Time { return now }, Log: discardLog(),
		})
	}
	if stats, err := eval().RunOnce(ctx); err != nil || stats.Fired != 1 || dispatch.callCount() != 1 {
		t.Fatalf("initial fire: stats=%+v calls=%d err=%v", stats, dispatch.callCount(), err)
	}
	changed, err := store.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{
		ObserveOnly: new(true), EnvironmentScope: new("staging"), NotifyResolved: new(false),
		Name: new("staging rule"), WebhookURL: new("https://example.com/staging"), WebhookSecretSealed: new([]byte("rotated")),
	})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	dispatch.result = webhookout.Result{StatusCode: 503, Err: errors.New("receiver unavailable")}
	if stats, err := eval().RunOnce(ctx); err != nil || stats.ResolvesFailed != 1 || dispatch.callCount() != 2 {
		t.Fatalf("first terminal attempt: stats=%+v calls=%d err=%v", stats, dispatch.callCount(), err)
	}
	now = now.Add(time.Minute)
	dispatch.result = webhookout.Result{StatusCode: 200}
	if stats, err := eval().RunOnce(ctx); err != nil || stats.ResolvesDelivered != 1 || dispatch.callCount() != 3 {
		t.Fatalf("retried terminal attempt: stats=%+v calls=%d err=%v", stats, dispatch.callCount(), err)
	}
	first, resolved := dispatch.calls[1], dispatch.calls[2]
	if resolved.URL != rule.WebhookURL || resolved.EventID == "" || resolved.EventID != first.EventID ||
		resolved.Payload["event"] != "alert.resolved" || resolved.Payload["rule_name"] != rule.Name ||
		resolved.Payload["environment_scope"] != "production" || resolved.Payload["reason"] != state.StandDownDisabled ||
		resolved.Payload["resolved_at"] != changed.UpdatedAt.UTC().Format(time.RFC3339Nano) || resolved.Signer == nil {
		t.Fatalf("stand-down did not preserve the original webhook and episode: %+v", resolved)
	}
	want := webhookout.NewSigner(secret).Sign(1, resolved.EventID, nil)
	if got := resolved.Signer.Sign(1, resolved.EventID, nil); got != want {
		t.Fatal("stand-down must be signed with the previous webhook secret")
	}
	now = now.Add(time.Minute)
	if _, err := eval().RunOnce(ctx); err != nil || dispatch.callCount() != 3 {
		t.Fatalf("delivered terminal event was queued again: calls=%d err=%v", dispatch.callCount(), err)
	}
}
