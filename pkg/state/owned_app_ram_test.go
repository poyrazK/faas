// adr: 20261026170000 — ownership load is the RAM of each node's live apps.
package state_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func checkOwnedAppRAM(t *testing.T, s state.Store) {
	t.Helper()
	reader, ok := s.(state.AppOwnershipLoadReader)
	if !ok {
		t.Fatalf("%T does not report ownership load", s)
	}
	a := completionNode(t, s, state.NodeLifecycleActive)
	b := completionNode(t, s, state.NodeLifecycleActive)
	account, err := s.CreateAccount(t.Context(), uuid.NewString()+"@owned.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range []state.App{
		{NodeID: a.ID, RAMMB: 512}, {NodeID: a.ID, RAMMB: 256}, {NodeID: b.ID, RAMMB: 1024}, {RAMMB: 1000},
	} {
		app.AccountID, app.Slug, app.MaxConcurrency = account.ID, "owned-"+uuid.NewString()[:8], 1
		if _, err := s.CreateApp(t.Context(), app); err != nil {
			t.Fatal(err)
		}
	}
	owned, err := reader.OwnedAppRAMByNode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if owned[a.ID] != 768 || owned[b.ID] != 1024 {
		t.Fatalf("owned app RAM = a %d, b %d; want 768 and 1024", owned[a.ID], owned[b.ID])
	}
}

func TestOwnedAppRAMMemory(t *testing.T) {
	checkOwnedAppRAM(t, state.NewMemStore())
}
