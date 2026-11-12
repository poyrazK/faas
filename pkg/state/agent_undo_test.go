// adr: 20261112080601
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// exerciseAgentUndo checks undo records round-trip newest first within a
// window, are marked undone once and only when undoable, and age out.
func exerciseAgentUndo(t *testing.T, store contractTestStore) {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().Format("150405.000000")
	acct, err := store.CreateAccount(ctx, "agent-undo-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	_, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateAPIKey(ctx, acct.ID, hash, "undo-agent-"+suffix, []string{api.ScopeAgent})
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	base := time.Now().UTC().Truncate(time.Second)
	undoable := state.AgentUndoRecord{
		ActivityID: uuid.NewString(), KeyID: key.ID, AccountID: acct.ID, Method: "PATCH", Path: "/v1/apps/a",
		Kind: api.AgentUndoKindSettings, Summary: "Changed settings of app a: ram_mb",
		Inverse:   json.RawMessage(`{"method":"PATCH","path":"/v1/apps/a","body":{"ram_mb":512}}`),
		Guard:     json.RawMessage(`{"kind":"fields","path":"/v1/apps/a","fields":{"ram_mb":1024}}`),
		CreatedAt: base.Add(-time.Hour),
	}
	notUndoable := state.AgentUndoRecord{
		ActivityID: uuid.NewString(), KeyID: key.ID, AccountID: acct.ID, Method: "PUT", Path: "/v1/apps/a/secrets/T",
		Kind: api.AgentUndoKindSettings, Summary: "Set secret T on app a", NotUndoableReason: "secrets are not kept",
		CreatedAt: base,
	}
	old := undoable
	old.ActivityID, old.CreatedAt = uuid.NewString(), base.Add(-10*24*time.Hour)
	for _, rec := range []state.AgentUndoRecord{undoable, notUndoable, old} {
		if err := store.RecordAgentUndo(ctx, rec); err != nil {
			t.Fatalf("RecordAgentUndo(%s): %v", rec.Summary, err)
		}
	}
	both := undoable
	both.ActivityID, both.NotUndoableReason = uuid.NewString(), "also a reason"
	if err := store.RecordAgentUndo(ctx, both); err == nil {
		t.Fatal("a record with an inverse and a reason was stored")
	} else if _, isMem := store.(*state.MemStore); isMem && !errors.Is(err, state.ErrConflict) {
		t.Fatalf("MemStore: %v; want ErrConflict", err)
	}

	got, err := store.AgentUndoRecords(ctx, key.ID, base.Add(-24*time.Hour), 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("AgentUndoRecords = %d, %v; want the 2 of the last day", len(got), err)
	}
	if got[0].ActivityID != notUndoable.ActivityID || got[0].NotUndoableReason == "" || len(got[0].Inverse) != 0 {
		t.Fatalf("newest = %+v; want the not undoable one", got[0])
	}
	if got[1].ActivityID != undoable.ActivityID || got[1].UndoneAt != nil || !undoJSONEqual(got[1].Inverse, undoable.Inverse) || !undoJSONEqual(got[1].Guard, undoable.Guard) {
		t.Fatalf("older = %+v", got[1])
	}
	if limited, err := store.AgentUndoRecords(ctx, key.ID, base.Add(-24*time.Hour), 1); err != nil || len(limited) != 1 {
		t.Fatalf("limit 1 = %d, %v", len(limited), err)
	}

	if ok, err := store.MarkAgentUndone(ctx, got[0].ID, acct.ID); err != nil || ok {
		t.Fatalf("marking a not undoable record = %v, %v; want false", ok, err)
	}
	if ok, err := store.MarkAgentUndone(ctx, got[1].ID, acct.ID); err != nil || !ok {
		t.Fatalf("MarkAgentUndone = %v, %v", ok, err)
	}
	if ok, err := store.MarkAgentUndone(ctx, got[1].ID, acct.ID); err != nil || ok {
		t.Fatalf("second MarkAgentUndone = %v, %v; want false", ok, err)
	}
	after, err := store.AgentUndoRecords(ctx, key.ID, base.Add(-24*time.Hour), 10)
	if err != nil || after[1].UndoneAt == nil || after[1].UndoneBy != acct.ID {
		t.Fatalf("undone record = %+v, %v", after[1], err)
	}

	if _, err := store.DeleteAgentUndoRecordsBefore(ctx, base.Add(-api.AgentActivityRetention)); err != nil {
		t.Fatalf("DeleteAgentUndoRecordsBefore: %v", err)
	}
	if all, err := store.AgentUndoRecords(ctx, key.ID, base.Add(-30*24*time.Hour), 10); err != nil || len(all) != 2 {
		t.Fatalf("after the sweep = %d, %v; want the old record gone", len(all), err)
	}
}

func undoJSONEqual(a, b json.RawMessage) bool {
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return false
	}
	ja, _ := json.Marshal(va)
	jb, _ := json.Marshal(vb)
	return string(ja) == string(jb)
}

func TestMemStoreAgentUndo(t *testing.T) {
	exerciseAgentUndo(t, state.NewMemStore())
}

func TestPgStoreAgentUndo(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseAgentUndo(t, state.NewPgStore(pool))
}
