package alerts_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

// seedSLORule creates an SLO on seedRule's app and replaces the seeded rule
// with one on metric naming that SLO.
func seedSLORule(t *testing.T, store *state.MemStore, metric state.AlertMetric, cmp state.AlertComparison, threshold float64) (state.AlertRule, state.SLO, *age.X25519Identity) {
	t.Helper()
	ctx := context.Background()
	base, ident, _ := seedRule(t, store, metric, cmp, threshold)
	def, err := store.CreateSLO(ctx, state.SLO{AccountID: base.AccountID, AppID: base.AppID, Name: "checkout", SLI: state.SLIAvailability, ObjectiveBP: 9990, WindowDays: 30}, api.MaxSLOsPerApp)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAlertRule(ctx, base.ID); err != nil {
		t.Fatal(err)
	}
	base.ID, base.Name, base.SLOID = "", base.Name+"-slo", def.ID
	rule, err := store.CreateAlertRule(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	return rule, def, ident
}

func runOnce(t *testing.T, store state.Store, prom *selectivePromQL, ident *age.X25519Identity, now time.Time) alerts.Stats {
	t.Helper()
	ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store:      store,
		PromQL:     prom,
		Audit:      audit.New(store, discardLog(), nil, "meterd"),
		Identity:   func() *age.X25519Identity { return ident },
		Dispatcher: &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}},
		Now:        func() time.Time { return now },
		Log:        discardLog(),
	})
	stats, err := ev.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return stats
}

func TestEvaluator_SLOBudgetBurnUsesTheSLOsObjective(t *testing.T) {
	store := state.NewMemStore()
	_, _, ident := seedSLORule(t, store, state.AlertMetricSLOBudgetBurn, state.AlertGt, 14.4)
	// 2% failing in both windows against a 99.9% objective: burn 20 > 14.4.
	// (ADR-082's fixed 99.5% panel would only see a burn of 4.)
	prom := &selectivePromQL{fn: func(q string) (float64, error) {
		if strings.Contains(q, "5..") {
			return 1000, nil
		}
		return 980, nil
	}}
	if stats := runOnce(t, store, prom, ident, time.Now()); stats.Fired != 1 {
		t.Fatalf("stats = %+v; want one fire", stats)
	}
}

func TestEvaluator_SLOBudgetRemaining(t *testing.T) {
	store := state.NewMemStore()
	rule, def, ident := seedSLORule(t, store, state.AlertMetricSLOBudgetRemaining, state.AlertLt, 25)
	now := def.CreatedAt.Add(3 * time.Hour)
	noProm := &selectivePromQL{fn: func(string) (float64, error) { t.Fatal("budget remaining must not query Prometheus"); return 0, nil }}

	// No recorded traffic yet: unknown, not 100% remaining.
	if stats := runOnce(t, store, noProm, ident, now); stats.Fired != 0 {
		t.Fatalf("no traffic fired: %+v", stats)
	}
	if got, _ := store.AlertRuleByID(context.Background(), rule.ID); got.State != state.AlertStateUnknown {
		t.Fatalf("state = %q, want unknown", got.State)
	}
	// 9992 of 10000 good against 99.9%: 20% of the budget left, below 25.
	hour := def.CreatedAt.UTC().Truncate(time.Hour).Add(time.Hour)
	if err := store.UpsertSLOHour(context.Background(), def.ID, hour, 9992, 10000); err != nil {
		t.Fatal(err)
	}
	if stats := runOnce(t, store, noProm, ident, now); stats.Fired != 1 {
		t.Fatalf("stats = %+v; want one fire", stats)
	}
}

func TestEvaluator_SLORuleForDeletedSLOIsUnknown(t *testing.T) {
	store := state.NewMemStore()
	rule, def, ident := seedSLORule(t, store, state.AlertMetricSLOBudgetBurn, state.AlertGt, 1)
	if err := store.DeleteSLO(context.Background(), def.AppID, def.ID); err != nil {
		t.Fatal(err)
	}
	prom := &selectivePromQL{fn: func(string) (float64, error) { return 0, nil }}
	if stats := runOnce(t, store, prom, ident, time.Now()); stats.Fired != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	if got, _ := store.AlertRuleByID(context.Background(), rule.ID); got.State != state.AlertStateUnknown {
		t.Fatalf("state = %q, want unknown", got.State)
	}
}
