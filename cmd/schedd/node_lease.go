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

// nodeLeaseEnv turns on the node liveness lease (ADR-1344 §4, P3 phase 1):
// the node-local schedd renews its node's lease and reports its health.
// Off by default.
const nodeLeaseEnv = "FAAS_SCHEDD_NODE_LEASE"

// nodeLeaseGuardEnv turns on the takeover guard (phase 2): the stale-peer
// observer and the dead-node sweep leave a node alone while its lease is
// live. Turn it on only once every node renews its lease. Off by default.
const nodeLeaseGuardEnv = "FAAS_SCHEDD_NODE_LEASE_GUARD"

func envFlag(name string) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return false, nil
	}
	on, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("schedd: %s=%q is not a boolean: %w", name, raw, err)
	}
	return on, nil
}

// startNodeLeaseKeeper renews this node's lease from the node-local schedd.
// A control-plane or single-box schedd (no owner node) has no lease to keep.
func startNodeLeaseKeeper(ctx context.Context, store state.NodeLeaseStore, ownerNodeID string, ops *wire.OpsMetrics, log *slog.Logger) error {
	enabled, err := envFlag(nodeLeaseEnv)
	if err != nil || !enabled {
		return err
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
	log.Info("schedd: renewing the node liveness lease (ADR-1344 §4)", "node_id", ownerNodeID)
	go keeper.Run(ctx)
	return nil
}

// newNodeLeaseGuard returns the takeover guard when it is turned on, or nil.
func newNodeLeaseGuard(store state.NodeLeaseStore, ops *wire.OpsMetrics, log *slog.Logger) (*nodelease.Guard, error) {
	enabled, err := envFlag(nodeLeaseGuardEnv)
	if err != nil || !enabled {
		return nil, err
	}
	guard, err := nodelease.NewGuard(store, log)
	if err != nil {
		return nil, fmt.Errorf("schedd: node lease guard: %w", err)
	}
	guard.RegisterMetrics(ops.Registry(), ops.MetricPrefix())
	log.Info("schedd: node lease takeover guard on (ADR-1344 §4 phase 2)")
	return guard, nil
}
