package main

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/objectstorage/grantrevocation"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneObjectGrantRevocationWorkerStore interface {
	state.ProjectEnvironmentCloneWorkerLeaseStore
	state.ProjectEnvironmentCloneConfigurationValidationStore
	state.ProjectEnvironmentCloneObjectGrantRevocationStore
}

func cloneObjectGrantRevocationMatches(l state.ProjectEnvironmentCloneLease, plan capturedProjectEnvironmentObjectPlan, r state.ProjectEnvironmentCloneObjectGrantRevocation) bool {
	return cloneObjectRetirementMatches(cloneObjectGrantRevocationScope(l, plan), r)
}

func cloneObjectGrantRevocationScope(l state.ProjectEnvironmentCloneLease, plan capturedProjectEnvironmentObjectPlan) grantrevocation.Scope {
	op, source := l.Operation, plan.source
	return grantrevocation.Scope{OperationID: op.ID, AccountID: op.AccountID, ProjectID: op.ProjectID,
		SourceRevisionHash: op.SourceRevisionHash, BucketID: source.ID, AppID: plan.appID, SourceScope: plan.sourceScope,
		BackendID: source.BackendID, BackendFingerprint: source.BackendFingerprint, PhysicalName: source.PhysicalName}
}

func cloneObjectRetirementMatches(scope grantrevocation.Scope, r state.ProjectEnvironmentCloneObjectGrantRevocation) bool {
	return r.Plan.Validate() == nil && r.Plan.Scope == scope
}

// The private barrier driver uses this protocol without opening capture dispatch.
// Retiring tracked grants does not establish complete writers or a common point.
func (s *server) revokeProjectEnvironmentCloneObjectNativeGrants(ctx context.Context, l state.ProjectEnvironmentCloneLease, plan capturedProjectEnvironmentObjectPlan) (state.ProjectEnvironmentCloneLease, grantrevocation.Observation, error) {
	return s.resumeProjectEnvironmentCloneObjectGrantRetirement(ctx, l, cloneObjectGrantRevocationScope(l, plan))
}

// Compensation receives only an original scope read under the owned source hold.
// It cannot select a replacement roster or initiate an undispatched retirement.
func (s *server) resumeProjectEnvironmentCloneObjectGrantRetirement(ctx context.Context, l state.ProjectEnvironmentCloneLease, scope grantrevocation.Scope) (state.ProjectEnvironmentCloneLease, grantrevocation.Observation, error) {
	var zero grantrevocation.Observation
	phase := l.Operation.Status
	if phase != state.CloneOperationCapturing && phase != state.CloneOperationCompensating {
		return l, zero, state.ErrConflict
	}
	if phase == state.CloneOperationCapturing && len(l.Operation.Resources) != 0 {
		return l, zero, state.ErrConflict
	}
	op := l.Operation
	if scope.OperationID != op.ID || scope.AccountID != op.AccountID || scope.ProjectID != op.ProjectID || scope.SourceRevisionHash != op.SourceRevisionHash {
		return l, zero, state.ErrConflict
	}
	store, ok := s.store.(cloneObjectGrantRevocationWorkerStore)
	if !ok || s.objectStorage == nil || s.cloneWorkerAdmission == nil {
		return l, zero, objectstorage.ErrUnsupported
	}
	backend, err := s.objectStorage.Resolve(scope.BackendID, scope.BackendFingerprint)
	if err != nil {
		return l, zero, err
	}
	provider, ok := backend.Provider.(grantrevocation.Provider)
	if !ok {
		return l, zero, objectstorage.ErrUnsupported
	}
	l, err = renewCloneObjectWorkerLease(ctx, store, l, phase)
	if err != nil {
		return l, zero, err
	}
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return l, zero, err
	}
	if phase == state.CloneOperationCapturing {
		if _, err := store.ValidateProjectEnvironmentCloneSourceConfigurationForLease(ctx, l); err != nil {
			return l, zero, err
		}
	}
	var r state.ProjectEnvironmentCloneObjectGrantRevocation
	if phase == state.CloneOperationCapturing {
		r, err = store.ReserveProjectEnvironmentCloneObjectGrantRevocation(ctx, l, scope.BucketID)
	} else {
		// Compensation may resume a dispatched retirement, never start a new
		// one or change its original roster after source configuration edits.
		r, err = store.ProjectEnvironmentCloneObjectGrantRevocationForLease(ctx, l, scope.BucketID)
	}
	if err != nil {
		return l, zero, err
	}
	if !cloneObjectRetirementMatches(scope, r) {
		return l, zero, state.ErrConflict
	}
	if r.State != "drained" {
		if err := s.cloneWorkerAdmission(ctx); err != nil {
			return l, zero, err
		}
		r, err = store.DispatchProjectEnvironmentCloneObjectGrantRevocation(ctx, l, r.Plan)
		if err != nil {
			return l, zero, err
		}
		if !cloneObjectRetirementMatches(scope, r) {
			return l, zero, state.ErrConflict
		}
		if err := s.cloneWorkerAdmission(ctx); err != nil {
			return l, zero, err
		}
		// A retry invokes the provider's authenticated, idempotent resume with
		// the original request and roster. Missing journal data never clears a
		// grant; providers unable to resolve this identity must fail closed.
		if err := provider.RevokeNativeWriteGrants(ctx, r.Plan.Clone()); err != nil {
			return l, zero, fmt.Errorf("resume native object grant revocation: %w", err)
		}
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return l, zero, err
	}
	// A revoke reply is not accepted as observation. Recheck current authority
	// before the independent provider read, including after a long remote call.
	current, err := store.ProjectEnvironmentCloneObjectGrantRevocationForLease(ctx, l, scope.BucketID)
	if err != nil {
		return l, zero, err
	}
	hash, _ := r.Plan.SHA256()
	currentHash, _ := current.Plan.SHA256()
	if !cloneObjectRetirementMatches(scope, current) || hash != currentHash {
		return l, zero, state.ErrConflict
	}
	observation, err := provider.ObserveNativeWriteGrantRevocation(ctx, r.Plan.Clone())
	if err != nil {
		return l, zero, fmt.Errorf("observe native object grant revocation: %w", err)
	}
	if observation.Validate(r.Plan) != nil {
		return l, zero, state.ErrConflict
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return l, zero, err
	}
	if phase == state.CloneOperationCapturing {
		if _, err := store.ValidateProjectEnvironmentCloneSourceConfigurationForLease(ctx, l); err != nil {
			return l, zero, err
		}
	}
	if _, err := store.RecordProjectEnvironmentCloneObjectGrantRevocation(ctx, l, r.Plan, observation); err != nil {
		return l, zero, err
	}
	l, err = renewCloneObjectWorkerLease(ctx, store, l, phase)
	if err != nil {
		return l, zero, err
	}
	if err := ctx.Err(); err != nil {
		return l, zero, err
	}
	return l, observation, nil
}
