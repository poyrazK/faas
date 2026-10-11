//go:build !no_pg

package main

// adr: 957 — PgStore distinguishes a missing request row from a write.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPGStore_UpdateSpansSummaryReportsMissingRow(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	ctx := context.Background()
	app := seedPGApp(t, e, "pg-spans-no-row")
	accountID := uuid.MustParse(e.acct.ID)
	summary := []byte(`[{"trace_id":"5bf92f3577b34da6a3ce929d0e0e4736","span_id":"0000000000000001","name":"SELECT orders","duration_nanos":191000000,"end_time_unix_nano":2}]`)
	const traceID = "5bf92f3577b34da6a3ce929d0e0e4736"

	if err := e.store.UpdateSpansSummary(ctx, traceID, accountID, summary); !errors.Is(err, state.ErrRequestTelemetryRowNotFound) {
		t.Fatalf("update before row = %v, want ErrRequestTelemetryRowNotFound", err)
	}
	if err := e.store.InsertRequestTelemetry(ctx, sqlc.InsertRequestTelemetryParams{
		AccountID:    pgtype.UUID{Bytes: accountID, Valid: true},
		AppID:        pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true},
		DeploymentID: pgtype.UUID{Bytes: uuid.New(), Valid: true},
		Route:        "GET /checkout", Method: "GET", Status: 200, LatencyMs: 240,
		TraceID:    pgtype.Text{String: traceID, Valid: true},
		ReceivedAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		Count:      1, UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__",
	}); err != nil {
		t.Fatalf("InsertRequestTelemetry: %v", err)
	}
	if err := e.store.UpdateSpansSummary(ctx, traceID, accountID, summary); err != nil {
		t.Fatalf("update after row = %v", err)
	}
	// Another account cannot see the row, so its write is a missing row too.
	if err := e.store.UpdateSpansSummary(ctx, traceID, uuid.New(), summary); !errors.Is(err, state.ErrRequestTelemetryRowNotFound) {
		t.Fatalf("cross-account update = %v, want ErrRequestTelemetryRowNotFound", err)
	}
}
