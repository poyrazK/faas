package state

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreExecutionPrincipalListFiltersBeforePagination(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account := executionTestAccount(t, store, "principal-list")
	if err := store.UpdateAccountPlan(ctx, account.ID, api.PlanScale); err != nil {
		t.Fatalf("UpdateAccountPlan: %v", err)
	}
	principalA, principalB := "agent-a", "agent-b"
	base := time.Now().UTC().Add(time.Second)
	legacy, err := store.CreateExecution(ctx, executionTestParams(t, account.ID, base.Add(-time.Millisecond), 0, "legacy"))
	if err != nil {
		t.Fatalf("CreateExecution(legacy): %v", err)
	}
	created := map[string]Execution{}
	for i, item := range []struct {
		name      string
		principal *string
	}{
		{"a1", &principalA}, {"b1", &principalB}, {"a2", &principalA},
		{"b2", &principalB}, {"a3", &principalA},
	} {
		params := executionTestParams(t, account.ID, base.Add(time.Duration(i)*time.Millisecond), 0, item.name)
		params.RunsPrincipalID = item.principal
		row, err := store.CreateExecution(ctx, params)
		if err != nil {
			t.Fatalf("CreateExecution(%s): %v", item.name, err)
		}
		created[item.name] = row
	}
	page, err := store.ListExecutionsByPrincipal(ctx, account.ID, principalA, 1, 1)
	if err != nil || len(page) != 1 || page[0].ID != created["a2"].ID {
		t.Fatalf("agent A page = %#v, %v; want only its second-newest run", page, err)
	}
	ownedRows, err := store.ListExecutionsByPrincipal(ctx, account.ID, principalA, 10, 0)
	if err != nil || len(ownedRows) != 3 || ownedRows[0].ID == legacy.ID || ownedRows[1].ID == legacy.ID || ownedRows[2].ID == legacy.ID {
		t.Fatalf("agent A legacy visibility = %#v, %v", ownedRows, err)
	}
	if _, err := store.RequestExecutionCancellation(ctx, account.ID, created["a2"].ID, base.Add(time.Second)); err != nil {
		t.Fatalf("cancel agent A row: %v", err)
	}
	if _, err := store.RequestExecutionCancellation(ctx, account.ID, created["b2"].ID, base.Add(time.Second)); err != nil {
		t.Fatalf("cancel agent B row: %v", err)
	}
	cancelled, err := store.ListExecutionsByPrincipalStatus(ctx, account.ID, principalA, api.ExecutionStatusCancelled, 10, 0)
	if err != nil || len(cancelled) != 1 || cancelled[0].ID != created["a2"].ID {
		t.Fatalf("agent A cancelled rows = %#v, %v", cancelled, err)
	}
}

func TestMemStoreAPIKeyRotationPreservesRunsPrincipal(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account := executionTestAccount(t, store, "principal-rotation")
	oldHash, newHash := []byte("agent-key-old"), []byte("agent-key-new")
	key, err := store.CreateAPIKey(ctx, account.ID, oldHash, "agent", []string{api.ScopeRunsRead, api.ScopeRunsWrite})
	if err != nil {
		t.Fatal(err)
	}
	if key.RunsPrincipalID == "" {
		t.Fatal("created key has no Runs principal")
	}
	rotated, _, err := store.RotateAPIKey(ctx, account.ID, key.ID, newHash, "agent-v2", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, authenticated, err := store.AuthenticateKey(ctx, newHash)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.RunsPrincipalID != key.RunsPrincipalID || authenticated.RunsPrincipalID != key.RunsPrincipalID {
		t.Fatalf("principal changed across rotation: old=%q returned=%q authenticated=%q", key.RunsPrincipalID, rotated.RunsPrincipalID, authenticated.RunsPrincipalID)
	}
}
