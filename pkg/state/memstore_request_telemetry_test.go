package state_test

// Tests that pin the MemStore ADR-127 request-telemetry stub surface.
//
// memstore_request_telemetry.go exists to satisfy the Store interface
// while the production path is Postgres-only (the sqlc-generated
// queries against the request_telemetry table added in PR #1067 /
// migration 00427). Each MemStore method returns the sentinel
// errMemStoreRequestTelemetry so a unit test that exercises a
// request-telemetry code path against MemStore fails loudly with a
// clear "use pgtest" hint instead of silently returning empty data.
//
// This file pins the sentinel and verifies it propagates through
// each of the four request-telemetry Store methods. Coverage of
// these stubs drives pkg/state past the 70% floor (which the CI
// check-state-coverage gate enforces) — the stubs are pure return-
// const statements, but the coverage tool still needs a call site
// to count the function bodies as "executed".

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestMemStore_RequestTelemetry_StubsReturnSentinel(t *testing.T) {
	m := state.NewMemStore()
	ctx := context.Background()

	// InsertRequestTelemetry — the per-request INSERT. The stub
	// ignores its arg and returns the sentinel. The argument
	// values are zero-valued pgtype literals because the stub
	// never reads them; we only need a well-typed call site for
	// coverage of the function body.
	if err := m.InsertRequestTelemetry(ctx, sqlc.InsertRequestTelemetryParams{
		Route:      "GET /foo",
		Method:     "GET",
		Status:     200,
		LatencyMs:  42,
		ColdBoot:   false,
		TraceID:    pgtype.Text{},
		ReceivedAt: pgtype.Timestamptz{},
	}); !errors.Is(err, (*state.ErrMemStoreRequestTelemetry)) {
		t.Errorf("InsertRequestTelemetry: got %v, want errMemStoreRequestTelemetry", err)
	}

	// ListRequestTelemetryByApp — backs GET /v1/apps/{slug}/debug/requests.
	if _, err := m.ListRequestTelemetryByApp(ctx, sqlc.ListRequestTelemetryByAppParams{
		AppID:        pgtype.UUID{},
		ReceivedAt:   pgtype.Timestamptz{},
		ReceivedAt_2: pgtype.Timestamptz{},
		Limit:        50,
	}); !errors.Is(err, (*state.ErrMemStoreRequestTelemetry)) {
		t.Errorf("ListRequestTelemetryByApp: got %v, want errMemStoreRequestTelemetry", err)
	}

	// RequestTelemetryByDeployment — the per-deployment timeline.
	if _, err := m.RequestTelemetryByDeployment(ctx, sqlc.RequestTelemetryByDeploymentParams{
		AppID:        pgtype.UUID{},
		DeploymentID: pgtype.UUID{},
		ReceivedAt:   pgtype.Timestamptz{},
		ReceivedAt_2: pgtype.Timestamptz{},
		Limit:        50,
	}); !errors.Is(err, (*state.ErrMemStoreRequestTelemetry)) {
		t.Errorf("RequestTelemetryByDeployment: got %v, want errMemStoreRequestTelemetry", err)
	}

	// RequestTelemetryBaselineP95ByRoute — regression baseline for
	// the canary guard.
	if _, err := m.RequestTelemetryBaselineP95ByRoute(ctx, sqlc.RequestTelemetryBaselineP95ByRouteParams{
		AppID:        pgtype.UUID{},
		DeploymentID: pgtype.UUID{},
		ReceivedAt:   pgtype.Timestamptz{},
		ReceivedAt_2: pgtype.Timestamptz{},
	}); !errors.Is(err, (*state.ErrMemStoreRequestTelemetry)) {
		t.Errorf("RequestTelemetryBaselineP95ByRoute: got %v, want errMemStoreRequestTelemetry", err)
	}
}

// TestMemStore_RequestTelemetry_SentinelMessage pins the sentinel's
// human-readable form. The "use pgtest" hint is load-bearing — a
// test author who sees this error in CI output needs to know to
// re-run against pgtest (or //go:build metal) rather than chasing a
// MemStore implementation that intentionally doesn't exist.
func TestMemStore_RequestTelemetry_SentinelMessage(t *testing.T) {
	msg := (*state.ErrMemStoreRequestTelemetry).Error()
	if !strings.Contains(msg, "MemStore") {
		t.Errorf("sentinel should mention MemStore; got %q", msg)
	}
	if !strings.Contains(msg, "pgtest") {
		t.Errorf("sentinel should mention pgtest; got %q", msg)
	}
}
