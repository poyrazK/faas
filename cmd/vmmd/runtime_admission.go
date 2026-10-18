package main

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type computeNodeByName interface {
	ComputeNodeByName(ctx context.Context, name string) (state.ComputeNode, error)
}

// localRuntimeNodeID names the compute node that owns this daemon's runtime
// admission identity (ADR-435) and its failure reports (ADR-471). A named node
// registered itself and keeps that ID. A single-box daemon with a store uses
// the default-local row seeded by migration 00024. Without a store it returns
// "" and the daemon stays on the DB-less legacy path, which publishes no
// identity.
func localRuntimeNodeID(ctx context.Context, store computeNodeByName, nodeID string) (string, error) {
	if nodeID != "" || store == nil {
		return nodeID, nil
	}
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		return "", fmt.Errorf("vmmd: resolve default-local node: %w", err)
	}
	return node.ID, nil
}

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
