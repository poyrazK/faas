package main

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/gatewayconfirmation"
	"github.com/onebox-faas/faas/pkg/state"
)

func privateRuntimeDrainEnabled(getenv func(string) string) (bool, error) {
	enabled := getenv("FAAS_RUNTIME_UPGRADE_DRAIN_CONFIRMATION") == "1"
	if enabled && getenv("FAAS_RUNTIME_UPGRADE_ROUTING_CONFIRMATION") != "1" {
		return false, fmt.Errorf("private runtime drain confirmation requires routing confirmation")
	}
	return enabled, nil
}

func (a weightsStoreAdapter) DeploymentWeightsSnapshot(ctx context.Context, appID string) (gateway.DeploymentWeightsSnapshot, error) {
	if !a.drainEnabled {
		rows, err := a.LiveDeployments(ctx, appID)
		return gateway.DeploymentWeightsSnapshot{Rows: rows}, err
	}
	store, ok := a.store.(state.RuntimeUpgradeGatewayDrainStore)
	if !ok {
		return gateway.DeploymentWeightsSnapshot{}, state.ErrInvalidArgument
	}
	snapshot, err := store.RuntimeUpgradeGatewayDrainSnapshot(ctx, appID)
	if err != nil {
		return gateway.DeploymentWeightsSnapshot{}, err
	}
	out := gateway.DeploymentWeightsSnapshot{RoutingRevision: snapshot.RoutingRevision}
	for _, d := range snapshot.Deployments {
		out.Rows = append(out.Rows, gateway.DeploymentWeightsRow{ID: d.ID, TrafficPercent: d.TrafficPercent})
	}
	if p := snapshot.Plan; p != nil {
		out.Drain = &gateway.RuntimeUpgradeDrainPlan{OperationID: p.OperationID, DeploymentID: p.DeploymentID, ServingDeploymentID: p.ServingDeploymentID, GatewayRosterRevision: p.GatewayRosterRevision, CutoverAt: p.CutoverAt}
	}
	return out, nil
}

func (a weightsStoreAdapter) DeploymentWeightsSnapshotInstalled(ctx context.Context, appID string, snapshot gateway.DeploymentWeightsSnapshot) error {
	if a.drainEnabled {
		store, ok := a.store.(state.RuntimeUpgradeGatewayDrainStore)
		if !ok {
			return state.ErrInvalidArgument
		}
		if err := gatewayconfirmation.RecordDrain(ctx, store, a.drainTracker, a.slotID, appID, snapshot); err != nil {
			return err
		}
	}
	return a.DeploymentWeightsInstalled(ctx, appID, snapshot.Rows)
}
