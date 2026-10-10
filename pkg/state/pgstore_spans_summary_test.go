package state_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgStoreUpdateSpansSummaryMergesAndDeduplicatesWriters(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID := uuid.New()
	appID := uuid.New()
	deploymentID := uuid.New()
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	now := time.Now().UTC()
	if err := store.InsertRequestTelemetry(ctx, sqlc.InsertRequestTelemetryParams{
		AccountID: pgtype.UUID{Bytes: accountID, Valid: true}, AppID: pgtype.UUID{Bytes: appID, Valid: true},
		DeploymentID: pgtype.UUID{Bytes: deploymentID, Valid: true}, Route: "POST /checkout", Method: "POST",
		Status: 201, LatencyMs: 100, TraceID: pgtype.Text{String: traceID, Valid: true},
		ReceivedAt: pgtype.Timestamptz{Time: now, Valid: true}, Count: 1,
		UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__",
	}); err != nil {
		t.Fatalf("insert request telemetry: %v", err)
	}

	first := []byte(`[{"span_id":"service","end_time_unix_nano":"10","duration_nanos":11000000}]`)
	if err := store.UpdateSpansSummary(ctx, traceID, accountID, first); err != nil {
		t.Fatalf("write first producer summary: %v", err)
	}
	second := []byte(`[{"span_id":"outbound","end_time_unix_nano":"20","duration_nanos":49000000},{"span_id":"service","end_time_unix_nano":"10","duration_nanos":12000000}]`)
	if err := store.UpdateSpansSummary(ctx, traceID, accountID, second); err != nil {
		t.Fatalf("write second producer summary: %v", err)
	}

	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT spans_summary FROM request_telemetry WHERE trace_id = $1 AND account_id = $2`, traceID, accountID).Scan(&raw); err != nil {
		t.Fatalf("read merged summary: %v", err)
	}
	var got []struct {
		SpanID        string `json:"span_id"`
		DurationNanos uint64 `json:"duration_nanos"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode merged summary %s: %v", raw, err)
	}
	if len(got) != 2 {
		t.Fatalf("merged spans = %s, want both producer spans once", raw)
	}
	if got[0].SpanID != "outbound" || got[0].DurationNanos != 49_000_000 || got[1].SpanID != "service" || got[1].DurationNanos != 12_000_000 {
		t.Fatalf("merged spans = %+v, want slowest-first with the latest duplicate duration", got)
	}

	// The composite account predicate must still prevent a different tenant's
	// writer from replacing or appending evidence to this trace.
	// The foreign write matches no row, which the store reports as such (ADR-934).
	if err := store.UpdateSpansSummary(ctx, traceID, uuid.New(), []byte(`[{"span_id":"foreign"}]`)); !errors.Is(err, state.ErrRequestTelemetryRowNotFound) {
		t.Fatalf("cross-account update: %v, want ErrRequestTelemetryRowNotFound", err)
	}
	var afterForeign []byte
	if err := pool.QueryRow(ctx, `SELECT spans_summary FROM request_telemetry WHERE trace_id = $1 AND account_id = $2`, traceID, accountID).Scan(&afterForeign); err != nil {
		t.Fatalf("read summary after cross-account update: %v", err)
	}
	if string(afterForeign) != string(raw) {
		t.Fatalf("cross-account update changed summary: before=%s after=%s", raw, afterForeign)
	}
}
