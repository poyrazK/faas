package apidserver

import (
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Source requests carry only the startup probe; the existing typed override
// validator and persistence contract remain authoritative (ADR-053/057).
func sourceHealthcheckOverrides(probe *api.DeploymentHealthcheck) *api.CreateDeploymentOverrides {
	if probe == nil {
		return nil
	}
	return &api.CreateDeploymentOverrides{Healthcheck: probe}
}

// projectReadinessOverrides are the deployment startup and liveness probes
// a project deploy stores for its app: the HTTP or gRPC readiness check the
// scan read from a Kubernetes manifest (ADR-20261029093621 §3), checked both
// before the instance takes traffic and after it is ready, as an image
// healthcheck is (ADR-683/684). The liveness probe also replaces the plan
// default /healthz one, which would otherwise restart an app that serves its
// health elsewhere. On a plan without liveness overrides both stay nil, and
// readiness stays the app's TCP listener.
func projectReadinessOverrides(manifest state.AppManifest, plan api.Plan) (readiness, liveness json.RawMessage) {
	check := manifest.ProjectReadinessCheck
	if check == nil || !plan.LivenessAllowed() || (check.GRPC != nil && !plan.GRPCLivenessAllowed()) {
		return nil, nil
	}
	probe := api.DeploymentLivenessProbe{Path: check.Path}
	if check.GRPC != nil {
		probe = api.DeploymentLivenessProbe{GRPC: &api.DeploymentGRPCLivenessProbe{Service: check.GRPC.Service}}
	}
	readiness, err := json.Marshal(check)
	if err != nil {
		return nil, nil
	}
	liveness, err = json.Marshal(probe)
	if err != nil {
		return nil, nil
	}
	return readiness, liveness
}
