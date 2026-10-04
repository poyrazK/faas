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
