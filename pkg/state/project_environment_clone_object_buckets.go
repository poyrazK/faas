package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/google/uuid"
)

// The reservation writer chooses placement from the authenticated private
// capture, never from caller-supplied or mutable source bucket definitions.
// Buckets stay private during copying. Captured public policy is applied only
// by the future atomic publication writer after independent copy proofs.
type ProjectEnvironmentCloneObjectBucketStore interface {
	ReserveProjectEnvironmentCloneObjectBucket(context.Context, ProjectEnvironmentCloneLease, string, string, int) (ObjectBucket, bool, error)
	ProjectEnvironmentCloneObjectBucketForLease(context.Context, ProjectEnvironmentCloneLease, string, string, string) (ObjectBucket, error)
}

func ProjectEnvironmentCloneObjectBucketName(op ProjectEnvironmentCloneOperation, appID, sourceBucketID string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{op.ProjectID, op.ID, op.TargetEnvironment, appID, sourceBucketID}, "\x00")))
	return "env-" + op.TargetEnvironment + "-" + hex.EncodeToString(sum[:6])
}

func capturedCloneBucketReservation(op ProjectEnvironmentCloneOperation, views []ProjectEnvironmentCloneBindings, appID, sourceID string) (ObjectBucket, ProjectEnvironmentCloneObjectBucket, error) {
	for _, view := range views {
		if view.AppID != appID {
			continue
		}
		for _, source := range view.Buckets {
			if source.ID != sourceID {
				continue
			}
			id := uuid.NewString()
			return ObjectBucket{ID: id, AccountID: op.AccountID, AppID: appID,
				Name: ProjectEnvironmentCloneObjectBucketName(op, appID, source.ID), Scope: op.TargetEnvironment,
				Region: source.Region, BackendID: source.BackendID, BackendFingerprint: source.BackendFingerprint,
				PhysicalName: "gregale-" + strings.ReplaceAll(id, "-", ""), EnvironmentCloneSourceBucketID: source.ID,
				EnvironmentCloneOperationID: op.ID}, source, nil
		}
	}
	return ObjectBucket{}, ProjectEnvironmentCloneObjectBucket{}, ErrNotFound
}

func validateCloneBucketReservation(want, actual ObjectBucket, source ProjectEnvironmentCloneObjectBucket) error {
	if actual.ID == "" || actual.ID == source.ID || actual.AccountID != want.AccountID || actual.AppID != want.AppID ||
		actual.Name != want.Name || actual.Scope != want.Scope || actual.Region != want.Region ||
		actual.BackendID != want.BackendID || actual.BackendFingerprint != want.BackendFingerprint ||
		actual.EnvironmentCloneOperationID != want.EnvironmentCloneOperationID || actual.EnvironmentCloneSourceBucketID != source.ID ||
		actual.PhysicalName != "gregale-"+strings.ReplaceAll(actual.ID, "-", "") || actual.PhysicalName == source.PhysicalName ||
		actual.PublicRead || actual.ServeAt != "" || actual.State != "provisioning" && actual.State != "ready" {
		return ErrConflict
	}
	return nil
}

var _ ProjectEnvironmentCloneObjectBucketStore = (*MemStore)(nil)
var _ ProjectEnvironmentCloneObjectBucketStore = (*PgStore)(nil)
