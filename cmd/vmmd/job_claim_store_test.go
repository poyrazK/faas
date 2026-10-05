// ADR-594: legacy single-host Jobs use the durable default-local identity;
// this inference must never bind an unnamed vmmd to a shared-fleet node.
package main

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type unavailableLegacyJobIdentityStore struct{ state.Store }

func (unavailableLegacyJobIdentityStore) ListAllComputeNodes(context.Context) ([]state.ComputeNode, error) {
	return nil, errors.New("database unavailable")
}

func TestLegacyJobClaimNodeID(t *testing.T) {
	for _, scenario := range []string{"single-host", "shared-fleet", "inactive", "renamed", "reader-unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			store := state.NewMemStore()
			local, err := store.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			var reader state.Store = store
			switch scenario {
			case "shared-fleet":
				peer := local
				peer.ID = ""
				peer.Name = "peer"
				if _, err := store.CreateComputeNode(t.Context(), peer); err != nil {
					t.Fatal(err)
				}
			case "inactive":
				if err := store.SetComputeNodeActive(t.Context(), local.ID, false); err != nil {
					t.Fatal(err)
				}
			case "renamed":
				local.Name = "registered-worker"
				if _, err := store.UpsertComputeNodeFromOperator(t.Context(), local); err != nil {
					t.Fatal(err)
				}
			case "reader-unavailable":
				reader = unavailableLegacyJobIdentityStore{store}
			}
			id, err := legacyJobClaimNodeID(t.Context(), reader)
			if scenario == "single-host" {
				if err != nil || id != local.ID {
					t.Fatalf("id=%q err=%v", id, err)
				}
				return
			}
			if err == nil || id != "" {
				t.Fatalf("inferred unsafe legacy identity: id=%q err=%v", id, err)
			}
		})
	}
}
