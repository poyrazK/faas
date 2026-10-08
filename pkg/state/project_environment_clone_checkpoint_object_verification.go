package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

// Original retained versions, not a new source listing. No passwords, keys or
// native write authority belong in a read-only verification request.
type ProjectEnvironmentCloneCheckpointObjectVerificationPlan struct {
	Target                           ProjectEnvironmentCloneCheckpointObjectTargetPlan
	CreationSHA256, SourceResourceID string
	Objects                          []ProjectEnvironmentCloneObjectVersion
	TotalBytes                       int64
	CopySHA256                       string `json:",omitempty"`
}
type ProjectEnvironmentCloneCheckpointObjectVerification struct {
	Plan                     ProjectEnvironmentCloneCheckpointObjectVerificationPlan
	State, ObservationSHA256 string
	RequestedAt              time.Time
}

// A qualified comparator covers every pinned body and supported metadata field,
// and the entire target key set, including empty objects and empty buckets.
// Source/target logical digests exclude placement-specific version IDs and ETags.
// Observe must independently attest current closure and unchanged target contents.
type ProjectEnvironmentCloneCheckpointObjectVerificationObservation struct {
	RequestID, PlanSHA256, OwnerToken, NativeResourceID, SourceSHA256, TargetSHA256    string
	Complete, MetadataComplete, UnsupportedObjectsAbsent, ReadOnly, PinnedVersionsOnly bool
	Private, AllWritersClosed, NoCredentials, TargetContentsUnchanged                  bool
	ObjectCount, SourceReadBytes, TargetReadBytes                                      int64
	ComparedAt, ClosedThrough                                                          time.Time
}
type ProjectEnvironmentCloneCheckpointObjectVerificationStore interface {
	ReserveProjectEnvironmentCloneCheckpointObjectVerification(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentCloneCheckpointObjectVerification, error)
	ClaimProjectEnvironmentCloneCheckpointObjectVerification(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointObjectVerification, bool, error)
	FinishProjectEnvironmentCloneCheckpointObjectVerification(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentCloneCheckpointObjectVerificationObservation, ProjectEnvironmentCloneCheckpointReleaseObservation) error
}

func ProjectEnvironmentCloneCheckpointObjectVerificationRequestID(p ProjectEnvironmentCloneCheckpointObjectVerificationPlan) string {
	return "verify-" + p.Target.OperationID + "-" + p.Target.TargetBucketID
}
func ProjectEnvironmentCloneCheckpointObjectVerificationPlanSHA256(p ProjectEnvironmentCloneCheckpointObjectVerificationPlan) string {
	return cloneCheckpointSHA(p)
}
func checkpointObjectVerificationSHA(o ProjectEnvironmentCloneCheckpointObjectVerificationObservation) string {
	o.ClosedThrough = time.Time{}
	return cloneCheckpointSHA(o)
}
func validateCheckpointObjectVerificationObservation(in ProjectEnvironmentCloneCheckpointObjectVerification, o ProjectEnvironmentCloneCheckpointObjectVerificationObservation, until time.Time) error {
	if in.Plan.TotalBytes < 0 || in.Plan.TotalBytes > api.ObjectCheckpointVerificationBytesPerSideMax || in.State != "requested" && in.State != "verified" || in.RequestedAt.IsZero() || o.RequestID != ProjectEnvironmentCloneCheckpointObjectVerificationRequestID(in.Plan) || o.PlanSHA256 != cloneCheckpointSHA(in.Plan) || o.OwnerToken != in.Plan.Target.OperationID || o.NativeResourceID != in.Plan.Target.PhysicalName || !validCloneObjectSHA256(o.SourceSHA256) || o.SourceSHA256 != o.TargetSHA256 || !o.Complete || !o.MetadataComplete || !o.UnsupportedObjectsAbsent || !o.ReadOnly || !o.PinnedVersionsOnly || !o.Private || !o.AllWritersClosed || !o.NoCredentials || !o.TargetContentsUnchanged || o.ObjectCount != int64(len(in.Plan.Objects)) || o.SourceReadBytes != in.Plan.TotalBytes || o.TargetReadBytes != in.Plan.TotalBytes || o.ComparedAt.Location() != time.UTC || o.ComparedAt.Nanosecond()%1000 != 0 || o.ComparedAt.Before(in.RequestedAt) || o.ComparedAt.After(time.Now()) || o.ClosedThrough.Before(until) {
		return ErrConflict
	}
	if in.State == "verified" && in.ObservationSHA256 != checkpointObjectVerificationSHA(o) {
		return ErrConflict
	}
	return nil
}
