package sched

import (
	"encoding/json"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
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

// healthcheckPathFromDep resolves the readiness endpoint for every boot path.
// The explicit customer override is authoritative. Existing source
// deployments derive the value at wake time from the durable hosting receipt
// or inference profile, so they gain HTTP readiness without a redeploy.
func healthcheckPathFromDep(dep state.Deployment) string {
	if len(dep.OverrideHealthcheck) > 0 {
		var hc api.DeploymentHealthcheck
		if err := json.Unmarshal(dep.OverrideHealthcheck, &hc); err == nil && validRuntimeHealthPath(hc.Path) {
			return hc.Path
		}
	}
	if len(dep.APIHostingReceipt) > 0 {
		var receipt struct {
			Profile frameworkprofile.Profile `json:"profile"`
		}
		if err := json.Unmarshal(dep.APIHostingReceipt, &receipt); err == nil && validRuntimeHealthPath(receipt.Profile.HealthPath) {
			return receipt.Profile.HealthPath
		}
	}
	if len(dep.InferredProfile) > 0 {
		var profile frameworkprofile.Profile
		if err := json.Unmarshal(dep.InferredProfile, &profile); err == nil &&
			profile.Version == frameworkprofile.Version && validRuntimeHealthPath(profile.HealthPath) {
			return profile.HealthPath
		}
	}
	return ""
}

func validRuntimeHealthPath(path string) bool {
	return strings.HasPrefix(path, "/") && !strings.ContainsAny(path, "\r\n")
}

// DeploymentRuntimePort exposes the scheduler's canonical runtime-port
// resolution to gateway restart reconciliation. Both paths must rebuild the
// same Target or a custom-port app becomes unreachable after gatewayd restarts.
func DeploymentRuntimePort(dep state.Deployment) int {
	if dep.OverridePort != 0 {
		return dep.OverridePort
	}
	// Function layers always use the Gregale runner manifest, which listens on
	// DefaultAppPort. Framework inference describes the uploaded source and can
	// still report a conventional framework port (for example Node's 3000), but
	// imaged deliberately does not stamp that port into a function manifest.
	// Handler is the durable function discriminator carried on the deployment.
	if dep.Handler != "" {
		return api.DefaultAppPort
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
