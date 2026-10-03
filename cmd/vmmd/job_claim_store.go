package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state"
)

// Legacy Jobs still have durable claims even though the single-box vmmd does
// not self-register. Never infer this identity in a shared compute fleet.
func legacyJobClaimNodeID(ctx context.Context, store state.Store) (string, error) {
	nodes, err := store.ListAllComputeNodes(ctx)
	if err != nil {
		return "", fmt.Errorf("read legacy job compute identity: %w", err)
	}
	if len(nodes) != 1 || nodes[0].ID == "" || nodes[0].Name != state.DefaultLocalNodeName || !nodes[0].Active {
		return "", errors.New("legacy job execution requires a sole active default-local compute node; configure self-registration for shared fleets")
	}
	return nodes[0].ID, nil
}

func loadLegacyJobClaimStore(ctx context.Context, deps runDeps, dbURL string) (*pgxpool.Pool, *state.PgStore, string, error) {
	pool, err := deps.openDB(ctx, dbURL)
	if err != nil {
		return nil, nil, "", fmt.Errorf("open legacy job claim store: %w", err)
	}
	store := deps.openStore(pool)
	nodeID, err := legacyJobClaimNodeID(ctx, store)
	if err != nil {
		pool.Close()
		return nil, nil, "", err
	}
	return pool, store, nodeID, nil
}
