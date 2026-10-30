package alerts_test

// adr: 20261029203000 — a rule with notify_resolved sends its webhook a signed
// alert.resolved event when it recovers; every fire says alert.fired; a rule
// without it gets fires only, as receivers written before it expect.

import (
	"context"
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
	ctx := context.Background()
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
			if _, err := store.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{NotifyResolved: &tc.notifyResolved}); err != nil {
				t.Fatal(err)
			}
			prom := &stubPromQL{value: 10}
			dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
			ev := resolveEvaluator(store, prom, dispatch, ident)
			if stats, err := ev.RunOnce(ctx); err != nil || stats.Fired != 1 {
				t.Fatalf("firing tick = %+v, %v", stats, err)
			}
			prom.value = 0
			stats, err := ev.RunOnce(ctx)
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
