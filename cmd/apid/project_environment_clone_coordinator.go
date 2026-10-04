package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

const projectEnvironmentCloneWorkerInterval = 5 * time.Second

var errCloneCheckpointUnavailable = errors.New("coordinated clone capture is unavailable")
var errCloneCompensationUnavailable = errors.New("complete clone compensation is unavailable")

type projectEnvironmentCloneCoordinatorStore interface {
	state.ProjectEnvironmentCloneWorkerLeaseStore
	state.ProjectEnvironmentCloneOperationStore
	state.ProjectEnvironmentCloneConfigurationCaptureStore
	state.ProjectEnvironmentCloneBindingCaptureStore
	state.ProjectEnvironmentCloneWorkloadStore
	state.ProjectEnvironmentCloneMaterializationStore
	state.ProjectEnvironmentClonePublicationStore
}

// The durable queue survives missed notifications and apid restarts. Public
// complete admission remains closed. Data-bearing pending/capturing operations
// wait for a coordinated checkpoint instead of inventing a timestamp.
func (s *server) runProjectEnvironmentCloneCoordinator(ctx context.Context) {
	_ = s.runProjectEnvironmentCloneCoordinatorWithAdmission(ctx, nil, nil)
}

func (s *server) runProjectEnvironmentCloneCoordinatorWithAdmission(ctx context.Context, admit func(context.Context) error, beat func()) error {
	store, ok := s.store.(projectEnvironmentCloneCoordinatorStore)
	if !ok {
		return state.ErrConflict
	}
	ticker := time.NewTicker(projectEnvironmentCloneWorkerInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		if admit != nil {
			if err := admit(ctx); err != nil {
				return err // lost host authority must stop before another claim
			}
		}
		if err := s.processNextProjectEnvironmentClone(ctx, store); err != nil && ctx.Err() == nil && s.log != nil {
			// Provider errors can contain credential material. Queue diagnostics
			// expose only the stable category, never an arbitrary error string.
			s.log.Warn("project environment clone deferred", "reason", cloneCoordinatorErrorCode(err))
		}
		if beat != nil {
			beat()
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (s *server) processNextProjectEnvironmentClone(ctx context.Context, store projectEnvironmentCloneCoordinatorStore) error {
	lease, err := store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), projectEnvironmentCloneWorkerLeaseDuration)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	stepCtx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	lease, stepErr := s.processProjectEnvironmentCloneLease(stepCtx, store, lease)
	cancel()
	if lease.Operation.Status == state.CloneOperationReady {
		return stepErr // publication has already cleared the lease atomically
	}
	// Cancellation must still relinquish a successfully checkpointed lease.
	// Unknown commit outcomes deliberately wait for expiry rather than adopting
	// a current revision that may belong to another worker.
	releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), projectEnvironmentCloneWorkerInterval)
	defer releaseCancel()
	delay := projectEnvironmentCloneWorkerInterval
	if stepErr != nil {
		delay = cloneCoordinatorRetryDelay(lease.AttemptCount)
	}
	return errors.Join(stepErr, store.ReleaseProjectEnvironmentCloneLease(releaseCtx, lease, delay))
}

func cloneCoordinatorRetryDelay(attempt int32) time.Duration {
	delay := 30 * time.Second
	for i := int32(1); i < attempt && delay < projectEnvironmentCloneWorkerLeaseDuration; i++ {
		delay *= 2
	}
	return min(delay, projectEnvironmentCloneWorkerLeaseDuration)
}

func cloneCoordinatorErrorCode(err error) string {
	switch {
	case errors.Is(err, errCloneCheckpointUnavailable):
		return "data_checkpoint_unavailable"
	case errors.Is(err, errCloneCompensationUnavailable):
		return "compensation_unavailable"
	case errors.Is(err, objectstorage.ErrObjectSnapshotRetentionUnavailable):
		return "object_snapshot_retention_unavailable"
	case errors.Is(err, state.ErrProjectEnvironmentCloneResourcePublicationProof):
		return "resource_publication_proof_unavailable"
	case errors.Is(err, state.ErrProjectEnvironmentCloneWorkPolicyIsolationUnavailable):
		return "work_policy_activation_unavailable"
	case errors.Is(err, state.ErrProjectEnvironmentQueueActivationUnavailable):
		return "queue_activation_unavailable"
	case errors.Is(err, state.ErrConflict), errors.Is(err, state.ErrInvalidArgument):
		return "clone_fence_rejected"
	default:
		return "clone_step_failed"
	}
}

func (s *server) processProjectEnvironmentCloneLease(ctx context.Context, store projectEnvironmentCloneCoordinatorStore, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, error) {
	renewed, err := store.RenewProjectEnvironmentCloneLease(ctx, lease, projectEnvironmentCloneWorkerLeaseDuration)
	if err != nil {
		return lease, err
	}
	lease = renewed
	switch lease.Operation.Status {
	case state.CloneOperationPending, state.CloneOperationCapturing:
		lease, err = captureProjectEnvironmentClone(ctx, store, lease)
		if err != nil {
			return lease, err
		}
	case state.CloneOperationCompensating:
		lease, err = s.abandonProjectEnvironmentClonePostgresWriteFences(ctx, lease)
		if err != nil {
			return lease, err
		}
		lease, err = s.abandonProjectEnvironmentCloneObjectWriteFences(ctx, lease)
		if err != nil {
			return lease, err
		}
		var copiesRetired bool
		lease, copiesRetired, err = s.cleanupProjectEnvironmentClonePostgresCopyTargets(ctx, lease)
		if err != nil {
			return lease, err
		}
		if !copiesRetired {
			return lease, errCloneCompensationUnavailable
		}
		var forksRetired bool
		lease, forksRetired, err = s.cleanupProjectEnvironmentClonePostgresSnapshotRestores(ctx, lease)
		if err != nil {
			return lease, err
		}
		if !forksRetired {
			return lease, errCloneCompensationUnavailable
		}
		lease, _, err = s.cleanupProjectEnvironmentClonePostgresSnapshots(ctx, lease)
		if err != nil {
			return lease, err
		}
		return lease, errCloneCompensationUnavailable
	case state.CloneOperationCopying, state.CloneOperationPublishing:
	default:
		return lease, state.ErrConflict
	}
	capture, err := store.ProjectEnvironmentCloneConfigurationForLease(ctx, lease)
	if err != nil {
		return lease, err
	}
	views, err := store.ProjectEnvironmentCloneWorkloads(ctx, lease.Operation.AccountID, lease.Operation.ProjectID, lease.Operation.ID)
	if err != nil {
		return lease, err
	}
	if err := validateCloneCoordinatorInventory(lease.Operation, capture, views); err != nil {
		return lease, fmt.Errorf("validate clone configuration inventory: %w", err)
	}
	if lease.Operation.Status == state.CloneOperationCopying {
		if err := s.validateCloneCoordinatorDataInventory(ctx, lease.Operation); err != nil {
			return lease, fmt.Errorf("validate clone data inventory: %w", err)
		}
		lease, err = s.copyProjectEnvironmentClone(ctx, store, lease)
		if err != nil || lease.Operation.Status != state.CloneOperationPublishing {
			return lease, err
		}
	}
	ttl, err := s.projectEnvironmentCloneReleaseTTL(ctx, lease.Operation, views)
	if err != nil {
		return lease, err
	}
	op := lease.Operation
	if _, err := store.PublishProjectEnvironmentCloneReleaseSet(ctx, op.AccountID, op.ProjectID, op.ID, op.Revision, ttl); err != nil {
		return lease, fmt.Errorf("publish clone release graph: %w", err)
	}
	// Preserve the terminal status even if reading the commit receipt fails.
	// The next request can recover that receipt; this worker cannot release it.
	lease.Operation.Status = state.CloneOperationReady
	finished, err := store.ProjectEnvironmentCloneOperationByID(ctx, op.AccountID, op.ProjectID, op.ID)
	if err == nil {
		lease.Operation = finished
	}
	return lease, err
}

func (s *server) validateCloneCoordinatorDataInventory(ctx context.Context, op state.ProjectEnvironmentCloneOperation) error {
	databases, err := s.capturedProjectEnvironmentDatabasePlans(ctx, op)
	if err != nil {
		return err
	}
	objects, err := s.capturedProjectEnvironmentObjectPlans(ctx, op)
	if err != nil {
		return err
	}
	if _, _, err := validateCapturedProjectEnvironmentDatabaseResources(databases, op.Resources); err != nil {
		return err
	}
	_, _, err = validateCapturedProjectEnvironmentObjectResources(objects, op.Resources, false)
	return err
}

func (s *server) copyProjectEnvironmentClone(ctx context.Context, store projectEnvironmentCloneCoordinatorStore, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, error) {
	lease, databasesReady, err := s.prepareProjectEnvironmentCloneDatabases(ctx, lease)
	if err != nil {
		return lease, fmt.Errorf("prepare clone databases: %w", err)
	}
	lease, objectsReady, err := s.prepareProjectEnvironmentCloneObjects(ctx, lease)
	if err != nil || !databasesReady || !objectsReady {
		if err != nil {
			return lease, fmt.Errorf("prepare clone objects: %w", err)
		}
		return lease, nil
	}
	lease, postgresIDs, postgresCount, err := s.prepareProjectEnvironmentClonePostgresBindings(ctx, lease)
	if err != nil {
		return lease, fmt.Errorf("prepare clone PostgreSQL credentials: %w", err)
	}
	lease, objectIDs, objectCount, err := s.prepareProjectEnvironmentCloneObjectCredentials(ctx, lease)
	if err != nil {
		return lease, fmt.Errorf("prepare clone object credentials: %w", err)
	}
	account, err := s.store.AccountByID(ctx, lease.Operation.AccountID)
	if err != nil {
		return lease, err
	}
	if _, err := store.MaterializeProjectEnvironmentCloneForLease(ctx, lease, append(postgresIDs, objectIDs...), postgresCount+objectCount, api.MustLimitsFor(account.Plan)); err != nil {
		return lease, fmt.Errorf("materialize clone configuration: %w", err)
	}
	resources, ready, err := s.prepareProjectEnvironmentCloneDeployments(ctx, lease.Operation)
	if err != nil {
		return lease, fmt.Errorf("prepare clone deployments: %w", err)
	}
	for i := range resources {
		switch resources[i].Kind {
		case "project_config", "workload_settings", "variables", "secrets", "route_policy", "edge_policy":
			resources[i].Status = "ready"
		}
	}
	op := lease.Operation
	if !cloneCoordinatorResourcesEqual(op.Resources, resources) {
		updated, err := store.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, op.Status, op.Revision, resources, "")
		if err != nil {
			return lease, fmt.Errorf("checkpoint clone readiness: %w", err)
		}
		lease.Operation, op = updated, updated
	}
	if !ready {
		return lease, nil
	}
	updated, err := store.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationPublishing, op.Revision, resources, "")
	if err == nil {
		lease.Operation = updated
	}
	if err != nil {
		return lease, fmt.Errorf("verify clone publication readiness: %w", err)
	}
	return lease, nil
}

func cloneCoordinatorResourcesEqual(a, b []state.ProjectEnvironmentCloneResource) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s *server) projectEnvironmentCloneReleaseTTL(ctx context.Context, op state.ProjectEnvironmentCloneOperation, views []state.ProjectEnvironmentCloneWorkload) (int, error) {
	settings, ok := s.store.(state.ProjectEnvironmentWorkloadSpecReader)
	if !ok || len(views) == 0 {
		return 0, state.ErrConflict
	}
	ttl := api.RevisionPinMaxTTLSeconds
	for _, view := range views {
		spec, err := settings.ProjectEnvironmentWorkloadSpec(ctx, op.AccountID, op.ProjectID, op.TargetEnvironment, view.AppID)
		if err != nil {
			return 0, err
		}
		app, err := s.store.AppByID(ctx, view.AppID)
		if err != nil {
			return 0, err
		}
		// The legacy release publisher also checks the App projection's pin
		// window. Use the shortest supported window, never an invented limit.
		ttl = min(ttl, spec.Settings.Manifest.RevisionPinTTLSeconds, app.Manifest.RevisionPinTTLSeconds)
	}
	if ttl <= 0 {
		return 0, state.ErrConflict
	}
	return ttl, nil
}
