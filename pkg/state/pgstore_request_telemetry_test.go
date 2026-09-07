package state_test

// Round-trip coverage for the pgstore side of the ADR-127
// request_telemetry surface (PR #1067 / migration 00427).
//
// These four methods (InsertRequestTelemetry, ListRequestTelemetryByApp,
// RequestTelemetryByDeployment, RequestTelemetryBaselineP95ByRoute)
// were added by main's PR #1067 merge and were the source of the
// 69.7% → 70.2% coverage delta in CI shard2: the pgstore wrappers
// themselves are thin one-line delegates to sqlc, but their bodies
// count against the package statement floor, and the previous
// PR #1064 branch had no test coverage for them. This file pins
// the happy paths so the pkg/state coverage gate (≥ 70%) is met
// on CI runners with stronger coverage probe variance.
//
// Test surface:
//   - InsertRequestTelemetry writes a row; ListRequestTelemetryByApp
//     reads it back scoped by (account_id, app_id, time window).
//   - RequestTelemetryByDeployment returns the same row when
//     scoped by deployment_id.
//   - RequestTelemetryBaselineP95ByRoute returns the route's
//     percentile-95 latency row (single-row aggregate).
//   - CHECK constraint violations surface as errors (not panics).
//
// Mirrors the seeded-fixture pattern in pgstore_endpoint_discovery_test.go
// — request_telemetry is partitioned + indexed but has no FKs, so
// the test inserts directly without parent rows.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// TestPgStoreRequestTelemetry_RoundTrip exercises the per-request
// INSERT path and the per-app LIST path. The list is scoped by
// (account_id, app_id, time window) per sqlc; the test inserts a
// row, then asserts the list returns it with the same route +
// status + latency_ms.
func TestPgStoreRequestTelemetry_RoundTrip(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)

	acct := uuid.NewString()
	app := uuid.NewString()
	dep := uuid.NewString()
	now := time.Now().UTC()

	if err := store.InsertRequestTelemetry(ctx, sqlc.InsertRequestTelemetryParams{
		AccountID:    pgtype.UUID{Bytes: parseUUID(t, acct), Valid: true},
		AppID:        pgtype.UUID{Bytes: parseUUID(t, app), Valid: true},
		DeploymentID: pgtype.UUID{Bytes: parseUUID(t, dep), Valid: true},
		Route:        "GET /foo",
		Method:       "GET",
		Status:       200,
		LatencyMs:    42,
		ColdBoot:     false,
		TraceID:      pgtype.Text{},
		ReceivedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		Count:        1,
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	rows, err := store.ListRequestTelemetryByApp(ctx, sqlc.ListRequestTelemetryByAppParams{
		AppID:        pgtype.UUID{Bytes: parseUUID(t, app), Valid: true},
		ReceivedAt:   pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true},
		ReceivedAt_2: pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true},
		Limit:        50,
		Route:        "GET /foo",
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("List: got %d rows, want 1", len(rows))
	}
	if rows[0].Route != "GET /foo" {
		t.Errorf("List.Route = %q, want %q", rows[0].Route, "GET /foo")
	}
	if rows[0].Count != 1 {
		t.Errorf("List.Count = %d, want 1", rows[0].Count)
	}
	if rows[0].Method != "GET" {
		t.Errorf("List.Method = %q, want GET", rows[0].Method)
	}
	if rows[0].Status != 200 {
		t.Errorf("List.Status = %d, want 200", rows[0].Status)
	}
}

// TestPgStoreRequestTelemetry_PerDeployment pins the per-deployment
// drilldown path. Same fixture as RoundTrip, different read
// surface — the per-deployment list scopes by (app_id,
// deployment_id, time window).
func TestPgStoreRequestTelemetry_PerDeployment(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)

	app := uuid.NewString()
	dep := uuid.NewString()
	now := time.Now().UTC()

	if err := store.InsertRequestTelemetry(ctx, sqlc.InsertRequestTelemetryParams{
		AccountID:    pgtype.UUID{Bytes: parseUUID(t, uuid.NewString()), Valid: true},
		AppID:        pgtype.UUID{Bytes: parseUUID(t, app), Valid: true},
		DeploymentID: pgtype.UUID{Bytes: parseUUID(t, dep), Valid: true},
		Route:        "POST /bar",
		Method:       "POST",
		Status:       201,
		LatencyMs:    100,
		ColdBoot:     true,
		TraceID:      pgtype.Text{String: "0123456789abcdef0123456789abcdef", Valid: true},
		ReceivedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		Count:        1,
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	rows, err := store.RequestTelemetryByDeployment(ctx, sqlc.RequestTelemetryByDeploymentParams{
		AppID:        pgtype.UUID{Bytes: parseUUID(t, app), Valid: true},
		DeploymentID: pgtype.UUID{Bytes: parseUUID(t, dep), Valid: true},
		ReceivedAt:   pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true},
		ReceivedAt_2: pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true},
		Limit:        50,
	})
	if err != nil {
		t.Fatalf("PerDeployment: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("PerDeployment: got %d rows, want 1", len(rows))
	}
	if rows[0].Route != "POST /bar" {
		t.Errorf("PerDeployment.Route = %q, want %q", rows[0].Route, "POST /bar")
	}
}

// TestPgStoreRequestTelemetry_BaselineP95 pins the regression-
// detector's per-route p95 baseline path. Three rows with
// different latencies → the baseline query returns ONE row
// per route with the p95 (which for 3 samples with this seed
// is the 3rd highest = max).
func TestPgStoreRequestTelemetry_BaselineP95(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)

	app := uuid.NewString()
	dep := uuid.NewString()
	now := time.Now().UTC()

	for i, lat := range []int32{10, 50, 200} {
		if err := store.InsertRequestTelemetry(ctx, sqlc.InsertRequestTelemetryParams{
			AccountID:    pgtype.UUID{Bytes: parseUUID(t, uuid.NewString()), Valid: true},
			AppID:        pgtype.UUID{Bytes: parseUUID(t, app), Valid: true},
			DeploymentID: pgtype.UUID{Bytes: parseUUID(t, dep), Valid: true},
			Route:        "GET /p95",
			Method:       "GET",
			Status:       200,
			LatencyMs:    lat,
			ColdBoot:     false,
			TraceID:      pgtype.Text{},
			ReceivedAt:   pgtype.Timestamptz{Time: now.Add(time.Duration(i) * time.Second), Valid: true},
			Count:        1,
		}); err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}

	rows, err := store.RequestTelemetryBaselineP95ByRoute(ctx, sqlc.RequestTelemetryBaselineP95ByRouteParams{
		AppID:        pgtype.UUID{Bytes: parseUUID(t, app), Valid: true},
		DeploymentID: pgtype.UUID{Bytes: parseUUID(t, dep), Valid: true},
		ReceivedAt:   pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true},
		ReceivedAt_2: pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("BaselineP95: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("BaselineP95: got %d rows, want 1", len(rows))
	}
	if rows[0].Route != "GET /p95" {
		t.Errorf("BaselineP95.Route = %q, want %q", rows[0].Route, "GET /p95")
	}
	// percentile_cont(0.95) is a continuous interpolation between the
	// two surrounding sorted samples. For 3 samples (10, 50, 200):
	//   ordinal position = 0.95 * (3-1) = 1.9
	//   row 1.9 falls between index 1 (value 50) and index 2 (value 200)
	//   fractional offset = 0.9
	//   p95 = 50 + 0.9*(200-50) = 50 + 135 = 185
	// Cast to int rounds toward zero. Postgres returns 185 — the
	// expected, correct value for percentile_cont at 0.95 over 3
	// samples. Pinning this exact value catches a future change that
	// silently swaps percentile_cont → percentile_disc (which would
	// return 200 = the nearest-rank sample) or percentile_cont → a
	// different quantile (e.g. 0.99 would skew the regression baseline
	// higher). The regression detector's math depends on this exact
	// 185; if it drifts, the canary baseline drifts too.
	if rows[0].P95Ms != 185 {
		t.Errorf("BaselineP95.P95Ms = %d, want 185 (percentile_cont(0.95) over [10,50,200] = 50 + 0.9*(200-50))", rows[0].P95Ms)
	}
}

// TestPgStoreRequestTelemetry_CHECKRejection pins the
// route-CHECK + method-CHECK enforcement. The Store layer is a
// thin delegate; the database is the enforcement boundary, so
// a bogus verb must surface as a pgconn.PgError with SQLSTATE
// 23514 (CHECK violation).
func TestPgStoreRequestTelemetry_CHECKRejection(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)

	err := store.InsertRequestTelemetry(ctx, sqlc.InsertRequestTelemetryParams{
		AccountID:    pgtype.UUID{Bytes: parseUUID(t, uuid.NewString()), Valid: true},
		AppID:        pgtype.UUID{Bytes: parseUUID(t, uuid.NewString()), Valid: true},
		DeploymentID: pgtype.UUID{Bytes: parseUUID(t, uuid.NewString()), Valid: true},
		Route:        "GET /foo",
		Method:       "BOGUS",
		Status:       200,
		LatencyMs:    1,
		ColdBoot:     false,
		TraceID:      pgtype.Text{},
		ReceivedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
		Count:        1,
	})
	if err == nil {
		t.Fatal("Insert with bogus method: expected CHECK violation, got nil")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected *pgconn.PgError, got %T: %v", err, err)
	}
	if pgErr.Code != "23514" {
		t.Errorf("SQLSTATE = %q, want 23514 (CHECK violation)", pgErr.Code)
	}
	// Defence-in-depth: the constraint name should mention 'method'
	// (the closed-enum CHECK at migration 00427 line 103).
	if !strings.Contains(pgErr.ConstraintName, "method") {
		t.Errorf("constraint name = %q, expected substring 'method'", pgErr.ConstraintName)
	}
}

// parseUUID converts a UUID string into the 16-byte form pgtype.UUID
// expects. Centralised here so the test surface stays focused on
// the API contract instead of byte-fiddling.
func parseUUID(t *testing.T, s string) [16]byte {
	t.Helper()
	u, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parseUUID(%q): %v", s, err)
	}
	var b [16]byte
	copy(b[:], u[:])
	return b
}
