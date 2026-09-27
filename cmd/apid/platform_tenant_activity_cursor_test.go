package main

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPlatformTenantActivityCursorRoundTripAndValidation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	rowID := uuid.New()
	tenantID, appID := uuid.NewString(), uuid.NewString()
	row := sqlc.ListRequestTelemetryByPlatformTenantRow{
		ID:         pgtype.UUID{Bytes: rowID, Valid: true},
		ReceivedAt: pgtype.Timestamptz{Time: now.Add(-time.Minute), Valid: true},
	}
	token, err := encodePlatformTenantActivityCursor(tenantID, appID, 503, time.Hour, now.Add(-time.Hour), now, false, row)
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}
	got, err := decodePlatformTenantActivityCursor(token)
	if err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	if got.TenantID != tenantID || got.AppID != appID || got.Status != 503 || got.SinceNanos != int64(time.Hour) || got.ID != rowID.String() || !got.ReceivedAt.Equal(row.ReceivedAt.Time) {
		t.Fatalf("decoded cursor = %+v", got)
	}

	for _, malformed := range []string{"not-base64", "", "e30"} {
		if _, err := decodePlatformTenantActivityCursor(malformed); err == nil {
			t.Errorf("decode malformed cursor %q succeeded", malformed)
		}
	}
}
