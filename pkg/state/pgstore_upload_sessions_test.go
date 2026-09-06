package state_test

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgUploadSessionAppendUsesExpectedAndNewOffsets(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	accountID, _, _ := seedLiveDeploy(t, s, ctx, "upload-cas", "upload-cas")
	var accountUUID pgtype.UUID
	if err := accountUUID.Scan(accountID); err != nil {
		t.Fatalf("parse account id: %v", err)
	}

	const sessionID = "upload-cas-session"
	if _, err := s.CreateUploadSession(ctx, sqlc.CreateUploadSessionParams{
		ID:            sessionID,
		AccountID:     accountUUID,
		AppSlug:       "pg-app-upload-cas",
		TotalSize:     1024,
		ChunkSize:     512,
		PartPath:      "/tmp/upload-cas-session.part",
		DeployOptions: []byte(`{}`),
	}); err != nil {
		t.Fatalf("CreateUploadSession: %v", err)
	}

	appendAt := func(expected, next int64) (sqlc.AppendUploadBytesRow, error) {
		return s.AppendUploadBytes(ctx, sqlc.AppendUploadBytesParams{
			ID:                    sessionID,
			ExpectedReceivedBytes: expected,
			NewReceivedBytes:      next,
		})
	}
	if row, err := appendAt(0, 512); err != nil {
		t.Fatalf("first append: %v", err)
	} else if row.ReceivedBytes != 512 {
		t.Fatalf("first append offset = %d, want 512", row.ReceivedBytes)
	}
	if _, err := appendAt(0, 512); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("repeated stale offset error = %v, want ErrConflict", err)
	}
	if row, err := appendAt(512, 1024); err != nil {
		t.Fatalf("second append: %v", err)
	} else if row.ReceivedBytes != 1024 {
		t.Fatalf("second append offset = %d, want 1024", row.ReceivedBytes)
	}
}
