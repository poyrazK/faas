package main

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

func registerRuntimeAdmissionIdentity(ctx context.Context, store state.Store, identity runtimeadmission.Identity) error {
	registrar, ok := store.(state.ComputeNodeRuntimeIdentityStore)
	if !ok {
		return fmt.Errorf("vmmd: native process registrar unavailable")
	}
	if err := registrar.RegisterComputeNodeRuntimeIdentity(ctx, identity); err != nil {
		return fmt.Errorf("vmmd: register native process: %w", err)
	}
	return nil
}

// runtimeAdmissionNodeID is the node whose native identity this vmmd
// publishes. A named node registered itself; the single-box path (no node
// name, with a store) owns the default-local row, as failure reports do.
// Without it the identity is invalid and vmmd refuses to start.
func runtimeAdmissionNodeID(ctx context.Context, store state.Store, nodeID string) (string, error) {
	if nodeID != "" || store == nil {
		return nodeID, nil
	}
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		return "", fmt.Errorf("vmmd: resolve local native process node: %w", err)
	}
	return node.ID, nil
}
