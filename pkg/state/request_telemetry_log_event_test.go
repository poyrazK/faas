package state

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestRequestTelemetryLogEventRedactsAndNormalizes(t *testing.T) {
	accountID, appID, deploymentID, eventID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	receivedAt := time.Date(2026, 9, 22, 14, 35, 0, 0, time.UTC)
	traceID := strings.Repeat("a", 32)
	arg := sqlc.InsertRequestTelemetryParams{
		AccountID:    pgtype.UUID{Bytes: accountID, Valid: true},
		AppID:        pgtype.UUID{Bytes: appID, Valid: true},
		DeploymentID: pgtype.UUID{Bytes: deploymentID, Valid: true},
		Route:        "POST /checkout/{id}", Method: "POST", Status: 503,
		LatencyMs: 45, ColdBoot: true,
		TraceID:    pgtype.Text{String: traceID, Valid: true},
		ReceivedAt: pgtype.Timestamptz{Time: receivedAt, Valid: true},
		Count:      3, InstanceID: pgtype.Text{String: "inst-1", Valid: true},
		UaFamily: "browser", ReferrerHost: "private.example", Country: "TR",
	}
	event, err := requestTelemetryLogEvent(arg, eventID.String())
	if err != nil {
		t.Fatal(err)
	}
	if event.Source != LogEventSourceHTTP || event.SourceEventID != eventID.String() || !event.OccurredAt.Equal(receivedAt) {
		t.Fatalf("source identity = %+v", event)
	}
	if event.AccountID != accountID.String() || event.AppID != appID.String() || event.DeploymentID != deploymentID.String() {
		t.Fatalf("tenant identity = %+v", event)
	}
	if event.Route != "/checkout/{id}" || event.Method != "POST" || event.Status != 503 || event.Level != "error" || event.Stream != "access" {
		t.Fatalf("HTTP dimensions = %+v", event)
	}
	if event.RequestID != traceID || event.TraceID != traceID || event.InstanceID != "inst-1" || event.Occurrences != 3 || !event.ColdBoot {
		t.Fatalf("correlation = %+v", event)
	}
	if event.LatencyMS == nil || *event.LatencyMS != 45 || event.Message != "POST /checkout/{id} returned 503 in 45ms" {
		t.Fatalf("message = %+v", event)
	}
	if string(event.Fields) != "{}" || strings.Contains(event.Message, arg.ReferrerHost) {
		t.Fatalf("projection retained unrelated telemetry metadata: %+v", event)
	}
}

func TestRequestTelemetryLogEventRequiresStableIdentity(t *testing.T) {
	arg := sqlc.InsertRequestTelemetryParams{
		AccountID:    pgtype.UUID{Bytes: uuid.New(), Valid: true},
		AppID:        pgtype.UUID{Bytes: uuid.New(), Valid: true},
		DeploymentID: pgtype.UUID{Bytes: uuid.New(), Valid: true},
		Route:        "GET /health", Method: "GET", Status: 200, Count: 1,
		ReceivedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	if _, err := requestTelemetryLogEvent(arg, "not-a-uuid"); err == nil {
		t.Fatal("expected malformed event id to fail")
	}
	arg.ReceivedAt.Valid = false
	if _, err := requestTelemetryLogEvent(arg, uuid.NewString()); err == nil {
		t.Fatal("expected missing timestamp to fail")
	}
}
