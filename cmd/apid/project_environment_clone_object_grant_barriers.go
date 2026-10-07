package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/objectstorage/grantrevocation"
	"github.com/onebox-faas/faas/pkg/state"
)

// Preflight every required native capability before any provider dispatch.
// A retained retirement still requires observation when its local count is zero.
func (s *server) cloneCaptureObjectGrantRetirementPlans(ctx context.Context, store state.ProjectEnvironmentCloneObjectGrantRevocationStore, l state.ProjectEnvironmentCloneLease,
	plans []capturedProjectEnvironmentObjectPlan, fences []state.ObjectBucketWriteFence) ([]capturedProjectEnvironmentObjectPlan, error) {
	if len(plans) != len(fences) {
		return nil, state.ErrConflict
	}
	var out []capturedProjectEnvironmentObjectPlan
	for i, plan := range plans {
		if !cloneCaptureObjectFenceMatches(l.Operation, plan, fences[i]) {
			return nil, state.ErrConflict
		}
		r, err := store.ProjectEnvironmentCloneObjectGrantRevocationForLease(ctx, l, plan.source.ID)
		missing := errors.Is(err, state.ErrNotFound)
		if missing && fences[i].NativeGrants == 0 {
			continue
		}
		if err != nil && !missing {
			return nil, err
		}
		if !missing && !cloneObjectGrantRevocationMatches(l, plan, r) {
			return nil, state.ErrConflict
		}
		if s.objectStorage == nil {
			return nil, objectstorage.ErrUnsupported
		}
		backend, err := s.objectStorage.Resolve(plan.source.BackendID, plan.source.BackendFingerprint)
		if err != nil {
			return nil, err
		}
		if _, ok := backend.Provider.(grantrevocation.Provider); !ok {
			return nil, objectstorage.ErrUnsupported
		}
		out = append(out, plan)
	}
	return out, nil
}

// Only dispatched original retirements are resumed during abandonment. No
// fence is released until all such retirements have independent drain evidence.
func (s *server) resumeCloneObjectGrantRetirementsForAbandonment(ctx context.Context, l state.ProjectEnvironmentCloneLease, fences []state.ObjectBucketWriteFence) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(state.ProjectEnvironmentCloneObjectGrantRevocationStore)
	if !ok {
		return l, errCloneCompensationUnavailable
	}
	scopes := make([]grantrevocation.Scope, 0, len(fences))
	seen := make(map[string]bool, len(fences))
	for _, fence := range fences {
		b, op := fence.Bucket, l.Operation
		if seen[fence.BucketID] || fence.BucketID != b.ID || b.AccountID != op.AccountID || fence.CloneOperationID != op.ID || fence.Token != op.ID || fence.Requests < 0 || fence.NativeGrants < 0 || fence.Deletions < 0 || fence.Protections < 0 || fence.Uploads < 0 || fence.Multipart < 0 {
			return l, state.ErrConflict
		}
		seen[fence.BucketID] = true
		r, err := store.ProjectEnvironmentCloneObjectGrantRevocationForLease(ctx, l, fence.BucketID)
		if errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err != nil {
			return l, err
		}
		scope := r.Plan.Scope
		if r.Plan.Validate() != nil || scope.OperationID != op.ID || scope.AccountID != op.AccountID || scope.ProjectID != op.ProjectID || scope.SourceRevisionHash != op.SourceRevisionHash ||
			scope.BucketID != b.ID || scope.AppID != b.AppID || scope.SourceScope != b.Scope || scope.BackendID != b.BackendID || scope.BackendFingerprint != b.BackendFingerprint || scope.PhysicalName != b.PhysicalName {
			return l, state.ErrConflict
		}
		if !r.RequestStartedAt.IsZero() {
			scopes = append(scopes, scope)
		}
	}
	for _, scope := range scopes {
		var observation grantrevocation.Observation
		var err error
		l, observation, err = s.resumeProjectEnvironmentCloneObjectGrantRetirement(ctx, l, scope)
		if err != nil {
			return l, err
		}
		if !observation.Drained() {
			return l, errCloneCompensationUnavailable
		}
	}
	return l, nil
}
