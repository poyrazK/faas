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
	"context"
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

func claimPgWebhookDelivery(t *testing.T, s *state.PgStore, ctx context.Context, id string) state.AppWebhookDelivery {
	t.Helper()
	claimed, err := s.ClaimDueAppWebhookDeliveries(ctx, 100, time.Now().Add(time.Second))
	if err != nil {
		t.Fatalf("claim delivery %s: %v", id, err)
	}
	for _, d := range claimed {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("delivery %s was not claimed", id)
	return state.AppWebhookDelivery{}
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
// 'pending'/'in_flight' filter + due time + limit clamp.
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
	dead := pgSampleDelivery(wh.ID, app, acct)
	dead.Status = state.AppWebhookDeliveryDead
	_, err = s.RecordAppWebhookDelivery(ctx, dead)
	if err != nil {
		t.Fatalf("record d2: %v", err)
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

func TestPgStore_ClaimDueAppWebhookDeliveries_FairAcrossAccounts(t *testing.T) {
	s, ctx := pgStore(t)
	const accounts, perAccount, cap = 3, 5, 5
	for i := 0; i < accounts; i++ {
		suffix := fmt.Sprintf("fair-claim-%d", i)
		acct, app, _ := seedLiveDeploy(t, s, ctx, suffix, suffix)
		wh, err := s.CreateAppWebhook(ctx, pgSampleWebhook(acct, app))
		if err != nil {
			t.Fatal(err)
		}
		for j := 0; j < perAccount; j++ {
			d := pgSampleDelivery(wh.ID, app, acct)
			d.NextAttemptAt = time.Now().Add(-time.Minute)
			if _, err := s.RecordAppWebhookDelivery(ctx, d); err != nil {
				t.Fatalf("record account %d delivery %d: %v", i, j, err)
			}
		}
	}
	now := time.Now().Add(time.Second)
	totals := make(map[string]int)
	for tick := 0; tick < 2; tick++ {
		claimed, err := s.ClaimDueAppWebhookDeliveries(ctx, cap, now.Add(time.Duration(tick)*5*time.Second))
		if err != nil {
			t.Fatalf("tick %d claim: %v", tick, err)
		}
		if len(claimed) != cap {
			t.Fatalf("tick %d claimed %d rows, want %d", tick, len(claimed), cap)
		}
		counts := make(map[string]int)
		for i, d := range claimed {
			counts[d.AccountID]++
			totals[d.AccountID]++
			if i > 0 && claimed[i-1].AccountID == d.AccountID {
				t.Errorf("tick %d adjacent claims share account %s", tick, d.AccountID)
			}
		}
		if len(counts) != accounts {
			t.Fatalf("tick %d reached %d accounts, want %d: %+v", tick, len(counts), accounts, counts)
		}
		for accountID, count := range counts {
			if count < 1 || count > 2 {
				t.Errorf("tick %d account %s claimed %d rows, want 1 or 2", tick, accountID, count)
			}
		}
	}
	min, max := cap*2, 0
	for _, count := range totals {
		if count < min {
			min = count
		}
		if count > max {
			max = count
		}
	}
	if max-min > 1 {
		t.Errorf("two-tick account totals = %+v, want gap <= 1", totals)
	}
}

func TestPgStore_ClaimDueAppWebhookDeliveries_RotatesBeyondBatchSize(t *testing.T) {
	s, ctx := pgStore(t)
	const accounts, batchSize = 12, 4
	for i := 0; i < accounts; i++ {
		suffix := fmt.Sprintf("rotate-claim-%d", i)
		acct, app, _ := seedLiveDeploy(t, s, ctx, suffix, suffix)
		wh, err := s.CreateAppWebhook(ctx, pgSampleWebhook(acct, app))
		if err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 3; j++ {
			d := pgSampleDelivery(wh.ID, app, acct)
			d.NextAttemptAt = time.Now().Add(-time.Minute)
			if _, err := s.RecordAppWebhookDelivery(ctx, d); err != nil {
				t.Fatalf("record account %d delivery %d: %v", i, j, err)
			}
		}
	}
	base := time.Now().Truncate(5 * time.Second).Add(5 * time.Second)
	seen := make(map[string]bool)
	for tick := 0; tick < 3; tick++ {
		claimed, err := s.ClaimDueAppWebhookDeliveries(ctx, batchSize, base.Add(time.Duration(tick)*5*time.Second))
		if err != nil || len(claimed) != batchSize {
			t.Fatalf("tick %d claimed %d rows, %v; want %d", tick, len(claimed), err, batchSize)
		}
		for _, d := range claimed {
			if seen[d.AccountID] {
				t.Errorf("account %s claimed again before every account received a slot", d.AccountID)
			}
			seen[d.AccountID] = true
		}
	}
	if len(seen) != accounts {
		t.Errorf("claims reached %d accounts, want %d", len(seen), accounts)
	}
}

func TestPgStore_AppWebhookDelivery_AttemptHistorySurvivesReplay(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx, "attempt-history")
	wh, err := s.CreateAppWebhook(ctx, pgSampleWebhook(acct, app))
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.RecordAppWebhookDelivery(ctx, pgSampleDelivery(wh.ID, app, acct))
	if err != nil {
		t.Fatal(err)
	}
	claim := func() state.AppWebhookDelivery {
		t.Helper()
		rows, err := s.ClaimDueAppWebhookDeliveries(ctx, 1, time.Now().Add(time.Second))
		if err != nil || len(rows) != 1 {
			t.Fatalf("claim = %+v, err=%v", rows, err)
		}
		return rows[0]
	}
	first := claim()
	started := time.Now().UTC().Add(-150 * time.Millisecond).Truncate(time.Microsecond)
	finished := started.Add(75 * time.Millisecond)
	if err := s.MarkAppWebhookDeliveryFailed(ctx, d.ID, 503, first.Attempt, first.NextAttemptAt,
		"receiver unavailable", time.Now().Add(-time.Second), state.AppWebhookAttemptMetadata{StartedAt: started, FinishedAt: finished}); err != nil {
		t.Fatal(err)
	}
	second := claim()
	if err := s.MarkAppWebhookDeliveryDead(ctx, d.ID, second.Attempt, second.NextAttemptAt,
		"receiver rejected", state.AppWebhookAttemptMetadata{ResponseCode: 410}); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetAppWebhookDeliveryFromDead(ctx, d.ID, wh.ID, acct, time.Now()); err != nil {
		t.Fatal(err)
	}
	third := claim()
	if err := s.MarkAppWebhookDeliverySucceeded(ctx, d.ID, 204, third.Attempt, third.NextAttemptAt, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAppWebhookDeliverySucceeded(ctx, d.ID, 200, first.Attempt, first.NextAttemptAt, time.Now()); !errors.Is(err, state.ErrConflict) {
		t.Errorf("stale outcome = %v, want conflict", err)
	}
	page, next, err := s.ListAppWebhookDeliveryAttempts(ctx, d.ID, wh.ID, acct, 2, "")
	if err != nil || len(page) != 2 || next == "" {
		t.Fatalf("first page = %+v, next=%q, err=%v", page, next, err)
	}
	if page[0].ReplayGeneration != 1 || page[0].AttemptNumber != 1 || page[0].Outcome != "succeeded" || page[0].ResponseCode != 204 {
		t.Errorf("replay attempt = %+v", page[0])
	}
	if page[1].ReplayGeneration != 0 || page[1].AttemptNumber != 2 || page[1].Outcome != "dead" || page[1].ResponseCode != 410 {
		t.Errorf("terminal attempt = %+v", page[1])
	}
	older, next, err := s.ListAppWebhookDeliveryAttempts(ctx, d.ID, wh.ID, acct, 2, next)
	if err != nil || len(older) != 1 || next != "" {
		t.Fatalf("older page = %+v, next=%q, err=%v", older, next, err)
	}
	if older[0].Outcome != "retrying" || older[0].ResponseCode != 503 || !older[0].StartedAt.Equal(started) || !older[0].FinishedAt.Equal(finished) || older[0].NextAttemptAt == nil {
		t.Errorf("first attempt = %+v", older[0])
	}
	foreign, _, err := s.ListAppWebhookDeliveryAttempts(ctx, d.ID, wh.ID, "00000000-0000-0000-0000-000000000000", 10, "")
	if err != nil || len(foreign) != 0 {
		t.Errorf("foreign history = %+v, err=%v", foreign, err)
	}
}

func TestPgStore_ClaimDueAppWebhookDeliveries_SkipsLockedRowsWithinAccount(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx, "locked-claim")
	wh, err := s.CreateAppWebhook(ctx, pgSampleWebhook(acct, app))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		d := pgSampleDelivery(wh.ID, app, acct)
		d.NextAttemptAt = time.Now().Add(-time.Minute)
		if _, err := s.RecordAppWebhookDelivery(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		select id from app_webhook_deliveries
		 where webhook_id = $1
		 order by next_attempt_at, id
		 limit 10 for update
	`, wh.ID)
	if err != nil {
		t.Fatal(err)
	}
	locked := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		locked[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(locked) != 10 {
		t.Fatalf("locked %d rows, want 10", len(locked))
	}
	claimed, err := s.ClaimDueAppWebhookDeliveries(ctx, 10, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 10 {
		t.Fatalf("claimed %d rows despite 20 unlocked rows, want 10", len(claimed))
	}
	for _, d := range claimed {
		if locked[d.ID] {
			t.Errorf("claimed locked row %s", d.ID)
		}
	}
}

func TestPgStore_AppWebhookDelivery_ClaimLeaseFencesStaleOutcome(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx, "lease")
	wh, err := s.CreateAppWebhook(ctx, pgSampleWebhook(acct, app))
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.RecordAppWebhookDelivery(ctx, pgSampleDelivery(wh.ID, app, acct))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(time.Second).UTC().Truncate(time.Microsecond)
	first, err := s.ClaimDueAppWebhookDeliveries(ctx, 1, now)
	if err != nil || len(first) != 1 || first[0].ID != d.ID {
		t.Fatalf("first claim = %+v, %v", first, err)
	}
	if got := first[0].NextAttemptAt; !got.Equal(now.Add(state.AppWebhookClaimLease)) {
		t.Fatalf("claim deadline = %v, want %v", got, now.Add(state.AppWebhookClaimLease))
	}
	early, err := s.ClaimDueAppWebhookDeliveries(ctx, 1, now.Add(state.AppWebhookClaimLease-time.Microsecond))
	if err != nil || len(early) != 0 {
		t.Fatalf("reclaim before expiry = %+v, %v", early, err)
	}
	second, err := s.ClaimDueAppWebhookDeliveries(ctx, 1, first[0].NextAttemptAt.Add(time.Second))
	if err != nil || len(second) != 1 || second[0].ID != d.ID {
		t.Fatalf("reclaim after expiry = %+v, %v", second, err)
	}
	if err := s.MarkAppWebhookDeliverySucceeded(ctx, d.ID, 200, first[0].Attempt, first[0].NextAttemptAt, now); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale completion = %v, want ErrConflict", err)
	}
	if err := s.MarkAppWebhookDeliveryFailed(ctx, d.ID, 500, first[0].Attempt, first[0].NextAttemptAt, "stale", now); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale retry = %v, want ErrConflict", err)
	}
	if err := s.MarkAppWebhookDeliveryDead(ctx, d.ID, first[0].Attempt, first[0].NextAttemptAt, "stale"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale dead letter = %v, want ErrConflict", err)
	}
	if err := s.MarkAppWebhookDeliverySucceeded(ctx, d.ID, 200, second[0].Attempt, second[0].NextAttemptAt, now); err != nil {
		t.Fatalf("current completion: %v", err)
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
	ds = claimPgWebhookDelivery(t, s, ctx, ds.ID)
	if err := s.MarkAppWebhookDeliverySucceeded(ctx, ds.ID, 200, ds.Attempt, ds.NextAttemptAt, now); err != nil {
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
	df = claimPgWebhookDelivery(t, s, ctx, df.ID)
	if err := s.MarkAppWebhookDeliveryFailed(ctx, df.ID, 500, df.Attempt, df.NextAttemptAt, "boom", resched); err != nil {
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
	dd = claimPgWebhookDelivery(t, s, ctx, dd.ID)
	if err := s.MarkAppWebhookDeliveryDead(ctx, dd.ID, dd.Attempt, dd.NextAttemptAt, "exhausted"); err != nil {
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
	if err := s.MarkAppWebhookDeliverySucceeded(ctx, "00000000-0000-0000-0000-000000000000", 200, 0, time.Time{}, time.Now()); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("MarkSucceeded missing = %v; want ErrNotFound", err)
	}
	if err := s.MarkAppWebhookDeliveryFailed(ctx, "00000000-0000-0000-0000-000000000000", 500, 0, time.Time{}, "x", time.Now()); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("MarkFailed missing = %v; want ErrNotFound", err)
	}
	if err := s.MarkAppWebhookDeliveryDead(ctx, "00000000-0000-0000-0000-000000000000", 0, time.Time{}, "x"); !errors.Is(err, state.ErrNotFound) {
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
