package ingressroute

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ErrNoReadyInstance refuses a warm cohort without waking a replacement for
// residents that still consume scheduler capacity.
var ErrNoReadyInstance = errors.New("raw ingress has no ready instance")

type readinessSource interface {
	LatestInstanceReadinessForTargets(context.Context, []state.ReadinessTarget) (map[string]map[string]state.InstanceReadiness, error)
}

// ReadyInstances checks deployment-local probe configuration and the current
// app/instance/wake/node observations. A disabled probe needs no observations;
// configured probes fail closed when their reader or evidence is unavailable.
func ReadyInstances(ctx context.Context, source DeploymentSource, app, deployment string, targets []state.Instance) ([]state.Instance, error) {
	readCtx, cancel := context.WithTimeout(ctx, api.TrafficReadinessReadTimeout)
	defer cancel()
	rows, err := deploymentRows(readCtx, source, app)
	if err != nil {
		return nil, fmt.Errorf("read raw ingress readiness configuration: %w", err)
	}
	if err := readCtx.Err(); err != nil {
		return nil, err
	}
	var config *state.Deployment
	for _, row := range productionRows(rows, app) {
		if row.ID == deployment {
			copy := row
			config = &copy
			break
		}
	}
	if config == nil {
		return nil, errors.New("raw ingress readiness deployment is no longer live in production")
	}
	required, err := readinessSources(*config)
	if err != nil {
		return nil, err
	}
	if len(required) == 0 {
		return targets, nil
	}
	if len(targets) > api.TrafficReadinessBatchSize {
		return nil, fmt.Errorf("raw ingress readiness batch exceeds %d targets", api.TrafficReadinessBatchSize)
	}
	reader, ok := source.(readinessSource)
	if !ok {
		return nil, errors.New("raw ingress readiness reader is unavailable")
	}
	identities := make([]state.ReadinessTarget, 0, len(targets))
	for _, target := range targets {
		if target.AppID != app || target.DeploymentID != deployment || target.ID == "" || target.NodeID == "" {
			return nil, errors.New("raw ingress readiness target identity changed")
		}
		identities = append(identities, state.ReadinessTarget{AppID: app, InstanceID: target.ID, WakeID: target.WakeID, NodeID: target.NodeID})
	}
	observations, err := reader.LatestInstanceReadinessForTargets(readCtx, identities)
	if err != nil {
		return nil, fmt.Errorf("read raw ingress readiness observations: %w", err)
	}
	if err := readCtx.Err(); err != nil {
		return nil, err
	}
	ready := make([]state.Instance, 0, len(targets))
	for _, target := range targets {
		allowed := true
		for _, name := range required {
			observation, ok := observations[target.ID][name]
			if !ok || !observation.Ready || observation.At.IsZero() {
				allowed = false
				break
			}
		}
		if allowed {
			ready = append(ready, target)
		}
	}
	return ready, nil
}

// Follow the primary-app and primary-ingress companion contract used by the
// HTTP target readiness loader. Auxiliary sidecars do not gate customer traffic.
func readinessSources(deployment state.Deployment) ([]string, error) {
	var required []string
	if len(deployment.OverrideReadinessProbe) > 0 {
		var probe api.DeploymentReadinessProbe
		if err := json.Unmarshal(deployment.OverrideReadinessProbe, &probe); err != nil {
			return nil, fmt.Errorf("decode raw ingress primary readiness: %w", err)
		}
		if probe.Path != "" || probe.GRPC != nil {
			required = append(required, "primary_app")
		}
	}
	var companions []struct {
		Name           string            `json:"name"`
		Type           api.SidecarType   `json:"type"`
		PrimaryIngress bool              `json:"primary_ingress"`
		ReadinessProbe *api.SidecarProbe `json:"readiness_probe"`
	}
	if len(deployment.Sidecars) > 0 {
		if err := json.Unmarshal(deployment.Sidecars, &companions); err != nil {
			return nil, fmt.Errorf("decode raw ingress companion readiness: %w", err)
		}
	}
	for _, companion := range companions {
		if companion.Type == api.SidecarTypeSidecar && companion.PrimaryIngress && companion.ReadinessProbe != nil && companion.Name != "" {
			required = append(required, "sidecar:"+companion.Name)
		}
	}
	return required, nil
}
