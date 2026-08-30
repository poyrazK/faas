package state_test

// PR #399 follow-up: round-trip the alert rules + deliveries surface
// against a real Postgres cluster so a typo in the tx body, a missing
// column scan, a wrong `idempotency_key` UNIQUE handling, or a
// `CountFailedInvocationsSince` predicate that misses the source
// filter can't ship silently. Mirrors pkg/state/pgstore_cron_quota_test.go
// — same pgtest.Open skip-when-no-pg pattern, same package.
//
// pgtest.Open skips the whole file when Postgres is unreachable so the
// CI matrix with -short / no-pg still passes; the make test-state-coverage
// gate runs with DATABASE_URL and these tests bump coverage above the
// 70% threshold for pkg/state.

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// pgSampleAlertRule is the simplest valid AlertRule the tests below
// use. Fresh per call so callers can mutate fields without aliasing.
func pgSampleAlertRule(accountID, appID string) state.AlertRule {
	return state.AlertRule{
		AccountID:           accountID,
		AppID:               appID,
		Name:                "rule-" + uuid.NewString(),
		Enabled:             true,
		Metric:              state.AlertMetricErrorRate,
		Comparison:          state.AlertGt,
		Threshold:           0.5,
		WindowSpec:          state.AlertWindow5m,
		WebhookURL:          "https://example.com/hook",
		WebhookSecretSealed: []byte("sealed-secret"),
		CooldownMinutes:     30,
	}
}

// TestPgStore_AlertRule_RoundTrip exercises Create + AlertRuleByID +
// Update + Delete. Proves the SELECT column order in scanAlertRuleCols
// matches the INSERT statement and that the delete-then-lookup returns
// ErrNotFound (no soft-delete residue).
func TestPgStore_AlertRule_RoundTrip(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx)

	created, err := s.CreateAlertRule(ctx, pgSampleAlertRule(acct, app))
	if err != nil {
		t.Fatalf("CreateAlertRule: %v", err)
	}
	if created.ID == "" {
		t.Errorf("expected non-empty id from CreateAlertRule")
	}
	if created.State != state.AlertStateOk {
		t.Errorf("default state = %q, want %q", created.State, state.AlertStateOk)
	}

	got, err := s.AlertRuleByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("AlertRuleByID: %v", err)
	}
	if got.Name != created.Name || got.Metric != state.AlertMetricErrorRate {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	enabled := false
	threshold := 0.9
	updated, err := s.UpdateAlertRule(ctx, created.ID, state.UpdateAlertRuleParams{
		Enabled:   &enabled,
		Threshold: &threshold,
	})
	if err != nil {
		t.Fatalf("UpdateAlertRule: %v", err)
	}
	if updated.Enabled || updated.Threshold != 0.9 {
		t.Errorf("update did not apply: %+v", updated)
	}

	if err := s.DeleteAlertRule(ctx, created.ID); err != nil {
		t.Fatalf("DeleteAlertRule: %v", err)
	}
	if _, err := s.AlertRuleByID(ctx, created.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("post-delete lookup = %v; want ErrNotFound", err)
	}
}

// TestPgStore_CreateAlertRuleIfUnderQuota_PerAppCap fills one app to
// its per-app alert limit (Pro: 10) and asserts the next insert
// returns *state.AlertRuleQuotaError with Scope=App. Pins the
// FOR UPDATE on apps + count predicate in pgstore.go:2336-2367.
func TestPgStore_CreateAlertRuleIfUnderQuota_PerAppCap(t *testing.T) {
	s, ctx := pgStore(t)
	limits := api.MustLimitsFor(api.PlanPro) // 10/app, 30/acct
	acct, app, _ := seedLiveDeploy(t, s, ctx)

	for i := 0; i < limits.AlertRuleLimitPerApp; i++ {
		if _, err := s.CreateAlertRule(ctx, pgSampleAlertRule(acct, app)); err != nil {
			t.Fatalf("seed alert #%d: %v", i, err)
		}
	}
	overflow := pgSampleAlertRule(acct, app)
	_, err := s.CreateAlertRuleIfUnderQuota(ctx, overflow, limits)
	if err == nil {
		t.Fatal("expected *AlertRuleQuotaError at per-app cap, got nil")
	}
	var qe *state.AlertRuleQuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("expected *AlertRuleQuotaError, got %T: %v", err, err)
	}
	if qe.Scope != state.AlertRuleQuotaScopeApp {
		t.Errorf("scope = %q, want %q", qe.Scope, state.AlertRuleQuotaScopeApp)
	}
	if qe.Limit != limits.AlertRuleLimitPerApp {
		t.Errorf("limit = %d, want %d", qe.Limit, limits.AlertRuleLimitPerApp)
	}
	if qe.Observed != limits.AlertRuleLimitPerApp {
		t.Errorf("observed = %d, want %d", qe.Observed, limits.AlertRuleLimitPerApp)
	}
	if !errors.Is(err, state.ErrAlertRuleQuotaExceeded) {
		t.Error("errors.Is(err, ErrAlertRuleQuotaExceeded) = false; want true")
	}
}

// TestPgStore_CreateAlertRuleIfUnderQuota_PerAccountCap seeds alerts
// across three apps on the same account so the next insert lands at
// the per-account cap with per-app still under. Proves the per-account
// join through apps + the FOR UPDATE row lock + per-app count
// together let the per-account count fire before the per-app cap.
func TestPgStore_CreateAlertRuleIfUnderQuota_PerAccountCap(t *testing.T) {
	s, ctx := pgStore(t)
	limits := api.MustLimitsFor(api.PlanPro) // 10/app, 30/acct
	acct, _, _ := seedLiveDeploy(t, s, ctx)

	// Two more apps on the same account.
	appARec, err := s.CreateApp(ctx, state.App{
		AccountID: acct, Slug: "alert-acct-a", Type: state.AppTypeFunction,
		RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp A: %v", err)
	}
	appBRec, err := s.CreateApp(ctx, state.App{
		AccountID: acct, Slug: "alert-acct-b", Type: state.AppTypeFunction,
		RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp B: %v", err)
	}
	appA, appB := appARec.ID, appBRec.ID
	for i := 0; i < limits.AlertRuleLimitPerApp-1; i++ {
		if _, err := s.CreateAlertRule(ctx, pgSampleAlertRule(acct, appA)); err != nil {
			t.Fatalf("seed appA #%d: %v", i, err)
		}
	}
	for i := 0; i < limits.AlertRuleLimitPerApp-1; i++ {
		if _, err := s.CreateAlertRule(ctx, pgSampleAlertRule(acct, appB)); err != nil {
			t.Fatalf("seed appB #%d: %v", i, err)
		}
	}
	// Now fill the remainder of the per-account cap.
	fillC := limits.AlertRuleLimitPerAccount - 2*(limits.AlertRuleLimitPerApp-1)
	for i := 0; i < fillC; i++ {
		if _, err := s.CreateAlertRule(ctx, pgSampleAlertRule(acct, appB)); err != nil {
			t.Fatalf("seed appB tail #%d: %v", i, err)
		}
	}

	// One more rule on a fourth app trips the per-account cap
	// while per-app is still under.
	appCRec, err := s.CreateApp(ctx, state.App{
		AccountID: acct, Slug: "alert-acct-c", Type: state.AppTypeFunction,
		RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp C: %v", err)
	}
	overflow := pgSampleAlertRule(acct, appCRec.ID)
	_, err = s.CreateAlertRuleIfUnderQuota(ctx, overflow, limits)
	if err == nil {
		t.Fatal("expected *AlertRuleQuotaError at per-account cap, got nil")
	}
	var qe *state.AlertRuleQuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("expected *AlertRuleQuotaError, got %T: %v", err, err)
	}
	if qe.Scope != state.AlertRuleQuotaScopeAccount {
		t.Errorf("scope = %q, want %q", qe.Scope, state.AlertRuleQuotaScopeAccount)
	}
	if qe.Limit != limits.AlertRuleLimitPerAccount {
		t.Errorf("limit = %d, want %d", qe.Limit, limits.AlertRuleLimitPerAccount)
	}
}

// TestPgStore_AlertRule_DuplicateNameRejected pins the
// (account_id, name) UNIQUE constraint — a second rule with the
// same name on the same account returns ErrConflict. MetStore unit
// test (TestMemStoreAlertRule_DuplicateNameRejected) covers the
// in-process path; this is the byte-for-byte Postgres equivalent.
func TestPgStore_AlertRule_DuplicateNameRejected(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx)

	r := pgSampleAlertRule(acct, app)
	r.Name = "duplicate-name"
	if _, err := s.CreateAlertRule(ctx, r); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if _, err := s.CreateAlertRule(ctx, r); !errors.Is(err, state.ErrConflict) {
		t.Errorf("duplicate: expected ErrConflict, got %v", err)
	}
}

// TestPgStore_AlertRule_ListAndFilter proves ListAlertRulesForAccount
// scopes to a single account (cross-account rows are excluded) and
// ListEnabledAlertRules returns only enabled=true rows across all
// accounts. The disabled-row discipline is the load-bearing detail
// for the meterd evaluator sweep — a disabled rule must never fire.
func TestPgStore_AlertRule_ListAndFilter(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx)

	other, err := s.CreateAccount(ctx, "alert-other-"+uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	otherApp, err := s.CreateApp(ctx, state.App{
		AccountID: other.ID, Slug: "alert-other", Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	enabled := pgSampleAlertRule(acct, app)
	enabled.Name = "alpha"
	if _, err := s.CreateAlertRule(ctx, enabled); err != nil {
		t.Fatalf("seed enabled: %v", err)
	}

	// disabled=true via UpdateAlertRuleParams.
	dis := pgSampleAlertRule(acct, app)
	dis.Name = "beta"
	dis.Enabled = true
	createdDis, err := s.CreateAlertRule(ctx, dis)
	if err != nil {
		t.Fatalf("seed disabled: %v", err)
	}
	enabledTrue := false
	if _, err := s.UpdateAlertRule(ctx, createdDis.ID, state.UpdateAlertRuleParams{Enabled: &enabledTrue}); err != nil {
		t.Fatalf("flip to disabled: %v", err)
	}

	cross := pgSampleAlertRule(other.ID, otherApp.ID)
	cross.Name = "gamma"
	if _, err := s.CreateAlertRule(ctx, cross); err != nil {
		t.Fatalf("seed cross: %v", err)
	}

	listA, err := s.ListAlertRulesForAccount(ctx, acct)
	if err != nil {
		t.Fatalf("ListAlertRulesForAccount: %v", err)
	}
	if len(listA) != 2 {
		t.Errorf("account list = %d entries (%+v), want 2", len(listA), listA)
	}
	listAll, err := s.ListEnabledAlertRules(ctx)
	if err != nil {
		t.Fatalf("ListEnabledAlertRules: %v", err)
	}
	if len(listAll) != 2 { // alpha (this acct) + gamma (other acct), excluding disabled beta
		t.Errorf("enabled list = %d entries (%+v), want 2", len(listAll), listAll)
	}
}

// TestPgStore_AlertRule_ClaimDedupe exercises ClaimAlertFire's load-bearing
// CTE: the first claim wins, a duplicate within the same
// idempotency_key bucket loses, a fresh bucket wins, and an unknown
// rule errors. See pgstore.go:2534 for the CTE shape — this test
// is the byte-for-byte parallel of the MemStore test and the only
// thing that catches a future edit which loses the WHERE clause on
// the UPDATE.
func TestPgStore_AlertRule_ClaimDedupe(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx)
	rule, err := s.CreateAlertRule(ctx, pgSampleAlertRule(acct, app))
	if err != nil {
		t.Fatalf("CreateAlertRule: %v", err)
	}

	t0 := time.Now()
	id1, won1, err := s.ClaimAlertFire(ctx, rule.ID, rule.ID+":bucket-1", []byte(`{"metric":"error_rate_pct"}`), 10.5, t0)
	if err != nil || !won1 || id1 == "" {
		t.Fatalf("first claim = (%q, %v, %v); want (nonempty, true, nil)", id1, won1, err)
	}
	id2, won2, err := s.ClaimAlertFire(ctx, rule.ID, rule.ID+":bucket-1", []byte(`{"metric":"error_rate_pct"}`), 10.5, t0.Add(time.Second))
	if err != nil || won2 || id2 != "" {
		t.Fatalf("duplicate claim = (%q, %v, %v); want (\"\", false, nil)", id2, won2, err)
	}
	id3, won3, err := s.ClaimAlertFire(ctx, rule.ID, rule.ID+":bucket-2", []byte(`{"metric":"error_rate_pct"}`), 10.5, t0.Add(time.Hour))
	if err != nil || !won3 || id3 == "" || id3 == id1 {
		t.Fatalf("next-bucket claim = (%q, %v, %v); want (nonempty, true, nil) and a new id", id3, won3, err)
	}
	idMissing, wonMissing, err := s.ClaimAlertFire(ctx, "00000000-0000-0000-0000-000000000000", "x:bucket", nil, 0, t0)
	if err == nil || wonMissing || idMissing != "" {
		t.Errorf("unknown rule should error; got (%q, %v, %v)", idMissing, wonMissing, err)
	}
}

// TestPgStore_AlertRule_StateTransition pins the SetAlertRuleState
// path: ok→firing, repeat is a no-op, firing→ok reverts, missing
// rule errors with ErrNotFound.
func TestPgStore_AlertRule_StateTransition(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx)
	rule, err := s.CreateAlertRule(ctx, pgSampleAlertRule(acct, app))
	if err != nil {
		t.Fatal(err)
	}

	changed, err := s.SetAlertRuleState(ctx, rule.ID, state.AlertStateFiring, time.Now())
	if err != nil || !changed {
		t.Fatalf("first transition = (%v, %v); want (true, nil)", changed, err)
	}
	changed, err = s.SetAlertRuleState(ctx, rule.ID, state.AlertStateFiring, time.Now())
	if err != nil || changed {
		t.Errorf("repeat = (%v, %v); want (false, nil)", changed, err)
	}
	changed, err = s.SetAlertRuleState(ctx, rule.ID, state.AlertStateOk, time.Now())
	if err != nil || !changed {
		t.Errorf("revert = (%v, %v); want (true, nil)", changed, err)
	}
	if _, err := s.SetAlertRuleState(ctx, "00000000-0000-0000-0000-000000000000", state.AlertStateFiring, time.Now()); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("missing rule = %v; want ErrNotFound", err)
	}
}

// TestPgStore_AlertDelivery_RecordAndUpdate exercises the alert_deliveries
// INSERT, the idempotency_key UNIQUE rejection, the UPDATE path that
// stamps last_status_code + last_error + delivered_at, and the
// ListAlertDeliveriesForRule ordering/limit. A typo in any of the
// UPDATE column lists would let an update silently no-op and fail
// the assertion here, not at runtime in meterd.
func TestPgStore_AlertDelivery_RecordAndUpdate(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx)
	rule, err := s.CreateAlertRule(ctx, pgSampleAlertRule(acct, app))
	if err != nil {
		t.Fatal(err)
	}

	d := state.AlertDelivery{
		RuleID:         rule.ID,
		AccountID:      acct,
		AppID:          app,
		IdempotencyKey: rule.ID + ":bucket-1",
		Payload:        []byte(`{"event":"test"}`),
		ObservedValue:  0.7,
	}
	if _, err := s.RecordAlertDelivery(ctx, d); err != nil {
		t.Fatalf("record: %v", err)
	}
	if _, err := s.RecordAlertDelivery(ctx, d); !errors.Is(err, state.ErrConflict) {
		t.Errorf("duplicate idempotency_key = %v; want ErrConflict", err)
	}

	list, err := s.ListAlertDeliveriesForRule(ctx, rule.ID, 50, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list: %d entries, want 1", len(list))
	}
	if list[0].Status != state.AlertDeliveryPending {
		t.Errorf("default status = %q, want pending", list[0].Status)
	}

	deliveredAt := time.Now()
	if err := s.UpdateAlertDeliveryStatus(ctx, list[0].ID, state.AlertDeliveryDelivered, 1, 200, "", &deliveredAt); err != nil {
		t.Fatalf("update: %v", err)
	}
	list, _ = s.ListAlertDeliveriesForRule(ctx, rule.ID, 50, false)
	if list[0].Status != state.AlertDeliveryDelivered || list[0].LastStatusCode != 200 {
		t.Errorf("status update didn't land: %+v", list[0])
	}
}

// TestPgStore_AlertRule_CountFailedInvocationsSince exercises the
// meterd-side Postgres predicate that backs the failed_invocations
// metric. Pins account-scoped, app-scoped, source filter, and the
// `since` lower bound. Mirrors the MemStore test at
// memstore_alert_rules_test.go:310 and is the only thing that catches
// a future drift between the two stores on the source filter —
// cron/queue/delayed_task/async_invoke must each select independently.
func TestPgStore_AlertRule_CountFailedInvocationsSince(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, depID := seedLiveDeploy(t, s, ctx)

	t0 := time.Now().Add(-time.Minute)
	// 2 failed cron invocations on this account+app.
	for i := 0; i < 2; i++ {
		inv, err := s.EnqueueInvocation(ctx, state.Invocation{
			AppID: app, AccountID: acct, Source: state.InvocationCron,
			State: state.InvocationPending, Method: "POST", Path: "/",
			CreatedAt: t0,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.FailInvocation(ctx, inv.ID, "boom", 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	// 1 successful cron invocation on this account+app — must NOT
	// count, otherwise the predicate dropped the state filter.
	if _, err := s.EnqueueInvocation(ctx, state.Invocation{
		AppID: app, AccountID: acct, Source: state.InvocationCron,
		State: state.InvocationPending, Method: "POST", Path: "/",
		CreatedAt: t0,
	}); err != nil {
		t.Fatal(err)
	}
	// 1 failed queue invocation on this account+app, different source.
	qInv, err := s.EnqueueInvocation(ctx, state.Invocation{
		AppID: app, AccountID: acct, Source: state.InvocationQueue,
		State: state.InvocationPending, Method: "POST", Path: "/",
		CreatedAt: t0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FailInvocation(ctx, qInv.ID, "boom", 0, 0); err != nil {
		t.Fatal(err)
	}

	// Failed cross-account row — must NOT count.
	other, err := s.CreateAccount(ctx, "alert-other2-"+uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	otherApp, err := s.CreateApp(ctx, state.App{
		AccountID: other.ID, Slug: "alert-other2", Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: otherApp.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployPending,
	}); err != nil {
		t.Fatal(err)
	}
	xInv, err := s.EnqueueInvocation(ctx, state.Invocation{
		AppID: otherApp.ID, AccountID: other.ID, Source: state.InvocationCron,
		State: state.InvocationPending, Method: "POST", Path: "/",
		CreatedAt: t0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FailInvocation(ctx, xInv.ID, "boom", 0, 0); err != nil {
		t.Fatal(err)
	}

	// App-scoped, cron source → 2 (the two failed cron invocations
	// on this account+app).
	n, err := s.CountFailedInvocationsSince(ctx, acct, app, state.InvocationCron, t0.Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("app-scoped cron count = %d, want 2", n)
	}
	// App-scoped, queue source → 1.
	n, err = s.CountFailedInvocationsSince(ctx, acct, app, state.InvocationQueue, t0.Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("app-scoped queue count = %d, want 1", n)
	}
	// Account-wide (empty appID) cron → 2.
	n, err = s.CountFailedInvocationsSince(ctx, acct, "", state.InvocationCron, t0.Add(-time.Second))
	if err != nil || n != 2 {
		t.Errorf("acct-wide cron count = %d (%v), want 2", n, err)
	}
	// Since in the future → 0.
	n, err = s.CountFailedInvocationsSince(ctx, acct, app, state.InvocationCron, t0.Add(time.Hour))
	if err != nil || n != 0 {
		t.Errorf("count after `since` in the future = %d (%v); want 0", n, err)
	}

	_ = depID // suppress unused (seedLiveDeploy returns it)
}

// TestPgStore_AlertRule_SetLastEvaluatedAndDelete exercises the
// remaining two Store methods so the test coverage gate has no
// uncovered branches: SetAlertRuleLastEvaluated stamps the column
// without disturbing state, and DeleteAlertRule on a missing id
// returns ErrNotFound.
func TestPgStore_AlertRule_SetLastEvaluatedAndDelete(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx)
	rule, err := s.CreateAlertRule(ctx, pgSampleAlertRule(acct, app))
	if err != nil {
		t.Fatal(err)
	}

	t0 := time.Now()
	if err := s.SetAlertRuleLastEvaluated(ctx, rule.ID, t0); err != nil {
		t.Fatalf("SetAlertRuleLastEvaluated: %v", err)
	}
	got, err := s.AlertRuleByID(ctx, rule.ID)
	if err != nil {
		t.Fatalf("AlertRuleByID: %v", err)
	}
	if got.LastEvaluatedAt.IsZero() {
		t.Errorf("last_evaluated_at was not stamped")
	}
	if got.State != state.AlertStateOk {
		t.Errorf("last_evaluated_at update should not change state; got %q", got.State)
	}

	// Missing rule → ErrNotFound on SetAlertRuleLastEvaluated.
	if err := s.SetAlertRuleLastEvaluated(ctx, "00000000-0000-0000-0000-000000000000", time.Now()); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("SetAlertRuleLastEvaluated missing = %v; want ErrNotFound", err)
	}
	if err := s.DeleteAlertRule(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("DeleteAlertRule missing = %v; want ErrNotFound", err)
	}
}
