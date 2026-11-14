package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261107220000 — a key's write requests list newest first, page by
// (created_at, id), stop at the retention cutoff, and the sweep deletes old
// rows in batches.
func exerciseAgentActivity(t *testing.T, store contractTestStore) {
	t.Helper()
	ctx := context.Background()
	keyID, otherKey, accountID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	approvalID := uuid.NewString()
	for i, path := range []string{"/v1/apps/a/park", "/v1/apps/a/wake", "/v1/apps/a/env", "/v1/apps/a"} {
		row := state.AgentActivity{KeyID: keyID, AccountID: accountID, Method: "POST", Path: path, Status: 200 + i}
		if i == 3 {
			row.Method, row.ApprovalID = "DELETE", approvalID
		}
		if err := store.RecordAgentActivity(ctx, row); err != nil {
			t.Fatalf("RecordAgentActivity: %v", err)
		}
		time.Sleep(5 * time.Millisecond) // distinct created_at for a stable order
	}
	if err := store.RecordAgentActivity(ctx, state.AgentActivity{KeyID: otherKey, AccountID: accountID, Method: "PUT", Path: "/v1/other", Status: 204}); err != nil {
		t.Fatalf("RecordAgentActivity other key: %v", err)
	}
	since := time.Now().Add(-time.Hour)

	first, err := store.ListAgentActivity(ctx, keyID, since, state.AgentActivityCursor{}, 3)
	if err != nil {
		t.Fatalf("ListAgentActivity: %v", err)
	}
	if len(first) != 3 || first[0].Path != "/v1/apps/a" || first[0].Method != "DELETE" || first[0].ApprovalID != approvalID || first[0].Status != 203 {
		t.Fatalf("first page = %+v, want the DELETE with its approval first", first)
	}
	last := first[len(first)-1]
	rest, err := store.ListAgentActivity(ctx, keyID, since, state.AgentActivityCursor{At: last.CreatedAt, ID: last.ID}, 3)
	if err != nil {
		t.Fatalf("ListAgentActivity page 2: %v", err)
	}
	if len(rest) != 1 || rest[0].Path != "/v1/apps/a/park" || rest[0].ApprovalID != "" {
		t.Fatalf("second page = %+v, want the oldest row only", rest)
	}

	if rows, err := store.ListAgentActivity(ctx, keyID, time.Now().Add(time.Hour), state.AgentActivityCursor{}, 10); err != nil || len(rows) != 0 {
		t.Fatalf("rows after the cutoff = %+v, %v; want none", rows, err)
	}

	n, err := store.DeleteAgentActivityBefore(ctx, time.Now().Add(time.Minute), 2)
	if err != nil || n != 2 {
		t.Fatalf("DeleteAgentActivityBefore batch = %d, %v; want 2", n, err)
	}
	for {
		n, err = store.DeleteAgentActivityBefore(ctx, time.Now().Add(time.Minute), 2)
		if err != nil {
			t.Fatalf("DeleteAgentActivityBefore: %v", err)
		}
		if n == 0 {
			break
		}
	}
	if rows, err := store.ListAgentActivity(ctx, keyID, since, state.AgentActivityCursor{}, 10); err != nil || len(rows) != 0 {
		t.Fatalf("rows after the sweep = %+v, %v; want none", rows, err)
	}
}

func TestMemStoreAgentActivity(t *testing.T) {
	exerciseAgentActivity(t, state.NewMemStore())
}

func TestPgStoreAgentActivity(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseAgentActivity(t, state.NewPgStore(pool))
}
