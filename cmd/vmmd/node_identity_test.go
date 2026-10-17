package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// A single-box vmmd with a database runs as the default-local node. Its
// runtime admission identity needs that node's ID; an empty one failed
// validation and stopped vmmd at startup.
func TestPhysicalNodeID(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	local, err := mem.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil || local.ID == "" {
		t.Fatalf("MemStore default-local node = %+v err=%v", local, err)
	}
	for _, tc := range []struct {
		name       string
		store      state.Store
		registered string
		want       string
	}{
		{"a named node keeps its registered id", mem, "node-a", "node-a"},
		{"a single box runs as default-local", mem, "", local.ID},
		{"no store, no identity", nil, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := physicalNodeID(ctx, tc.store, tc.registered)
			if err != nil || got != tc.want {
				t.Fatalf("physicalNodeID = %q err=%v, want %q", got, err, tc.want)
			}
		})
	}
}
