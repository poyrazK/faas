package state_test

// PR #476 (issue #476 / ADR-076) round-trip the
// app_webhooks + app_webhook_deliveries surface against a real
// Postgres cluster so a typo in the tx body, a missing column scan,
// a wrong `idempotency_key` UNIQUE handling, or a claim transaction
// predicate that misses the status filter can't ship silently.
//
// Mirrors pkg/state/pgstore_alert_rules_test.go — same
// pgtest.Open skip-when-no-pg pattern, same package. These tests
// contribute to the make check-state-coverage gate (≥ 70%).

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// pgSampleWebhook is the simplest valid AppWebhook the tests below
// use. Fresh per call so callers can mutate fields without aliasing.
func pgSampleWebhook(accountID, appID string) state.AppWebhook {
	return state.AppWebhook{
		AccountID:    accountID,
		AppID:        appID,
		TargetURL:    "https://example.com/hook-" + uuid.NewString(),
		SecretSealed: []byte("sealed-secret"),
		EventFilter:  []string{"cron.fired"},
		RetryPolicy:  state.AppWebhookRetryDefault,
		Enabled:      true,
	}
}

// pgSampleDelivery is the simplest valid AppWebhookDelivery.
func pgSampleDelivery(webhookID, appID, accountID string) state.AppWebhookDelivery {
	return state.AppWebhookDelivery{
		WebhookID:     webhookID,
		AppID:         appID,
		AccountID:     accountID,
		Event:         "cron.fired",
		Payload:       []byte(`{"k":"v"}`),
		Attempt:       0,
		Status:        state.AppWebhookDeliveryPending,
		NextAttemptAt: time.Now(),
	}
}

// TestPgStore_AppWebhook_RoundTrip exercises Create + AppWebhookByID +
// Update + Delete. Proves the SELECT column order in
// scanAppWebhookCols matches the INSERT statement and that the
// delete-then-lookup returns ErrNotFound.
func TestPgStore_AppWebhook_RoundTrip(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx)

	created, err := s.CreateAppWebhook(ctx, pgSampleWebhook(acct, app))
	if err != nil {
		t.Fatalf("CreateAppWebhook: %v", err)
	}
	if created.ID == "" {
		t.Errorf("expected non-empty id from CreateAppWebhook")
	}
	if created.RetryPolicy != state.AppWebhookRetryDefault {
		t.Errorf("default retry_policy = %q, want %q", created.RetryPolicy, state.AppWebhookRetryDefault)
	}
	if created.Scope != state.AppWebhookScopeApp {
		t.Errorf("default webhook scope = %q, want app", created.Scope)
	}

	got, err := s.AppWebhookByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("AppWebhookByID: %v", err)
	}
	if got.TargetURL != created.TargetURL {
		t.Errorf("round-trip mismatch: target_url=%q want %q", got.TargetURL, created.TargetURL)
	}

	enabled := false
	newPolicy := state.AppWebhookRetryAggressive
	updated, err := s.UpdateAppWebhook(ctx, created.ID, state.UpdateAppWebhookParams{
		Enabled:     &enabled,
		RetryPolicy: &newPolicy,
	})
	if err != nil {
		t.Fatalf("UpdateAppWebhook: %v", err)
	}
	if updated.Enabled || updated.RetryPolicy != state.AppWebhookRetryAggressive {
		t.Errorf("update did not apply: %+v", updated)
	}

	if err := s.DeleteAppWebhook(ctx, created.ID); err != nil {
		t.Fatalf("DeleteAppWebhook: %v", err)
	}
	if _, err := s.AppWebhookByID(ctx, created.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("post-delete lookup = %v; want ErrNotFound", err)
	}
}

func TestPgStore_AppWebhook_NotFound(t *testing.T) {
	s, ctx := pgStore(t)
	if _, err := s.AppWebhookByID(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("AppWebhookByID missing = %v; want ErrNotFound", err)
	}
	if _, err := s.UpdateAppWebhook(ctx, "00000000-0000-0000-0000-000000000000", state.UpdateAppWebhookParams{}); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("UpdateAppWebhook missing = %v; want ErrNotFound", err)
	}
	if err := s.DeleteAppWebhook(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("DeleteAppWebhook missing = %v; want ErrNotFound", err)
	}
}

// TestPgStore_AppWebhook_DuplicateTargetRejected pins the
// (app_id, target_url) UNIQUE constraint from migration 00140 —
// a typo in the index name or a missing UNIQUE keyword would let
// duplicates slip through.
func TestPgStore_AppWebhook_DuplicateTargetRejected(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx)

	w := pgSampleWebhook(acct, app)
	if _, err := s.CreateAppWebhook(ctx, w); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if _, err := s.CreateAppWebhook(ctx, w); !errors.Is(err, state.ErrConflict) {
		t.Errorf("duplicate insert = %v; want ErrConflict", err)
	}
}

// TestPgStore_CreateAppWebhookIfUnderQuota_PerAppCap fills one app
// to its per-app webhook limit and asserts the next insert returns
// *state.AppWebhookQuotaError with Scope=App. Pins the FOR UPDATE
// lock on apps + count predicate.
func TestPgStore_CreateAppWebhookIfUnderQuota_PerAppCap(t *testing.T) {
	s, ctx := pgStore(t)
	limits := api.MustLimitsFor(api.PlanPro)
	acct, app, _ := seedLiveDeploy(t, s, ctx)

	for i := 0; i < limits.WebhookPerApp; i++ {
		if _, err := s.CreateAppWebhookIfUnderQuota(ctx, pgSampleWebhook(acct, app), limits); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	_, err := s.CreateAppWebhookIfUnderQuota(ctx, pgSampleWebhook(acct, app), limits)
	var qerr *state.AppWebhookQuotaError
	if !errors.As(err, &qerr) || qerr.Scope != state.AppWebhookQuotaScopeApp {
		t.Errorf("expected AppWebhookQuotaError(Scope=App); got %v", err)
	}
}

// TestPgStore_CreateAppWebhookIfUnderQuota_PerAccountCap fills one
// account to its per-account webhook limit across four apps, then
// asserts the next insert returns *state.AppWebhookQuotaError
// with Scope=Account. Pins the per-account FOR UPDATE predicate.
//
// Hobby plan: WebhookPerApp=3, WebhookPerAccount=10. The per-app
// cap of 3 < 10 per-account, so we need at least ⌈10/3⌉ = 4 apps
// to reach per-account=10 without per-app=3 firing first. The
// quota gate is `count >= limit` (off-by-zero): filling to exactly
// `WebhookPerAccount` leaves count=10 at the next check, which
// trips the gate. Round-robin of 10 inserts over 4 apps gives
// 3,3,2,2 — every app stays at or below its per-app cap of 3.
// The 11th insert targets an app with headroom (≤2) so per-app
// can't fire; the per-account gate must trip with Scope=Account.
func TestPgStore_CreateAppWebhookIfUnderQuota_PerAccountCap(t *testing.T) {
	s, ctx := pgStore(t)
	limits := api.MustLimitsFor(api.PlanHobby)
	acct, _, _ := seedLiveDeploy(t, s, ctx, "cap")

	appIDs := make([]string, 0, 4)
	for i := 0; i < 4; i++ {
		a, err := s.CreateApp(ctx, state.App{
			AccountID: acct, Slug: fmt.Sprintf("p-%d-%s", i, uuid.NewString()),
			Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1, IdleTimeoutS: 60,
		})
		if err != nil {
			t.Fatalf("CreateApp %d: %v", i, err)
		}
		appIDs = append(appIDs, a.ID)
	}

	// Fill to exactly the per-account cap. Round-robin 10/4 → 3,3,2,2
	// across the four apps — every app stays ≤ its per-app cap of 3.
	for i := 0; i < limits.WebhookPerAccount; i++ {
		appID := appIDs[i%len(appIDs)]
		if _, err := s.CreateAppWebhookIfUnderQuota(ctx, pgSampleWebhook(acct, appID), limits); err != nil {
			t.Fatalf("insert %d (app %s): %v", i, appID, err)
		}
	}
	// Per-account cap should now reject the next insert.
	_, err := s.CreateAppWebhookIfUnderQuota(ctx, pgSampleWebhook(acct, appIDs[3]), limits)
	var qerr *state.AppWebhookQuotaError
	if !errors.As(err, &qerr) || qerr.Scope != state.AppWebhookQuotaScopeAccount {
		t.Errorf("expected AppWebhookQuotaError(Scope=Account); got %v", err)
	}
}

// ADR-224: two apps in one account must serialize at the account lock so
// concurrent creates cannot each claim the final shared webhook slot.
func TestPgStore_CreateAppWebhookIfUnderQuota_ConcurrentApps(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appA, _ := seedLiveDeploy(t, s, ctx, "shared-hook-cap")
	appB, err := s.CreateApp(ctx, state.App{
		AccountID: accountID, Slug: "shared-hook-cap-" + uuid.NewString(),
		Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, appID := range []string{appA, appB.ID} {
		go func(id string) {
			<-start
			_, createErr := s.CreateAppWebhookIfUnderQuota(ctx, pgSampleWebhook(accountID, id), api.Limits{
				WebhookPerApp: 2, WebhookPerAccount: 1,
			})
			results <- createErr
		}(appID)
	}
	close(start)
	successes, quotas := 0, 0
	for range 2 {
		result := <-results
		if result == nil {
			successes++
			continue
		}
		var quota *state.AppWebhookQuotaError
		if errors.As(result, &quota) && quota.Scope == state.AppWebhookQuotaScopeAccount {
			quotas++
			continue
		}
		t.Fatalf("unexpected concurrent creation result: %v", result)
	}
	if successes != 1 || quotas != 1 {
		t.Errorf("successes=%d quotas=%d, want 1/1", successes, quotas)
	}
}

// TestPgStore_ListAppWebhooks_AppAndAccount covers the two list
// scopes. Both must order by created_at DESC for consistent UI.
func TestPgStore_ListAppWebhooks_AppAndAccount(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx, "list")

	for i := 0; i < 3; i++ {
		if _, err := s.CreateAppWebhook(ctx, pgSampleWebhook(acct, app)); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	appList, err := s.ListAppWebhooksForApp(ctx, app)
	if err != nil {
		t.Fatalf("ListAppWebhooksForApp: %v", err)
	}
	if len(appList) != 3 {
		t.Errorf("app-scoped list = %d, want 3", len(appList))
	}
	acctList, err := s.ListAppWebhooksForAccount(ctx, acct)
	if err != nil {
		t.Fatalf("ListAppWebhooksForAccount: %v", err)
	}
	if len(acctList) != 3 {
		t.Errorf("account-scoped list = %d, want 3", len(acctList))
	}
}

// TestPgStore_AppWebhookDelivery_RoundTrip exercises
// RecordAppWebhookDelivery + AppWebhookDeliveryByID. Pins the
// default-status stamping + auto-id assignment.
func TestPgStore_AppWebhookDelivery_RoundTrip(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx, "del")

	wh, err := s.CreateAppWebhook(ctx, pgSampleWebhook(acct, app))
	if err != nil {
		t.Fatalf("CreateAppWebhook: %v", err)
	}
	d, err := s.RecordAppWebhookDelivery(ctx, pgSampleDelivery(wh.ID, app, acct))
	if err != nil {
		t.Fatalf("RecordAppWebhookDelivery: %v", err)
	}
	if d.ID == "" {
		t.Errorf("ID not auto-assigned")
	}
	if d.Status != state.AppWebhookDeliveryPending {
		t.Errorf("default status = %q, want %q", d.Status, state.AppWebhookDeliveryPending)
	}

	got, err := s.AppWebhookDeliveryByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("AppWebhookDeliveryByID: %v", err)
	}
	if got.WebhookID != wh.ID {
		t.Errorf("webhook_id = %q, want %q", got.WebhookID, wh.ID)
	}

	if _, err := s.AppWebhookDeliveryByID(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("AppWebhookDeliveryByID missing = %v; want ErrNotFound", err)
	}
}

// TestPgStore_ClaimDueAppWebhookDeliveries exercises the dispatcher's
// tick entry. Pins the FOR UPDATE SKIP LOCKED claim + status
// 'pending'/'in_flight' filter + per-account ORDER BY + limit clamp.
func TestPgStore_ClaimDueAppWebhookDeliveries(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx, "claim")

	wh, err := s.CreateAppWebhook(ctx, pgSampleWebhook(acct, app))
	if err != nil {
		t.Fatalf("CreateAppWebhook: %v", err)
	}

	// pending + due → claimable
	d1, err := s.RecordAppWebhookDelivery(ctx, pgSampleDelivery(wh.ID, app, acct))
	if err != nil {
		t.Fatalf("record d1: %v", err)
	}
	// dead → not claimable
	d2, err := s.RecordAppWebhookDelivery(ctx, pgSampleDelivery(wh.ID, app, acct))
	if err != nil {
		t.Fatalf("record d2: %v", err)
	}
	if err := s.MarkAppWebhookDeliveryDead(ctx, d2.ID, d2.Attempt, "exhausted"); err != nil {
		t.Fatalf("MarkDead d2: %v", err)
	}
	// pending but in the future → not claimable
	d3, err := s.RecordAppWebhookDelivery(ctx, pgSampleDelivery(wh.ID, app, acct))
	if err != nil {
		t.Fatalf("record d3: %v", err)
	}
	d3.NextAttemptAt = time.Now().Add(time.Hour)
	if _, err := pool.Exec(ctx,
		`update app_webhook_deliveries set next_attempt_at = $1 where id = $2`,
		d3.NextAttemptAt, d3.ID); err != nil {
		t.Fatalf("push d3 next_attempt_at: %v", err)
	}

	claimed, err := s.ClaimDueAppWebhookDeliveries(ctx, 10, time.Now())
	if err != nil {
		t.Fatalf("ClaimDueAppWebhookDeliveries: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != d1.ID {
		t.Errorf("claimed = %v, want only d1 (%s)", claimed, d1.ID)
	}
	if claimed[0].Status != state.AppWebhookDeliveryInFlight {
		t.Errorf("post-claim status = %q, want in_flight", claimed[0].Status)
	}
}

// TestPgStore_AppWebhookDelivery_Markers exercises Succeeded +
// Failed + Dead + Reset. Pins the column-stamp side-effects.
func TestPgStore_AppWebhookDelivery_Markers(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx, "markers")
	wh, err := s.CreateAppWebhook(ctx, pgSampleWebhook(acct, app))
	if err != nil {
		t.Fatal(err)
	}

	// Succeeded
	ds, err := s.RecordAppWebhookDelivery(ctx, pgSampleDelivery(wh.ID, app, acct))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := s.MarkAppWebhookDeliverySucceeded(ctx, ds.ID, 200, ds.Attempt, now); err != nil {
		t.Fatalf("MarkSucceeded: %v", err)
	}
	got, err := s.AppWebhookDeliveryByID(ctx, ds.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.AppWebhookDeliverySucceeded || got.LastResponseCode != 200 {
		t.Errorf("Succeeded side-effects: %+v", got)
	}

	// Failed → status=pending, next_attempt_at set, attempt++.
	df, err := s.RecordAppWebhookDelivery(ctx, pgSampleDelivery(wh.ID, app, acct))
	if err != nil {
		t.Fatal(err)
	}
	resched := time.Now().Add(30 * time.Second)
	if err := s.MarkAppWebhookDeliveryFailed(ctx, df.ID, 500, df.Attempt, "boom", resched); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	got, err = s.AppWebhookDeliveryByID(ctx, df.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.AppWebhookDeliveryPending || got.LastError != "boom" {
		t.Errorf("Failed side-effects: %+v", got)
	}

	// Dead → status=dead, attempt++.
	dd, err := s.RecordAppWebhookDelivery(ctx, pgSampleDelivery(wh.ID, app, acct))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAppWebhookDeliveryDead(ctx, dd.ID, dd.Attempt, "exhausted"); err != nil {
		t.Fatalf("MarkDead: %v", err)
	}
	got, err = s.AppWebhookDeliveryByID(ctx, dd.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.AppWebhookDeliveryDead {
		t.Errorf("Dead side-effects: status=%q", got.Status)
	}

	// ResetFromDead on a non-dead row → ErrConflict.
	if err := s.ResetAppWebhookDeliveryFromDead(ctx, df.ID, wh.ID, acct, time.Now()); !errors.Is(err, state.ErrConflict) {
		t.Errorf("ResetFromDead on pending row = %v; want ErrConflict", err)
	}

	// ResetFromDead IDOR guard: wrong account → ErrNotFound.
	if err := s.ResetAppWebhookDeliveryFromDead(ctx, dd.ID, wh.ID, "00000000-0000-0000-0000-000000000000", time.Now()); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("ResetFromDead wrong-acct = %v; want ErrNotFound", err)
	}

	// ResetFromDead happy path → status=pending, attempt=0.
	if err := s.ResetAppWebhookDeliveryFromDead(ctx, dd.ID, wh.ID, acct, time.Now()); err != nil {
		t.Fatalf("ResetFromDead: %v", err)
	}
	got, err = s.AppWebhookDeliveryByID(ctx, dd.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.AppWebhookDeliveryPending || got.Attempt != 0 {
		t.Errorf("ResetFromDead side-effects: %+v", got)
	}

	// Marker NotFound guards.
	if err := s.MarkAppWebhookDeliverySucceeded(ctx, "00000000-0000-0000-0000-000000000000", 200, 0, time.Now()); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("MarkSucceeded missing = %v; want ErrNotFound", err)
	}
	if err := s.MarkAppWebhookDeliveryFailed(ctx, "00000000-0000-0000-0000-000000000000", 500, 0, "x", time.Now()); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("MarkFailed missing = %v; want ErrNotFound", err)
	}
	if err := s.MarkAppWebhookDeliveryDead(ctx, "00000000-0000-0000-0000-000000000000", 0, "x"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("MarkDead missing = %v; want ErrNotFound", err)
	}
	if err := s.ResetAppWebhookDeliveryFromDead(ctx, "00000000-0000-0000-0000-000000000000", wh.ID, acct, time.Now()); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("ResetFromDead missing = %v; want ErrNotFound", err)
	}
}

// TestPgStore_ListAppWebhookDeliveries pins the
// (app_id, webhook_id) scope + pageSize cap. The pageToken shape is
// covered by alert_deliveries precedent and not re-pinned here.
func TestPgStore_ListAppWebhookDeliveries(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx, "listdel")
	wh, err := s.CreateAppWebhook(ctx, pgSampleWebhook(acct, app))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := s.RecordAppWebhookDelivery(ctx, pgSampleDelivery(wh.ID, app, acct)); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	rows, _, err := s.ListAppWebhookDeliveries(ctx, app, wh.ID, 2, "")
	if err != nil {
		t.Fatalf("ListAppWebhookDeliveries: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("pageSize=2 returned %d, want 2", len(rows))
	}
	rows, _, err = s.ListAppWebhookDeliveries(ctx, app, wh.ID, 50, "")
	if err != nil {
		t.Fatalf("ListAppWebhookDeliveries all: %v", err)
	}
	if len(rows) != 5 {
		t.Errorf("pageSize=50 returned %d, want 5", len(rows))
	}
}
