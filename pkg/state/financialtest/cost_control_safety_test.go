package financialtest

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// Exercise the persisted contract with the same cases in both adapters. Avoid
// a new Store-sized interface or concrete-store embedding in a test fake.
func TestCostControlSafetyStores(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "postgres" {
				store, _, _ = financialPostgres(t)
			}
			t.Run("immutable_delivery_quantity", func(t *testing.T) { costControlIntentStore(t, store) })
			t.Run("notice_retry_and_fence", func(t *testing.T) { costControlNoticeStore(t, store) })
			t.Run("plan_round_trip", func(t *testing.T) { costControlPlanStore(t, store) })
			t.Run("disable_deleted_scope", func(t *testing.T) { costControlDeletedScope(t, store) })
			t.Run("image_run_inventory", func(t *testing.T) { costControlJobInventory(t, store) })
			t.Run("historical_alert_preserves_current_runtime", func(t *testing.T) { costControlHistoricalAlert(t, store) })
		})
	}
}

func costControlIntentStore(t *testing.T, store state.Store) {
	a, err := store.CreateAccount(t.Context(), uuid.NewString()+"@intent.example", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	intents := store.(state.BillingUsageIntentStore)
	hour := time.Now().UTC().Truncate(time.Hour)
	first, err := intents.ReserveBillingUsageIntent(t.Context(), "polar", a.ID, state.BillingMeterCompute, hour, 20)
	if err != nil || first != 20 {
		t.Fatalf("first=%d %v", first, err)
	}
	var wg sync.WaitGroup
	for _, q := range []int64{0, 50, 100} {
		wg.Go(func() {
			got, err := intents.ReserveBillingUsageIntent(t.Context(), "polar", a.ID, state.BillingMeterCompute, hour, q)
			if err != nil || got != 20 {
				t.Errorf("retry quantity=%d: got %d, %v", q, got, err)
			}
		})
	}
	wg.Wait()
	for _, q := range []int64{0, 50} {
		got, err := intents.ReserveBillingUsageIntent(t.Context(), "polar", a.ID, state.BillingMeterEgress, hour, q)
		if err != nil || got != 0 {
			t.Fatalf("zero intent changed: %d %v", got, err)
		}
	}
	got, err := intents.ReserveBillingUsageIntent(t.Context(), "stripe", a.ID, state.BillingMeterCompute, hour, 50)
	if err != nil || got != 50 {
		t.Fatalf("provider key not isolated: %d %v", got, err)
	}
}

func costControlNoticeStore(t *testing.T, store state.Store) {
	a, err := store.CreateAccount(t.Context(), uuid.NewString()+"@notice.example", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	holds := store.(state.SpendCapHoldStore)
	outbox := store.(state.SpendCapNoticeStore)
	now := time.Now().UTC().Add(time.Second)
	hold := state.SpendCapHold{AccountID: a.ID, Month: financial.ComputeMonth(now), CapCents: 1, ObservedCents: 2}
	for range 2 {
		if err := holds.PutSpendCapHold(t.Context(), hold); err != nil {
			t.Fatal(err)
		}
	}
	// Clearing enforcement must not erase a queued notice.
	if err := holds.ClearSpendCapHold(t.Context(), a.ID); err != nil {
		t.Fatal(err)
	}
	claim, err := outbox.ClaimSpendCapNotice(t.Context(), now)
	if err != nil || claim.AccountID != a.ID {
		t.Fatalf("claim=%+v %v", claim, err)
	}
	if _, err := outbox.ClaimSpendCapNotice(t.Context(), now); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("concurrent claim: %v", err)
	}
	later := now.Add(api.FinancialBudgetAlertLease + time.Second)
	recovered, err := outbox.ClaimSpendCapNotice(t.Context(), later)
	if err != nil || recovered.ID != claim.ID || recovered.LeaseToken == claim.LeaseToken {
		t.Fatalf("lost-ack recovery=%+v %v", recovered, err)
	}
	if err := outbox.SettleSpendCapNotice(t.Context(), claim.ID, claim.LeaseToken, later, true); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale settlement: %v", err)
	}
	if err := outbox.SettleSpendCapNotice(t.Context(), recovered.ID, recovered.LeaseToken, later, false); err != nil {
		t.Fatal(err)
	}
	if _, err := outbox.ClaimSpendCapNotice(t.Context(), later); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("retry backoff ignored: %v", err)
	}
	later = later.Add(api.FinancialBudgetAlertRetryBase)
	retry, err := outbox.ClaimSpendCapNotice(t.Context(), later)
	if err != nil {
		t.Fatal(err)
	}
	if err := outbox.SettleSpendCapNotice(t.Context(), retry.ID, retry.LeaseToken, later, true); err != nil {
		t.Fatal(err)
	}
	if err := holds.PutSpendCapHold(t.Context(), hold); err != nil {
		t.Fatal(err)
	}
	if _, err := outbox.ClaimSpendCapNotice(t.Context(), later.Add(time.Hour)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("delivered notice duplicated: %v", err)
	}
}

func costControlPlanStore(t *testing.T, store state.Store) {
	a, err := store.CreateAccount(t.Context(), uuid.NewString()+"@plan.example", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	history := store.(state.PlanActivationStore)
	start := time.Now().UTC().Add(-time.Minute)
	for _, plan := range []api.Plan{api.PlanFree, api.PlanHobby, api.PlanHobby} {
		if err := store.UpdateAccountPlan(t.Context(), a.ID, plan); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := history.ListBillingPlanActivations(t.Context(), a.ID, start, time.Now().UTC().Add(time.Minute))
	if err != nil || len(rows) != 2 {
		t.Fatalf("plan mutations=%+v %v", rows, err)
	}
	if rows[0].From != api.PlanHobby || rows[0].To != api.PlanFree || rows[1].From != api.PlanFree || rows[1].To != api.PlanHobby || rows[1].Sequence <= rows[0].Sequence {
		t.Fatalf("round trip lost: %+v", rows)
	}
	head, err := history.BillingPlanActivationHead(t.Context())
	if err != nil || head < rows[1].Sequence {
		t.Fatalf("cache invalidation head=%d %v", head, err)
	}
}

func costControlDeletedScope(t *testing.T, store state.Store) {
	a, err := store.CreateAccount(t.Context(), uuid.NewString()+"@scope.example", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: a.ID, Slug: "scope-" + uuid.NewString()[:8], RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	budgets := store.(state.FinancialBudgetStore)
	spec := financialBudgetSpec()
	spec.Scope = financial.BudgetScope{Kind: "app", ID: app.ID}
	p, err := budgets.CreateFinancialBudget(t.Context(), a.ID, uuid.NewString(), "test", spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteApp(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	spec.Enabled = false
	p, err = budgets.UpdateFinancialBudget(t.Context(), a.ID, p.ID, p.Revision, "test", spec)
	if err != nil || p.Spec.Enabled {
		t.Fatalf("disable=%+v %v", p, err)
	}
	spec.Enabled = true
	if _, err := budgets.UpdateFinancialBudget(t.Context(), a.ID, p.ID, p.Revision, "test", spec); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted scope reactivated: %v", err)
	}
	spec.Enabled = false
	spec.Scope.ID = uuid.NewString()
	if _, err := budgets.UpdateFinancialBudget(t.Context(), a.ID, p.ID, p.Revision, "test", spec); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unowned changed draft scope: %v", err)
	}
}

func costControlJobInventory(t *testing.T, store state.Store) {
	a, err := store.CreateAccount(t.Context(), uuid.NewString()+"@jobs.example", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.(state.ImageRunJobStore).JobEnsureImageRun(t.Context(), state.ImageRunEnsure{AccountID: a.ID, Name: "cost-image", ImageRef: "example.test/cost:v1", RAMMB: 128, TaskTimeoutSec: 30, MaxCached: 10})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.(state.FinancialBudgetJobLister).FinancialBudgetJobs(t.Context(), a.ID, api.FinancialAllocationMax+1)
	if err != nil || len(rows) != 1 || rows[0].ID != job.Job.ID {
		t.Fatalf("billing inventory=%+v %v", rows, err)
	}
	public, err := store.JobListByAccount(t.Context(), a.ID, 10, 0)
	if err != nil || len(public) != 0 {
		t.Fatalf("public filter changed: %+v %v", public, err)
	}
}

func costControlHistoricalAlert(t *testing.T, store state.Store) {
	a, err := store.CreateAccount(t.Context(), uuid.NewString()+"@history.example", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.(state.FinancialBudgetStore).CreateFinancialBudget(t.Context(), a.ID, uuid.NewString(), "test", financialBudgetSpec())
	if err != nil {
		t.Fatal(err)
	}
	monitor := store.(state.FinancialBudgetMonitorStore)
	current := financial.ComputeMonth(time.Now())
	now := current.Add(time.Hour)
	known := int64(0)
	e := financial.BudgetEvaluation{PeriodStart: current, PeriodEnd: current.AddDate(0, 1, 0), AsOf: now, EvaluatedAt: now, KnownMillicents: &known, Fresh: true}
	if err := monitor.RecordFinancialBudgetEvaluation(t.Context(), a.ID, p.ID, p.Revision, e); err != nil {
		t.Fatal(err)
	}
	prior := current.AddDate(0, -1, 0)
	crossed := int64(1000)
	e.PeriodStart, e.PeriodEnd, e.AsOf, e.KnownMillicents = prior, current, current, &crossed
	if err := monitor.RecordFinancialBudgetEvaluation(t.Context(), a.ID, p.ID, p.Revision, e); err != nil {
		t.Fatal(err)
	}
	runtime, err := monitor.FinancialBudgetRuntime(t.Context(), a.ID, p.ID, p.Revision, current)
	if err != nil || runtime.Evaluation == nil || *runtime.Evaluation.KnownMillicents != 0 {
		t.Fatalf("old month replaced current evaluation: %+v %v", runtime, err)
	}
	old, err := monitor.FinancialBudgetRuntime(t.Context(), a.ID, p.ID, p.Revision, prior)
	if err != nil || len(old.Alerts) != 2 {
		t.Fatalf("late thresholds not retained: %+v %v", old, err)
	}
}

func TestCostControlMigrationReplayPreservesDurableDecisions(t *testing.T) {
	store, pool, ctx := financialPostgres(t)
	a, err := store.CreateAccount(ctx, "cost-replay@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Second)
	hold := state.SpendCapHold{AccountID: a.ID, Month: financial.ComputeMonth(now), CapCents: 1, ObservedCents: 2}
	if err := store.PutSpendCapHold(ctx, hold); err != nil {
		t.Fatal(err)
	}
	notice, err := store.ClaimSpendCapNotice(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SettleSpendCapNotice(ctx, notice.ID, notice.LeaseToken, now, true); err != nil {
		t.Fatal(err)
	}
	hour := now.Truncate(time.Hour)
	if _, err := store.ReserveBillingUsageIntent(ctx, "polar", a.ID, state.BillingMeterCompute, hour, 20); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountPlan(ctx, a.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	before, err := store.BillingPlanActivationHead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261111170000000_cost_control_delivery_safety.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, stmt := range pgtest.MigrationStatements(string(raw), true) {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			t.Fatalf("replay: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	q, err := store.ReserveBillingUsageIntent(ctx, "polar", a.ID, state.BillingMeterCompute, hour, 50)
	if err != nil || q != 20 {
		t.Fatalf("replay changed intent: %d %v", q, err)
	}
	if _, err := store.ClaimSpendCapNotice(ctx, now.Add(time.Hour)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("replay duplicated notice: %v", err)
	}
	after, err := store.BillingPlanActivationHead(ctx)
	if err != nil || after != before {
		t.Fatalf("replay changed plan history: %d -> %d %v", before, after, err)
	}
}
