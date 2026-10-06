package main

import (
	"context"
	"sort"

	"github.com/onebox-faas/faas/pkg/state"
)

// Captures without PostgreSQL or bucket data use the typed state capture as
// their immutable source. Captures with those datasets must first secure a
// coordinated checkpoint and are deferred before any mutation.
func captureProjectEnvironmentClone(ctx context.Context, store projectEnvironmentCloneCoordinatorStore, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, error) {
	op := lease.Operation
	if op.Status != state.CloneOperationPending && op.Status != state.CloneOperationCapturing {
		return lease, state.ErrConflict
	}
	capture, err := store.ProjectEnvironmentCloneConfigurationForLease(ctx, lease)
	if err != nil {
		return lease, err
	}
	views, err := store.ProjectEnvironmentCloneWorkloads(ctx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return lease, err
	}
	catalogue, err := store.ProjectEnvironmentCloneBindings(ctx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return lease, err
	}
	databases, err := buildCapturedProjectEnvironmentDatabasePlans(op, views, catalogue)
	if err != nil {
		return lease, err
	}
	objects, err := buildCapturedProjectEnvironmentObjectPlans(op, views, catalogue)
	if err != nil {
		return lease, err
	}
	if len(databases) != 0 || len(objects) != 0 {
		return lease, errCloneCheckpointUnavailable
	}
	resources, err := capturedCloneConfigurationResources(op, capture, views)
	if err != nil {
		return lease, err
	}
	if len(op.Resources) != 0 && !cloneCoordinatorResourcesEqual(op.Resources, resources) {
		return lease, state.ErrConflict
	}
	if op.Status == state.CloneOperationPending {
		updated, err := store.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCapturing, op.Revision, resources, "")
		if err != nil {
			return lease, err
		}
		lease.Operation, op = updated, updated
	}
	updated, err := store.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, resources, "")
	if err == nil {
		lease.Operation = updated
	}
	return lease, err
}

func capturedCloneConfigurationResources(op state.ProjectEnvironmentCloneOperation, capture state.ProjectEnvironmentCloneConfigurationCapture, views []state.ProjectEnvironmentCloneWorkload) ([]state.ProjectEnvironmentCloneResource, error) {
	if len(views) == 0 {
		return nil, state.ErrConflict
	}
	// Sort a private slice so capture replay is stable across reader ordering,
	// without modifying the caller's authenticated workload catalogue.
	views = append([]state.ProjectEnvironmentCloneWorkload(nil), views...)
	sort.Slice(views, func(i, j int) bool { return views[i].WorkloadSlug < views[j].WorkloadSlug })
	resources := []state.ProjectEnvironmentCloneResource{
		{Kind: "source_revision", Name: op.SourceEnvironment, SourceVersion: capture.Hash, Status: "ready"},
		{Kind: "project_config", Name: op.SourceEnvironment, SourceVersion: views[0].SourceProjectConfigHash, Status: "captured"},
	}
	for _, view := range views {
		if view.TargetDeploymentID != "" || view.SourceDeploymentID == "" || view.SourceProjectConfigHash == "" || view.SourceBindingsHash == "" {
			return nil, state.ErrConflict
		}
		resources = append(resources, state.ProjectEnvironmentCloneResource{Kind: "workload", Name: view.WorkloadSlug,
			SourceID: view.SourceDeploymentID, SourceVersion: view.SourceHash, Status: "captured"})
		for _, kind := range []string{"variables", "secrets"} {
			resources = append(resources, state.ProjectEnvironmentCloneResource{Kind: kind, Name: view.WorkloadSlug,
				SourceID: view.AppID, TargetID: view.AppID, SourceVersion: view.SourceValuesHash, Status: "captured"})
		}
		resources = append(resources,
			state.ProjectEnvironmentCloneResource{Kind: "workload_settings", Name: view.WorkloadSlug, SourceVersion: view.SourceSettingsHash, Status: "captured"},
			state.ProjectEnvironmentCloneResource{Kind: "route_policy", Name: view.WorkloadSlug, SourceVersion: view.SourcePoliciesHash, Status: "captured"},
			state.ProjectEnvironmentCloneResource{Kind: "edge_policy", Name: view.WorkloadSlug, SourceVersion: view.SourcePoliciesHash, Status: "captured"})
	}
	op.Resources = resources
	if err := validateCloneCoordinatorInventory(op, capture, views); err != nil {
		return nil, err
	}
	return resources, nil
}
