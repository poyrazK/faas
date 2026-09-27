// pgstore_alert_metrics_test.go — PgStore tests for the alert
// evaluator's signal-feeding reads (issue #1233 / ADR-123):
//
//   - CountFailedDeploymentsSince   (deployment_failed metric)
//   - WasInvokedSuccessfullySince   (api_up metric)
//   - MTDSpendEurCents              (account_spend_eur metric)
//   - UpsertAccountSpendSnapshot    (meterd tick loop)
//   - MinCertExpiryForApp           (cert_expiry_seconds metric)
//   - RefreshCertExpiryStates       (meterd refresher goroutine)
//   - ListCertExpiryStateForWalker  (meterd gauge writer)
//
// These methods sit on the alert-evaluator hot path; the migration
// floor (check-state-coverage ≥ 70%) requires them to be exercised
// end-to-end against the schema. Each test inserts a fresh fixture
// row tagged with the same UUIDs the methods filter on, against
// the live tables (deployments, invocations, account_spend_snapshot,
// tenant_surfaces + tenant_hostnames, meterd_tenant_surface_cert_expiry_state).
package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// seedTestAccount creates one account and one app on the test
// schema and returns their ids. Distinct from seedLiveDeploy in
// that we don't need a deployment — the alert-metric reads target
// invocations / spend / cert tables, not deployments.
func seedTestAccount(t *testing.T, s *state.PgStore, ctx context.Context, suffix string) (acctID, appID string) {
	t.Helper()
	acct, err := s.CreateAccount(ctx, "u-am-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := s.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "am-app-" + suffix, Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	return acct.ID, app.ID
}

func TestPg_CountFailedDeploymentsSince_EmptyWindow(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	acctID, appID := seedTestAccount(t, s, ctx, "fdep-empty")
	got, err := s.CountFailedDeploymentsSince(ctx, acctID, appID, time.Now().Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("CountFailedDeploymentsSince: %v", err)
	}
	if got != 0 {
		t.Errorf("count = %d; want 0 (no deployments seeded)", got)
	}
}

func TestPg_CountFailedDeploymentsSince_OnlyFailedCount(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acctID, appID := seedTestAccount(t, s, ctx, "fdep-count")

	// One failed + one live + one pending in the last hour.
	// Only the failed one must count. deployments.status CHECK
	// is {pending,building,imaging,snapshotting,live,failed,superseded}
	// per migrations/00001_init.sql:52.
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, image_digest, status, created_at)
		values (gen_random_uuid(), $1, 'img@sha256:0', 'failed',  now() - interval '5 minutes'),
		       (gen_random_uuid(), $1, 'img@sha256:0', 'live',    now() - interval '5 minutes'),
		       (gen_random_uuid(), $1, 'img@sha256:0', 'pending', now() - interval '5 minutes')`,
		appID); err != nil {
		t.Fatalf("insert deployments: %v", err)
	}
	got, err := s.CountFailedDeploymentsSince(ctx, acctID, appID, time.Now().Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("CountFailedDeploymentsSince: %v", err)
	}
	if got != 1 {
		t.Errorf("count = %d; want 1 (only the failed deployment)", got)
	}
}

func TestPg_CountFailedDeploymentsSince_EmptyAppArgMeansAccountScope(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acctID, appID := seedTestAccount(t, s, ctx, "fdep-account")

	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, image_digest, status, created_at)
		values (gen_random_uuid(), $1, 'img@sha256:0', 'failed', now() - interval '5 minutes')`,
		appID); err != nil {
		t.Fatalf("insert deployment: %v", err)
	}
	// Pass empty appID — the store treats "" as "any app on this
	// account". Used by the alert evaluator when scoping an
	// account-wide alert.
	got, err := s.CountFailedDeploymentsSince(ctx, acctID, "", time.Now().Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("CountFailedDeploymentsSince: %v", err)
	}
	if got != 1 {
		t.Errorf("count = %d; want 1 (account-scoped query)", got)
	}
}

func TestPg_WasInvokedSuccessfullySince_EmptyReturnsFalse(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	acctID, appID := seedTestAccount(t, s, ctx, "wiss-empty")
	got, err := s.WasInvokedSuccessfullySince(ctx, acctID, appID, time.Now().Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("WasInvokedSuccessfullySince: %v", err)
	}
	if got {
		t.Errorf("got = true; want false (cold start)")
	}
}

func TestPg_WasInvokedSuccessfullySince_TrueOnSuccess(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acctID, appID := seedTestAccount(t, s, ctx, "wiss-true")

	// invocations.source CHECK is {async_invoke,queue,delayed_task,cron};
	// invocations.state CHECK is {pending,dispatching,completed,failed,cancelled}.
	if _, err := pool.Exec(ctx, `
		insert into invocations (id, account_id, app_id, source, state, created_at)
		values (gen_random_uuid(), $1, $2, 'async_invoke', 'completed', now() - interval '5 minutes')`,
		acctID, appID); err != nil {
		t.Fatalf("insert invocation: %v", err)
	}
	got, err := s.WasInvokedSuccessfullySince(ctx, acctID, appID, time.Now().Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("WasInvokedSuccessfullySince: %v", err)
	}
	if !got {
		t.Errorf("got = false; want true (a succeeded invocation exists)")
	}
}

func TestPg_UpsertAccountSpendSnapshot_InsertAndUpsert(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acctID, _ := seedTestAccount(t, s, ctx, "spend")

	// account_spend_snapshot.source CHECK is
	// {running_seconds,overage,build_seconds,snapshot_storage}
	// per migrations/00407.
	const src = "running_seconds"
	now := time.Now().UTC()
	if err := s.UpsertAccountSpendSnapshot(ctx, acctID, now, now.Add(time.Minute), 12.5, 12345, src); err != nil {
		t.Fatalf("UpsertAccountSpendSnapshot (insert): %v", err)
	}
	// Verify row landed.
	var eur int64
	if err := pool.QueryRow(ctx, `select eur_cents from account_spend_snapshot where account_id = $1`, acctID).Scan(&eur); err != nil {
		t.Fatalf("query after insert: %v", err)
	}
	if eur != 12345 {
		t.Errorf("eur_cents = %d; want 12345", eur)
	}

	// ON CONFLICT (account_id, source, period_end) DO UPDATE —
	// the same period_end with a fresh eur_cents must overwrite.
	if err := s.UpsertAccountSpendSnapshot(ctx, acctID, now, now.Add(time.Minute), 12.5, 99999, src); err != nil {
		t.Fatalf("UpsertAccountSpendSnapshot (upsert): %v", err)
	}
	if err := pool.QueryRow(ctx, `select eur_cents from account_spend_snapshot where account_id = $1`, acctID).Scan(&eur); err != nil {
		t.Fatalf("query after upsert: %v", err)
	}
	if eur != 99999 {
		t.Errorf("eur_cents = %d; want 99999 (upsert must overwrite)", eur)
	}
}

// TestPg_MTDSpendEurCents_ReflectsMonthToDateUsage — the metric used to sum
// account_spend_snapshot, which no production code writes, so the enabled
// "Spend exceeds €20" preset could never fire. It now reads the month's
// usage beyond the plan allowance; snapshot rows alone move nothing.
func TestPg_MTDSpendEurCents_ReflectsMonthToDateUsage(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	acctID, appID := seedTestAccount(t, s, ctx, "mtd")

	now := time.Now().UTC()
	if err := s.UpsertAccountSpendSnapshot(ctx, acctID, now, now.Add(time.Minute), 1.0, 100, "running_seconds"); err != nil {
		t.Fatalf("UpsertAccountSpendSnapshot: %v", err)
	}
	got, err := s.MTDSpendEurCents(ctx, acctID)
	if err != nil {
		t.Fatalf("MTDSpendEurCents: %v", err)
	}
	if got != 0 {
		t.Fatalf("spend with no usage = %d cents; want 0", got)
	}

	// Pro includes 250 GB-h; 12 GB-h beyond it is 12 cents of overage.
	included := int64(api.PlanPro.PlanIncludedGBHours()) * api.SecondsPerGBHour
	if err := s.AppendUsage(ctx, acctID, appID, uuid.NewString(), now.Truncate(time.Minute), included+12*api.SecondsPerGBHour, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("AppendUsage: %v", err)
	}
	got, err = s.MTDSpendEurCents(ctx, acctID)
	if err != nil {
		t.Fatalf("MTDSpendEurCents: %v", err)
	}
	if got != 12 {
		t.Fatalf("spend = %d cents; want 12 (12 GB-h beyond the Pro allowance)", got)
	}
}

func TestPg_MTDSpendEurCents_EmptyAccountReturnsZero(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	acctID, _ := seedTestAccount(t, s, ctx, "mtd-empty")
	got, err := s.MTDSpendEurCents(ctx, acctID)
	if err != nil {
		t.Fatalf("MTDSpendEurCents: %v", err)
	}
	if got != 0 {
		t.Errorf("total = %d; want 0 (no rows for fresh account)", got)
	}
}

func TestPg_MinCertExpiryForApp_NoSurfacesReturnsMinusOne(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	acctID, appID := seedTestAccount(t, s, ctx, "cert-empty")
	got, err := s.MinCertExpiryForApp(ctx, acctID, appID)
	if err != nil {
		t.Fatalf("MinCertExpiryForApp: %v", err)
	}
	if got != -1 {
		t.Errorf("got = %d; want -1 (no surfaces)", got)
	}
}

// seedCertStateRow inserts a parent tenant_surfaces + tenant_hostnames
// row so the meterd_tenant_surface_cert_expiry_state FK is satisfiable,
// then inserts a mirror row directly. Mirrors the schema-level
// (surface, hostname) pair invariant from migrations/00408.
func seedCertStateRow(t *testing.T, pool *pgxpool.Pool, ctx context.Context, acctID, appID, hostname string, certNotAfter *time.Time, refreshedAt time.Time) (surfaceID string) {
	t.Helper()
	if err := pool.QueryRow(ctx, `
		insert into tenant_surfaces (id, account_id, app_id, name, cert_state, cert_not_after)
		values (gen_random_uuid(), $1, $2, $3, 'issued', $4)
		returning id`,
		acctID, appID, hostname+"_surface", certNotAfter).Scan(&surfaceID); err != nil {
		t.Fatalf("insert tenant_surface: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into tenant_hostnames (id, surface_id, hostname, verified_at)
		values (gen_random_uuid(), $1, $2, now())
	`, surfaceID, hostname); err != nil {
		t.Fatalf("insert tenant_hostnames: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into meterd_tenant_surface_cert_expiry_state
			(tenant_surface_id, account_id, app_id, hostname,
			 last_observed_cert_not_after, last_walk_status, last_refreshed_at)
		values ($1, $2, $3, $4, $5, 'ok', $6)
	`, surfaceID, acctID, appID, hostname, certNotAfter, refreshedAt); err != nil {
		t.Fatalf("insert cert state: %v", err)
	}
	return surfaceID
}

func TestPg_MinCertExpiryForApp_PicksMinAcrossRows(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acctID, appID := seedTestAccount(t, s, ctx, "cert-min")

	thirtyDays := time.Now().Add(30 * 24 * time.Hour)
	sevenDays := time.Now().Add(7 * 24 * time.Hour)
	seedCertStateRow(t, pool, ctx, acctID, appID, "a.example", &thirtyDays, time.Now())
	seedCertStateRow(t, pool, ctx, acctID, appID, "b.example", &sevenDays, time.Now())

	got, err := s.MinCertExpiryForApp(ctx, acctID, appID)
	if err != nil {
		t.Fatalf("MinCertExpiryForApp: %v", err)
	}
	// 7 days = 604800 seconds. The min must land in the 7-day
	// ballpark (allow ±5 min slack for clock drift between
	// insert and read).
	const sevenDaysSec = int64(7 * 24 * 60 * 60)
	if got < sevenDaysSec-300 || got > sevenDaysSec+300 {
		t.Errorf("min = %d seconds; want ~%d (7 days, ±5min)", got, sevenDaysSec)
	}
}

func TestPg_RefreshCertExpiryStates_UpsertsFromTenantSurfaces(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acctID, appID := seedTestAccount(t, s, ctx, "cert-refresh")

	// Insert one tenant_surface with cert_state='issued' + a
	// tenant_hostnames row so RefreshCertExpiryStates can JOIN
	// and emit a per-hostname mirror row.
	var surfaceID string
	if err := pool.QueryRow(ctx, `
		insert into tenant_surfaces (id, account_id, app_id, name, cert_state, cert_not_after)
		values (gen_random_uuid(), $1, $2, 'refresh_surface', 'issued', now() + interval '14 days')
		returning id`, acctID, appID).Scan(&surfaceID); err != nil {
		t.Fatalf("insert tenant_surface: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into tenant_hostnames (id, surface_id, hostname, verified_at)
		values (gen_random_uuid(), $1, 'r.example', now())
	`, surfaceID); err != nil {
		t.Fatalf("insert tenant_hostnames: %v", err)
	}

	n, err := s.RefreshCertExpiryStates(ctx)
	if err != nil {
		t.Fatalf("RefreshCertExpiryStates: %v", err)
	}
	if n != 1 {
		t.Errorf("rows upserted = %d; want 1", n)
	}
	var lastWalk string
	if err := pool.QueryRow(ctx, `
		select last_walk_status from meterd_tenant_surface_cert_expiry_state
		 where tenant_surface_id = $1 and hostname = 'r.example'`,
		surfaceID).Scan(&lastWalk); err != nil {
		t.Fatalf("query mirrored row: %v", err)
	}
	if lastWalk != "ok" {
		t.Errorf("last_walk_status = %q; want \"ok\"", lastWalk)
	}
}

func TestPg_RefreshCertExpiryStates_CertUnissuedWhenNotAfterIsNull(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acctID, appID := seedTestAccount(t, s, ctx, "cert-unissued")

	// Defensive path: cert_state='issued' but cert_not_after is
	// NULL. Status must land as 'cert_unissued'.
	var surfaceID string
	if err := pool.QueryRow(ctx, `
		insert into tenant_surfaces (id, account_id, app_id, name, cert_state, cert_not_after)
		values (gen_random_uuid(), $1, $2, 'unissued_surface', 'issued', null)
		returning id`, acctID, appID).Scan(&surfaceID); err != nil {
		t.Fatalf("insert tenant_surface: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into tenant_hostnames (id, surface_id, hostname, verified_at)
		values (gen_random_uuid(), $1, 'u.example', now())
	`, surfaceID); err != nil {
		t.Fatalf("insert tenant_hostnames: %v", err)
	}
	if _, err := s.RefreshCertExpiryStates(ctx); err != nil {
		t.Fatalf("RefreshCertExpiryStates: %v", err)
	}
	var lastWalk string
	if err := pool.QueryRow(ctx, `
		select last_walk_status from meterd_tenant_surface_cert_expiry_state
		 where tenant_surface_id = $1 and hostname = 'u.example'`,
		surfaceID).Scan(&lastWalk); err != nil {
		t.Fatalf("query mirrored row: %v", err)
	}
	if lastWalk != "cert_unissued" {
		t.Errorf("last_walk_status = %q; want \"cert_unissued\"", lastWalk)
	}
}

func TestPg_ListCertExpiryStateForWalker_StaleCutoffFilters(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acctID, appID := seedTestAccount(t, s, ctx, "cert-walker")

	// Two rows: one fresh, one stale (last_refreshed_at = 1h ago).
	// Each must be backed by a parent tenant_surfaces +
	// tenant_hostnames row to satisfy the FK.
	notAfter := time.Now().Add(14 * 24 * time.Hour)
	seedCertStateRow(t, pool, ctx, acctID, appID, "fresh.example", &notAfter, time.Now())
	seedCertStateRow(t, pool, ctx, acctID, appID, "stale.example", &notAfter, time.Now().Add(-1*time.Hour))

	got, err := s.ListCertExpiryStateForWalker(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("ListCertExpiryStateForWalker: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("rows = %d; want 1 (stale row filtered)", len(got))
	}
	if got[0].Hostname != "fresh.example" {
		t.Errorf("hostname = %q; want \"fresh.example\"", got[0].Hostname)
	}
}

// TestPg_WasInvokedSuccessfullySince_CountsServedHTTPRequests — api_up used
// to look only at the invocations table, which ordinary HTTP traffic never
// touches, so an app busily serving requests read as down and the "API is
// down" preset fired. A 5xx is not a served request, and a pending
// invocation is not evidence the app answered.
func TestPg_WasInvokedSuccessfullySince_CountsServedHTTPRequests(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acctID, appID := seedTestAccount(t, s, ctx, "wiss-http")
	since := time.Now().Add(-5 * time.Minute)
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: appID, Status: state.DeployLive, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	telemetry := func(status int32) {
		t.Helper()
		acct, _ := uuid.Parse(acctID)
		app, _ := uuid.Parse(appID)
		depID, _ := uuid.Parse(dep.ID)
		if err := s.InsertRequestTelemetry(ctx, sqlc.InsertRequestTelemetryParams{
			AccountID: pgtype.UUID{Bytes: acct, Valid: true}, AppID: pgtype.UUID{Bytes: app, Valid: true},
			DeploymentID: pgtype.UUID{Bytes: depID, Valid: true},
			Route:        "/", Method: "GET", Status: status, LatencyMs: 12, Count: 1,
			ReceivedAt:   pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
			GuestRuntime: "__unknown__", GuestOutcome: "missing",
			Country: "__unknown__", UaFamily: "__unknown__", ReferrerHost: "__unknown__",
		}); err != nil {
			t.Fatalf("InsertRequestTelemetry: %v", err)
		}
	}
	up := func() bool {
		t.Helper()
		ok, err := s.WasInvokedSuccessfullySince(ctx, acctID, appID, since)
		if err != nil {
			t.Fatalf("WasInvokedSuccessfullySince: %v", err)
		}
		return ok
	}

	if _, err := pool.Exec(ctx, `
		insert into invocations (id, account_id, app_id, source, state, created_at)
		values (gen_random_uuid(), $1, $2, 'async_invoke', 'pending', now())`,
		acctID, appID); err != nil {
		t.Fatalf("insert invocation: %v", err)
	}
	telemetry(503)
	if up() {
		t.Fatal("a pending invocation and a 503 counted as the app serving traffic")
	}
	telemetry(200)
	if !up() {
		t.Fatal("an app that answered HTTP 200 in the window reads as down")
	}
}
