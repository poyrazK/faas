package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/objectstorage/grantrevocation"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneCaptureBarrierStore interface {
	state.ProjectEnvironmentCloneWorkerLeaseStore
	state.ProjectEnvironmentCloneConfigurationValidationStore
	state.ProjectEnvironmentCloneWorkloadStore
	state.ProjectEnvironmentCloneBindingCaptureStore
	state.ProjectEnvironmentClonePostgresWriteFenceStore
	state.ProjectEnvironmentCloneObjectWriteFenceStore
	state.ProjectEnvironmentCloneObjectGrantRevocationStore
}

// Transient observations, never durable checkpoint or release authority.
// Zero tracked writers still excludes external/native provider writers and
// PostgreSQL background workers. No capture point is selected by this driver.
type cloneCaptureBarrierObservation struct {
	configuration              state.ProjectEnvironmentCloneConfigurationCapture
	configurationFence         state.ProjectEnvironmentCloneConfigurationFence
	postgres                   []managedpostgres.CheckpointConnectionClosure
	objects                    []state.ObjectBucketWriteFence
	objectRetirements          []grantrevocation.Observation
	instrumentedWritersDrained bool
}

// This private driver is deliberately outside public/coordinator admission.
// It joins the existing recoverable holds and original native selections, and
// returns no usable observation on uncertainty. Every committed hold survives
// errors, cancellation and handoff; abandonment uses the compensating protocol.
func (s *server) prepareProjectEnvironmentCloneCaptureBarriers(ctx context.Context, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, cloneCaptureBarrierObservation, error) {
	var zero cloneCaptureBarrierObservation
	if lease.Operation.Status != state.CloneOperationCapturing || len(lease.Operation.Resources) != 0 {
		return lease, zero, state.ErrConflict
	}
	store, ok := s.store.(cloneCaptureBarrierStore)
	if !ok || s.cloneWorkerAdmission == nil {
		return lease, zero, errCloneCheckpointUnavailable
	}
	lease, err := renewCloneObjectWorkerLease(ctx, store, lease, state.CloneOperationCapturing)
	if err != nil {
		return lease, zero, err
	}
	ctx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return lease, zero, err
	}
	capture, err := store.ValidateProjectEnvironmentCloneSourceConfigurationForLease(ctx, lease)
	if err != nil {
		return lease, zero, err
	}
	op := lease.Operation
	views, err := store.ProjectEnvironmentCloneWorkloads(ctx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return lease, zero, err
	}
	if _, err := capturedCloneConfigurationResources(op, capture, views); err != nil {
		return lease, zero, err
	}
	catalogue, err := store.ProjectEnvironmentCloneBindings(ctx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return lease, zero, err
	}
	databases, err := buildCapturedProjectEnvironmentDatabasePlans(op, views, catalogue)
	if err != nil {
		return lease, zero, err
	}
	objects, err := buildCapturedProjectEnvironmentObjectPlans(op, views, catalogue)
	if err != nil {
		return lease, zero, err
	}
	if len(databases)+len(objects) == 0 {
		return lease, zero, state.ErrConflict
	}
	if len(databases) != 0 && s.managedPostgres == nil {
		return lease, zero, managedpostgres.ErrUnavailable
	}
	// All source holds precede remote IO. Shared PostgreSQL bindings are one
	// source plan; each frozen object bucket is held exactly once.
	for _, plan := range databases {
		if err := s.cloneWorkerAdmission(ctx); err != nil {
			return lease, zero, err
		}
		fence, err := store.ReserveProjectEnvironmentClonePostgresWriteFence(ctx, lease, plan.source.ID)
		if err != nil {
			return lease, zero, err
		}
		if !cloneCapturePostgresFenceMatches(op, plan, fence) {
			return lease, zero, state.ErrConflict
		}
	}
	for _, plan := range objects {
		if err := s.cloneWorkerAdmission(ctx); err != nil {
			return lease, zero, err
		}
		fence, err := store.AcquireProjectEnvironmentCloneObjectWriteFence(ctx, lease, plan.source.ID)
		if err != nil {
			return lease, zero, err
		}
		if !cloneCaptureObjectFenceMatches(op, plan, fence) {
			return lease, zero, state.ErrConflict
		}
	}
	// Independently verify complete owned rosters before touching providers.
	fences, err := readCloneCaptureBarrierRosters(ctx, store, lease, databases, objects)
	if err != nil {
		return lease, zero, err
	}
	retirements, err := s.cloneCaptureObjectGrantRetirementPlans(ctx, store, lease, objects, fences)
	if err != nil {
		return lease, zero, err
	}
	out := cloneCaptureBarrierObservation{configuration: capture, instrumentedWritersDrained: true}
	for _, plan := range databases {
		if err := s.cloneWorkerAdmission(ctx); err != nil {
			return lease, zero, err
		}
		lease, _, err = s.prepareProjectEnvironmentClonePostgresMaintenance(ctx, lease, plan)
		if err != nil {
			return lease, zero, err
		}
		if _, err := s.discoverProjectEnvironmentClonePostgresCheckpointSelection(ctx, lease, plan); err != nil {
			return lease, zero, err
		}
		var closure managedpostgres.CheckpointConnectionClosure
		lease, closure, err = s.closeProjectEnvironmentClonePostgresCheckpointConnections(ctx, lease, plan)
		if err != nil {
			return lease, zero, err
		}
		out.postgres = append(out.postgres, closure)
		out.instrumentedWritersDrained = out.instrumentedWritersDrained && closure.Drained
	}
	for _, plan := range retirements {
		if _, err := readCloneCaptureBarrierRosters(ctx, store, lease, databases, objects); err != nil {
			return lease, zero, err
		}
		var observation grantrevocation.Observation
		lease, observation, err = s.revokeProjectEnvironmentCloneObjectNativeGrants(ctx, lease, plan)
		if err != nil {
			return lease, zero, err
		}
		out.objectRetirements = append(out.objectRetirements, observation)
		out.instrumentedWritersDrained = out.instrumentedWritersDrained && observation.Drained()
	}
	out.objects, err = readCloneCaptureBarrierRosters(ctx, store, lease, databases, objects)
	if err != nil {
		return lease, zero, err
	}
	for _, fence := range out.objects {
		out.instrumentedWritersDrained = out.instrumentedWritersDrained && fence.Requests == 0 && fence.NativeGrants == 0 && fence.Deletions == 0
	}
	// Detect config edits during remote IO without replacing the original root.
	// This snapshot does not fence edits after it or attest a common data point.
	current, err := store.ValidateProjectEnvironmentCloneSourceConfigurationForLease(ctx, lease)
	if err != nil {
		return lease, zero, err
	}
	if current != capture {
		return lease, zero, state.ErrConflict
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return lease, zero, err
	}
	lease, err = renewCloneObjectWorkerLease(ctx, store, lease, state.CloneOperationCapturing)
	if err != nil {
		return lease, zero, err
	}
	if err := ctx.Err(); err != nil {
		return lease, zero, err
	}
	return lease, out, nil
}

func readCloneCaptureBarrierRosters(ctx context.Context, store cloneCaptureBarrierStore, lease state.ProjectEnvironmentCloneLease, databases []capturedProjectEnvironmentDatabasePlan, objects []capturedProjectEnvironmentObjectPlan) ([]state.ObjectBucketWriteFence, error) {
	fences, err := store.ProjectEnvironmentClonePostgresWriteFencesForLease(ctx, lease)
	if err != nil {
		return nil, err
	}
	if len(fences) != len(databases) {
		return nil, state.ErrConflict
	}
	byDatabase := make(map[string]state.ProjectEnvironmentClonePostgresWriteFence, len(fences))
	for _, fence := range fences {
		if _, duplicate := byDatabase[fence.SourceDatabaseID]; duplicate {
			return nil, state.ErrConflict
		}
		byDatabase[fence.SourceDatabaseID] = fence
	}
	for _, plan := range databases {
		if !cloneCapturePostgresFenceMatches(lease.Operation, plan, byDatabase[plan.source.ID]) {
			return nil, state.ErrConflict
		}
	}
	buckets, err := store.ProjectEnvironmentCloneObjectWriteFencesForLease(ctx, lease)
	if err != nil {
		return nil, err
	}
	if len(buckets) != len(objects) {
		return nil, state.ErrConflict
	}
	byBucket := make(map[string]state.ObjectBucketWriteFence, len(buckets))
	for _, fence := range buckets {
		if _, duplicate := byBucket[fence.BucketID]; duplicate {
			return nil, state.ErrConflict
		}
		byBucket[fence.BucketID] = fence
	}
	ordered := make([]state.ObjectBucketWriteFence, 0, len(objects))
	for _, plan := range objects {
		fence := byBucket[plan.source.ID]
		if !cloneCaptureObjectFenceMatches(lease.Operation, plan, fence) {
			return nil, state.ErrConflict
		}
		ordered = append(ordered, fence)
	}
	return ordered, nil
}

func cloneCapturePostgresFenceMatches(op state.ProjectEnvironmentCloneOperation, plan capturedProjectEnvironmentDatabasePlan, f state.ProjectEnvironmentClonePostgresWriteFence) bool {
	return f.OperationID == op.ID && f.SourceDatabaseID == plan.source.ID && f.SourceVersion == plan.hash &&
		f.BackendID == plan.source.BackendID && f.BackendFingerprint == plan.source.BackendFingerprint &&
		f.SourceProviderResourceID == plan.source.ProviderResourceID && f.SourceDataResourceID == plan.source.DataResourceID &&
		f.State == "held" && f.RemoteTerminalState == "" && f.RemoteReleasedAt.IsZero() && f.ReleasedAt.IsZero()
}

func cloneCaptureObjectFenceMatches(op state.ProjectEnvironmentCloneOperation, plan capturedProjectEnvironmentObjectPlan, f state.ObjectBucketWriteFence) bool {
	b, source := f.Bucket, plan.source
	return f.BucketID == source.ID && f.Token == op.ID && f.CloneOperationID == op.ID && f.Requests >= 0 && f.NativeGrants >= 0 && f.Deletions >= 0 &&
		b.ID == source.ID && b.AccountID == op.AccountID && b.AppID == plan.appID && b.State == "ready" &&
		b.Scope == plan.sourceScope && b.Name == source.Name && b.Region == source.Region && b.BackendID == source.BackendID &&
		b.BackendFingerprint == source.BackendFingerprint && b.PhysicalName == source.PhysicalName &&
		b.PublicRead == source.PublicRead && b.ServeAt == source.ServeAt
}
