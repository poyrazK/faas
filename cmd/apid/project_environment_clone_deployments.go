package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// prepareProjectEnvironmentCloneDeployments is the resumable copying-phase
// step. The state store atomically attaches each cold-bootable, dark deployment
// to its capture; the durable notification outbox hands readiness to schedd.
// A retry reuses that deployment and may safely repeat its prime notification.
func (s *server) prepareProjectEnvironmentCloneDeployments(ctx context.Context, op state.ProjectEnvironmentCloneOperation) ([]state.ProjectEnvironmentCloneResource, bool, error) {
	store, ok := s.store.(state.ProjectEnvironmentCloneWorkloadStore)
	settings, settingsOK := s.store.(state.ProjectEnvironmentWorkloadSpecReader)
	if !ok || !settingsOK || s.notif == nil {
		return nil, false, errors.New("clone deployment preparation is unavailable")
	}
	if op.Status != state.CloneOperationCopying {
		return nil, false, state.ErrConflict
	}
	views, err := store.ProjectEnvironmentCloneWorkloads(ctx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return nil, false, err
	}
	indices := map[string]int{}
	resources := append([]state.ProjectEnvironmentCloneResource(nil), op.Resources...)
	for i, r := range resources {
		if r.Kind != "workload" {
			continue
		}
		if _, duplicate := indices[r.Name]; duplicate {
			return nil, false, state.ErrConflict
		}
		indices[r.Name] = i
	}
	if len(views) == 0 || len(views) != len(indices) {
		return nil, false, state.ErrConflict
	}
	for _, view := range views {
		i, ok := indices[view.WorkloadSlug]
		if !ok {
			return nil, false, state.ErrConflict
		}
		r := resources[i]
		if r.SourceID != view.SourceDeploymentID || r.SourceVersion != view.SourceHash || (r.TargetID != "" && r.TargetID != view.TargetDeploymentID) {
			return nil, false, state.ErrConflict
		}
	}
	ready := true
	for _, view := range views {
		spec, err := settings.ProjectEnvironmentWorkloadSpec(ctx, op.AccountID, op.ProjectID, op.TargetEnvironment, view.AppID)
		if err != nil {
			return nil, false, err
		}
		if view.TargetSettingsHash != "" && view.TargetSettingsHash != spec.Hash {
			return nil, false, state.ErrConflict
		}
		d, err := store.CreateDeploymentForEnvironmentClone(ctx, op.AccountID, op.ProjectID, op.ID, op.Revision, view.AppID, spec.Hash)
		if err != nil {
			return nil, false, fmt.Errorf("prepare clone workload %q: %w", view.WorkloadSlug, err)
		}
		if s.durableEntityValidatorReleaseGateEnabled && s.durableEntityApps[view.AppID] {
			if s.durableEntityValidatorArtifacts == nil {
				return nil, false, errors.New("shared validator artifacts are required for cloning")
			}
			if err := s.durableEntityValidatorArtifacts.Transfer(ctx, view.AppID, view.SourceDeploymentID, d.ID); err != nil {
				return nil, false, err
			}
		}

		i := indices[view.WorkloadSlug]
		resources[i].TargetID = d.ID
		if d.Status == state.DeployLive {
			resources[i].Status = "ready"
			continue
		}
		if d.Status != state.DeployPending {
			return nil, false, state.ErrConflict
		}
		ready, resources[i].Status = false, "verifying"
		payload, err := json.Marshal(map[string]string{"app_id": d.AppID, "deployment_id": d.ID})
		if err != nil {
			return nil, false, err
		}
		if err := s.notif.Notify(ctx, db.NotifySnapshotPrime, string(payload)); err != nil {
			return nil, false, fmt.Errorf("queue clone workload %q readiness: %w", view.WorkloadSlug, err)
		}
	}
	return resources, ready, nil
}
