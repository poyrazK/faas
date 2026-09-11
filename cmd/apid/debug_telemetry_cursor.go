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
	WindowStart      time.Time `json:"window_start"`
	WindowEnd        time.Time `json:"window_end"`
	RetentionClamped bool      `json:"retention_clamped,omitempty"`
	ReceivedAt       time.Time `json:"received_at"`
	ID               string    `json:"id"`
}

func encodeDebugTelemetryCursor(appID, route string, windowStart, windowEnd time.Time, retentionClamped bool, row sqlc.ListRequestTelemetryByAppRow) string {
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
	if _, err := uuid.Parse(cursor.ID); err != nil {
		return debugTelemetryCursor{}, fmt.Errorf("cursor id is invalid")
	}
	return cursor, nil
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
