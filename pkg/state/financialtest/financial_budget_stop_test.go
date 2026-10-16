package financialtest

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

type budgetStopStore interface {
	budgetMonitorStore
	state.FinancialBudgetStopStore
}

// adr: 566 — decisions survive corrections, restarts and UTC month boundaries;
// account ownership and independent holds survive every policy mutation.
func TestFinancialBudgetStops(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store budgetStopStore = state.NewMemStore()
			if backend == "postgres" {
				store, _, _ = financialPostgres(t)
			}
			financialBudgetStopSuite(t, store)
		})
	}
}

func financialBudgetStopSuite(t *testing.T, store budgetStopStore) {
	t.Helper()
	ctx := t.Context()
	a, other := financialAccount(t, store), financialAccount(t, store)
	preview, err := store.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "arbitrary-preview", PreviewOfSlug: "production", RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	production, err := store.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "pr-42", RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	spec := financialBudgetSpec()
	spec.Action, spec.DrainSeconds = "stop_previews", 30
	p, err := store.CreateFinancialBudget(ctx, a.ID, uuid.NewString(), "editor", spec)
	if err != nil {
		t.Fatal(err)
	}
	month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	now, amount := month.Add(time.Hour), int64(1000)
	e := financial.BudgetEvaluation{PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), AsOf: now, EvaluatedAt: now, KnownMillicents: &amount, Reasons: []string{}}
	if err := store.RecordFinancialBudgetEvaluation(ctx, a.ID, p.ID, p.Revision, e); err != nil {
		t.Fatal(err)
	}
	if stops, err := store.ActiveFinancialBudgetStops(ctx, a.ID, now); err != nil || len(stops) != 0 {
		t.Fatalf("stale evidence stopped work: %+v %v", stops, err)
	}
	e.Fresh = true // A known partial subtotal is sufficient to cross a threshold.
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if err := store.RecordFinancialBudgetEvaluation(ctx, a.ID, p.ID, p.Revision, e); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	read := func(at time.Time) state.FinancialBudgetStop {
		t.Helper()
		stops, err := store.ActiveFinancialBudgetStops(ctx, a.ID, at)
		if err != nil || len(stops) != 1 {
			t.Fatalf("latched decision: %+v %v", stops, err)
		}
		return stops[0]
	}
	stop := read(now)
	if !stop.DrainDeadline.Equal(now.Add(30*time.Second)) || !stop.Matches(preview, "") || stop.Matches(production, "") {
		t.Fatalf("preview selection/deadline: %+v", stop)
	}
	if stops, err := store.ActiveFinancialBudgetStops(ctx, other.ID, now); err != nil || len(stops) != 0 {
		t.Fatalf("foreign decisions: %+v %v", stops, err)
	}
	if err := state.CheckFinancialBudgetAdmission(ctx, store, preview, "", now); !errors.Is(err, state.ErrFinancialBudgetHeld) {
		t.Fatalf("preview admission: %v", err)
	}
	if err := state.CheckFinancialBudgetAdmission(ctx, store, production, "", now); err != nil {
		t.Fatalf("production stopped: %v", err)
	}
	// A downward correction cannot unlatch or rewrite the threshold evidence.
	amount = 1
	e.EvaluatedAt, e.AsOf = now.Add(time.Minute), now.Add(time.Minute)
	if err := store.RecordFinancialBudgetEvaluation(ctx, a.ID, p.ID, p.Revision, e); err != nil {
		t.Fatal(err)
	}
	if got := read(e.EvaluatedAt); got.ID != stop.ID || *got.Evaluation.KnownMillicents != 1000 {
		t.Fatalf("correction rewrote decision: %+v", got)
	}
	target := financial.BudgetStopTarget{AppID: preview.ID, State: "draining", ObservedAt: now, ResidentCount: 1}
	if err := store.RecordFinancialBudgetStopTarget(ctx, a.ID, stop.ID, target); err != nil {
		t.Fatal(err)
	}
	target.State = "enforced" // Never acknowledge success with resident resources.
	if err := store.RecordFinancialBudgetStopTarget(ctx, a.ID, stop.ID, target); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("false enforcement accepted: %v", err)
	}
	target.ResidentCount, target.ObservedAt = 0, now.Add(time.Minute)
	if err := store.RecordFinancialBudgetStopTarget(ctx, a.ID, stop.ID, target); err != nil {
		t.Fatal(err)
	}
	if rows, err := store.FinancialBudgetStopTargets(ctx, other.ID, stop.ID); err != nil || len(rows) != 0 {
		t.Fatalf("foreign target history: %+v %v", rows, err)
	}
	target.State, target.ObservedAt = "draining", now
	if err := store.RecordFinancialBudgetStopTarget(ctx, a.ID, stop.ID, target); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale scheduler overwrote acknowledgement: %v", err)
	}
	// Manual holds remain until an explicit policy mutation, even next month.
	read(e.PeriodEnd)
	spec.Enabled = false
	p, err = store.UpdateFinancialBudget(ctx, a.ID, p.ID, p.Revision, "editor", spec)
	if err != nil {
		t.Fatal(err)
	}
	if stops, err := store.ActiveFinancialBudgetStops(ctx, a.ID, now); err != nil || len(stops) != 0 {
		t.Fatalf("disabled hold retained: %+v %v", stops, err)
	}
	if err := store.RecordFinancialBudgetEvaluation(ctx, a.ID, p.ID, p.Revision-1, e); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("obsolete meter recreated hold: %v", err)
	}
	spec.Enabled, spec.ResumeRule = true, "next_period"
	p, err = store.UpdateFinancialBudget(ctx, a.ID, p.ID, p.Revision, "editor", spec)
	if err != nil {
		t.Fatal(err)
	}
	amount = 1000
	if err := store.RecordFinancialBudgetEvaluation(ctx, a.ID, p.ID, p.Revision, e); err != nil {
		t.Fatal(err)
	}
	read(e.EvaluatedAt)
	if stops, err := store.ActiveFinancialBudgetStops(ctx, a.ID, e.PeriodEnd); err != nil || len(stops) != 0 {
		t.Fatalf("UTC next-period resume: %+v %v", stops, err)
	}
}
