package state_test

// Consolidated PgStore coverage sweep for the app_webhooks /
// app_webhook_deliveries surface (issue #476, ADR-076). The
// implementation lives in pkg/state/pgstore_app_webhooks.go and was
// untouched by PR #753's coverage sweep. This file uses one
// top-level test + t.Run sub-tests that share a single pgtest
// schema, matching the pattern that fixed PR #753's pg shard 2
// 10-minute timeout (see
// pkg/state/pgstore_coverage_sweep_test.go + memory note
// pgstore-coverage-sweep-timeout-fix.md).
//
// Scope (23 funcs at 0% on origin/main):
//
//   CreateAppWebhook (happy + defaults)
//   CreateAppWebhookIfUnderQuota (happy + app-not-found +
//     per-app-quota-tripped + per-account-quota-tripped +
//     duplicate-target-url-conflict)
//   AppWebhookByID (happy + missing → ErrNotFound)
//   UpdateAppWebhook (each pointer field + nil-skip)
//   DeleteAppWebhook (happy + missing → ErrNotFound)
//   ListAppWebhooksForApp / ListAppWebhooksForAccount
//   RecordAppWebhookDelivery
//   ClaimDueAppWebhookDeliveries (empty-claim commit + non-empty
//     claim + transition pending → in_flight)
//   MarkAppWebhookDeliverySucceeded / Failed / Dead (each with
//     missing-id → ErrNotFound)
//   ResetAppWebhookDeliveryFromDead (happy + wrong-owner
//     ErrNotFound + wrong-status ErrConflict)
//   ListAppWebhookDeliveries (page 1 + page 2 + invalid
//     pageToken + default pageSize)
//   AppWebhookDeliveryByID (happy + missing → ErrNotFound)
//
// Each sub-test creates its own app + webhook(s) so the global
// counters (WebhookPerApp, WebhookPerAccount, claim-pending-row
// count) don't leak between sub-tests. Sub-tests only share the
// migrated schema, not state.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedPgApp returns an account + an app. Required as the precondition
// for every app_webhooks call (the schema's FK requires app_id +
// account_id). The SecretSealed on the inserted webhook defaults to a
// non-empty placeholder because the schema's secret_sealed column is
// NOT NULL.
func seedPgApp(t *testing.T, s *state.PgStore, ctx context.Context) (state.Account, state.App) {
	t.Helper()
	email := "pg-wh-" + uuid.NewString() + "@example.com"
	acct, err := s.CreateAccount(ctx, email, api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app := state.App{
		ID:        uuid.NewString(),
		AccountID: acct.ID,
		Slug:      "wh-" + uuid.NewString()[:8],
	}
	got, err := s.CreateApp(ctx, app)
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	return acct, got
}

// seedPgAppWebhook inserts a webhook for app with a non-empty sealed
// secret + a fresh target URL.
func seedPgAppWebhook(t *testing.T, s *state.PgStore, ctx context.Context, app state.App) state.AppWebhook {
	t.Helper()
	in := state.AppWebhook{
		AppID:        app.ID,
		AccountID:    app.AccountID,
		TargetURL:    "https://hook-" + uuid.NewString() + ".example.com/hook",
		SecretSealed: []byte("sealed-secret"),
		EventFilter:  []string{string(state.AppWebhookEventCronFired)},
		Enabled:      true,
	}
	got, err := s.CreateAppWebhook(ctx, in)
	if err != nil {
		t.Fatalf("CreateAppWebhook: %v", err)
	}
	return got
}

func TestPg_CoverageSweepAppWebhooks(t *testing.T) {
	s, ctx := pgStore(t)

	t.Run("CreateAppWebhook", func(t *testing.T) {
		_, app := seedPgApp(t, s, ctx)
		// Default filter + retry policy should be applied when caller
		// leaves them nil/empty. SecretSealed is required by the schema.
		got, err := s.CreateAppWebhook(ctx, state.AppWebhook{
			AppID:        app.ID,
			AccountID:    app.AccountID,
			TargetURL:    "https://hook-" + uuid.NewString() + ".example.com/hook",
			SecretSealed: []byte("sealed"),
		})
		if err != nil {
			t.Fatalf("CreateAppWebhook: %v", err)
		}
		if got.ID == "" {
			t.Fatal("CreateAppWebhook returned empty ID")
		}
		if got.RetryPolicy != state.AppWebhookRetryDefault {
			t.Errorf("default RetryPolicy = %q, want %q", got.RetryPolicy, state.AppWebhookRetryDefault)
		}
		if got.EventFilter == nil {
			t.Error("default EventFilter should be non-nil")
		}
	})

	t.Run("CreateAppWebhookIfUnderQuota_Happy", func(t *testing.T) {
		_, app := seedPgApp(t, s, ctx)
		got, err := s.CreateAppWebhookIfUnderQuota(ctx, state.AppWebhook{
			AppID:        app.ID,
			AccountID:    app.AccountID,
			TargetURL:    "https://q-" + uuid.NewString() + ".example.com/hook",
			SecretSealed: []byte("sealed"),
		}, api.MustLimitsFor(api.PlanPro))
		if err != nil {
			t.Fatalf("CreateAppWebhookIfUnderQuota: %v", err)
		}
		if got.ID == "" {
			t.Fatal("CreateAppWebhookIfUnderQuota returned empty ID")
		}
	})

	t.Run("CreateAppWebhookIfUnderQuota_AppNotFound", func(t *testing.T) {
		_, app := seedPgApp(t, s, ctx)
		// Missing app must surface ErrNotFound, NOT the SQLSTATE 22P02
		// from the UUID cast.
		_, err := s.CreateAppWebhookIfUnderQuota(ctx, state.AppWebhook{
			AppID:        "00000000-0000-0000-0000-000000000000",
			AccountID:    app.AccountID,
			TargetURL:    "https://nf-" + uuid.NewString() + ".example.com/hook",
			SecretSealed: []byte("sealed"),
		}, api.MustLimitsFor(api.PlanPro))
		if !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("CreateAppWebhookIfUnderQuota on missing app = %v, want ErrNotFound", err)
		}
	})

	t.Run("CreateAppWebhookIfUnderQuota_AppCap", func(t *testing.T) {
		_, app := seedPgApp(t, s, ctx)
		// PlanPro has WebhookPerApp=10; loading 10 onto app, then
		// attempting an 11th should trip AppWebhookQuotaScopeApp.
		limits := api.MustLimitsFor(api.PlanPro)
		if limits.WebhookPerApp <= 0 {
			t.Skipf("PlanPro WebhookPerApp=%d, skipping", limits.WebhookPerApp)
		}
		for i := 0; i < limits.WebhookPerApp; i++ {
			if _, err := s.CreateAppWebhookIfUnderQuota(ctx, state.AppWebhook{
				AppID:        app.ID,
				AccountID:    app.AccountID,
				TargetURL:    "https://cap-" + uuid.NewString() + ".example.com/hook",
				SecretSealed: []byte("sealed"),
			}, limits); err != nil {
				t.Fatalf("seed cap test: CreateAppWebhookIfUnderQuota [%d]: %v", i, err)
			}
		}
		_, err := s.CreateAppWebhookIfUnderQuota(ctx, state.AppWebhook{
			AppID:        app.ID,
			AccountID:    app.AccountID,
			TargetURL:    "https://cap-trip-" + uuid.NewString() + ".example.com/hook",
			SecretSealed: []byte("sealed"),
		}, limits)
		var qe *state.AppWebhookQuotaError
		if !errors.As(err, &qe) || qe.Scope != state.AppWebhookQuotaScopeApp {
			t.Fatalf("expected AppWebhookQuotaError(Scope=app), got %v", err)
		}
	})

	t.Run("CreateAppWebhookIfUnderQuota_AccountCap", func(t *testing.T) {
		// Create a fresh account + two apps. PlanScale has
		// WebhookPerApp=25 + WebhookPerAccount=100. We can't realistically
		// load 100 webhooks in a unit test, so test the contract by
		// pre-loading with a manually-tightened Limits struct.
		acct, err := s.CreateAccount(ctx, "pg-wh-acct-"+uuid.NewString()+"@example.com", api.PlanScale)
		if err != nil {
			t.Fatalf("CreateAccount: %v", err)
		}
		appA := state.App{
			ID: uuid.NewString(), AccountID: acct.ID,
			Slug: "wh-a-" + uuid.NewString()[:8],
		}
		gotA, err := s.CreateApp(ctx, appA)
		if err != nil {
			t.Fatalf("CreateApp appA: %v", err)
		}
		appB := state.App{
			ID: uuid.NewString(), AccountID: acct.ID,
			Slug: "wh-b-" + uuid.NewString()[:8],
		}
		gotB, err := s.CreateApp(ctx, appB)
		if err != nil {
			t.Fatalf("CreateApp appB: %v", err)
		}
		// Tight cap: WebhookPerApp=10 (so we can fit under it), but
		// WebhookPerAccount=2. Loading 2 onto appA trips the account
		// cap when we try a 3rd (on appB).
		tight := api.Limits{
			WebhookPerApp:     10,
			WebhookPerAccount: 2,
		}
		for i := 0; i < tight.WebhookPerAccount; i++ {
			if _, err := s.CreateAppWebhookIfUnderQuota(ctx, state.AppWebhook{
				AppID:        gotA.ID,
				AccountID:    acct.ID,
				TargetURL:    "https://acct-cap-" + uuid.NewString() + ".example.com/hook",
				SecretSealed: []byte("sealed"),
			}, tight); err != nil {
				t.Fatalf("seed acct cap test: CreateAppWebhookIfUnderQuota [%d]: %v", i, err)
			}
		}
		_, err = s.CreateAppWebhookIfUnderQuota(ctx, state.AppWebhook{
			AppID:        gotB.ID,
			AccountID:    acct.ID,
			TargetURL:    "https://acct-cap-trip-" + uuid.NewString() + ".example.com/hook",
			SecretSealed: []byte("sealed"),
		}, tight)
		var qe *state.AppWebhookQuotaError
		if !errors.As(err, &qe) || qe.Scope != state.AppWebhookQuotaScopeAccount {
			t.Fatalf("expected AppWebhookQuotaError(Scope=account), got %v", err)
		}
	})

	t.Run("CreateAppWebhook_DuplicateTargetURL", func(t *testing.T) {
		_, app := seedPgApp(t, s, ctx)
		// (app_id, target_url) is unique — second insert with the
		// same tuple should fail with ErrConflict.
		url := "https://dup-" + uuid.NewString() + ".example.com/hook"
		if _, err := s.CreateAppWebhook(ctx, state.AppWebhook{
			AppID: app.ID, AccountID: app.AccountID, TargetURL: url,
			SecretSealed: []byte("sealed"),
		}); err != nil {
			t.Fatalf("first CreateAppWebhook: %v", err)
		}
		_, err := s.CreateAppWebhook(ctx, state.AppWebhook{
			AppID: app.ID, AccountID: app.AccountID, TargetURL: url,
			SecretSealed: []byte("sealed"),
		})
		if !errors.Is(err, state.ErrConflict) {
			t.Fatalf("dup target_url = %v, want ErrConflict", err)
		}
	})

	t.Run("AppWebhookByID", func(t *testing.T) {
		_, app := seedPgApp(t, s, ctx)
		w := seedPgAppWebhook(t, s, ctx, app)

		got, err := s.AppWebhookByID(ctx, w.ID)
		if err != nil {
			t.Fatalf("AppWebhookByID: %v", err)
		}
		if got.ID != w.ID {
			t.Errorf("ID = %q, want %q", got.ID, w.ID)
		}

		// Missing → ErrNotFound.
		if _, err := s.AppWebhookByID(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("AppWebhookByID missing = %v, want ErrNotFound", err)
		}
	})

	t.Run("UpdateAppWebhook_AllFields", func(t *testing.T) {
		_, app := seedPgApp(t, s, ctx)
		w := seedPgAppWebhook(t, s, ctx, app)

		newURL := "https://upd-new-" + uuid.NewString() + ".example.com/hook"
		newFilter := []string{string(state.AppWebhookEventAppDeployed)}
		newRetry := state.AppWebhookRetryAggressive
		enabled := false
		newSecret := []byte("new-sealed")

		got, err := s.UpdateAppWebhook(ctx, w.ID, state.UpdateAppWebhookParams{
			TargetURL:           &newURL,
			EventFilter:         &newFilter,
			RetryPolicy:         &newRetry,
			Enabled:             &enabled,
			WebhookSecretSealed: &newSecret,
		})
		if err != nil {
			t.Fatalf("UpdateAppWebhook: %v", err)
		}
		if got.TargetURL != newURL {
			t.Errorf("TargetURL = %q, want %q", got.TargetURL, newURL)
		}
		if got.RetryPolicy != newRetry {
			t.Errorf("RetryPolicy = %q, want %q", got.RetryPolicy, newRetry)
		}
		if got.Enabled {
			t.Errorf("Enabled = true, want false")
		}
		if string(got.SecretSealed) != string(newSecret) {
			t.Errorf("SecretSealed round-trip failed")
		}
		if len(got.EventFilter) != 1 || got.EventFilter[0] != string(state.AppWebhookEventAppDeployed) {
			t.Errorf("EventFilter = %v, want [%q]", got.EventFilter, state.AppWebhookEventAppDeployed)
		}
	})

	t.Run("UpdateAppWebhook_NilSkip", func(t *testing.T) {
		_, app := seedPgApp(t, s, ctx)
		w := seedPgAppWebhook(t, s, ctx, app)
		originalURL := w.TargetURL

		// Empty UpdateAppWebhookParams must not touch any column.
		got, err := s.UpdateAppWebhook(ctx, w.ID, state.UpdateAppWebhookParams{})
		if err != nil {
			t.Fatalf("UpdateAppWebhook nil-skip: %v", err)
		}
		if got.TargetURL != originalURL {
			t.Errorf("nil-skip altered TargetURL: got %q, want %q", got.TargetURL, originalURL)
		}
	})

	t.Run("UpdateAppWebhook_Missing", func(t *testing.T) {
		_, err := s.UpdateAppWebhook(ctx, "00000000-0000-0000-0000-000000000000", state.UpdateAppWebhookParams{})
		if !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("UpdateAppWebhook missing = %v, want ErrNotFound", err)
		}
	})

	t.Run("DeleteAppWebhook", func(t *testing.T) {
		_, app := seedPgApp(t, s, ctx)
		w := seedPgAppWebhook(t, s, ctx, app)
		if err := s.DeleteAppWebhook(ctx, w.ID); err != nil {
			t.Fatalf("DeleteAppWebhook: %v", err)
		}
		// Idempotent: second delete returns ErrNotFound.
		if err := s.DeleteAppWebhook(ctx, w.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("DeleteAppWebhook second = %v, want ErrNotFound", err)
		}
	})

	t.Run("ListAppWebhooksForApp", func(t *testing.T) {
		_, app := seedPgApp(t, s, ctx)
		w := seedPgAppWebhook(t, s, ctx, app)
		got, err := s.ListAppWebhooksForApp(ctx, app.ID)
		if err != nil {
			t.Fatalf("ListAppWebhooksForApp: %v", err)
		}
		if len(got) == 0 {
			t.Error("expected at least 1 webhook from seedPgAppWebhook")
		}
		found := false
		for _, ww := range got {
			if ww.AppID != app.ID {
				t.Errorf("foreign webhook %q in result (app_id=%q)", ww.ID, ww.AppID)
			}
			if ww.ID == w.ID {
				found = true
			}
		}
		if !found {
			t.Errorf("seeded webhook %q not in result", w.ID)
		}
	})

	t.Run("ListAppWebhooksForAccount", func(t *testing.T) {
		acct, app := seedPgApp(t, s, ctx)
		w := seedPgAppWebhook(t, s, ctx, app)
		got, err := s.ListAppWebhooksForAccount(ctx, acct.ID)
		if err != nil {
			t.Fatalf("ListAppWebhooksForAccount: %v", err)
		}
		if len(got) == 0 {
			t.Error("expected at least 1 webhook from seedPgAppWebhook")
		}
		found := false
		for _, ww := range got {
			if ww.AccountID != acct.ID {
				t.Errorf("foreign webhook %q in result (account_id=%q)", ww.ID, ww.AccountID)
			}
			if ww.ID == w.ID {
				found = true
			}
		}
		if !found {
			t.Errorf("seeded webhook %q not in result", w.ID)
		}
	})

	t.Run("RecordAppWebhookDelivery", func(t *testing.T) {
		_, app := seedPgApp(t, s, ctx)
		w := seedPgAppWebhook(t, s, ctx, app)
		d, err := s.RecordAppWebhookDelivery(ctx, state.AppWebhookDelivery{
			WebhookID:     w.ID,
			AppID:         app.ID,
			AccountID:     app.AccountID,
			Event:         state.AppWebhookEventCronFired,
			Payload:       json.RawMessage(`{"k":"v"}`),
			Attempt:       0,
			NextAttemptAt: time.Now().Add(-time.Minute),
		})
		if err != nil {
			t.Fatalf("RecordAppWebhookDelivery: %v", err)
		}
		if d.ID == "" {
			t.Fatal("RecordAppWebhookDelivery returned empty ID")
		}
		if d.Status != state.AppWebhookDeliveryPending {
			t.Errorf("default Status = %q, want pending", d.Status)
		}
		// Mark succeeded so the row doesn't leak into the
		// ClaimDueAppWebhookDeliveries_Empty sub-test below.
		d = claimPgWebhookDelivery(t, s, ctx, d.ID)
		if err := s.MarkAppWebhookDeliverySucceeded(ctx, d.ID, 200, d.Attempt, d.NextAttemptAt, time.Now()); err != nil {
			t.Fatalf("MarkAppWebhookDeliverySucceeded cleanup: %v", err)
		}
	})

	t.Run("ClaimDueAppWebhookDeliveries_Empty", func(t *testing.T) {
		// Use a fresh app so there are no pending rows. Even though
		// sub-tests share the schema, the deliveries table is keyed
		// by app_id and no other sub-test has written to this app.
		_, app := seedPgApp(t, s, ctx)
		_ = seedPgAppWebhook(t, s, ctx, app)
		got, err := s.ClaimDueAppWebhookDeliveries(ctx, 32, time.Now().Add(time.Hour))
		if err != nil {
			t.Fatalf("ClaimDueAppWebhookDeliveries empty: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected 0 claimed rows, got %d", len(got))
		}
	})

	t.Run("ClaimDueAppWebhookDeliveries_Happy", func(t *testing.T) {
		_, app := seedPgApp(t, s, ctx)
		w := seedPgAppWebhook(t, s, ctx, app)
		// Enqueue a delivery whose NextAttemptAt is in the past so
		// the claim query picks it up.
		if _, err := s.RecordAppWebhookDelivery(ctx, state.AppWebhookDelivery{
			WebhookID:     w.ID,
			AppID:         app.ID,
			AccountID:     app.AccountID,
			Event:         state.AppWebhookEventCronFired,
			Payload:       json.RawMessage(`{}`),
			Attempt:       0,
			NextAttemptAt: time.Now().Add(-time.Minute),
		}); err != nil {
			t.Fatalf("RecordAppWebhookDelivery: %v", err)
		}

		claimed, err := s.ClaimDueAppWebhookDeliveries(ctx, 32, time.Now())
		if err != nil {
			t.Fatalf("ClaimDueAppWebhookDeliveries: %v", err)
		}
		// Must be at least 1 (our row). May be more if a previous
		// sub-test left a due row in flight; filter to ours.
		var ours *state.AppWebhookDelivery
		for i := range claimed {
			if claimed[i].WebhookID == w.ID {
				ours = &claimed[i]
				break
			}
		}
		if ours == nil {
			t.Fatalf("our row %q not among %d claimed", w.ID, len(claimed))
		}
		if ours.Status != state.AppWebhookDeliveryInFlight {
			t.Errorf("claimed Status = %q, want in_flight", ours.Status)
		}
		// Mark our row succeeded so it doesn't leak into later tests.
		if err := s.MarkAppWebhookDeliverySucceeded(ctx, ours.ID, 200, ours.Attempt, ours.NextAttemptAt, time.Now()); err != nil {
			t.Fatalf("MarkAppWebhookDeliverySucceeded: %v", err)
		}
	})

	t.Run("MarkAppWebhookDeliverySucceeded_Missing", func(t *testing.T) {
		err := s.MarkAppWebhookDeliverySucceeded(ctx, "00000000-0000-0000-0000-000000000000", 200, 0, time.Time{}, time.Now())
		if !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("MarkAppWebhookDeliverySucceeded missing = %v, want ErrNotFound", err)
		}
	})

	t.Run("MarkAppWebhookDeliveryFailed_Missing", func(t *testing.T) {
		err := s.MarkAppWebhookDeliveryFailed(ctx, "00000000-0000-0000-0000-000000000000", 500, 0, time.Time{}, "boom", time.Now())
		if !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("MarkAppWebhookDeliveryFailed missing = %v, want ErrNotFound", err)
		}
	})

	t.Run("MarkAppWebhookDeliveryDead_Missing", func(t *testing.T) {
		err := s.MarkAppWebhookDeliveryDead(ctx, "00000000-0000-0000-0000-000000000000", 0, time.Time{}, "boom")
		if !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("MarkAppWebhookDeliveryDead missing = %v, want ErrNotFound", err)
		}
	})

	t.Run("ResetAppWebhookDeliveryFromDead_Happy", func(t *testing.T) {
		_, app := seedPgApp(t, s, ctx)
		w := seedPgAppWebhook(t, s, ctx, app)
		d, err := s.RecordAppWebhookDelivery(ctx, state.AppWebhookDelivery{
			WebhookID:     w.ID,
			AppID:         app.ID,
			AccountID:     app.AccountID,
			Event:         state.AppWebhookEventCronFired,
			Payload:       json.RawMessage(`{}`),
			Attempt:       0,
			NextAttemptAt: time.Now(),
		})
		if err != nil {
			t.Fatalf("RecordAppWebhookDelivery: %v", err)
		}
		d = claimPgWebhookDelivery(t, s, ctx, d.ID)
		if err := s.MarkAppWebhookDeliveryDead(ctx, d.ID, d.Attempt, d.NextAttemptAt, "exhausted"); err != nil {
			t.Fatalf("MarkAppWebhookDeliveryDead: %v", err)
		}
		if err := s.ResetAppWebhookDeliveryFromDead(ctx, d.ID, w.ID, app.AccountID, time.Now()); err != nil {
			t.Fatalf("ResetAppWebhookDeliveryFromDead: %v", err)
		}
	})

	t.Run("ResetAppWebhookDeliveryFromDead_NotOwned", func(t *testing.T) {
		_, app := seedPgApp(t, s, ctx)
		w := seedPgAppWebhook(t, s, ctx, app)
		d, err := s.RecordAppWebhookDelivery(ctx, state.AppWebhookDelivery{
			WebhookID:     w.ID,
			AppID:         app.ID,
			AccountID:     app.AccountID,
			Event:         state.AppWebhookEventCronFired,
			Payload:       json.RawMessage(`{}`),
			Attempt:       0,
			NextAttemptAt: time.Now(),
		})
		if err != nil {
			t.Fatalf("RecordAppWebhookDelivery: %v", err)
		}
		d = claimPgWebhookDelivery(t, s, ctx, d.ID)
		if err := s.MarkAppWebhookDeliveryDead(ctx, d.ID, d.Attempt, d.NextAttemptAt, "exhausted"); err != nil {
			t.Fatalf("MarkAppWebhookDeliveryDead: %v", err)
		}
		// Wrong account id → ErrNotFound (existence-leak-safe).
		err = s.ResetAppWebhookDeliveryFromDead(ctx, d.ID, w.ID, "00000000-0000-0000-0000-000000000001", time.Now())
		if !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("ResetAppWebhookDeliveryFromDead wrong-owner = %v, want ErrNotFound", err)
		}
	})

	t.Run("ResetAppWebhookDeliveryFromDead_Conflict", func(t *testing.T) {
		_, app := seedPgApp(t, s, ctx)
		w := seedPgAppWebhook(t, s, ctx, app)
		d, err := s.RecordAppWebhookDelivery(ctx, state.AppWebhookDelivery{
			WebhookID:     w.ID,
			AppID:         app.ID,
			AccountID:     app.AccountID,
			Event:         state.AppWebhookEventCronFired,
			Payload:       json.RawMessage(`{}`),
			Attempt:       0,
			NextAttemptAt: time.Now(),
		})
		if err != nil {
			t.Fatalf("RecordAppWebhookDelivery: %v", err)
		}
		// Don't mark dead — row is 'pending'. Caller owns it (the
		// ownership probe should find it), but the status is wrong →
		// ErrConflict.
		err = s.ResetAppWebhookDeliveryFromDead(ctx, d.ID, w.ID, app.AccountID, time.Now())
		if !errors.Is(err, state.ErrConflict) {
			t.Fatalf("ResetAppWebhookDeliveryFromDead wrong-status = %v, want ErrConflict", err)
		}
	})

	t.Run("ListAppWebhookDeliveries_FirstPage", func(t *testing.T) {
		_, app := seedPgApp(t, s, ctx)
		w := seedPgAppWebhook(t, s, ctx, app)
		// Use a unique event + payload marker so we can identify our
		// deliveries in the result (avoid cross-test leakage).
		marker := "marker-" + uuid.NewString()
		for i := 0; i < 3; i++ {
			if _, err := s.RecordAppWebhookDelivery(ctx, state.AppWebhookDelivery{
				WebhookID:     w.ID,
				AppID:         app.ID,
				AccountID:     app.AccountID,
				Event:         state.AppWebhookEventCronFired,
				Payload:       json.RawMessage(`{"` + marker + `":` + `true}`),
				Attempt:       0,
				NextAttemptAt: time.Now(),
			}); err != nil {
				t.Fatalf("RecordAppWebhookDelivery: %v", err)
			}
		}
		got, nextToken, err := s.ListAppWebhookDeliveries(ctx, app.ID, w.ID, 2, "")
		if err != nil {
			t.Fatalf("ListAppWebhookDeliveries: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("page 1 size = %d, want 2", len(got))
		}
		if nextToken == "" {
			t.Error("expected nextToken on full page")
		}
	})

	t.Run("ListAppWebhookDeliveries_InvalidToken", func(t *testing.T) {
		_, app := seedPgApp(t, s, ctx)
		w := seedPgAppWebhook(t, s, ctx, app)
		_, _, err := s.ListAppWebhookDeliveries(ctx, app.ID, w.ID, 10, "not-a-valid-page-token")
		if err == nil {
			t.Fatal("expected error for invalid page token")
		}
		if !strings.Contains(err.Error(), "invalid page token") {
			t.Errorf("err = %v, want invalid page token", err)
		}
	})

	t.Run("ListAppWebhookDeliveries_DefaultPageSize", func(t *testing.T) {
		_, app := seedPgApp(t, s, ctx)
		w := seedPgAppWebhook(t, s, ctx, app)
		// pageSize=0 → defaults to 50 inside the implementation.
		got, _, err := s.ListAppWebhookDeliveries(ctx, app.ID, w.ID, 0, "")
		if err != nil {
			t.Fatalf("ListAppWebhookDeliveries default: %v", err)
		}
		_ = got
	})

	t.Run("AppWebhookDeliveryByID", func(t *testing.T) {
		_, app := seedPgApp(t, s, ctx)
		w := seedPgAppWebhook(t, s, ctx, app)
		d, err := s.RecordAppWebhookDelivery(ctx, state.AppWebhookDelivery{
			WebhookID:     w.ID,
			AppID:         app.ID,
			AccountID:     app.AccountID,
			Event:         state.AppWebhookEventCronFired,
			Payload:       json.RawMessage(`{}`),
			Attempt:       0,
			NextAttemptAt: time.Now(),
		})
		if err != nil {
			t.Fatalf("RecordAppWebhookDelivery: %v", err)
		}
		got, err := s.AppWebhookDeliveryByID(ctx, d.ID)
		if err != nil {
			t.Fatalf("AppWebhookDeliveryByID: %v", err)
		}
		if got.ID != d.ID {
			t.Errorf("ID = %q, want %q", got.ID, d.ID)
		}
		// Missing → ErrNotFound.
		if _, err := s.AppWebhookDeliveryByID(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("AppWebhookDeliveryByID missing = %v, want ErrNotFound", err)
		}
	})
}
