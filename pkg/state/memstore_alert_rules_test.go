package state

// Coverage for the MemStore side of the alert_rules +
// alert_deliveries surface introduced in issue #396 / ADR-045.
// Mirror pattern of memstore_coverage_test.go:190 (TestMemStoreCoverageDomainsAndCrons)
// so the same fixture / no-pg path covers both stores.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func alertFixture(t *testing.T) (m *MemStore, ctx context.Context, account Account, app App) {
	t.Helper()
	ctx = context.Background()
	m = NewMemStore()
	acct, err := m.CreateAccount(ctx, "alert-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	a, err := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "alert-" + uuid.NewString(),
		RAMMB: 512, Status: AppActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	return m, ctx, acct, a
}

// memSampleRule is the simplest valid AlertRule the tests use.
// Fresh per call so callers can mutate fields without aliasing.
func memSampleRule(accountID, appID string) AlertRule {
	return AlertRule{
		AccountID:           accountID,
		AppID:               appID,
		Name:                "rule-" + uuid.NewString(),
		Enabled:             true,
		Metric:              AlertMetricErrorRate,
		Comparison:          AlertGt,
		Threshold:           0.5,
		WindowSpec:          AlertWindow5m,
		WebhookURL:          "https://example.com/hook",
		WebhookSecretSealed: []byte("sealed-secret"),
		CooldownMinutes:     30,
	}
}

func TestMemStoreAlertRule_RoundTrip(t *testing.T) {
	m, ctx, acct, app := alertFixture(t)

	created, err := m.CreateAlertRule(ctx, memSampleRule(acct.ID, app.ID))
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}
	if created.ID == "" {
		t.Errorf("expected non-empty id from CreateAlertRule")
	}
	if created.State != AlertStateOk {
		t.Errorf("default state = %q, want %q", created.State, AlertStateOk)
	}

	got, err := m.AlertRuleByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("AlertRuleByID: %v", err)
	}
	if got.Name != created.Name || got.Metric != AlertMetricErrorRate {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	enabled := false
	threshold := 0.9
	updated, err := m.UpdateAlertRule(ctx, created.ID, UpdateAlertRuleParams{
		Enabled:   &enabled,
		Threshold: &threshold,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Enabled || updated.Threshold != 0.9 {
		t.Errorf("update did not apply: %+v", updated)
	}

	rotated := []byte("rotated-secret")
	more, err := m.UpdateAlertRule(ctx, created.ID, UpdateAlertRuleParams{WebhookSecretSealed: &rotated})
	if err != nil {
		t.Fatalf("rotate secret: %v", err)
	}
	if string(more.WebhookSecretSealed) != "rotated-secret" {
		t.Errorf("secret did not rotate: %q", string(more.WebhookSecretSealed))
	}

	// nil-pointer fields leave the row untouched. The previous
	// updates set Enabled=false and Threshold=0.9; this update only
	// sets Name, so those must remain.
	name := "preserved"
	more, err = m.UpdateAlertRule(ctx, created.ID, UpdateAlertRuleParams{Name: &name})
	if err != nil {
		t.Fatalf("name update: %v", err)
	}
	if more.Name != "preserved" || more.Enabled || more.Threshold != 0.9 {
		t.Errorf("nil-pointer side-effects: %+v", more)
	}

	if err := m.DeleteAlertRule(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := m.AlertRuleByID(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("post-delete lookup = %v", err)
	}
}

func TestMemStoreAlertRule_QuotaEnforced(t *testing.T) {
	m, ctx, acct, app := alertFixture(t)
	limits := api.Limits{
		AlertRuleLimitPerApp:     2,
		AlertRuleLimitPerAccount: 3,
	}

	// App-scoped rule fills the per-app cap exactly.
	for i := 0; i < 2; i++ {
		rule := memSampleRule(acct.ID, app.ID)
		rule.Name = "app-cap-" + uuid.NewString()
		if _, err := m.CreateAlertRuleIfUnderQuota(ctx, rule, limits); err != nil {
			t.Fatalf("create #%d: %v", i, err)
		}
	}
	overflow := memSampleRule(acct.ID, app.ID)
	if _, err := m.CreateAlertRuleIfUnderQuota(ctx, overflow, limits); err == nil {
		t.Errorf("per-app cap: third rule should fail")
	} else {
		var qerr *AlertRuleQuotaError
		if !errors.As(err, &qerr) || qerr.Scope != AlertRuleQuotaScopeApp {
			t.Errorf("expected *AlertRuleQuotaError(Scope=app), got %T %v", err, err)
		}
	}

	// Account-wide rule bypasses per-app; trips per-account cap.
	// With 2 app-scoped rules already inserted, the per-account
	// cap of 3 admits exactly one acct-wide rule before failing.
	first := memSampleRule(acct.ID, "")
	first.Name = "acct-cap-1"
	if _, err := m.CreateAlertRuleIfUnderQuota(ctx, first, limits); err != nil {
		t.Fatalf("acct-wide create first: %v", err)
	}
	second := memSampleRule(acct.ID, "")
	second.Name = "acct-cap-2"
	if _, err := m.CreateAlertRuleIfUnderQuota(ctx, second, limits); err == nil {
		t.Errorf("per-account cap: fourth rule (2 app + 2 acct) should fail at limit=3")
	} else {
		var qerr *AlertRuleQuotaError
		if !errors.As(err, &qerr) || qerr.Scope != AlertRuleQuotaScopeAccount {
			t.Errorf("expected *AlertRuleQuotaError(Scope=account), got %T %v", err, err)
		}
	}

	// errors.Is should match the sentinel for handler use.
	overflow = memSampleRule(acct.ID, "")
	if _, err := m.CreateAlertRuleIfUnderQuota(ctx, overflow, limits); !errors.Is(err, ErrAlertRuleQuotaExceeded) {
		t.Errorf("expected errors.Is(err, ErrAlertRuleQuotaExceeded) = true; got %v", err)
	}
}

func TestMemStoreAlertRule_DuplicateNameRejected(t *testing.T) {
	m, ctx, acct, app := alertFixture(t)
	r := memSampleRule(acct.ID, app.ID)
	r.Name = "duplicate"
	if _, err := m.CreateAlertRule(ctx, r); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if _, err := m.CreateAlertRule(ctx, r); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate: expected ErrConflict, got %v", err)
	}
}

func TestMemStoreAlertRule_ListAndFilter(t *testing.T) {
	m, ctx, acct, app := alertFixture(t)
	otherAcct, _ := m.CreateAccount(ctx, "other-"+uuid.NewString()+"@example.com", api.PlanHobby)
	otherApp, _ := m.CreateApp(ctx, App{
		AccountID: otherAcct.ID, Slug: "other-" + uuid.NewString(),
		RAMMB: 256, Status: AppActive,
	})

	a1 := memSampleRule(acct.ID, app.ID)
	a1.Name = "alpha"
	a2 := memSampleRule(acct.ID, "")
	a2.Name = "beta"
	a3 := memSampleRule(otherAcct.ID, otherApp.ID)
	a3.Name = "gamma"
	for _, r := range []AlertRule{a1, a2, a3} {
		if _, err := m.CreateAlertRule(ctx, r); err != nil {
			t.Fatalf("seed %s: %v", r.Name, err)
		}
	}

	listA, err := m.ListAlertRulesForAccount(ctx, acct.ID)
	if err != nil || len(listA) != 2 {
		t.Errorf("ListAlertRulesForAccount = %d entries (%v)", len(listA), listA)
	}
	listAll, err := m.ListEnabledAlertRules(ctx)
	if err != nil || len(listAll) != 3 {
		t.Errorf("ListEnabledAlertRules = %d entries (%v)", len(listAll), listAll)
	}
}

func TestMemStoreAlertRule_ClaimDedupe(t *testing.T) {
	m, ctx, acct, app := alertFixture(t)
	rule, err := m.CreateAlertRule(ctx, memSampleRule(acct.ID, app.ID))
	if err != nil {
		t.Fatal(err)
	}

	t0 := time.Now()
	id1, won1, err := m.ClaimAlertFire(ctx, rule.ID, rule.ID+":bucket-1", []byte(`{"metric":"error_rate_pct"}`), 10.5, t0)
	if err != nil || !won1 || id1 == "" {
		t.Fatalf("first claim = (%q, %v, %v); want (nonempty, true, nil)", id1, won1, err)
	}
	id2, won2, err := m.ClaimAlertFire(ctx, rule.ID, rule.ID+":bucket-1", []byte(`{"metric":"error_rate_pct"}`), 10.5, t0.Add(time.Second))
	if err != nil || won2 || id2 != "" {
		t.Fatalf("duplicate claim = (%q, %v, %v); want (\"\", false, nil)", id2, won2, err)
	}
	id3, won3, err := m.ClaimAlertFire(ctx, rule.ID, rule.ID+":bucket-2", []byte(`{"metric":"error_rate_pct"}`), 10.5, t0.Add(time.Hour))
	if err != nil || !won3 || id3 == "" || id3 == id1 {
		t.Fatalf("next-bucket claim = (%q, %v, %v); want (nonempty, true, nil) and a new id", id3, won3, err)
	}
	idMissing, wonMissing, err := m.ClaimAlertFire(ctx, "missing-rule", "x:bucket", nil, 0, t0)
	if err == nil || wonMissing || idMissing != "" {
		t.Errorf("unknown rule should error; got (%q, %v, %v)", idMissing, wonMissing, err)
	}
}

func TestMemStoreAlertRule_StateTransition(t *testing.T) {
	m, ctx, acct, app := alertFixture(t)
	rule, err := m.CreateAlertRule(ctx, memSampleRule(acct.ID, app.ID))
	if err != nil {
		t.Fatal(err)
	}

	changed, err := m.SetAlertRuleState(ctx, rule.ID, AlertStateFiring, time.Now())
	if err != nil || !changed {
		t.Fatalf("first transition = (%v, %v); want (true, nil)", changed, err)
	}
	// no-op transition
	changed, err = m.SetAlertRuleState(ctx, rule.ID, AlertStateFiring, time.Now())
	if err != nil || changed {
		t.Errorf("repeat = (%v, %v); want (false, nil)", changed, err)
	}
	// real transition back
	changed, err = m.SetAlertRuleState(ctx, rule.ID, AlertStateOk, time.Now())
	if err != nil || !changed {
		t.Errorf("revert = (%v, %v); want (true, nil)", changed, err)
	}
	// missing rule
	_, err = m.SetAlertRuleState(ctx, "missing", AlertStateFiring, time.Now())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("missing rule = %v; want ErrNotFound", err)
	}
}

func TestMemStoreAlertDelivery_RecordAndUpdate(t *testing.T) {
	m, ctx, acct, app := alertFixture(t)
	rule, err := m.CreateAlertRule(ctx, memSampleRule(acct.ID, app.ID))
	if err != nil {
		t.Fatal(err)
	}

	d := AlertDelivery{
		RuleID:         rule.ID,
		AccountID:      acct.ID,
		AppID:          app.ID,
		IdempotencyKey: rule.ID + ":bucket-1",
		ObservedValue:  0.7,
	}
	if _, err := m.RecordAlertDelivery(ctx, d); err != nil {
		t.Fatalf("record: %v", err)
	}
	// duplicate idempotency_key → ErrConflict
	if _, err := m.RecordAlertDelivery(ctx, d); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate idempotency_key = %v; want ErrConflict", err)
	}

	list, err := m.ListAlertDeliveriesForRule(ctx, rule.ID, 50, false)
	if err != nil || len(list) != 1 {
		t.Errorf("list: %d entries (%v)", len(list), list)
	}
	if list[0].Status != AlertDeliveryPending {
		t.Errorf("default status = %q, want pending", list[0].Status)
	}

	deliveredAt := time.Now()
	if err := m.UpdateAlertDeliveryStatus(ctx, list[0].ID, AlertDeliveryDelivered, 1, 200, "", &deliveredAt); err != nil {
		t.Fatalf("update: %v", err)
	}
	list, _ = m.ListAlertDeliveriesForRule(ctx, rule.ID, 50, false)
	if list[0].Status != AlertDeliveryDelivered || list[0].LastStatusCode != 200 {
		t.Errorf("status update didn't land: %+v", list[0])
	}

	if err := m.UpdateAlertDeliveryStatus(ctx, "missing", AlertDeliveryFailed, 5, 500, "boom", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing update = %v; want ErrNotFound", err)
	}
}

func TestMemStoreAlertRule_CountFailedInvocationsSince(t *testing.T) {
	m, ctx, acct, app := alertFixture(t)

	t0 := time.Now().Add(-time.Minute)
	// Seed three failed invocations: 2 on this account+app, 1 on
	// another account. The "since this minute" filter must select
	// the 2 on this account and exclude the 1 on the other.
	for i := 0; i < 2; i++ {
		inv, err := m.EnqueueInvocation(ctx, Invocation{
			AppID: app.ID, AccountID: acct.ID, Source: InvocationCron,
			State: InvocationPending, Method: "POST", Path: "/",
			CreatedAt: t0,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := m.FailInvocation(ctx, inv.ID, "boom", 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	otherAcct, _ := m.CreateAccount(ctx, "other-"+uuid.NewString()+"@example.com", api.PlanHobby)
	otherApp, _ := m.CreateApp(ctx, App{
		AccountID: otherAcct.ID, Slug: "x-" + uuid.NewString(),
		RAMMB: 256, Status: AppActive,
	})
	inv, _ := m.EnqueueInvocation(ctx, Invocation{
		AppID: otherApp.ID, AccountID: otherAcct.ID, Source: InvocationCron,
		State: InvocationPending, Method: "POST", Path: "/",
		CreatedAt: t0,
	})
	_ = m.FailInvocation(ctx, inv.ID, "boom", 0, 0)

	// App-scoped.
	n, err := m.CountFailedInvocationsSince(ctx, acct.ID, app.ID, InvocationCron, t0.Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("app-scoped count = %d, want 2", n)
	}
	// Account-wide with cron source.
	n, err = m.CountFailedInvocationsSince(ctx, acct.ID, "", InvocationCron, t0.Add(-time.Second))
	if err != nil || n != 2 {
		t.Errorf("acct-wide source=cron count = %d (%v), want 2", n, err)
	}
	// Since BEFORE the failures → count = 0
	n, err = m.CountFailedInvocationsSince(ctx, acct.ID, app.ID, InvocationCron, t0.Add(time.Hour))
	if err != nil || n != 0 {
		t.Errorf("count after `since` in the future = %d (%v); want 0", n, err)
	}
}

// TestMemStore_ListAlertDeliveriesForRule_IncludeTestToggle —
// ADR-123 PR-D: include_test=false hides rows where IsTest=true
// (the production-default customer view); include_test=true surfaces
// them (the operator pane reachable via `?include_test=true`).
// Pins the toggle contract that the new pgstore partial index
// preserves at the SQL layer.
func TestMemStore_ListAlertDeliveriesForRule_IncludeTestToggle(t *testing.T) {
	m, ctx, acct, app := alertFixture(t)
	rule, err := m.CreateAlertRule(ctx, memSampleRule(acct.ID, app.ID))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	prod := AlertDelivery{
		ID:             newID(),
		RuleID:         rule.ID,
		AccountID:      acct.ID,
		AppID:          app.ID,
		IdempotencyKey: rule.ID + ":prod-bucket",
		Payload:        json.RawMessage(`{"test":false}`),
		Status:         AlertDeliveryDelivered,
		AttemptCount:   1,
		LastStatusCode: 200,
		ObservedValue:  5.1,
		FiredAt:        now,
		DeliveredAt:    now,
		IsTest:         false,
	}
	testRow := AlertDelivery{
		ID:             newID(),
		RuleID:         rule.ID,
		AccountID:      acct.ID,
		AppID:          app.ID,
		IdempotencyKey: rule.ID + ":test-bucket",
		Payload:        json.RawMessage(`{"test":true}`),
		Status:         AlertDeliveryDelivered,
		AttemptCount:   1,
		LastStatusCode: 200,
		ObservedValue:  5.1,
		FiredAt:        now.Add(time.Second),
		DeliveredAt:    now.Add(time.Second),
		IsTest:         true,
	}
	for _, d := range []AlertDelivery{prod, testRow} {
		if _, err := m.RecordAlertDelivery(ctx, d); err != nil {
			t.Fatalf("record %s: %v", d.IdempotencyKey, err)
		}
	}

	// includeTest=false → 1 row (production only).
	prodList, err := m.ListAlertDeliveriesForRule(ctx, rule.ID, 50, false)
	if err != nil {
		t.Fatalf("ListAlertDeliveriesForRule(includeTest=false): %v", err)
	}
	if len(prodList) != 1 {
		t.Fatalf("production read returned %d rows; want 1", len(prodList))
	}
	if prodList[0].IsTest {
		t.Errorf("production read returned IsTest=true row; production-default MUST hide test rows")
	}
	// includeTest=true → 2 rows (newest-first by fired_at).
	allList, err := m.ListAlertDeliveriesForRule(ctx, rule.ID, 50, true)
	if err != nil {
		t.Fatalf("ListAlertDeliveriesForRule(includeTest=true): %v", err)
	}
	if len(allList) != 2 {
		t.Fatalf("operator read returned %d rows; want 2", len(allList))
	}
	if !allList[0].IsTest {
		t.Errorf("operator read newest row IsTest = false; want true (test row was inserted with FiredAt=now+1s)")
	}
	if allList[1].IsTest {
		t.Errorf("operator read oldest row IsTest = true; want false (production row was inserted first)")
	}
}
