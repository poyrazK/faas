package financialtest

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

type budgetMonitorStore interface {
	financialBudgetTestStore
	state.FinancialBudgetMonitorStore
}

// adr: 566 — the same contract runs against memory and migrated PostgreSQL.
func TestFinancialBudgetMonitoring(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store budgetMonitorStore = state.NewMemStore()
			if backend == "postgres" {
				store, _, _ = financialPostgres(t)
			}
			budgetMonitoringSuite(t, store)
		})
	}
}

func budgetMonitoringSuite(t *testing.T, store budgetMonitorStore) {
	t.Helper()
	ctx := t.Context()
	a, other := financialAccount(t, store), financialAccount(t, store)
	spec := financialBudgetSpec()
	spec.LimitMillicents, spec.NotifyMillicents = 100, []int64{80}
	p, err := store.CreateFinancialBudget(ctx, a.ID, uuid.NewString(), "editor", spec)
	if err != nil {
		t.Fatal(err)
	}
	month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	now, known := month.Add(time.Hour), int64(90)
	e := financial.BudgetEvaluation{PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), AsOf: now, EvaluatedAt: now, EvidenceThroughID: 10, KnownMillicents: &known, Fresh: true, Reasons: []string{"compute:missing_sampling_windows"}}
	staleSource := e
	staleSource.Fresh = false
	if err := store.RecordFinancialBudgetEvaluation(ctx, a.ID, p.ID, p.Revision, staleSource); err != nil {
		t.Fatal(err)
	}
	initial, err := store.FinancialBudgetRuntime(ctx, a.ID, p.ID, p.Revision, month)
	if err != nil || len(initial.Alerts) != 0 {
		t.Fatalf("stale source produced an alert: %+v, %v", initial, err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if err := store.RecordFinancialBudgetEvaluation(ctx, a.ID, p.ID, p.Revision, e); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	read := func(want int) financial.BudgetRuntime {
		t.Helper()
		r, err := store.FinancialBudgetRuntime(ctx, a.ID, p.ID, p.Revision, e.PeriodStart)
		if err != nil || r.Evaluation == nil || len(r.Alerts) != want {
			t.Fatalf("runtime: %+v, %v", r, err)
		}
		return r
	}
	r := read(1)
	if r.Evaluation.CoverageComplete || r.Evaluation.KnownMillicents == nil || *r.Evaluation.KnownMillicents != 90 {
		t.Fatalf("partial observation lost: %+v", r)
	}
	if _, err := store.FinancialBudgetRuntime(ctx, other.ID, p.ID, p.Revision, month); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign runtime: %v", err)
	}
	if err := store.RecordFinancialBudgetEvaluation(ctx, other.ID, p.ID, p.Revision, e); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign evaluation: %v", err)
	}

	// Exactly one claimant wins; a crashed claimant can be recovered and fenced.
	claims := make(chan state.FinancialBudgetAlertDelivery, 4)
	for range 4 {
		wg.Go(func() {
			claim, err := store.ClaimFinancialBudgetAlert(ctx, now)
			if err == nil {
				claims <- claim
			} else if !errors.Is(err, state.ErrNotFound) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	close(claims)
	if len(claims) != 1 {
		t.Fatalf("concurrent claims: %d", len(claims))
	}
	first := <-claims
	now = now.Add(api.FinancialBudgetAlertLease)
	second, err := store.ClaimFinancialBudgetAlert(ctx, now)
	if err != nil || second.ID != first.ID || second.LeaseToken == first.LeaseToken || second.Attempts != 2 {
		t.Fatalf("lease recovery: %+v, %v", second, err)
	}
	if err := store.SettleFinancialBudgetAlert(ctx, first.ID, first.LeaseToken, now, true); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("old worker settled: %v", err)
	}
	if err := store.SettleFinancialBudgetAlert(ctx, second.ID, second.LeaseToken, now, false); err != nil {
		t.Fatal(err)
	}
	r = read(1)
	if r.Alerts[0].State != "retrying" || r.Alerts[0].LastError != "mail_delivery_failed" {
		t.Fatalf("retry lost: %+v", r)
	}
	if _, err := store.ClaimFinancialBudgetAlert(ctx, now); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("retry ignored backoff: %v", err)
	}
	now = r.Alerts[0].NextAttemptAt
	third, err := store.ClaimFinancialBudgetAlert(ctx, now)
	if err != nil || third.ID != first.ID {
		t.Fatalf("retry: %+v, %v", third, err)
	}
	if err := store.SettleFinancialBudgetAlert(ctx, third.ID, third.LeaseToken, now, true); err != nil {
		t.Fatal(err)
	}
	if read(1).Alerts[0].State != "delivered" {
		t.Fatal("delivery not retained")
	}

	// A downward correction and recrossing cannot create a second threshold event.
	for _, value := range []int64{20, 90, 120} {
		known = value
		e.EvaluatedAt, e.AsOf = now.Add(time.Minute), now.Add(time.Minute)
		now = e.EvaluatedAt
		if err := store.RecordFinancialBudgetEvaluation(ctx, a.ID, p.ID, p.Revision, e); err != nil {
			t.Fatal(err)
		}
	}
	r = read(2) // configured 80 threshold plus the 100 limit, deduplicated.
	if *r.Alerts[0].Evaluation.KnownMillicents != 90 {
		t.Fatal("later corrections rewrote the original threshold evidence")
	}
	stale := e
	stale.EvaluatedAt = e.EvaluatedAt.Add(-time.Minute)
	stale.AsOf = stale.EvaluatedAt
	if err := store.RecordFinancialBudgetEvaluation(ctx, a.ID, p.ID, p.Revision, stale); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale observation accepted: %v", err)
	}

	// Disabled/deleted/edited policies cannot dispatch an obsolete revision.
	spec.Enabled = false
	p, err = store.UpdateFinancialBudget(ctx, a.ID, p.ID, p.Revision, "editor", spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordFinancialBudgetEvaluation(ctx, a.ID, p.ID, p.Revision-1, e); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("old revision accepted: %v", err)
	}
	if _, err := store.ClaimFinancialBudgetAlert(ctx, now.Add(time.Hour)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("disabled alert claimed: %v", err)
	}
	spec.Enabled = true
	p, err = store.UpdateFinancialBudget(ctx, a.ID, p.ID, p.Revision, "editor", spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordFinancialBudgetEvaluation(ctx, a.ID, p.ID, p.Revision, e); err != nil {
		t.Fatal(err)
	}
	read(2) // the new revision evaluates the existing monthly amount.

	// Monthly identity resets even when the new month's evidence head is lower.
	e.PeriodStart, e.PeriodEnd = month.AddDate(0, 1, 0), month.AddDate(0, 2, 0)
	e.AsOf, e.EvaluatedAt, e.EvidenceThroughID = e.PeriodStart.Add(time.Minute), e.PeriodStart.Add(time.Minute), 1
	if err := store.RecordFinancialBudgetEvaluation(ctx, a.ID, p.ID, p.Revision, e); err != nil {
		t.Fatal(err)
	}
	read(2)
	// Unavailable data is not retained as a zero-cost evaluation or new alert.
	e.KnownMillicents, e.Fresh, e.CoverageComplete = nil, false, false
	e.EvaluatedAt = e.EvaluatedAt.Add(time.Minute)
	e.Reasons = []string{"cost_evaluation_unavailable"}
	if err := store.RecordFinancialBudgetEvaluation(ctx, a.ID, p.ID, p.Revision, e); err != nil {
		t.Fatal(err)
	}
	if read(2).Evaluation.KnownMillicents != nil {
		t.Fatal("unavailable amount became zero")
	}
	// Deletion cancels new claims without erasing the audit or affecting holds.
	if _, err := store.DeleteFinancialBudget(ctx, a.ID, p.ID, p.Revision, "editor"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimFinancialBudgetAlert(ctx, e.EvaluatedAt.Add(time.Hour)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted policy delivered: %v", err)
	}
	// Final account erasure must also remove retained monitoring and outbox rows.
	if err := store.UpdateAccountStatus(ctx, a.ID, state.AccountDeletedPending); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAccount(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
}
