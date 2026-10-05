package state

import (
	"context"

	"github.com/google/uuid"
)

func cloneObjectWriteFenceToken(operationID string) string {
	id, _ := uuid.Parse(operationID)
	return id.String()
}

func validCloneObjectFenceLease(lease ProjectEnvironmentCloneLease) bool {
	return validCloneLeaseIdentity(lease) && validCloneCredentialSourceID(lease.Operation.ID) &&
		validCloneCredentialSourceID(lease.Operation.AccountID) && validCloneCredentialSourceID(lease.Operation.ProjectID)
}

// These private barriers cover tracked object writers only. The coordinated
// capture owner must establish coverage and PostgreSQL/application drainage
// before selecting a checkpoint. Fences survive lease turnover without expiry.
type ProjectEnvironmentCloneObjectWriteFenceStore interface {
	AcquireProjectEnvironmentCloneObjectWriteFence(context.Context, ProjectEnvironmentCloneLease, string) (ObjectBucketWriteFence, error)
	ProjectEnvironmentCloneObjectWriteFencesForLease(context.Context, ProjectEnvironmentCloneLease) ([]ObjectBucketWriteFence, error)
	// Abandon capture only in compensating. Successful capture release needs
	// a separate, verified immutable-source checkpoint protocol.
	AbandonProjectEnvironmentCloneObjectWriteFences(context.Context, ProjectEnvironmentCloneLease) error
}

func capturedCloneWriteFenceBucket(op ProjectEnvironmentCloneOperation, views []ProjectEnvironmentCloneBindings, sourceID string) (ObjectBucket, error) {
	var out ObjectBucket
	for _, view := range views {
		for _, source := range view.Buckets {
			if source.ID != sourceID {
				continue
			}
			if out.ID != "" {
				return ObjectBucket{}, ErrConflict
			}
			out = ObjectBucket{ID: source.ID, AccountID: op.AccountID, AppID: view.AppID, Scope: view.SourceScope,
				Name: source.Name, Region: source.Region, BackendID: source.BackendID,
				BackendFingerprint: source.BackendFingerprint, PhysicalName: source.PhysicalName, State: "ready"}
		}
	}
	if out.ID == "" {
		return out, ErrNotFound
	}
	if !validObjectMutationBucket(out) {
		return ObjectBucket{}, ErrConflict
	}
	return out, nil
}
