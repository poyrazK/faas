package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const platformTenantActivityCursorVersion = 1

type platformTenantActivityCursor struct {
	Version          int       `json:"v"`
	TenantID         string    `json:"tenant_id"`
	AppID            string    `json:"app_id,omitempty"`
	Status           int       `json:"status,omitempty"`
	SinceNanos       int64     `json:"since_nanos"`
	WindowStart      time.Time `json:"window_start"`
	WindowEnd        time.Time `json:"window_end"`
	RetentionClamped bool      `json:"retention_clamped,omitempty"`
	ReceivedAt       time.Time `json:"received_at"`
	ID               string    `json:"id"`
}

func encodePlatformTenantActivityCursor(tenantID, appID string, status int, since time.Duration, windowStart, windowEnd time.Time, retentionClamped bool, row sqlc.ListRequestTelemetryByPlatformTenantRow) (string, error) {
	if !row.ID.Valid || !row.ReceivedAt.Valid || since <= 0 {
		return "", fmt.Errorf("activity cursor row is incomplete")
	}
	rowID := uuid.UUID(row.ID.Bytes)
	if rowID == uuid.Nil {
		return "", fmt.Errorf("activity cursor row id is empty")
	}
	payload := platformTenantActivityCursor{
		Version: platformTenantActivityCursorVersion, TenantID: tenantID, AppID: appID,
		Status: status, SinceNanos: int64(since), WindowStart: windowStart.UTC(),
		WindowEnd: windowEnd.UTC(), RetentionClamped: retentionClamped,
		ReceivedAt: row.ReceivedAt.Time.UTC(), ID: rowID.String(),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode activity cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodePlatformTenantActivityCursor(raw string) (platformTenantActivityCursor, error) {
	if len(raw) > 4096 {
		return platformTenantActivityCursor{}, fmt.Errorf("cursor is too long")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return platformTenantActivityCursor{}, fmt.Errorf("cursor is not valid base64")
	}
	var cursor platformTenantActivityCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		return platformTenantActivityCursor{}, fmt.Errorf("cursor payload is invalid")
	}
	if cursor.Version != platformTenantActivityCursorVersion || cursor.TenantID == "" || cursor.WindowStart.IsZero() || cursor.WindowEnd.IsZero() || cursor.ReceivedAt.IsZero() || cursor.SinceNanos <= 0 || cursor.ID == "" {
		return platformTenantActivityCursor{}, fmt.Errorf("cursor payload is incomplete")
	}
	if _, err := uuid.Parse(cursor.TenantID); err != nil {
		return platformTenantActivityCursor{}, fmt.Errorf("cursor tenant is invalid")
	}
	if cursor.AppID != "" {
		if _, err := uuid.Parse(cursor.AppID); err != nil {
			return platformTenantActivityCursor{}, fmt.Errorf("cursor app is invalid")
		}
	}
	if cursor.Status != 0 && (cursor.Status < 100 || cursor.Status > 599) {
		return platformTenantActivityCursor{}, fmt.Errorf("cursor status is invalid")
	}
	if _, err := uuid.Parse(cursor.ID); err != nil {
		return platformTenantActivityCursor{}, fmt.Errorf("cursor row id is invalid")
	}
	if cursor.WindowEnd.Before(cursor.WindowStart) || cursor.ReceivedAt.Before(cursor.WindowStart) || !cursor.ReceivedAt.Before(cursor.WindowEnd) {
		return platformTenantActivityCursor{}, fmt.Errorf("cursor window is invalid")
	}
	return cursor, nil
}
