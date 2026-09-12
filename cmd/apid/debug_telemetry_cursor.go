package main

// debug_telemetry_cursor.go contains the server-side codec for the request
// telemetry list cursor. The token is deliberately opaque to API clients,
// but carries the complete ordering and query window so a page walk remains
// stable while new telemetry arrives.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const debugTelemetryCursorVersion = 1

type debugTelemetryCursor struct {
	Version          int       `json:"v"`
	AppID            string    `json:"app_id"`
	Route            string    `json:"route,omitempty"`
	DeploymentID     string    `json:"deployment_id,omitempty"`
	Status           int       `json:"status,omitempty"`
	ColdBoot         *bool     `json:"cold_boot,omitempty"`
	ConsumerID       string    `json:"consumer_id,omitempty"`
	MinLatencyMS     int       `json:"min_latency_ms,omitempty"`
	WindowStart      time.Time `json:"window_start"`
	WindowEnd        time.Time `json:"window_end"`
	RetentionClamped bool      `json:"retention_clamped,omitempty"`
	ReceivedAt       time.Time `json:"received_at"`
	ID               string    `json:"id"`
}

func encodeDebugTelemetryCursor(appID, route string, windowStart, windowEnd time.Time, retentionClamped bool, row sqlc.ListRequestTelemetryByAppRow) string {
	return encodeDebugTelemetryCursorWithFilters(appID, route, debugTelemetryCursorFilters{}, windowStart, windowEnd, retentionClamped, row)
}

func encodeDebugTelemetryCursorWithFilters(appID, route string, filters debugTelemetryCursorFilters, windowStart, windowEnd time.Time, retentionClamped bool, row sqlc.ListRequestTelemetryByAppRow) string {
	if !row.ID.Valid || !row.ReceivedAt.Valid || appID == "" {
		return ""
	}
	uid := uuid.UUID(row.ID.Bytes)
	if uid == uuid.Nil {
		return ""
	}
	payload := debugTelemetryCursor{
		Version:          debugTelemetryCursorVersion,
		AppID:            appID,
		Route:            route,
		DeploymentID:     filters.DeploymentID,
		Status:           filters.Status,
		ColdBoot:         filters.ColdBoot,
		ConsumerID:       filters.ConsumerID,
		MinLatencyMS:     filters.MinLatencyMS,
		WindowStart:      windowStart.UTC(),
		WindowEnd:        windowEnd.UTC(),
		RetentionClamped: retentionClamped,
		ReceivedAt:       row.ReceivedAt.Time.UTC(),
		ID:               uid.String(),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeDebugTelemetryCursor(raw string) (debugTelemetryCursor, error) {
	if strings.TrimSpace(raw) == "" {
		return debugTelemetryCursor{}, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return debugTelemetryCursor{}, fmt.Errorf("cursor is not valid base64")
	}
	var cursor debugTelemetryCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		return debugTelemetryCursor{}, fmt.Errorf("cursor payload is invalid")
	}
	if cursor.Version != debugTelemetryCursorVersion || cursor.AppID == "" || cursor.WindowStart.IsZero() || cursor.WindowEnd.IsZero() || cursor.ReceivedAt.IsZero() {
		return debugTelemetryCursor{}, fmt.Errorf("cursor payload is incomplete")
	}
	if cursor.WindowEnd.Before(cursor.WindowStart) || cursor.ReceivedAt.Before(cursor.WindowStart) || !cursor.ReceivedAt.Before(cursor.WindowEnd) {
		return debugTelemetryCursor{}, fmt.Errorf("cursor window is invalid")
	}
	if _, err := uuid.Parse(cursor.AppID); err != nil {
		return debugTelemetryCursor{}, fmt.Errorf("cursor app is invalid")
	}
	if cursor.DeploymentID != "" {
		if _, err := uuid.Parse(cursor.DeploymentID); err != nil {
			return debugTelemetryCursor{}, fmt.Errorf("cursor deployment is invalid")
		}
	}
	if cursor.Status != 0 && (cursor.Status < 100 || cursor.Status > 599) {
		return debugTelemetryCursor{}, fmt.Errorf("cursor status is invalid")
	}
	if cursor.MinLatencyMS < 0 || cursor.MinLatencyMS > 86_400_000 {
		return debugTelemetryCursor{}, fmt.Errorf("cursor latency filter is invalid")
	}
	if cursor.ConsumerID != "" && cursor.ConsumerID != debugTelemetryAnonymousConsumer {
		if _, err := uuid.Parse(cursor.ConsumerID); err != nil {
			return debugTelemetryCursor{}, fmt.Errorf("cursor consumer is invalid")
		}
	}
	if _, err := uuid.Parse(cursor.ID); err != nil {
		return debugTelemetryCursor{}, fmt.Errorf("cursor id is invalid")
	}
	return cursor, nil
}

func (c debugTelemetryCursor) filters() debugTelemetryCursorFilters {
	return debugTelemetryCursorFilters{
		DeploymentID: c.DeploymentID,
		Status:       c.Status,
		ColdBoot:     c.ColdBoot,
		ConsumerID:   c.ConsumerID,
		MinLatencyMS: c.MinLatencyMS,
	}
}

func debugTelemetryCursorParams(cursor debugTelemetryCursor) (pgtype.Timestamptz, pgtype.UUID) {
	if cursor.Version == 0 {
		return pgtype.Timestamptz{}, pgtype.UUID{}
	}
	id, err := uuid.Parse(cursor.ID)
	if err != nil {
		return pgtype.Timestamptz{}, pgtype.UUID{}
	}
	return pgtype.Timestamptz{Time: cursor.ReceivedAt.UTC(), Valid: true}, pgtype.UUID{Bytes: id, Valid: true}
}
