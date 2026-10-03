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
