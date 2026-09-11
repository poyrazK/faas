package sched

import (
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/state"
)

// deploymentRuntimePort resolves the port used by the guest, readiness DNAT,
// and request bridge. Explicit deployment overrides remain authoritative.
// Source builds otherwise use the persisted inference profile that imaged also
// applies to app.json. Returning zero preserves the platform's legacy 8080
// default for container deployments and older rows.
func deploymentRuntimePort(dep state.Deployment) int {
	return DeploymentRuntimePort(dep)
}

// DeploymentRuntimePort exposes the scheduler's canonical runtime-port
// resolution to gateway restart reconciliation. Both paths must rebuild the
// same Target or a custom-port app becomes unreachable after gatewayd restarts.
func DeploymentRuntimePort(dep state.Deployment) int {
	if dep.OverridePort != 0 {
		return dep.OverridePort
	}
	if len(dep.InferredProfile) == 0 {
		return 0
	}
	var profile frameworkprofile.Profile
	if err := json.Unmarshal(dep.InferredProfile, &profile); err != nil ||
		profile.Version != frameworkprofile.Version || profile.Port < 1 || profile.Port > 65535 {
		return 0
	}
	return profile.Port
}
