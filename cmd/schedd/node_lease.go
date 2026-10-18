package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/nodelease"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// nodeLeaseEnv turns on the node liveness lease (ADR-1344 §4, P3 phase 1).
// Off by default. Phase 1 only renews the lease and reports its health;
// nothing reads it to recover or fence work yet.
const nodeLeaseEnv = "FAAS_SCHEDD_NODE_LEASE"

// startNodeLeaseKeeper renews this node's lease from the node-local schedd.
// A control-plane or single-box schedd (no owner node) has no lease to keep.
func startNodeLeaseKeeper(ctx context.Context, store state.NodeLeaseStore, ownerNodeID string, ops *wire.OpsMetrics, log *slog.Logger) error {
	raw := strings.TrimSpace(os.Getenv(nodeLeaseEnv))
	if raw == "" {
		return nil
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return fmt.Errorf("schedd: %s=%q is not a boolean: %w", nodeLeaseEnv, raw, err)
	}
	if !enabled {
		return nil
	}
	if ownerNodeID == "" {
		log.Info("schedd: node lease requested but this schedd owns no compute node; not renewing")
		return nil
	}
	keeper, err := nodelease.New(store, ownerNodeID, nodelease.Holder(), log)
	if err != nil {
		return fmt.Errorf("schedd: node lease keeper: %w", err)
	}
	keeper.RegisterMetrics(ops.Registry(), ops.MetricPrefix())
	log.Info("schedd: renewing the node liveness lease (ADR-1344 §4, phase 1: observe only)", "node_id", ownerNodeID)
	go keeper.Run(ctx)
	return nil
}
