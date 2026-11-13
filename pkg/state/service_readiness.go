package state

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// DeploymentReadinessSources is shared by traffic routing and service rollout
// admission. Independent ingress probes must all report ready.
func DeploymentReadinessSources(d Deployment) ([]string, error) {
	var sources []string
	if len(d.OverrideReadinessProbe) > 0 {
		var p api.DeploymentReadinessProbe
		if err := json.Unmarshal(d.OverrideReadinessProbe, &p); err != nil {
			return nil, fmt.Errorf("decode readiness: %w", err)
		}
		if p.Path != "" || p.GRPC != nil {
			sources = append(sources, "primary_app")
		}
	}
	var companions []struct {
		Name           string            `json:"name"`
		Type           api.SidecarType   `json:"type"`
		PrimaryIngress bool              `json:"primary_ingress"`
		ReadinessProbe *api.SidecarProbe `json:"readiness_probe"`
	}
	if len(d.Sidecars) > 0 {
		if err := json.Unmarshal(d.Sidecars, &companions); err != nil {
			return nil, fmt.Errorf("decode companions: %w", err)
		}
	}
	for _, c := range companions {
		if c.Type == api.SidecarTypeSidecar && c.PrimaryIngress && c.ReadinessProbe != nil && c.Name != "" {
			sources = append(sources, "sidecar:"+c.Name)
		}
	}
	return sources, nil
}

func serviceReplicaReady(sources []string, signals map[string]InstanceReadiness) bool {
	for _, source := range sources {
		if signal := signals[source]; !signal.Ready || signal.At.IsZero() {
			return false
		}
	}
	return true
}

func serviceReadinessTargets(replicas []Instance) []ReadinessTarget {
	var targets []ReadinessTarget
	for _, i := range replicas {
		if i.Mode == string(InstanceModeService) && State(i.State) == StateRunning {
			targets = append(targets, ReadinessTarget{AppID: i.AppID, InstanceID: i.ID, WakeID: i.WakeID, NodeID: i.NodeID})
		}
	}
	return targets
}

// ReadyServiceReplicas requires current observations for configured probes.
// Missing evidence fails closed; deployments without probes retain the boot gate.
func ReadyServiceReplicas(ctx context.Context, store Store, dep Deployment, replicas []Instance) (map[string]bool, error) {
	sources, err := DeploymentReadinessSources(dep)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool)
	targets := serviceReadinessTargets(replicas)
	if len(sources) == 0 {
		for _, target := range targets {
			out[target.InstanceID] = true
		}
		return out, nil
	}
	reader, ok := store.(InstanceReadinessForTargetsReader)
	if !ok {
		return nil, fmt.Errorf("service readiness reader unavailable")
	}
	for start := 0; start < len(targets); start += api.TrafficReadinessBatchSize {
		end := min(start+api.TrafficReadinessBatchSize, len(targets))
		signals, err := reader.LatestInstanceReadinessForTargets(ctx, targets[start:end])
		if err != nil {
			return nil, err
		}
		for _, target := range targets[start:end] {
			out[target.InstanceID] = serviceReplicaReady(sources, signals[target.InstanceID])
		}
	}
	return out, nil
}

// Caller holds m.mu. Resolve only immutable deployment inputs, never a desired head.
func (m *MemStore) serviceAppForDeploymentLocked(app App, dep Deployment) (App, error) {
	if specID := m.projectEnvironmentWorkloadDeploymentSpecs[dep.ID]; specID != "" {
		spec, ok := m.projectEnvironmentWorkloadSpecs[specID]
		if !ok || spec.AppID != app.ID || spec.EnvironmentSlug != workloadEnvironmentSlug(dep.Scope) {
			return App{}, ErrConflict
		}
		hash, err := WorkloadSettingsHash(spec.Settings)
		if err != nil || hash != spec.Hash {
			return App{}, ErrConflict
		}
		app, err = spec.Settings.ApplyTo(app)
		if err != nil {
			return App{}, err
		}
	}
	return AppForDeploymentRuntime(app, dep)
}

func (m *MemStore) hasServiceDeploymentLocked(app App) bool {
	if app.Manifest.ExecutionMode == api.ExecutionModeService {
		return true
	}
	for _, dep := range m.deployments {
		if dep.AppID != app.ID || dep.Status != DeployLive {
			continue
		}
		deployed, err := m.serviceAppForDeploymentLocked(app, dep)
		if err == nil && deployed.Manifest.ExecutionMode == api.ExecutionModeService {
			return true
		}
	}
	return false
}
