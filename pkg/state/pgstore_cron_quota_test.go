package state_test

// PR #340 follow-up: round-trip CreateCronIfUnderQuota against a real
// Postgres cluster so a typo in the tx body, an off-by-one on the
// count, or a Scope mis-wire can't ship silently. The HTTP tests in
// cmd/apid/handlers_ext_test.go run against the in-process MemStore;
// this file is the only thing that proves the PgStore predicate
// matches the MemStore one byte-for-byte.
//
// Uses pgtest.Open so the whole file skips cleanly when Postgres is
// unreachable (same pattern as pgstore_account_deletion_test.go).

import (
	"errors"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgStore_CreateCronIfUnderQuota_PerAppCap fills one app to its
// per-app cron limit (Pro: 20) and asserts the next insert returns
// *state.CronQuotaError with Scope=App. The hand-written SQL in
// pgstore.go is the only thing exercising this — losing the partial
// index or the count predicate would silently raise the cap.
//
// Each seed cron uses a distinct path so the (app_id, schedule, path)
// UNIQUE constraint added in migration 00208 does not short-circuit
// the loop with pgx 23505 before the per-app count predicate fires.
// The constraint is the source-of-truth gate for the manifest dedupe
// primitive (pkg/gregalemanifest/manifest.go); the quota predicate
// (per-app, per-account) is a separate concern enforced at the
// CreateCron layer.
func TestPgStore_CreateCronIfUnderQuota_PerAppCap(t *testing.T) {
	s, ctx := pgStore(t)
	limits := api.MustLimitsFor(api.PlanPro) // 20/app, 50/acct
	acct, app, _ := seedLiveDeploy(t, s, ctx)
	for i := 0; i < limits.CronLimitPerApp; i++ {
		path := fmt.Sprintf("/cron-%d", i)
		if _, err := s.CreateCron(ctx, app, "*/5 * * * *", path, true); err != nil {
			t.Fatalf("seed cron %d: %v", i, err)
		}
	}
	_, err := s.CreateCronIfUnderQuota(ctx, app, "*/5 * * * *", "/beyond-cap", true, limits)
	if err == nil {
		t.Fatal("expected *CronQuotaError at per-app cap, got nil")
	}
	var qe *state.CronQuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("expected *CronQuotaError, got %T: %v", err, err)
	}
	if qe.Scope != state.CronQuotaScopeApp {
		t.Errorf("scope = %q, want %q", qe.Scope, state.CronQuotaScopeApp)
	}
	if qe.Limit != limits.CronLimitPerApp {
		t.Errorf("limit = %d, want %d", qe.Limit, limits.CronLimitPerApp)
	}
	if qe.Observed != limits.CronLimitPerApp {
		t.Errorf("observed = %d, want %d", qe.Observed, limits.CronLimitPerApp)
	}
	// errors.Is must match the sentinel — handlers depend on this.
	if !errors.Is(err, state.ErrCronQuotaExceeded) {
		t.Error("errors.Is(err, ErrCronQuotaExceeded) = false, want true")
	}
	_ = acct // suppress unused
}

// TestPgStore_CreateCronIfUnderQuota_PerAccountCap seeds crons across
// three apps on the same account so the next insert on the third app
// lands at the per-account cap (Pro: 50) with per-app still under.
// Proves the join through apps and the FOR UPDATE on the apps row
// together let the per-account count fire before the per-app cap.
//
// Each seed cron uses a distinct path (per-app) so the
// (app_id, schedule, path) UNIQUE constraint added in migration 00208
// does not short-circuit the loop with pgx 23505 before the per-account
// count predicate fires. See TestPgStore_CreateCronIfUnderQuota_PerAppCap
// for the rationale.
func TestPgStore_CreateCronIfUnderQuota_PerAccountCap(t *testing.T) {
	s, ctx := pgStore(t)
	limits := api.MustLimitsFor(api.PlanPro) // 20/app, 50/acct
	acct, _, _ := seedLiveDeploy(t, s, ctx)

	// Two more apps on the same account. Apps carry CHECK
	// constraints (apps_max_concurrency_check, etc.) so we set the
	// same shape as seedLiveDeploy's App row.
	appARec, err := s.CreateApp(ctx, state.App{
		AccountID: acct, Slug: "cron-acct-a", Type: state.AppTypeFunction,
		RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp A: %v", err)
	}
	appBRec, err := s.CreateApp(ctx, state.App{
		AccountID: acct, Slug: "cron-acct-b", Type: state.AppTypeFunction,
		RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp B: %v", err)
	}
	appA, appB := appARec.ID, appBRec.ID
	for i := 0; i < limits.CronLimitPerApp-1; i++ {
		path := fmt.Sprintf("/a-%d", i)
		if _, err := s.CreateCron(ctx, appA, "*/5 * * * *", path, true); err != nil {
			t.Fatalf("seed appA %d: %v", i, err)
		}
	}
	for i := 0; i < limits.CronLimitPerApp-1; i++ {
		path := fmt.Sprintf("/b-%d", i)
		if _, err := s.CreateCron(ctx, appB, "*/5 * * * *", path, true); err != nil {
			t.Fatalf("seed appB %d: %v", i, err)
		}
	}
	fillC := limits.CronLimitPerAccount - 2*(limits.CronLimitPerApp-1)
	for i := 0; i < fillC; i++ {
		path := fmt.Sprintf("/b-tail-%d", i)
		if _, err := s.CreateCron(ctx, appB, "*/5 * * * *", path, true); err != nil {
			t.Fatalf("seed appB tail %d: %v", i, err)
		}
	}
	// Now per-account is at the cap. Next insert via the *quota*
	// method on a third app must trip the account arm. The path is
	// distinct from anything seeded so a 23505 cannot mask the quota
	// error if the per-account count regressed.
	appCRec, err := s.CreateApp(ctx, state.App{
		AccountID: acct, Slug: "cron-acct-c", Type: state.AppTypeFunction,
		RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp C: %v", err)
	}
	appC := appCRec.ID
	_, err = s.CreateCronIfUnderQuota(ctx, appC, "*/5 * * * *", "/c-trigger", true, limits)
	if err == nil {
		t.Fatal("expected *CronQuotaError at per-account cap, got nil")
	}
	var qe *state.CronQuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("expected *CronQuotaError, got %T: %v", err, err)
	}
	if qe.Scope != state.CronQuotaScopeAccount {
		t.Errorf("scope = %q, want %q (per-account is at cap; per-app on appC is 0/20)", qe.Scope, state.CronQuotaScopeAccount)
	}
	if qe.Limit != limits.CronLimitPerAccount {
		t.Errorf("limit = %d, want %d", qe.Limit, limits.CronLimitPerAccount)
	}
	if qe.Observed != limits.CronLimitPerAccount {
		t.Errorf("observed = %d, want %d", qe.Observed, limits.CronLimitPerAccount)
	}
	_ = appC // suppress unused
}

// TestPgStore_CreateCronIfUnderQuota_AppDeletedReturnsNotFound guards
// the failure mode the doc comment calls out: a soft-deleted app must
// not be cron-creatable. Mirrors MemStore's predicate (status ==
// AppDeleted → ErrNotFound).
func TestPgStore_CreateCronIfUnderQuota_AppDeletedReturnsNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	limits := api.MustLimitsFor(api.PlanPro)
	_, app, _ := seedLiveDeploy(t, s, ctx)
	deleted := state.AppDeleted
	if _, err := s.UpdateApp(ctx, app, state.UpdateAppParams{Status: &deleted}); err != nil {
		t.Fatalf("UpdateApp Status=AppDeleted: %v", err)
	}
	_, err := s.CreateCronIfUnderQuota(ctx, app, "*/5 * * * *", "/x", true, limits)
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("expected ErrNotFound for soft-deleted app, got %v", err)
	}
}

func TestPgStore_CreateCronRetryIsIdempotentAtQuota(t *testing.T) {
	s, ctx := pgStore(t)
	_, app, _ := seedLiveDeploy(t, s, ctx)
	limits := api.Limits{CronLimitPerApp: 1, CronLimitPerAccount: 1}
	first, err := s.CreateCronIfUnderQuotaWithOptions(ctx, app, "0 3 * * *", "/tick", true, limits, state.CronOptions{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.CreateCronIfUnderQuotaWithOptions(ctx, app, "0 3 * * *", "/tick", true, limits, state.CronOptions{Timezone: "UTC"})
	if err != nil {
		t.Fatalf("identical retry at quota: %v", err)
	}
	if retry.ID != first.ID {
		t.Fatalf("retry ID = %q, want existing %q", retry.ID, first.ID)
	}
}
