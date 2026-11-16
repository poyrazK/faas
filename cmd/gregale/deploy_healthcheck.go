package main

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
)

func resolveDeployHealthcheck(path string, grpc bool, service string, explicit map[string]bool) (*api.DeploymentHealthcheck, error) {
	if explicit["healthcheck-grpc-service"] && !grpc {
		return nil, errors.New("--healthcheck-grpc-service requires --healthcheck-grpc")
	}
	if explicit["healthcheck-path"] && path == "" {
		return nil, errors.New("--healthcheck-path must be a non-empty HTTP path")
	}
	if path == "" && !grpc {
		return nil, nil
	}
	probe := &api.DeploymentHealthcheck{Path: path}
	if grpc {
		probe.GRPC = &api.DeploymentGRPCHealthcheck{Service: service}
	}
	if problem := deployHealthcheckOverrides(probe).Validate(api.MustLimitsFor(api.PlanFree)); problem != nil {
		return nil, &api.APIError{Problem: *problem}
	}
	return probe, nil
}

func deployHealthcheckOverrides(probe *api.DeploymentHealthcheck) *api.CreateDeploymentOverrides {
	if probe == nil {
		return nil
	}
	return &api.CreateDeploymentOverrides{Healthcheck: probe}
}

// resolveDeployReadiness turns --readiness-path into the deployment's
// steady-state readiness probe. gregale.yaml hosting.readiness_probe sets the
// same probe from the source; the flag wins for this one deployment.
func resolveDeployReadiness(path string, explicit map[string]bool) (*api.DeploymentReadinessProbe, error) {
	if explicit["readiness-path"] && path == "" {
		return nil, errors.New("--readiness-path must be a non-empty HTTP path")
	}
	if path == "" {
		return nil, nil
	}
	probe := &api.DeploymentReadinessProbe{Path: path}
	if problem := (&api.CreateDeploymentOverrides{ReadinessProbe: probe}).Validate(api.MustLimitsFor(api.PlanFree)); problem != nil {
		return nil, &api.APIError{Problem: *problem}
	}
	return probe, nil
}

// deployProbeOverrides is the JSON deploy request's probe overrides.
func deployProbeOverrides(startup *api.DeploymentHealthcheck, readiness *api.DeploymentReadinessProbe) *api.CreateDeploymentOverrides {
	overrides := deployHealthcheckOverrides(startup)
	if readiness == nil {
		return overrides
	}
	if overrides == nil {
		overrides = &api.CreateDeploymentOverrides{}
	}
	overrides.ReadinessProbe = readiness
	return overrides
}
