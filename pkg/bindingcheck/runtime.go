package bindingcheck

import (
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

func (r *Report) checkRuntime(runtime *api.BindingRuntimeFreshness) {
	if runtime == nil || runtime.Source != "instance_started_at" || runtime.ObservedAt.IsZero() || runtime.ObservedAt.After(r.CheckedAt) {
		r.block("runtime_unknown", "Runtime freshness observations are missing or unknown.")
		return
	}
	selectedDeployment := false
	seen := make(map[string]bool)
	for _, deployment := range runtime.Deployments {
		if deployment.Scope != r.Scope {
			continue
		}
		r.Runtime = append(r.Runtime, deployment)
		selectedDeployment = selectedDeployment || deployment.DeploymentID == r.DeploymentID
		if deployment.DeploymentID == "" || seen[deployment.DeploymentID] || deployment.DeploymentID == r.DeploymentID && deployment.DeploymentStatus != "live" {
			r.runtimeBlock(deployment, "runtime_unknown", "Runtime deployment identity is missing or contradictory.")
		}
		seen[deployment.DeploymentID] = true
		if !validRuntimeCounts(deployment) {
			r.runtimeBlock(deployment, "runtime_unknown", "Runtime freshness counts are incomplete or contradictory.")
			continue
		}
		if deployment.Resident.Stale > 0 {
			r.runtimeBlock(deployment, "runtime_stale", "Resident instances predate the configuration change; complete their refresh before checking again.")
		}
		if deployment.Resident.Unknown > 0 || deployment.Resident.Current > 0 && (runtime.ConfigChangedAt == nil || runtime.ConfigChangedAt.IsZero() || runtime.ConfigChangedAt.After(runtime.ObservedAt)) {
			r.runtimeBlock(deployment, "runtime_unknown", "Resident configuration freshness is unknown.")
		}
		if deployment.Starting > 0 {
			r.runtimeBlock(deployment, "runtime_updating", "Replacement instances are still starting; wait for the refresh to settle.")
		}
		if deployment.Resident == (api.BindingRuntimeInstanceCounts{}) {
			r.Warnings = append(r.Warnings, Finding{Code: "runtime_inactive", Scope: deployment.Scope, DeploymentID: deployment.DeploymentID,
				Message: "No resident instances were observed; runtime adoption cannot be assessed for this deployment."})
		}
	}
	sort.Slice(r.Runtime, func(i, j int) bool { return r.Runtime[i].DeploymentID < r.Runtime[j].DeploymentID })
	if !selectedDeployment && r.DeploymentID != "" {
		r.block("runtime_unknown", "The selected live deployment is absent from runtime freshness observations.")
	}
}

func (r *Report) runtimeBlock(deployment api.BindingRuntimeDeployment, code, message string) {
	r.Blockers = append(r.Blockers, Finding{Code: code, Scope: deployment.Scope, DeploymentID: deployment.DeploymentID, Message: message})
}

func validRuntimeCounts(d api.BindingRuntimeDeployment) bool {
	s, r := d.Serving, d.Resident
	if s.Current < 0 || s.Stale < 0 || s.Unknown < 0 || r.Current < 0 || r.Stale < 0 || r.Unknown < 0 || d.Starting < 0 ||
		s.Current > r.Current || s.Stale > r.Stale || s.Unknown > r.Unknown || s.Current+s.Stale+s.Unknown+d.Starting > r.Current+r.Stale+r.Unknown {
		return false
	}
	status := "inactive"
	switch {
	case r.Stale > 0:
		status = "stale"
	case r.Unknown > 0:
		status = "unknown"
	case d.Starting > 0:
		status = "updating"
	case r.Current > 0:
		status = "current"
	}
	return d.Status == status
}
