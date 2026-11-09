// adr: 20261102205122 — the reaper skips the strict compute sweep for an app
// it has nothing to apply to: no budget on its account, not a preview, no
// lease that ran out.
package sched_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestStrictComputeSweepSkipsAppsWithNothingToApply(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	newApp := func(email string, preview string) state.App {
		t.Helper()
		acct, err := store.CreateAccount(ctx, email, api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "idle-" + uuid.NewString()[:8], PreviewOfSlug: preview, RAMMB: 128, MaxConcurrency: 4})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	plain := newApp("plain@example.com", "")
	budgeted := newApp("budgeted@example.com", "")
	preview := newApp("preview@example.com", "production")
	spec := financial.BudgetSpec{Name: "Compute", Scope: financial.BudgetScope{Kind: "account"}, Currency: "USD", Meters: []string{"compute"},
		Basis: "gross_usage", LimitMillicents: 100, Mode: "monitored", Action: "stop_previews", ResumeRule: "manual", Enabled: true}
	if _, err := store.CreateFinancialBudget(ctx, budgeted.AccountID, uuid.NewString(), "editor", spec); err != nil {
		t.Fatal(err)
	}
	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	apps := []state.App{plain, budgeted, preview}
	for _, tc := range []struct {
		app  state.App
		idle bool
	}{
		{plain, true},
		{budgeted, false},
		{preview, false},
	} {
		if got := engine.StrictComputeSweepIdle(ctx, apps, tc.app); got != tc.idle {
			t.Errorf("app %s (preview %q): idle = %v, want %v", tc.app.Slug, tc.app.PreviewOfSlug, got, tc.idle)
		}
	}
}
