package alerts_test

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

func runAlertTick(t *testing.T, store *state.MemStore, value float64, at time.Time) alerts.Stats {
	t.Helper()
	ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store:      store,
		PromQL:     &stubPromQL{value: value},
		Audit:      audit.New(store, discardLog(), nil, "meterd"),
		Identity:   func() *age.X25519Identity { return nil }, // claim only; no dispatch
		Dispatcher: &recordingDispatcher{result: webhookout.Result{StatusCode: 200}},
		Now:        func() time.Time { return at },
		Log:        discardLog(),
	})
	stats, err := ev.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce at %s: %v", at, err)
	}
	return stats
}

// TestEvaluator_RefiresAfterCooldown — the cool-down bucket key was derived
// from last_fired_at, which only moves when a claim wins. After the first
// fire every later tick computed the same key and lost the claim, so a rule
// fired once and then stayed silent: through a sustained breach past its
// cool-down, and for any new incident after it resolved.
func TestEvaluator_RefiresAfterCooldown(t *testing.T) {
	store := state.NewMemStore()
	rule, _, _ := seedRule(t, store, state.AlertMetricErrorRate, state.AlertGt, 5)
	cooldown := time.Duration(rule.CooldownMinutes) * time.Minute
	if cooldown <= 0 {
		t.Fatalf("seeded rule has cooldown %d minutes", rule.CooldownMinutes)
	}
	t0 := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

	if s := runAlertTick(t, store, 10, t0); s.Fired != 1 {
		t.Fatalf("first breach: Fired = %d, want 1", s.Fired)
	}
	if s := runAlertTick(t, store, 10, t0.Add(cooldown/2)); s.Fired != 0 {
		t.Fatalf("inside cool-down: Fired = %d, want 0", s.Fired)
	}
	if s := runAlertTick(t, store, 10, t0.Add(cooldown+time.Minute)); s.Fired != 1 {
		t.Fatalf("sustained breach past cool-down: Fired = %d, want 1", s.Fired)
	}

	// Resolve, then a fresh incident well after the cool-down.
	runAlertTick(t, store, 1, t0.Add(3*cooldown))
	if s := runAlertTick(t, store, 10, t0.Add(5*cooldown)); s.Fired != 1 {
		t.Fatalf("new incident after resolve: Fired = %d, want 1", s.Fired)
	}
}

// Production-us hunt #9 (H9-94): an error-rate rule fired, resolved to ok,
// and a bad release then returned 80% 5xx inside the 15-minute cool-down.
// The cool-down correctly withheld a second delivery, but it returned before
// recording the breach, so the rule read "ok" for the whole incident.
func TestEvaluator_BreachInsideCooldownAfterResolveIsFiring(t *testing.T) {
	store := state.NewMemStore()
	rule, _, _ := seedRule(t, store, state.AlertMetricErrorRate, state.AlertGt, 5)
	t0 := time.Date(2026, 10, 12, 1, 10, 0, 0, time.UTC)

	if s := runAlertTick(t, store, 99, t0); s.Fired != 1 {
		t.Fatalf("first breach: Fired = %d, want 1", s.Fired)
	}
	runAlertTick(t, store, 0, t0.Add(3*time.Minute))
	if got := alertRuleState(t, store, rule.ID); got != state.AlertStateOk {
		t.Fatalf("after recovery: state = %q, want ok", got)
	}
	if s := runAlertTick(t, store, 80, t0.Add(6*time.Minute)); s.Fired != 0 {
		t.Fatalf("breach inside cool-down: Fired = %d, want 0 (cool-down dedupe)", s.Fired)
	}
	if got := alertRuleState(t, store, rule.ID); got != state.AlertStateFiring {
		t.Fatalf("breach inside cool-down: state = %q, want firing", got)
	}
}

func alertRuleState(t *testing.T, store *state.MemStore, id string) state.AlertState {
	t.Helper()
	rule, err := store.AlertRuleByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return rule.State
}
