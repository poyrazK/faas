package billing

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 530 — selective consequences, retained scope spending and no preview writes.
func TestFinancialBudgetPreview(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	store := &visibilityTestStore{MemStore: state.NewMemStore(), retained: start, timeNow: start.Add(24 * time.Hour)}
	account, err := store.CreateAccount(t.Context(), "budget-preview@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	apps := []state.App{}
	for _, definition := range []state.App{
		{Slug: "critical-production", WorkloadClass: state.WorkloadClassHTTP, MinInstances: 1},
		{Slug: "random-preview-name", WorkloadClass: state.WorkloadClassHTTP, PreviewOfSlug: "critical-production", PreviewPrNumber: 123},
		{Slug: "worker", WorkloadClass: state.WorkloadClassWorker},
	} {
		definition.AccountID, definition.Type, definition.Runtime, definition.RAMMB, definition.MaxConcurrency = account.ID, state.AppTypeApp, "node22", 256, 1
		app, err := store.CreateApp(t.Context(), definition)
		if err != nil {
			t.Fatal(err)
		}
		apps = append(apps, app)
	}
	job, err := store.JobCreate(t.Context(), account.ID, "batch", "batch", "registry.example/batch:v1", []string{"/job"}, 256, 60, 1, 0, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	price := state.FinancialPriceSnapshot{AccountID: account.ID, PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0), Plan: api.PlanPro, EffectiveFrom: start, DeliveryMode: "live", Price: financial.Price{Version: "pro-v1", Meter: "compute", Currency: "EUR", Unit: "mb_seconds", UnitQuantity: 1, MillicentsPerUnit: 1, IncludedQuantity: 50}}
	if _, err := store.PutFinancialPriceSnapshot(t.Context(), price); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(t.Context(), account.ID, apps[1].ID, uuid.NewString(), start, 100, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendJobUsage(t.Context(), account.ID, job.ID, uuid.NewString(), start, 100, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	for at := start; at.Before(store.timeNow); at = at.Add(time.Minute) {
		if err := store.RecordFinancialSamplingWindow(t.Context(), at, true, false); err != nil {
			t.Fatal(err)
		}
	}
	spec := financial.BudgetSpec{Name: "cost guard", Scope: financial.BudgetScope{Kind: "account"}, Currency: "EUR", Meters: []string{"compute"}, Basis: "net_usage", LimitMillicents: 100, NotifyMillicents: []int64{80}, Mode: "monitored", Action: "notify", ResumeRule: "manual", Enabled: true}
	before, err := store.FinancialEvidenceHead(t.Context(), account.ID, start, price.PeriodEnd)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		action               string
		selected, continuing int
	}{
		{"notify", 4, 4}, {"stop_previews", 1, 3}, {"suspend_background", 2, 2}, {"reject_traffic", 2, 4}, {"suspend_workloads", 4, 0},
	} {
		t.Run(tc.action, func(t *testing.T) {
			spec.Action = tc.action
			out, err := PreviewFinancialBudget(t.Context(), store, account.ID, spec, store.timeNow)
			if err != nil || len(out.Targets) != tc.selected || len(out.ContinuingTargets) != tc.continuing || out.KnownMillicents != 150 || !out.KnownLimitReached || !out.CoverageComplete || out.EnforcementReady {
				t.Fatalf("preview: %+v, %v", out, err)
			}
			if tc.action == "stop_previews" && out.Targets[0].ID != apps[1].ID {
				t.Fatalf("selected production as preview: %+v", out.Targets)
			}
		})
	}
	spec.Enabled = false
	disabled, err := PreviewFinancialBudget(t.Context(), store, account.ID, spec, store.timeNow)
	if err != nil || len(disabled.Targets) != 0 || len(disabled.ContinuingTargets) != 4 {
		t.Fatalf("disabled policy would stop work: %+v, %v", disabled, err)
	}
	spec.Enabled = true
	spec.Scope, spec.Action, spec.Mode, spec.Basis = financial.BudgetScope{Kind: "job", ID: job.ID}, "suspend_background", "strict", "gross_usage"
	jobPreview, err := PreviewFinancialBudget(t.Context(), store, account.ID, spec, store.timeNow)
	if err != nil || jobPreview.KnownMillicents != 100 || len(jobPreview.Targets) != 1 || jobPreview.Targets[0].Kind != "job" || jobPreview.EnforcementReady {
		t.Fatalf("strict job preview: %+v, %v", jobPreview, err)
	}
	spec.Scope, spec.Action, spec.Mode, spec.Basis = financial.BudgetScope{Kind: "app", ID: apps[1].ID}, "stop_previews", "monitored", "net_usage"
	out, err := PreviewFinancialBudget(t.Context(), store, account.ID, spec, store.timeNow)
	if err != nil || out.KnownMillicents != 75 || len(out.Targets) != 1 {
		t.Fatalf("scope repeated allowance: %+v, %v", out, err)
	}
	other, err := store.CreateAccount(t.Context(), "foreign-budget-preview@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PreviewFinancialBudget(t.Context(), store, other.ID, spec, store.timeNow); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign preview: %v", err)
	}
	after, err := store.FinancialEvidenceHead(t.Context(), account.ID, start, price.PeriodEnd)
	if err != nil || after != before {
		t.Fatalf("preview wrote evidence: %d,%d,%v", before, after, err)
	}
	policies, err := store.ListFinancialBudgets(t.Context(), account.ID)
	if err != nil || len(policies) != 0 {
		t.Fatalf("preview saved policies: %+v,%v", policies, err)
	}
	readApps, err := store.ListApps(t.Context(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range apps {
		for _, got := range readApps {
			if got.ID == want.ID && !reflect.DeepEqual(got, want) {
				t.Fatalf("preview changed workload: %+v", got)
			}
		}
	}
	store.retained = start.Add(time.Minute)
	out, err = PreviewFinancialBudget(t.Context(), store, account.ID, spec, store.timeNow)
	if err != nil || out.CoverageComplete {
		t.Fatalf("incomplete evidence claimed complete: %+v,%v", out, err)
	}
}
