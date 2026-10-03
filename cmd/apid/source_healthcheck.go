package main

import "github.com/onebox-faas/faas/pkg/api"

// Source requests carry only the startup probe; the existing typed override
// validator and persistence contract remain authoritative (ADR-053/057).
func sourceHealthcheckOverrides(probe *api.DeploymentHealthcheck) *api.CreateDeploymentOverrides {
	if probe == nil {
		return nil
	}
	return &api.CreateDeploymentOverrides{Healthcheck: probe}
}
