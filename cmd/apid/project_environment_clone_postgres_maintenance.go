package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// The caller has already reserved the operation-owned source hold. Every
// remote phase consumes the same private registry owner and frozen plan.
// SQL bootstrap retries serialize on the source session advisory lock, so an
// uncertain reply can recover the same role/database without a new identity.
func (s *server) prepareProjectEnvironmentClonePostgresMaintenance(ctx context.Context, lease state.ProjectEnvironmentCloneLease, plan capturedProjectEnvironmentDatabasePlan) (state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentClonePostgresMaintenance, error) {
	if lease.Operation.Status != state.CloneOperationCapturing && lease.Operation.Status != state.CloneOperationCompensating {
		return lease, state.ProjectEnvironmentClonePostgresMaintenance{}, state.ErrConflict
	}
	store, ok := s.store.(state.ProjectEnvironmentClonePostgresMaintenanceStore)
	leases, leasesOK := s.store.(state.ProjectEnvironmentCloneWorkerLeaseStore)
	if !ok || !leasesOK || s.managedPostgres == nil {
		return lease, state.ProjectEnvironmentClonePostgresMaintenance{}, managedpostgres.ErrUnavailable
	}
	var receipt state.ProjectEnvironmentClonePostgresMaintenance
	for {
		renewed, err := leases.RenewProjectEnvironmentCloneLease(ctx, lease, projectEnvironmentCloneWorkerLeaseDuration)
		if err != nil {
			return lease, receipt, err
		}
		lease = renewed
		stepCtx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
		receipt, err = store.ReserveProjectEnvironmentClonePostgresMaintenance(stepCtx, lease, plan.source.ID)
		if err == nil {
			err = validateClonePostgresMaintenancePlan(plan, receipt)
		}
		if err != nil {
			cancel()
			return lease, receipt, err
		}
		phase := clonePostgresMaintenancePhase(receipt.State)
		if phase == "" {
			cancel()
			return lease, receipt, state.ErrConflict
		}
		if phase != "ready" {
			receipt, _, err = store.ClaimProjectEnvironmentClonePostgresMaintenanceDispatch(stepCtx, lease, plan.source.ID, phase)
		}
		var actual managedpostgres.CheckpointMaintenance
		if err == nil {
			actual, err = s.managedPostgres.ReconcileCheckpointMaintenance(stepCtx, clonePostgresSnapshotDefinition(plan),
				managedpostgres.CheckpointMaintenanceRequest{OwnerToken: receipt.ID, SourceResourceID: receipt.SourceDataResourceID,
					Phase: phase, OwnerOID: receipt.OwnerOID, DatabaseOID: receipt.DatabaseOID})
		}
		if err == nil && phase != "ready" {
			receipt, err = store.RecordProjectEnvironmentClonePostgresMaintenance(stepCtx, lease, plan.source.ID, phase,
				state.ProjectEnvironmentClonePostgresMaintenanceObservation{OwnerToken: actual.OwnerToken, SourceDataResourceID: actual.SourceResourceID,
					State: actual.State, OwnerOID: actual.OwnerOID, DatabaseOID: actual.DatabaseOID})
		} else if err == nil {
			// Recheck live lease/source placement after ready-owner observation.
			receipt, err = store.ProjectEnvironmentClonePostgresMaintenanceForLease(stepCtx, lease, plan.source.ID)
		}
		cancel()
		if err != nil || phase == "ready" {
			return lease, receipt, err
		}
	}
}

func validateClonePostgresMaintenancePlan(plan capturedProjectEnvironmentDatabasePlan, r state.ProjectEnvironmentClonePostgresMaintenance) error {
	if r.ID == "" || r.SourceDatabaseID != plan.source.ID || r.BackendID != plan.source.BackendID ||
		r.BackendFingerprint != plan.source.BackendFingerprint || r.SourceProviderResourceID != plan.source.ProviderResourceID ||
		r.SourceDataResourceID != plan.source.DataResourceID {
		return state.ErrConflict
	}
	return nil
}

func clonePostgresMaintenancePhase(value string) string {
	switch value {
	case "reserved", "role_requested":
		return "role"
	case "role_reserved", "database_requested":
		return "database"
	case "database_created", "activation_requested":
		return "activation"
	case "ready":
		return "ready"
	default:
		return ""
	}
}

func clonePostgresMaintenanceObservation(r state.ProjectEnvironmentClonePostgresMaintenance) managedpostgres.CheckpointMaintenance {
	return managedpostgres.CheckpointMaintenance{OwnerToken: r.ID, SourceResourceID: r.SourceDataResourceID,
		State: r.State, OwnerOID: r.OwnerOID, DatabaseOID: r.DatabaseOID}
}
