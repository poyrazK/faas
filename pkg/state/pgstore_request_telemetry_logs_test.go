package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgStoreRequestTelemetryWithLogEventAtomicReplay(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID, deploymentID, eventID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	receivedAt := time.Now().UTC().Truncate(time.Minute)
	arg := sqlc.InsertRequestTelemetryParams{
		AccountID:    pgtype.UUID{Bytes: accountID, Valid: true},
		AppID:        pgtype.UUID{Bytes: appID, Valid: true},
		DeploymentID: pgtype.UUID{Bytes: deploymentID, Valid: true},
		Route:        "POST /checkout", Method: "POST", Status: 503,
		LatencyMs: 42, Count: 2,
		ReceivedAt: pgtype.Timestamptz{Time: receivedAt, Valid: true},
		UaFamily:   "__unknown__", ReferrerHost: "__none__", Country: "__unknown__",
	}
	if err := store.InsertRequestTelemetryWithLogEvent(ctx, arg, eventID.String()); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	replay := arg
	replay.Status = 200
	replay.Count = 99
	if err := store.InsertRequestTelemetryWithLogEvent(ctx, replay, eventID.String()); err != nil {
		t.Fatalf("replay: %v", err)
	}
	assertRows := func(label, query string, want int) {
		t.Helper()
		var got int
		if err := pool.QueryRow(ctx, query, accountID, appID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s rows = %d, want %d", label, got, want)
		}
	}
	assertRows("request_telemetry", "select count(*) from request_telemetry where account_id = $1 and app_id = $2", 1)
	assertRows("log_events", "select count(*) from log_events where account_id = $1 and app_id = $2", 1)

	page, more, err := store.ListLogEvents(ctx, state.LogEventFilter{
		AccountID: accountID.String(), AppID: appID.String(),
		Since: receivedAt.Add(-time.Minute), Until: receivedAt.Add(time.Minute),
		Route: "/checkout", Status: 503,
	})
	if err != nil || more || len(page) != 1 || page[0].Occurrences != 2 || page[0].Status != 503 {
		t.Fatalf("query = %+v, more=%v, err=%v", page, more, err)
	}

	bad := arg
	bad.Route = ""
	bad.ReceivedAt.Time = receivedAt.Add(time.Minute)
	if err := store.InsertRequestTelemetryWithLogEvent(ctx, bad, uuid.NewString()); err == nil {
		t.Fatal("expected telemetry CHECK failure")
	}
	assertRows("request_telemetry", "select count(*) from request_telemetry where account_id = $1 and app_id = $2", 1)
	assertRows("log_events", "select count(*) from log_events where account_id = $1 and app_id = $2", 1)
}
