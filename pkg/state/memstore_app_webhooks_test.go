package state

// Coverage for the MemStore side of the issue #476 / ADR-076
// outbound webhook subscription + delivery ledger surface. Mirrors
// the alert_rules + alert_deliveries test pattern at
// memstore_alert_rules_test.go so the same fixture / no-pg path
// covers the new store surface.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func webhookFixture(t *testing.T) (m *MemStore, ctx context.Context, account Account, app App) {
	t.Helper()
	ctx = context.Background()
	m = NewMemStore()
	acct, err := m.CreateAccount(ctx, "webhook-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	a, err := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "webhook-" + uuid.NewString(),
		RAMMB: 512, Status: AppActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	return m, ctx, acct, a
}

// memSampleWebhook is the simplest valid AppWebhook the tests use.
// Fresh per call so callers can mutate fields without aliasing.
func memSampleWebhook(accountID, appID string) AppWebhook {
	return AppWebhook{
		AccountID:    accountID,
		AppID:        appID,
		TargetURL:    "https://example.com/hook-" + uuid.NewString(),
		SecretSealed: []byte("sealed-secret"),
		EventFilter:  []string{"cron.fired"},
		RetryPolicy:  AppWebhookRetryDefault,
		Enabled:      true,
	}
}

func TestMemStoreAppWebhook_RoundTrip(t *testing.T) {
	m, ctx, acct, app := webhookFixture(t)

	created, err := m.CreateAppWebhook(ctx, memSampleWebhook(acct.ID, app.ID))
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	if created.ID == "" {
		t.Errorf("expected non-empty id from CreateAppWebhook")
	}
	if created.RetryPolicy != AppWebhookRetryDefault {
		t.Errorf("default retry_policy = %q, want %q", created.RetryPolicy, AppWebhookRetryDefault)
	}
	if created.CreatedAt.IsZero() {
		t.Errorf("CreatedAt not stamped")
	}

	got, err := m.AppWebhookByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("AppWebhookByID: %v", err)
	}
	if got.TargetURL != created.TargetURL {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	// UpdateAppWebhook nil-skip semantics.
	newTarget := "https://example.com/hook-other"
	newPolicy := AppWebhookRetryAggressive
	newEnabled := false
	updated, err := m.UpdateAppWebhook(ctx, created.ID, UpdateAppWebhookParams{
		TargetURL:   &newTarget,
		RetryPolicy: &newPolicy,
		Enabled:     &newEnabled,
	})
	if err != nil {
		t.Fatalf("UpdateAppWebhook: %v", err)
	}
	if updated.TargetURL != newTarget {
		t.Errorf("TargetURL = %q, want %q", updated.TargetURL, newTarget)
	}
	if updated.RetryPolicy != newPolicy {
		t.Errorf("RetryPolicy = %q, want %q", updated.RetryPolicy, newPolicy)
	}
	if updated.Enabled != false {
		t.Errorf("Enabled = %v, want false", updated.Enabled)
	}
	if updated.SecretSealed == nil {
		t.Errorf("SecretSealed nil after partial update — should have been preserved")
	}
}

func TestMemStoreAppWebhook_NotFound(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	if _, err := m.AppWebhookByID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("AppWebhookByID missing = %v, want ErrNotFound", err)
	}
	if _, err := m.UpdateAppWebhook(ctx, "missing", UpdateAppWebhookParams{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateAppWebhook missing = %v, want ErrNotFound", err)
	}
	if err := m.DeleteAppWebhook(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteAppWebhook missing = %v, want ErrNotFound", err)
	}
}

func TestMemStoreAppWebhook_DuplicateTargetRejected(t *testing.T) {
	m, ctx, acct, app := webhookFixture(t)
	w := memSampleWebhook(acct.ID, app.ID)
	if _, err := m.CreateAppWebhook(ctx, w); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if _, err := m.CreateAppWebhook(ctx, w); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate insert = %v, want ErrConflict", err)
	}
}

func TestMemStoreAppWebhook_Delete(t *testing.T) {
	m, ctx, acct, app := webhookFixture(t)
	created, err := m.CreateAppWebhook(ctx, memSampleWebhook(acct.ID, app.ID))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := m.DeleteAppWebhook(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := m.AppWebhookByID(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete AppWebhookByID = %v, want ErrNotFound", err)
	}
}

func TestMemStoreAppWebhook_ListForAppAndAccount(t *testing.T) {
	m, ctx, acct, app := webhookFixture(t)

	// Three on this app, one on a sibling app (under the same acct).
	for i := 0; i < 3; i++ {
		w := memSampleWebhook(acct.ID, app.ID)
		if _, err := m.CreateAppWebhook(ctx, w); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	sibling, err := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "webhook-sibling-" + uuid.NewString(),
		RAMMB: 256, Status: AppActive,
	})
	if err != nil {
		t.Fatalf("sibling app: %v", err)
	}
	if _, err := m.CreateAppWebhook(ctx, memSampleWebhook(acct.ID, sibling.ID)); err != nil {
		t.Fatalf("sibling webhook: %v", err)
	}

	appList, err := m.ListAppWebhooksForApp(ctx, app.ID)
	if err != nil {
		t.Fatalf("ListAppWebhooksForApp: %v", err)
	}
	if len(appList) != 3 {
		t.Errorf("app-scoped list = %d, want 3", len(appList))
	}
	acctList, err := m.ListAppWebhooksForAccount(ctx, acct.ID)
	if err != nil {
		t.Fatalf("ListAppWebhooksForAccount: %v", err)
	}
	if len(acctList) != 4 {
		t.Errorf("account-scoped list = %d, want 4", len(acctList))
	}
}

func TestMemStoreAppWebhook_PerAppQuotaExceeded(t *testing.T) {
	m, ctx, acct, app := webhookFixture(t)
	limits := api.Limits{WebhookPerApp: 2, WebhookPerAccount: 100}
	for i := 0; i < 2; i++ {
		w := memSampleWebhook(acct.ID, app.ID)
		if _, err := m.CreateAppWebhookIfUnderQuota(ctx, w, limits); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	_, err := m.CreateAppWebhookIfUnderQuota(ctx, memSampleWebhook(acct.ID, app.ID), limits)
	var qerr *AppWebhookQuotaError
	if !errors.As(err, &qerr) || qerr.Scope != AppWebhookQuotaScopeApp {
		t.Errorf("expected AppWebhookQuotaError(Scope=App); got %v", err)
	}
}

func TestMemStoreAppWebhook_PerAccountQuotaExceeded(t *testing.T) {
	m, ctx, acct, _ := webhookFixture(t)
	appA, _ := m.CreateApp(ctx, App{AccountID: acct.ID, Slug: "a-" + uuid.NewString(), RAMMB: 128, Status: AppActive})
	appB, _ := m.CreateApp(ctx, App{AccountID: acct.ID, Slug: "b-" + uuid.NewString(), RAMMB: 128, Status: AppActive})
	limits := api.Limits{WebhookPerApp: 100, WebhookPerAccount: 1}
	if _, err := m.CreateAppWebhookIfUnderQuota(ctx, memSampleWebhook(acct.ID, appA.ID), limits); err != nil {
		t.Fatalf("appA: %v", err)
	}
	_, err := m.CreateAppWebhookIfUnderQuota(ctx, memSampleWebhook(acct.ID, appB.ID), limits)
	var qerr *AppWebhookQuotaError
	if !errors.As(err, &qerr) || qerr.Scope != AppWebhookQuotaScopeAccount {
		t.Errorf("expected AppWebhookQuotaError(Scope=Account); got %v", err)
	}
}

func TestMemStoreAppWebhook_QuotaRejectsDeletedApp(t *testing.T) {
	m, ctx, acct, app := webhookFixture(t)
	limits := api.Limits{WebhookPerApp: 10, WebhookPerAccount: 10}
	if _, err := m.SoftDeleteAppCascade(ctx, app.ID); err != nil {
		t.Fatalf("SoftDeleteAppCascade: %v", err)
	}
	_, err := m.CreateAppWebhookIfUnderQuota(ctx, memSampleWebhook(acct.ID, app.ID), limits)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for deleted app; got %v", err)
	}
}

func TestMemStoreAppWebhook_QuotaRejectsMissingApp(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	_, err := m.CreateAppWebhookIfUnderQuota(ctx, AppWebhook{AppID: "missing", AccountID: "x"}, api.Limits{})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("missing app: expected ErrNotFound, got %v", err)
	}
}

func TestMemStoreAppWebhook_QuotaRejectsEmptyAppID(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	_, err := m.CreateAppWebhookIfUnderQuota(ctx, AppWebhook{AppID: "", AccountID: "x"}, api.Limits{})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("empty app_id: expected ErrNotFound, got %v", err)
	}
}

func TestMemStoreAppWebhook_UpdateSecret(t *testing.T) {
	m, ctx, acct, app := webhookFixture(t)
	created, err := m.CreateAppWebhook(ctx, memSampleWebhook(acct.ID, app.ID))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	newSecret := []byte("new-sealed-bytes")
	_, err = m.UpdateAppWebhook(ctx, created.ID, UpdateAppWebhookParams{
		WebhookSecretSealed: &newSecret,
	})
	if err != nil {
		t.Fatalf("UpdateAppWebhook (secret): %v", err)
	}
	got, err := m.AppWebhookByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("AppWebhookByID post-update: %v", err)
	}
	if string(got.SecretSealed) != string(newSecret) {
		t.Errorf("SecretSealed not updated; got %q want %q", got.SecretSealed, newSecret)
	}
}

// --- Delivery ledger ---

func memSampleDelivery(webhookID, appID, accountID, id string) AppWebhookDelivery {
	now := time.Now()
	return AppWebhookDelivery{
		ID:            id,
		WebhookID:     webhookID,
		AppID:         appID,
		AccountID:     accountID,
		Event:         "cron.fired",
		Attempt:       0,
		Status:        AppWebhookDeliveryPending,
		NextAttemptAt: now,
	}
}

func TestMemStoreAppWebhookDelivery_RecordDefaults(t *testing.T) {
	m, ctx, acct, app := webhookFixture(t)
	wh, err := m.CreateAppWebhook(ctx, memSampleWebhook(acct.ID, app.ID))
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	d, err := m.RecordAppWebhookDelivery(ctx, AppWebhookDelivery{
		WebhookID: wh.ID, AppID: app.ID, AccountID: acct.ID,
		Event: "cron.fired",
	})
	if err != nil {
		t.Fatalf("RecordAppWebhookDelivery: %v", err)
	}
	if d.ID == "" {
		t.Errorf("ID not auto-assigned")
	}
	if d.Status != AppWebhookDeliveryPending {
		t.Errorf("default status = %q, want %q", d.Status, AppWebhookDeliveryPending)
	}
	if d.NextAttemptAt.IsZero() {
		t.Errorf("NextAttemptAt not stamped")
	}
}

func TestMemStoreAppWebhookDelivery_RecordRejectsMissingWebhook(t *testing.T) {
	m, ctx, _, _ := webhookFixture(t)
	_, err := m.RecordAppWebhookDelivery(ctx, AppWebhookDelivery{
		WebhookID: "missing", Event: "cron.fired",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("missing webhook: expected ErrNotFound, got %v", err)
	}
}

func TestMemStoreAppWebhookDelivery_ClaimRespectsStatus(t *testing.T) {
	m, ctx, acct, app := webhookFixture(t)
	wh, _ := m.CreateAppWebhook(ctx, memSampleWebhook(acct.ID, app.ID))
	pending := memSampleDelivery(wh.ID, app.ID, acct.ID, "d-pending")
	if _, err := m.RecordAppWebhookDelivery(ctx, pending); err != nil {
		t.Fatalf("record pending: %v", err)
	}
	dead := memSampleDelivery(wh.ID, app.ID, acct.ID, "d-dead")
	dead.Status = AppWebhookDeliveryDead
	if _, err := m.RecordAppWebhookDelivery(ctx, dead); err != nil {
		t.Fatalf("record dead: %v", err)
	}
	future := memSampleDelivery(wh.ID, app.ID, acct.ID, "d-future")
	future.NextAttemptAt = time.Now().Add(time.Hour)
	if _, err := m.RecordAppWebhookDelivery(ctx, future); err != nil {
		t.Fatalf("record future: %v", err)
	}

	claimed, err := m.ClaimDueAppWebhookDeliveries(ctx, 10, time.Now())
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed = %d, want 1 (pending only)", len(claimed))
	}
	if claimed[0].ID != "d-pending" {
		t.Errorf("claimed id = %q, want d-pending", claimed[0].ID)
	}
	if claimed[0].Status != AppWebhookDeliveryInFlight {
		t.Errorf("post-claim status = %q, want in_flight", claimed[0].Status)
	}
}

func TestMemStoreAppWebhookDelivery_ClaimHonorsLimit(t *testing.T) {
	m, ctx, acct, app := webhookFixture(t)
	wh, _ := m.CreateAppWebhook(ctx, memSampleWebhook(acct.ID, app.ID))
	for i := 0; i < 5; i++ {
		if _, err := m.RecordAppWebhookDelivery(ctx,
			memSampleDelivery(wh.ID, app.ID, acct.ID, "d-"+uuid.NewString())); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	claimed, err := m.ClaimDueAppWebhookDeliveries(ctx, 2, time.Now())
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 2 {
		t.Errorf("claimed = %d, want 2 (limit)", len(claimed))
	}
}

func TestMemStoreAppWebhookDelivery_ClaimPerAccountFairness(t *testing.T) {
	m, ctx, _, _ := webhookFixture(t)
	// Two accounts, three deliveries each. The claim query returns
	// rows sorted by (account_id ASC, next_attempt_at ASC). With
	// limit >= total rows, all rows are returned in that order;
	// the in-memory impl pins that contract.
	mk := func(acctID, appID, id string) {
		wh, _ := m.CreateAppWebhook(ctx, AppWebhook{
			AccountID: acctID, AppID: appID,
			TargetURL: "https://example.com/" + id,
			Enabled:   true,
		})
		if _, err := m.RecordAppWebhookDelivery(ctx,
			memSampleDelivery(wh.ID, appID, acctID, id)); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	mk("acct-A", "app-A1", "d-A1")
	mk("acct-A", "app-A2", "d-A2")
	mk("acct-A", "app-A3", "d-A3")
	mk("acct-B", "app-B1", "d-B1")
	mk("acct-B", "app-B2", "d-B2")
	mk("acct-B", "app-B3", "d-B3")

	claimed, err := m.ClaimDueAppWebhookDeliveries(ctx, 10, time.Now())
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 6 {
		t.Fatalf("claimed = %d, want 6", len(claimed))
	}
	// Verify ordering: every acct-A row comes before any acct-B row.
	lastA := -1
	firstB := -1
	for i, c := range claimed {
		switch c.AccountID {
		case "acct-A":
			lastA = i
		case "acct-B":
			if firstB == -1 {
				firstB = i
			}
		}
	}
	if lastA == -1 || firstB == -1 {
		t.Fatalf("missing one of the accounts; got %+v", claimed)
	}
	if lastA >= firstB {
		t.Errorf("rows not grouped by account; got %+v", claimed)
	}
}

func TestMemStoreAppWebhookDelivery_MarkSucceeded(t *testing.T) {
	m, ctx, acct, app := webhookFixture(t)
	wh, _ := m.CreateAppWebhook(ctx, memSampleWebhook(acct.ID, app.ID))
	d, _ := m.RecordAppWebhookDelivery(ctx, memSampleDelivery(wh.ID, app.ID, acct.ID, "d-1"))
	now := time.Now()
	if err := m.MarkAppWebhookDeliverySucceeded(ctx, d.ID, 200, d.Attempt, now); err != nil {
		t.Fatalf("MarkSucceeded: %v", err)
	}
	got, _ := m.AppWebhookDeliveryByID(ctx, d.ID)
	if got.Status != AppWebhookDeliverySucceeded {
		t.Errorf("status = %q, want succeeded", got.Status)
	}
	if got.LastResponseCode != 200 {
		t.Errorf("LastResponseCode = %d, want 200", got.LastResponseCode)
	}
	if got.Attempt != 1 {
		t.Errorf("attempt = %d, want 1", got.Attempt)
	}
	if got.DeliveredAt == nil || !got.DeliveredAt.Equal(now) {
		t.Errorf("DeliveredAt = %v, want %v", got.DeliveredAt, now)
	}
}

func TestMemStoreAppWebhookDelivery_MarkFailed(t *testing.T) {
	m, ctx, acct, app := webhookFixture(t)
	wh, _ := m.CreateAppWebhook(ctx, memSampleWebhook(acct.ID, app.ID))
	d, _ := m.RecordAppWebhookDelivery(ctx, memSampleDelivery(wh.ID, app.ID, acct.ID, "d-1"))
	resched := time.Now().Add(30 * time.Second)
	if err := m.MarkAppWebhookDeliveryFailed(ctx, d.ID, 500, d.Attempt, "boom", resched); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	got, _ := m.AppWebhookDeliveryByID(ctx, d.ID)
	if got.Status != AppWebhookDeliveryPending {
		t.Errorf("status = %q, want pending (reschedule)", got.Status)
	}
	if got.LastError != "boom" {
		t.Errorf("LastError = %q", got.LastError)
	}
	if !got.NextAttemptAt.Equal(resched) {
		t.Errorf("NextAttemptAt = %v, want %v", got.NextAttemptAt, resched)
	}
}

func TestMemStoreAppWebhookDelivery_MarkDead(t *testing.T) {
	m, ctx, acct, app := webhookFixture(t)
	wh, _ := m.CreateAppWebhook(ctx, memSampleWebhook(acct.ID, app.ID))
	d, _ := m.RecordAppWebhookDelivery(ctx, memSampleDelivery(wh.ID, app.ID, acct.ID, "d-1"))
	if err := m.MarkAppWebhookDeliveryDead(ctx, d.ID, d.Attempt, "exhausted"); err != nil {
		t.Fatalf("MarkDead: %v", err)
	}
	got, _ := m.AppWebhookDeliveryByID(ctx, d.ID)
	if got.Status != AppWebhookDeliveryDead {
		t.Errorf("status = %q, want dead", got.Status)
	}
	if got.Attempt != 1 {
		t.Errorf("attempt = %d, want 1", got.Attempt)
	}
}

func TestMemStoreAppWebhookDelivery_DeadLetterProjectionAndReplay(t *testing.T) {
	m, ctx, acct, app := webhookFixture(t)
	wh, _ := m.CreateAppWebhook(ctx, memSampleWebhook(acct.ID, app.ID))
	delivery, _ := m.RecordAppWebhookDelivery(ctx, memSampleDelivery(wh.ID, app.ID, acct.ID, "dead-letter"))
	if err := m.MarkAppWebhookDeliveryDead(ctx, delivery.ID, delivery.Attempt, "receiver unavailable"); err != nil {
		t.Fatalf("MarkDead: %v", err)
	}
	events, err := m.ListDeadLetterEvents(ctx, app.ID, 20, "")
	if err != nil {
		t.Fatalf("ListDeadLetterEvents: %v", err)
	}
	if len(events) != 1 || events[0].Source != "webhook_delivery" || events[0].SourceID != delivery.ID {
		t.Fatalf("events = %+v, want one webhook delivery event", events)
	}
	replayed, err := m.ReplayDeadLetterEvent(ctx, acct.ID, app.ID, events[0].ID)
	if err != nil {
		t.Fatalf("ReplayDeadLetterEvent: %v", err)
	}
	if replayed.ReplayedAt == nil {
		t.Fatal("replayed_at = nil, want timestamp")
	}
	got, err := m.AppWebhookDeliveryByID(ctx, delivery.ID)
	if err != nil {
		t.Fatalf("AppWebhookDeliveryByID: %v", err)
	}
	if got.Status != AppWebhookDeliveryPending || got.Attempt != 0 {
		t.Fatalf("delivery after replay = status %q attempt %d, want pending/0", got.Status, got.Attempt)
	}
}

func TestMemStoreAppWebhookDelivery_ResetFromDead(t *testing.T) {
	m, ctx, acct, app := webhookFixture(t)
	wh, _ := m.CreateAppWebhook(ctx, memSampleWebhook(acct.ID, app.ID))
	d, _ := m.RecordAppWebhookDelivery(ctx, memSampleDelivery(wh.ID, app.ID, acct.ID, "d-1"))
	_ = m.MarkAppWebhookDeliveryDead(ctx, d.ID, d.Attempt, "exhausted")
	now := time.Now()
	if err := m.ResetAppWebhookDeliveryFromDead(ctx, d.ID, wh.ID, acct.ID, now); err != nil {
		t.Fatalf("ResetFromDead: %v", err)
	}
	got, _ := m.AppWebhookDeliveryByID(ctx, d.ID)
	if got.Status != AppWebhookDeliveryPending {
		t.Errorf("status = %q, want pending", got.Status)
	}
	if got.Attempt != 0 {
		t.Errorf("attempt = %d, want 0 (reset)", got.Attempt)
	}
	if !got.NextAttemptAt.Equal(now) {
		t.Errorf("NextAttemptAt = %v, want %v", got.NextAttemptAt, now)
	}
}

func TestMemStoreAppWebhookDelivery_ResetFromDead_IDORGuarded(t *testing.T) {
	m, ctx, acct, app := webhookFixture(t)
	wh, _ := m.CreateAppWebhook(ctx, memSampleWebhook(acct.ID, app.ID))
	d, _ := m.RecordAppWebhookDelivery(ctx, memSampleDelivery(wh.ID, app.ID, acct.ID, "d-1"))
	_ = m.MarkAppWebhookDeliveryDead(ctx, d.ID, d.Attempt, "exhausted")
	// Wrong account id → ErrNotFound (no info leak).
	if err := m.ResetAppWebhookDeliveryFromDead(ctx, d.ID, wh.ID, "other-acct", time.Now()); !errors.Is(err, ErrNotFound) {
		t.Errorf("wrong acct: expected ErrNotFound, got %v", err)
	}
	// Wrong webhook id → ErrNotFound.
	if err := m.ResetAppWebhookDeliveryFromDead(ctx, d.ID, "other-wh", acct.ID, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Errorf("wrong webhook: expected ErrNotFound, got %v", err)
	}
	// Not in dead state → ErrConflict.
	live, _ := m.RecordAppWebhookDelivery(ctx, memSampleDelivery(wh.ID, app.ID, acct.ID, "d-2"))
	if err := m.ResetAppWebhookDeliveryFromDead(ctx, live.ID, wh.ID, acct.ID, time.Now()); !errors.Is(err, ErrConflict) {
		t.Errorf("live row: expected ErrConflict, got %v", err)
	}
}

func TestMemStoreAppWebhookDelivery_MarkerNotFound(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	if err := m.MarkAppWebhookDeliverySucceeded(ctx, "missing", 200, 0, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Errorf("MarkSucceeded missing: %v", err)
	}
	if err := m.MarkAppWebhookDeliveryFailed(ctx, "missing", 500, 0, "x", time.Now()); !errors.Is(err, ErrNotFound) {
		t.Errorf("MarkFailed missing: %v", err)
	}
	if err := m.MarkAppWebhookDeliveryDead(ctx, "missing", 0, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("MarkDead missing: %v", err)
	}
}

func TestMemStoreAppWebhookDelivery_ListScopesByWebhookID(t *testing.T) {
	m, ctx, acct, app := webhookFixture(t)
	whA, _ := m.CreateAppWebhook(ctx, memSampleWebhook(acct.ID, app.ID))
	whB, _ := m.CreateAppWebhook(ctx, memSampleWebhook(acct.ID, app.ID))
	for i := 0; i < 3; i++ {
		_, _ = m.RecordAppWebhookDelivery(ctx,
			memSampleDelivery(whA.ID, app.ID, acct.ID, "a-"+uuid.NewString()))
	}
	for i := 0; i < 2; i++ {
		_, _ = m.RecordAppWebhookDelivery(ctx,
			memSampleDelivery(whB.ID, app.ID, acct.ID, "b-"+uuid.NewString()))
	}
	rows, _, err := m.ListAppWebhookDeliveries(ctx, app.ID, whA.ID, 50, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("List webhookA = %d, want 3", len(rows))
	}
}

func TestMemStoreAppWebhookDelivery_ListPagination(t *testing.T) {
	m, ctx, acct, app := webhookFixture(t)
	wh, _ := m.CreateAppWebhook(ctx, memSampleWebhook(acct.ID, app.ID))
	for i := 0; i < 5; i++ {
		_, _ = m.RecordAppWebhookDelivery(ctx,
			memSampleDelivery(wh.ID, app.ID, acct.ID, "d-"+uuid.NewString()))
	}
	rows, _, err := m.ListAppWebhookDeliveries(ctx, app.ID, wh.ID, 2, "")
	if err != nil {
		t.Fatalf("List page=2: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("pageSize=2 returned %d, want 2", len(rows))
	}
}

func TestMemStoreAppWebhookDelivery_ByID_NotFound(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	if _, err := m.AppWebhookDeliveryByID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("AppWebhookDeliveryByID missing = %v, want ErrNotFound", err)
	}
}
