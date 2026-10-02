// adr: 375
package main

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type targetReadinessStore interface {
	DeploymentReadinessConfigs(context.Context, []string) (map[string]state.DeploymentReadinessConfig, error)
	LatestInstanceReadinessForTargets(context.Context, []state.ReadinessTarget) (map[string]map[string]state.InstanceReadiness, error)
}

func newTargetReadinessLoader(store targetReadinessStore) gateway.TargetReadinessLoader {
	return func(ctx context.Context, targets []gateway.Target) (map[string]gateway.TargetReadinessSnapshot, error) {
		if len(targets) == 0 {
			return map[string]gateway.TargetReadinessSnapshot{}, nil
		}
		if len(targets) > api.TrafficReadinessBatchSize {
			return nil, fmt.Errorf("readiness batch exceeds %d targets", api.TrafficReadinessBatchSize)
		}
		deployments := make([]string, 0, len(targets))
		seen := make(map[string]bool, len(targets))
		for _, target := range targets {
			if target.DeploymentID != "" && !seen[target.DeploymentID] {
				deployments = append(deployments, target.DeploymentID)
				seen[target.DeploymentID] = true
			}
		}
		configs, err := store.DeploymentReadinessConfigs(ctx, deployments)
		if err != nil {
			return nil, fmt.Errorf("load target readiness configuration: %w", err)
		}
		out := make(map[string]gateway.TargetReadinessSnapshot, len(targets))
		instances := make([]state.ReadinessTarget, 0, len(targets))
		for _, target := range targets {
			config, ok := configs[target.DeploymentID]
			if !ok || config.AppID != target.AppID || config.DeploymentID != target.DeploymentID {
				continue
			}
			sources, err := readinessSourcesForDeployment(state.Deployment{OverrideReadinessProbe: config.OverrideReadinessProbe, Sidecars: config.Sidecars})
			if err != nil {
				continue
			}
			out[target.InstanceID] = gateway.TargetReadinessSnapshot{AppID: target.AppID, DeploymentID: target.DeploymentID, InstanceID: target.InstanceID, WakeID: target.WakeID, NodeID: target.NodeID,
				RequiredSources: sources, States: make(map[string]gateway.ReadinessState)}
			if len(sources) > 0 {
				instances = append(instances, state.ReadinessTarget{AppID: target.AppID, InstanceID: target.InstanceID, WakeID: target.WakeID, NodeID: target.NodeID})
			}
		}
		states, err := store.LatestInstanceReadinessForTargets(ctx, instances)
		if err != nil {
			return nil, fmt.Errorf("load target readiness observations: %w", err)
		}
		for instance, snapshot := range out {
			for _, source := range snapshot.RequiredSources {
				if current, ok := states[instance][source]; ok {
					snapshot.States[source] = gateway.ReadinessState{Ready: current.Ready, UpdatedAt: current.At, EventID: current.EventID}
				}
			}
		}
		return out, nil
	}
}
