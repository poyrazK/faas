package main

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

// physicalNodeID names the compute node this vmmd runs as. A named node uses
// its registered ID. A single-box vmmd registers no name and runs as the
// migration-seeded default-local node: runtime admission (ADR-435/595),
// durable failure reports (ADR-471) and native qualification all identify it
// by that row. Without a store there is no identity to publish.
//
// registeredNodeID stays the gate for multi-node loops (capacity publication,
// snapshot fan-out); only the identity falls back to default-local. Before
// this, a single-box vmmd with a database could not start: its runtime
// admission identity had no node ID and failed validation.
func physicalNodeID(ctx context.Context, store state.Store, registeredNodeID string) (string, error) {
	if registeredNodeID != "" || store == nil {
		return registeredNodeID, nil
	}
	local, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		return "", fmt.Errorf("vmmd: resolve the %s compute node: %w", state.DefaultLocalNodeName, err)
	}
	if local.ID == "" {
		return "", fmt.Errorf("vmmd: the %s compute node has no id", state.DefaultLocalNodeName)
	}
	return local.ID, nil
}
