package main

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestDebugTelemetryCursorRoundTrip(t *testing.T) {
	appID := uuid.New()
	requestID := uuid.New()
	receivedAt := time.Date(2026, 9, 12, 10, 11, 12, 123456789, time.UTC)
	windowStart := receivedAt.Add(-time.Hour)
	windowEnd := receivedAt.Add(time.Minute)
	row := sqlc.ListRequestTelemetryByAppRow{
		ID:         pgtype.UUID{Bytes: requestID, Valid: true},
		ReceivedAt: pgtype.Timestamptz{Time: receivedAt, Valid: true},
	}
	raw := encodeDebugTelemetryCursor(appID.String(), "GET /orders", windowStart, windowEnd, true, row)
	if raw == "" {
		t.Fatal("encodeDebugTelemetryCursor() returned empty cursor")
	}
	got, err := decodeDebugTelemetryCursor(raw)
	if err != nil {
		t.Fatalf("decodeDebugTelemetryCursor() error = %v", err)
	}
	if got.AppID != appID.String() || got.Route != "GET /orders" || got.ID != requestID.String() {
		t.Fatalf("decoded cursor = %+v", got)
	}
	if !got.RetentionClamped {
		t.Fatal("decoded cursor lost retention clamp metadata")
	}
	if !got.WindowStart.Equal(windowStart) || !got.WindowEnd.Equal(windowEnd) || !got.ReceivedAt.Equal(receivedAt) {
		t.Fatalf("decoded times = %+v, want start=%s end=%s received=%s", got, windowStart, windowEnd, receivedAt)
	}
	received, id := debugTelemetryCursorParams(got)
	if !received.Valid || !received.Time.Equal(receivedAt) || !id.Valid || uuid.UUID(id.Bytes) != requestID {
		t.Fatalf("cursor params = received=%+v id=%+v", received, id)
	}
}

func TestDebugTelemetryCursorCarriesAllFilters(t *testing.T) {
	appID := uuid.New()
	requestID := uuid.New()
	receivedAt := time.Date(2026, 9, 12, 10, 11, 12, 0, time.UTC)
	coldBoot := false
	row := sqlc.ListRequestTelemetryByAppRow{
		ID:         pgtype.UUID{Bytes: requestID, Valid: true},
		ReceivedAt: pgtype.Timestamptz{Time: receivedAt, Valid: true},
	}
	filters := debugTelemetryCursorFilters{
		DeploymentID: uuid.NewString(), Status: 503, ColdBoot: &coldBoot,
		ConsumerID: debugTelemetryAnonymousConsumer, MinLatencyMS: 250,
	}
	raw := encodeDebugTelemetryCursorWithFilters(appID.String(), "/checkout", filters, receivedAt.Add(-time.Hour), receivedAt.Add(time.Hour), false, row)
	got, err := decodeDebugTelemetryCursor(raw)
	if err != nil {
		t.Fatalf("decodeDebugTelemetryCursor() error = %v", err)
	}
	publicFilters := debugTelemetryFilters{
		DeploymentID: filters.DeploymentID, Status: filters.Status, ColdBoot: filters.ColdBoot,
		ConsumerID: filters.ConsumerID, MinLatencyMS: filters.MinLatencyMS,
	}
	if !publicFilters.same(got.filters()) {
		t.Fatalf("decoded filters = %+v, want %+v", got.filters(), filters)
	}
}

func TestDebugTelemetryCursorRejectsMalformedAndOutOfWindow(t *testing.T) {
	for _, raw := range []string{"not-base64", "", "e30"} {
		if raw == "" {
			continue
		}
		if _, err := decodeDebugTelemetryCursor(raw); err == nil {
			t.Errorf("decodeDebugTelemetryCursor(%q) error = nil", raw)
		}
	}
	appID := uuid.New()
	row := sqlc.ListRequestTelemetryByAppRow{
		ID:         pgtype.UUID{Bytes: uuid.New(), Valid: true},
		ReceivedAt: pgtype.Timestamptz{Time: time.Unix(100, 0).UTC(), Valid: true},
	}
	raw := encodeDebugTelemetryCursor(appID.String(), "", time.Unix(0, 0).UTC(), time.Unix(200, 0).UTC(), false, row)
	if _, err := decodeDebugTelemetryCursor(raw); err != nil {
		t.Fatalf("decodeDebugTelemetryCursor(valid) error = %v", err)
	}
}
