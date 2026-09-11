package state

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemStoreListAuditLogBeforeCursorUsesTimestampAndIDTieBreak(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	accountID := uuid.New()
	at := time.Date(2026, 9, 11, 12, 34, 56, 0, time.UTC)
	ids := []uuid.UUID{
		uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		uuid.MustParse("00000000-0000-0000-0000-000000000003"),
		uuid.MustParse("00000000-0000-0000-0000-000000000002"),
	}
	for _, id := range ids {
		idCopy := accountID
		if err := store.InsertAuditLog(ctx, AuditLog{
			ID:         id,
			Kind:       "account.deleted",
			AccountID:  &idCopy,
			ReceivedAt: at,
		}); err != nil {
			t.Fatalf("InsertAuditLog(%s): %v", id, err)
		}
	}

	first, err := store.ListAuditLog(ctx, AuditLogFilter{
		AccountID: &accountID,
		Limit:     2,
	})
	if err != nil {
		t.Fatalf("first ListAuditLog: %v", err)
	}
	if got := []uuid.UUID{first[0].ID, first[1].ID}; !sameUUIDs(got, []uuid.UUID{ids[1], ids[2]}) {
		t.Fatalf("first page IDs = %v, want %v", got, []uuid.UUID{ids[1], ids[2]})
	}

	older, err := store.ListAuditLog(ctx, AuditLogFilter{
		AccountID: &accountID,
		Before:    &AuditLogCursor{ReceivedAt: first[1].ReceivedAt, ID: first[1].ID},
		Limit:     2,
	})
	if err != nil {
		t.Fatalf("older ListAuditLog: %v", err)
	}
	if len(older) != 1 || older[0].ID != ids[0] {
		t.Fatalf("older page = %v, want [%s]", older, ids[0])
	}
}

func sameUUIDs(got, want []uuid.UUID) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
