package main

import "github.com/onebox-faas/faas/pkg/state"

// Validate the required frozen configuration receipts before any provider IO.
// This authenticates the implemented catalogue, not full schema coverage or a
// cross-resource data checkpoint; those still gate complete public admission.
func validateCloneCoordinatorInventory(op state.ProjectEnvironmentCloneOperation, capture state.ProjectEnvironmentCloneConfigurationCapture, views []state.ProjectEnvironmentCloneWorkload) error {
	if capture.OperationID != op.ID || capture.Version != 1 || capture.Hash != op.SourceRevisionHash || capture.WorkloadCount != len(views) || len(views) == 0 || op.ErrorCode != "" {
		return state.ErrConflict
	}
	byName := make(map[string]state.ProjectEnvironmentCloneWorkload, len(views))
	apps := map[string]bool{}
	for _, view := range views {
		if view.OperationID != op.ID || view.AppID == "" || view.WorkloadSlug == "" || view.SourceHash == "" || view.SourceValuesHash == "" ||
			view.SourceSettingsHash == "" || view.SourcePoliciesHash == "" || apps[view.AppID] || byName[view.WorkloadSlug].AppID != "" {
			return state.ErrConflict
		}
		apps[view.AppID], byName[view.WorkloadSlug] = true, view
	}
	seen := map[string]bool{}
	for _, resource := range op.Resources {
		key := resource.Kind + "\x00" + resource.Name
		if seen[key] {
			return state.ErrConflict
		}
		seen[key] = true
		if resource.Status != "captured" && resource.Status != "copying" && resource.Status != "verifying" && resource.Status != "ready" {
			return state.ErrConflict
		}
		view, found := byName[resource.Name]
		switch resource.Kind {
		case "source_revision":
			if resource.Name != op.SourceEnvironment || resource.SourceVersion != capture.Hash || resource.Status != "ready" {
				return state.ErrConflict
			}
		case "project_config":
			if resource.Name != op.SourceEnvironment || resource.SourceVersion != views[0].SourceProjectConfigHash {
				return state.ErrConflict
			}
		case "workload":
			if !found || resource.SourceID != view.SourceDeploymentID || resource.SourceVersion != view.SourceHash ||
				resource.TargetID != "" && resource.TargetID != view.TargetDeploymentID {
				return state.ErrConflict
			}
		case "variables", "secrets":
			if !found || resource.SourceID != view.AppID || resource.TargetID != view.AppID || resource.SourceVersion != view.SourceValuesHash {
				return state.ErrConflict
			}
		case "workload_settings":
			if !found || resource.SourceVersion != view.SourceSettingsHash {
				return state.ErrConflict
			}
		case "route_policy", "edge_policy":
			if !found || resource.SourceVersion != view.SourcePoliciesHash {
				return state.ErrConflict
			}
		case "managed_postgres", "object_storage":
			// The resource-specific workers validate the complete catalogue,
			// common capture point, private reservations and actual copy proofs.
		default:
			return state.ErrConflict
		}
	}
	if !seen["source_revision\x00"+op.SourceEnvironment] || !seen["project_config\x00"+op.SourceEnvironment] {
		return state.ErrConflict
	}
	for _, view := range views {
		if view.SourceProjectConfigHash != views[0].SourceProjectConfigHash {
			return state.ErrConflict
		}
		for _, kind := range []string{"workload", "variables", "secrets"} {
			if !seen[kind+"\x00"+view.WorkloadSlug] {
				return state.ErrConflict
			}
		}
	}
	return nil
}
