package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261030133644 — a key's agent client is named once, only by its own
// account, and listed while the key is usable.
func exerciseAgentClients(t *testing.T, store contractTestStore) {
	t.Helper()
	ctx := context.Background()
	stamp := time.Now().Format("150405.000000")
	owner, err := store.CreateAccount(ctx, "agent-clients-"+stamp+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	other, err := store.CreateAccount(ctx, "agent-clients-other-"+stamp+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	newKey := func() state.APIKey {
		_, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		expires := time.Now().Add(time.Hour)
		k, err := store.CreateAPIKeyWithExpiryAndProvenance(ctx, owner.ID, hash, api.MCPLoginKeyLabel, []string{api.ScopeAppsRead}, &expires, "", "", nil)
		if err != nil {
			t.Fatalf("CreateAPIKeyWithExpiryAndProvenance: %v", err)
		}
		return k
	}
	named, unnamed := newKey(), newKey()
	claude := state.AgentClient{Name: "Claude", Site: "claude.ai"}
	if err := store.SetAPIKeyAgentClient(ctx, owner.ID, named.ID, claude); err != nil {
		t.Fatalf("SetAPIKeyAgentClient: %v", err)
	}
	if err := store.SetAPIKeyAgentClient(ctx, owner.ID, named.ID, state.AgentClient{Name: "Renamed"}); err != nil {
		t.Fatalf("second SetAPIKeyAgentClient: %v", err)
	}
	if err := store.SetAPIKeyAgentClient(ctx, other.ID, unnamed.ID, state.AgentClient{Name: "Intruder"}); err != nil {
		t.Fatalf("cross-account SetAPIKeyAgentClient: %v", err)
	}
	clients, err := store.APIKeyAgentClients(ctx, owner.ID)
	if err != nil {
		t.Fatalf("APIKeyAgentClients: %v", err)
	}
	if len(clients) != 1 || clients[named.ID] != claude {
		t.Fatalf("clients = %+v, want only %s named %+v", clients, named.ID, claude)
	}
	if theirs, err := store.APIKeyAgentClients(ctx, other.ID); err != nil || len(theirs) != 0 {
		t.Fatalf("other account's clients = %+v, %v; want none", theirs, err)
	}
	if _, err := store.MarkAPIKeyRevoked(ctx, owner.ID, named.ID); err != nil {
		t.Fatalf("MarkAPIKeyRevoked: %v", err)
	}
	if clients, err := store.APIKeyAgentClients(ctx, owner.ID); err != nil || len(clients) != 0 {
		t.Fatalf("clients after revocation = %+v, %v; want none", clients, err)
	}
}

func TestMemStoreAgentClients(t *testing.T) {
	exerciseAgentClients(t, state.NewMemStore())
}

func TestPgStoreAgentClients(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseAgentClients(t, state.NewPgStore(pool))
}
