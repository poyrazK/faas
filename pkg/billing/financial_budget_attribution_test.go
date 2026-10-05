package billing_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

type budgetAttributionTestStore struct {
	*state.MemStore
	retained, timeNow time.Time
}

func (s *budgetAttributionTestStore) FinancialEvidenceCoverage(context.Context) (time.Time, error) {
	return s.retained, nil
}
func (s *budgetAttributionTestStore) FinancialSamplingCoverage(ctx context.Context, start, end time.Time) (state.FinancialSamplingCoverage, error) {
	c, err := s.MemStore.FinancialSamplingCoverage(ctx, start, end)
	c.ObservedAt = s.timeNow.Add(-time.Minute)
	return c, err
}

// adr: 566 — completeness must include attribution coverage for selected scopes.
func TestEnvironmentSyntheticFloorCoverage(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	store := &budgetAttributionTestStore{MemStore: state.NewMemStore(), retained: start, timeNow: start.Add(24 * time.Hour)}
	account, err := store.CreateAccount(t.Context(), "environment-review@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "floor-project"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "warm-staging", Type: state.AppTypeApp, Runtime: "node22", RAMMB: 256, MinInstances: 1, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Status: state.DeployLive, Kind: state.DeploymentKindImage, Scope: "staging"}); err != nil {
		t.Fatal(err)
	}
	price := state.FinancialPriceSnapshot{AccountID: account.ID, PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0), EffectiveFrom: start, Plan: api.PlanHobby, DeliveryMode: "live", Price: financial.Price{Version: "review-v1", Meter: "compute", Currency: "EUR", Unit: "mb_seconds", UnitQuantity: 1, MillicentsPerUnit: 1}}
	if _, err := store.PutFinancialPriceSnapshot(t.Context(), price); err != nil {
		t.Fatal(err)
	}
	rows, err := meter.NewSampler(store, nil, func() time.Time { return start }).SampleAndRoll(t.Context())
	if err != nil || len(rows) != 1 || !rows[0].SyntheticFloor {
		t.Fatalf("floor fixture: %v %v", rows, err)
	}
	for at := start; at.Before(store.timeNow); at = at.Add(time.Minute) {
		if err := store.RecordFinancialSamplingWindow(t.Context(), at, true, false); err != nil {
			t.Fatal(err)
		}
	}
	spec := financial.BudgetSpec{Name: "staging", Scope: financial.BudgetScope{Kind: "environment", ID: env.ID}, Currency: "EUR", Meters: []string{"compute"}, Basis: "gross_usage", LimitMillicents: 1000, Mode: "monitored", Action: "suspend_workloads", ResumeRule: "manual", Enabled: true}
	out, err := billing.PreviewFinancialBudget(t.Context(), store, account.ID, spec, store.timeNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Targets) != 1 {
		t.Fatalf("environment membership fixture: %v", out.Targets)
	}
	if out.CoverageComplete || out.KnownMillicents != 0 || !slices.Contains(out.Reasons, "compute:missing_scope_attribution") {
		t.Fatalf("environment omitted attribution warning: %+v", out)
	}
	for _, scope := range []financial.BudgetScope{{Kind: "account"}, {Kind: "app", ID: app.ID}, {Kind: "project", ID: project.ID}} {
		spec.Scope = scope
		scoped, err := billing.PreviewFinancialBudget(t.Context(), store, account.ID, spec, store.timeNow)
		if err != nil || !scoped.CoverageComplete || scoped.KnownMillicents != rows[0].MBSeconds {
			t.Fatalf("authoritative %s allocation changed: %+v, %v", scope.Kind, scoped, err)
		}
	}
}
