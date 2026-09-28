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
	"sync"
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

func TestPgStore_AppWebhookDeliveryHealth(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx, "delivery-health")
	hook, err := s.CreateAppWebhook(ctx, pgSampleWebhook(acct, app))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	first := pgSampleDelivery(hook.ID, app, acct)
	first.NextAttemptAt = now.Add(-3 * time.Minute)
	delivered, err := s.RecordAppWebhookDelivery(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	claim := claimPgWebhookDelivery(t, s, ctx, delivered.ID)
	if err := s.MarkAppWebhookDeliverySucceeded(ctx, delivered.ID, 200, claim.Attempt, claim.NextAttemptAt, now); err != nil {
		t.Fatal(err)
	}
	second := pgSampleDelivery(hook.ID, app, acct)
	second.NextAttemptAt = now.Add(-2 * time.Minute)
	dead, err := s.RecordAppWebhookDelivery(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	claim = claimPgWebhookDelivery(t, s, ctx, dead.ID)
	if err := s.MarkAppWebhookDeliveryDead(ctx, dead.ID, claim.Attempt, claim.NextAttemptAt, "receiver rejected"); err != nil {
		t.Fatal(err)
	}
	third := pgSampleDelivery(hook.ID, app, acct)
	third.NextAttemptAt = now.Add(-time.Minute)
	if _, err := s.RecordAppWebhookDelivery(ctx, third); err != nil {
		t.Fatal(err)
	}
	health, err := s.AppWebhookDeliveryHealth(ctx, hook.ID, acct, now)
	if err != nil || health.ReceiverState != state.AppWebhookReceiverReady ||
		health.PendingCount != 1 || health.DeadCount != 1 || health.RecentSucceededCount != 1 ||
		health.RecentDeadCount != 1 || health.OldestOverdueAt == nil || !health.OldestOverdueAt.Equal(third.NextAttemptAt) {
		t.Fatalf("health = %+v, err=%v", health, err)
	}
	oldest, err := s.OldestOverdueAppWebhookDeliveryAt(ctx, now)
	if err != nil || oldest == nil || !oldest.Equal(third.NextAttemptAt) {
		t.Fatalf("fleet oldest = %v, err=%v", oldest, err)
	}
	if _, err := s.AppWebhookDeliveryHealth(ctx, hook.ID, "00000000-0000-0000-0000-000000000000", now); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign health = %v, want not found", err)
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

func TestPgStore_ClaimDueAppWebhookDeliveries_CapsConcurrentSchedulers(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	otherScheduler := state.NewPgStore(pool)
	acct, app, _ := seedLiveDeploy(t, s, ctx, "subscription-cap")
	slow, err := s.CreateAppWebhook(ctx, pgSampleWebhook(acct, app))
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateAppWebhook(ctx, pgSampleWebhook(acct, app))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)
	for i := 0; i < 12; i++ {
		d := pgSampleDelivery(slow.ID, app, acct)
		d.NextAttemptAt = now.Add(-time.Minute)
		if _, err := s.RecordAppWebhookDelivery(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		d := pgSampleDelivery(other.ID, app, acct)
		d.NextAttemptAt = now.Add(-time.Second)
		if _, err := s.RecordAppWebhookDelivery(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	type result struct {
		rows []state.AppWebhookDelivery
		err  error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, scheduler := range []*state.PgStore{s, otherScheduler} {
		go func(scheduler *state.PgStore) {
			ready.Done()
			<-start
			rows, err := scheduler.ClaimDueAppWebhookDeliveries(ctx, 20, now)
			results <- result{rows: rows, err: err}
		}(scheduler)
	}
	ready.Wait()
	close(start)
	var slowClaims []state.AppWebhookDelivery
	otherClaims := 0
	for i := 0; i < 2; i++ {
		out := <-results
		if out.err != nil {
			t.Fatal(out.err)
		}
		for _, d := range out.rows {
			if d.WebhookID == slow.ID {
				slowClaims = append(slowClaims, d)
			} else if d.WebhookID == other.ID {
				otherClaims++
			}
		}
	}
	if len(slowClaims) != state.AppWebhookMaxInFlightPerSubscription || otherClaims != 2 {
		t.Fatalf("concurrent claims: slow=%d other=%d, want %d and 2", len(slowClaims), otherClaims, state.AppWebhookMaxInFlightPerSubscription)
	}
	if oldest, err := s.OldestOverdueAppWebhookDeliveryAt(ctx, now.Add(time.Second)); err != nil || oldest != nil {
		t.Fatalf("fleet oldest with full live capacity = %v, %v; want nil", oldest, err)
	}
	blocked, err := s.ClaimDueAppWebhookDeliveries(ctx, 20, now.Add(time.Second))
	if err != nil || len(blocked) != 0 {
		t.Fatalf("claim while leases live = %+v, %v; want none", blocked, err)
	}
	claimed := slowClaims[0]
	if err := s.MarkAppWebhookDeliverySucceeded(ctx, claimed.ID, 200, claimed.Attempt, claimed.NextAttemptAt, now); err != nil {
		t.Fatal(err)
	}
	replacement, err := s.ClaimDueAppWebhookDeliveries(ctx, 20, now.Add(time.Second))
	if err != nil || len(replacement) != 1 || replacement[0].WebhookID != slow.ID {
		t.Fatalf("claim after slot freed = %+v, %v; want one slow delivery", replacement, err)
	}
	afterExpiry, err := s.ClaimDueAppWebhookDeliveries(ctx, 20, now.Add(state.AppWebhookClaimLease+2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	expiredCounts := make(map[string]int)
	for _, d := range afterExpiry {
		expiredCounts[d.WebhookID]++
	}
	if expiredCounts[slow.ID] != state.AppWebhookMaxInFlightPerSubscription || expiredCounts[other.ID] != 2 {
		t.Fatalf("claim after expiry: slow=%d other=%d, want %d and 2", expiredCounts[slow.ID], expiredCounts[other.ID], state.AppWebhookMaxInFlightPerSubscription)
	}
}

func TestPgStore_ClaimDueAppWebhookDeliveries_FairAcrossSubscriptions(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx, "subscription-fairness")
	busy, err := s.CreateAppWebhook(ctx, pgSampleWebhook(acct, app))
	if err != nil {
		t.Fatal(err)
	}
	quiet, err := s.CreateAppWebhook(ctx, pgSampleWebhook(acct, app))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)
	// More busy rows than the old account-wide candidate window.
	for i := 0; i < 129; i++ {
		d := pgSampleDelivery(busy.ID, app, acct)
		d.NextAttemptAt = now.Add(-time.Minute)
		if _, err := s.RecordAppWebhookDelivery(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	d := pgSampleDelivery(quiet.ID, app, acct)
	d.NextAttemptAt = now.Add(-time.Second)
	if _, err := s.RecordAppWebhookDelivery(ctx, d); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimDueAppWebhookDeliveries(ctx, 32, now)
	if err != nil {
		t.Fatal(err)
	}
	counts := make(map[string]int)
	for _, row := range claimed {
		counts[row.WebhookID]++
	}
	if counts[busy.ID] != state.AppWebhookMaxInFlightPerSubscription || counts[quiet.ID] != 1 {
		t.Fatalf("first batch: busy=%d quiet=%d, want %d and 1", counts[busy.ID], counts[quiet.ID], state.AppWebhookMaxInFlightPerSubscription)
	}
}

func TestPgStore_ClaimDueAppWebhookDeliveries_RotatesSubscriptionsBeyondBatch(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx, "subscription-rotation")
	const hooks, batchSize = 10, 4
	now := time.Now().UTC().Add(time.Second).Truncate(5 * time.Second)
	for i := 0; i < hooks; i++ {
		hook, err := s.CreateAppWebhook(ctx, pgSampleWebhook(acct, app))
		if err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 3; j++ {
			d := pgSampleDelivery(hook.ID, app, acct)
			d.NextAttemptAt = now.Add(-time.Minute)
			if _, err := s.RecordAppWebhookDelivery(ctx, d); err != nil {
				t.Fatal(err)
			}
		}
	}
	seen := make(map[string]bool)
	for tick := 0; tick < 3; tick++ {
		claimed, err := s.ClaimDueAppWebhookDeliveries(ctx, batchSize, now.Add(time.Duration(tick)*5*time.Second))
		if err != nil || len(claimed) != batchSize {
			t.Fatalf("tick %d claim = %+v, %v; want %d", tick, claimed, err, batchSize)
		}
		for _, d := range claimed {
			seen[d.WebhookID] = true
		}
	}
	if len(seen) != hooks {
		t.Fatalf("reached %d subscriptions in three ticks, want %d", len(seen), hooks)
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

func TestPgStore_AppWebhookReceiverCooldownClaimAndHealth(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx, "receiver-cooldown")
	paused, err := s.CreateAppWebhook(ctx, pgSampleWebhook(acct, app))
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateAppWebhook(ctx, pgSampleWebhook(acct, app))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)
	for i := 0; i < 3; i++ {
		d := pgSampleDelivery(paused.ID, app, acct)
		d.NextAttemptAt = now.Add(time.Duration(i-3) * time.Minute)
		if _, err := s.RecordAppWebhookDelivery(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	claims, err := s.ClaimDueAppWebhookDeliveries(ctx, 2, now)
	if err != nil || len(claims) != 2 || claims[0].WebhookID != paused.ID || claims[1].WebhookID != paused.ID {
		t.Fatalf("first claims = %+v, %v; want two paused-subscription rows", claims, err)
	}
	longUntil, shortUntil := now.Add(5*time.Minute), now.Add(time.Minute)
	if err := s.MarkAppWebhookDeliveryFailed(ctx, claims[0].ID, 429, claims[0].Attempt, claims[0].NextAttemptAt,
		"rate limited", longUntil, state.AppWebhookAttemptMetadata{ReceiverCooldownUntil: &longUntil, ReceiverCooldownTargetURL: paused.TargetURL}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAppWebhookDeliveryFailed(ctx, claims[1].ID, 503, claims[1].Attempt, claims[1].NextAttemptAt,
		"unavailable", shortUntil, state.AppWebhookAttemptMetadata{ReceiverCooldownUntil: &shortUntil, ReceiverCooldownTargetURL: paused.TargetURL}); err != nil {
		t.Fatal(err)
	}
	staleUntil := now.Add(10 * time.Minute)
	if err := s.MarkAppWebhookDeliveryFailed(ctx, claims[0].ID, 429, claims[0].Attempt, claims[0].NextAttemptAt,
		"stale", staleUntil, state.AppWebhookAttemptMetadata{ReceiverCooldownUntil: &staleUntil, ReceiverCooldownTargetURL: paused.TargetURL}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale cooldown extension = %v, want conflict", err)
	}
	otherDelivery := pgSampleDelivery(other.ID, app, acct)
	otherDelivery.NextAttemptAt = now.Add(-time.Second)
	otherDelivery, err = s.RecordAppWebhookDelivery(ctx, otherDelivery)
	if err != nil {
		t.Fatal(err)
	}
	health, err := s.AppWebhookDeliveryHealth(ctx, paused.ID, acct, now.Add(2*time.Minute))
	if err != nil || health.ReceiverState != state.AppWebhookReceiverCoolingDown ||
		health.ReceiverCooldownUntil == nil || !health.ReceiverCooldownUntil.Equal(longUntil) ||
		health.PendingCount != 3 || health.OldestOverdueAt != nil {
		t.Fatalf("paused health = %+v, %v", health, err)
	}
	claims, err = s.ClaimDueAppWebhookDeliveries(ctx, 10, now.Add(2*time.Minute))
	if err != nil || len(claims) != 1 || claims[0].ID != otherDelivery.ID {
		t.Fatalf("claims during cooldown = %+v, %v; want other subscription only", claims, err)
	}
	if err := s.MarkAppWebhookDeliverySucceeded(ctx, claims[0].ID, 200, claims[0].Attempt, claims[0].NextAttemptAt, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if oldest, err := s.OldestOverdueAppWebhookDeliveryAt(ctx, now.Add(2*time.Minute)); err != nil || oldest != nil {
		t.Fatalf("fleet oldest during cooldown = %v, %v; want nil", oldest, err)
	}
	if health, err := s.AppWebhookDeliveryHealth(ctx, paused.ID, acct, longUntil); err != nil ||
		health.ReceiverState != state.AppWebhookReceiverAwaitingProbe || health.OldestOverdueAt == nil {
		t.Fatalf("health awaiting recovery probe = %+v, %v", health, err)
	}
	claims, err = s.ClaimDueAppWebhookDeliveries(ctx, 10, longUntil)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims at cooldown expiry = %+v, %v; want one recovery probe", claims, err)
	}
	blocked, err := s.ClaimDueAppWebhookDeliveries(ctx, 10, longUntil.Add(time.Second))
	if err != nil || len(blocked) != 0 {
		t.Fatalf("claims while probe is live = %+v, %v; want none", blocked, err)
	}
	if health, err := s.AppWebhookDeliveryHealth(ctx, paused.ID, acct, longUntil.Add(time.Second)); err != nil ||
		health.ReceiverState != state.AppWebhookReceiverProbing || health.OldestOverdueAt != nil {
		t.Fatalf("health during probe = %+v, %v", health, err)
	}
	if oldest, err := s.OldestOverdueAppWebhookDeliveryAt(ctx, longUntil.Add(time.Second)); err != nil || oldest != nil {
		t.Fatalf("fleet oldest during probe = %v, %v; want nil", oldest, err)
	}
	finished := longUntil.Add(2 * time.Second)
	if err := s.MarkAppWebhookDeliverySucceeded(ctx, claims[0].ID, 200, claims[0].Attempt, claims[0].NextAttemptAt,
		finished, state.AppWebhookAttemptMetadata{FinishedAt: finished}); err != nil {
		t.Fatal(err)
	}
	if health, err := s.AppWebhookDeliveryHealth(ctx, paused.ID, acct, finished); err != nil ||
		health.ReceiverState != state.AppWebhookReceiverReady || health.OldestOverdueAt == nil {
		t.Fatalf("health after successful probe = %+v, %v", health, err)
	}
	reopened, err := s.ClaimDueAppWebhookDeliveries(ctx, 10, finished)
	if err != nil || len(reopened) != 2 {
		t.Fatalf("claims after probe succeeds = %+v, %v; want remaining two", reopened, err)
	}
}

func TestPgStore_AppWebhookReceiverCooldownRetarget(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx, "receiver-cooldown-retarget")
	hook, err := s.CreateAppWebhook(ctx, pgSampleWebhook(acct, app))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Second)
	for i := 0; i < 3; i++ {
		d := pgSampleDelivery(hook.ID, app, acct)
		d.NextAttemptAt = now.Add(time.Duration(i-3) * time.Minute)
		if _, err := s.RecordAppWebhookDelivery(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	claims, err := s.ClaimDueAppWebhookDeliveries(ctx, 2, now)
	if err != nil || len(claims) != 2 {
		t.Fatalf("claims = %+v, %v", claims, err)
	}
	until := now.Add(5 * time.Minute)
	meta := state.AppWebhookAttemptMetadata{ReceiverCooldownUntil: &until, ReceiverCooldownTargetURL: hook.TargetURL}
	if err := s.MarkAppWebhookDeliveryFailed(ctx, claims[0].ID, 429, claims[0].Attempt, claims[0].NextAttemptAt,
		"rate limited", until, meta); err != nil {
		t.Fatal(err)
	}
	newURL := "https://example.com/new-receiver-" + uuid.NewString()
	if _, err := s.UpdateAppWebhook(ctx, hook.ID, state.UpdateAppWebhookParams{TargetURL: &newURL}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAppWebhookDeliveryFailed(ctx, claims[1].ID, 429, claims[1].Attempt, claims[1].NextAttemptAt,
		"old receiver", until, meta); err != nil {
		t.Fatal(err)
	}
	health, err := s.AppWebhookDeliveryHealth(ctx, hook.ID, acct, now)
	if err != nil || health.ReceiverCooldownUntil != nil {
		t.Fatalf("health after retarget = %+v, %v; want no cooldown", health, err)
	}
	claims, err = s.ClaimDueAppWebhookDeliveries(ctx, 10, now)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims for new receiver = %+v, %v; want remaining due row", claims, err)
	}
}

func TestPgStore_AppWebhookRecoveryProbeAcrossSchedulers(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	otherScheduler := state.NewPgStore(pool)
	acct, app, _ := seedLiveDeploy(t, s, ctx, "receiver-recovery")
	hook, err := s.CreateAppWebhook(ctx, pgSampleWebhook(acct, app))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)
	for i := 0; i < 5; i++ {
		d := pgSampleDelivery(hook.ID, app, acct)
		d.NextAttemptAt = now.Add(-time.Minute)
		if _, err := s.RecordAppWebhookDelivery(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	initial, err := s.ClaimDueAppWebhookDeliveries(ctx, 1, now)
	if err != nil || len(initial) != 1 {
		t.Fatalf("initial claim = %+v, %v", initial, err)
	}
	until := now.Add(time.Minute)
	if err := s.MarkAppWebhookDeliveryFailed(ctx, initial[0].ID, 429, initial[0].Attempt, initial[0].NextAttemptAt,
		"rate limited", until, state.AppWebhookAttemptMetadata{FinishedAt: now, ReceiverCooldownUntil: &until, ReceiverCooldownTargetURL: hook.TargetURL}); err != nil {
		t.Fatal(err)
	}
	type result struct {
		rows []state.AppWebhookDelivery
		err  error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, scheduler := range []*state.PgStore{s, otherScheduler} {
		go func(scheduler *state.PgStore) {
			ready.Done()
			<-start
			rows, err := scheduler.ClaimDueAppWebhookDeliveries(ctx, 10, until)
			results <- result{rows, err}
		}(scheduler)
	}
	ready.Wait()
	close(start)
	var probes []state.AppWebhookDelivery
	for i := 0; i < 2; i++ {
		out := <-results
		if out.err != nil {
			t.Fatal(out.err)
		}
		probes = append(probes, out.rows...)
	}
	if len(probes) != 1 {
		t.Fatalf("two schedulers claimed %+v; want one recovery probe", probes)
	}
	if rows, err := s.ClaimDueAppWebhookDeliveries(ctx, 10, until.Add(time.Second)); err != nil || len(rows) != 0 {
		t.Fatalf("while probe live = %+v, %v; want none", rows, err)
	}
	failedAt := until.Add(2 * time.Second)
	if err := s.MarkAppWebhookDeliveryFailed(ctx, probes[0].ID, 503, probes[0].Attempt, probes[0].NextAttemptAt,
		"unavailable", failedAt.Add(time.Minute), state.AppWebhookAttemptMetadata{FinishedAt: failedAt}); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.ClaimDueAppWebhookDeliveries(ctx, 10, failedAt.Add(state.AppWebhookRecoveryRetryDelay-time.Microsecond)); err != nil || len(rows) != 0 {
		t.Fatalf("before recovery fallback = %+v, %v; want none", rows, err)
	}
	second, err := s.ClaimDueAppWebhookDeliveries(ctx, 10, failedAt.Add(state.AppWebhookRecoveryRetryDelay))
	if err != nil || len(second) != 1 {
		t.Fatalf("second probe = %+v, %v", second, err)
	}
	renewed := failedAt.Add(2 * time.Minute)
	if err := s.MarkAppWebhookDeliveryFailed(ctx, second[0].ID, 429, second[0].Attempt, second[0].NextAttemptAt,
		"rate limited again", renewed, state.AppWebhookAttemptMetadata{FinishedAt: failedAt.Add(state.AppWebhookRecoveryRetryDelay), ReceiverCooldownUntil: &renewed, ReceiverCooldownTargetURL: hook.TargetURL}); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.ClaimDueAppWebhookDeliveries(ctx, 10, renewed.Add(-time.Microsecond)); err != nil || len(rows) != 0 {
		t.Fatalf("before renewed deadline = %+v, %v; want none", rows, err)
	}
	third, err := s.ClaimDueAppWebhookDeliveries(ctx, 10, renewed)
	if err != nil || len(third) != 1 {
		t.Fatalf("third probe = %+v, %v", third, err)
	}
	finished := renewed.Add(time.Second)
	if err := s.MarkAppWebhookDeliverySucceeded(ctx, third[0].ID, 200, third[0].Attempt, third[0].NextAttemptAt,
		finished, state.AppWebhookAttemptMetadata{FinishedAt: finished}); err != nil {
		t.Fatal(err)
	}
	remaining, err := s.ClaimDueAppWebhookDeliveries(ctx, 10, finished)
	if err != nil || len(remaining) != state.AppWebhookMaxInFlightPerSubscription {
		t.Fatalf("after successful probe = %+v, %v; want normal capacity", remaining, err)
	}
}

func TestPgStore_AppWebhookRecoveryProbeExpiredLease(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx, "receiver-probe-reclaim")
	hook, err := s.CreateAppWebhook(ctx, pgSampleWebhook(acct, app))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)
	d := pgSampleDelivery(hook.ID, app, acct)
	d.NextAttemptAt = now.Add(-time.Minute)
	if _, err := s.RecordAppWebhookDelivery(ctx, d); err != nil {
		t.Fatal(err)
	}
	initial, err := s.ClaimDueAppWebhookDeliveries(ctx, 1, now)
	if err != nil || len(initial) != 1 {
		t.Fatalf("initial claim = %+v, %v", initial, err)
	}
	until := now.Add(time.Minute)
	if err := s.MarkAppWebhookDeliveryFailed(ctx, initial[0].ID, 429, initial[0].Attempt, initial[0].NextAttemptAt,
		"rate limited", until, state.AppWebhookAttemptMetadata{ReceiverCooldownUntil: &until, ReceiverCooldownTargetURL: hook.TargetURL}); err != nil {
		t.Fatal(err)
	}
	probe, err := s.ClaimDueAppWebhookDeliveries(ctx, 1, until)
	if err != nil || len(probe) != 1 {
		t.Fatalf("recovery claim = %+v, %v", probe, err)
	}
	if health, err := s.AppWebhookDeliveryHealth(ctx, hook.ID, acct, until); err != nil || health.ReceiverState != state.AppWebhookReceiverProbing {
		t.Fatalf("live probe health = %+v, %v", health, err)
	}
	expiredAt := probe[0].NextAttemptAt.Add(time.Microsecond)
	if health, err := s.AppWebhookDeliveryHealth(ctx, hook.ID, acct, expiredAt); err != nil ||
		health.ReceiverState != state.AppWebhookReceiverAwaitingProbe || health.OldestOverdueAt == nil {
		t.Fatalf("expired probe health = %+v, %v", health, err)
	}
	reclaimed, err := state.NewPgStore(pool).ClaimDueAppWebhookDeliveries(ctx, 1, expiredAt)
	if err != nil || len(reclaimed) != 1 || reclaimed[0].ID != probe[0].ID {
		t.Fatalf("reclaimed probe = %+v, %v", reclaimed, err)
	}
	if err := s.MarkAppWebhookDeliverySucceeded(ctx, probe[0].ID, 200, probe[0].Attempt, probe[0].NextAttemptAt,
		until); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale probe outcome = %v; want conflict", err)
	}
	finished := reclaimed[0].NextAttemptAt.Add(-time.Second)
	if err := s.MarkAppWebhookDeliverySucceeded(ctx, reclaimed[0].ID, 200, reclaimed[0].Attempt, reclaimed[0].NextAttemptAt,
		finished, state.AppWebhookAttemptMetadata{FinishedAt: finished}); err != nil {
		t.Fatal(err)
	}
	var cooldown, marker any
	if err := pool.QueryRow(ctx, `select receiver_cooldown_until, receiver_recovery_probe_delivery_id from app_webhooks where id = $1`, hook.ID).Scan(&cooldown, &marker); err != nil {
		t.Fatal(err)
	}
	if cooldown != nil || marker != nil {
		t.Fatalf("successful reclaimed probe left cooldown=%v marker=%v", cooldown, marker)
	}
}

func TestPgStore_AppWebhookRecoveryProbePreservesNewerCooldown(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx, "receiver-probe-newer-cooldown")
	hook, err := s.CreateAppWebhook(ctx, pgSampleWebhook(acct, app))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)
	for i := 0; i < 3; i++ {
		d := pgSampleDelivery(hook.ID, app, acct)
		d.NextAttemptAt = now.Add(time.Duration(i-3) * time.Minute)
		if _, err := s.RecordAppWebhookDelivery(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	initial, err := s.ClaimDueAppWebhookDeliveries(ctx, 2, now)
	if err != nil || len(initial) != 2 {
		t.Fatalf("initial claims = %+v, %v", initial, err)
	}
	until := now.Add(time.Minute)
	if err := s.MarkAppWebhookDeliveryFailed(ctx, initial[0].ID, 429, initial[0].Attempt, initial[0].NextAttemptAt,
		"rate limited", until, state.AppWebhookAttemptMetadata{ReceiverCooldownUntil: &until, ReceiverCooldownTargetURL: hook.TargetURL}); err != nil {
		t.Fatal(err)
	}
	probe, err := s.ClaimDueAppWebhookDeliveries(ctx, 10, until)
	if err != nil || len(probe) != 1 || probe[0].ID == initial[1].ID {
		t.Fatalf("recovery probe = %+v, %v; want the still-pending row", probe, err)
	}
	newer := until.Add(time.Minute)
	if err := s.MarkAppWebhookDeliveryFailed(ctx, initial[1].ID, 429, initial[1].Attempt, initial[1].NextAttemptAt,
		"late rate limit", newer, state.AppWebhookAttemptMetadata{FinishedAt: until, ReceiverCooldownUntil: &newer, ReceiverCooldownTargetURL: hook.TargetURL}); err != nil {
		t.Fatal(err)
	}
	finished := until.Add(time.Second)
	if err := s.MarkAppWebhookDeliverySucceeded(ctx, probe[0].ID, 200, probe[0].Attempt, probe[0].NextAttemptAt,
		finished, state.AppWebhookAttemptMetadata{FinishedAt: finished}); err != nil {
		t.Fatal(err)
	}
	health, err := s.AppWebhookDeliveryHealth(ctx, hook.ID, acct, finished)
	if err != nil || health.ReceiverCooldownUntil == nil || !health.ReceiverCooldownUntil.Equal(newer) {
		t.Fatalf("late cooldown lost after probe success: %+v, %v", health, err)
	}
	if rows, err := s.ClaimDueAppWebhookDeliveries(ctx, 10, finished); err != nil || len(rows) != 0 {
		t.Fatalf("claims during newer cooldown = %+v, %v; want none", rows, err)
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
	if len(claimed) != state.AppWebhookMaxInFlightPerSubscription {
		t.Fatalf("claimed %d rows despite 20 unlocked rows, want %d", len(claimed), state.AppWebhookMaxInFlightPerSubscription)
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
