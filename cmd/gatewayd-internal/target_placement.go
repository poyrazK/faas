// adr: 570
package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/gateway"
	schedpkg "github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type targetPlacementStore interface {
	RunningTrafficPlacements(context.Context, []string) (map[string]state.TrafficPlacementSnapshot, error)
}

func newTargetPlacementLoader(store targetPlacementStore) gateway.TargetPlacementLoader {
	return func(ctx context.Context, apps []string) (map[string]gateway.TargetPlacementSnapshot, error) {
		rows, err := store.RunningTrafficPlacements(ctx, apps)
		if err != nil {
			return nil, err
		}
		out := make(map[string]gateway.TargetPlacementSnapshot, len(rows))
		for app, row := range rows {
			snapshot := gateway.TargetPlacementSnapshot{AppID: row.AppID, Complete: row.Complete}
			for _, placement := range row.Targets {
				snapshot.Targets = append(snapshot.Targets, gateway.TargetPlacement{DeploymentLive: placement.DeploymentLive,
					Target: trafficPlacementTarget(placement)})
			}
			out[app] = snapshot
		}
		return out, nil
	}
}

func trafficPlacementTarget(placement state.TrafficPlacement) gateway.Target {
	dep := state.Deployment{OverridePort: placement.OverridePort, InferredProfile: placement.InferredProfile}
	if placement.FunctionHandler {
		dep.Handler = "function"
	}
	return gateway.Target{AppID: placement.AppID, InstanceID: placement.InstanceID, DeploymentID: placement.DeploymentID,
		NodeID: placement.NodeID, WakeID: placement.WakeID, Port: schedpkg.DeploymentRuntimePort(dep),
		Region: placement.Region, CommitSHA: placement.CommitSHA, DeploymentTag: placement.DeploymentTag,
		DeploymentCreatedAt: placement.DeploymentCreatedAt, ImageDigest: placement.ImageDigest}
}
