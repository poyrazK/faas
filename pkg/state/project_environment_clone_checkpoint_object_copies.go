package state

import (
	"context"
	"time"
)

// The original write deadline is immutable across worker handoff. A qualified
// provider fences every write to this request, pins source versions and seals
// all writers permanently before acknowledging completion. No provider is yet
// qualified for this protocol; deadlines are not merely client timeouts.
type ProjectEnvironmentCloneCheckpointObjectCopyPlan struct {
	Inventory  ProjectEnvironmentCloneCheckpointObjectVerificationPlan
	WriteUntil time.Time
}
type ProjectEnvironmentCloneCheckpointObjectCopy struct {
	Plan                     ProjectEnvironmentCloneCheckpointObjectCopyPlan
	State, ObservationSHA256 string
	RequestedAt              time.Time
}
type ProjectEnvironmentCloneCheckpointObjectCopyObservation struct {
	RequestID, PlanSHA256, OwnerToken, NativeResourceID                       string
	Complete, MetadataComplete, UnsupportedObjectsAbsent, PinnedVersionsOnly  bool
	Private, NoCredentials, WritersPermanentlyClosed, TargetContentsUnchanged bool
	ObjectCount, BytesCopied                                                  int64
	CopiedAt, ClosedThrough                                                   time.Time
}
type ProjectEnvironmentCloneCheckpointObjectCopyStore interface {
	ReserveProjectEnvironmentCloneCheckpointObjectCopy(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentCloneCheckpointObjectCopy, error)
	ClaimProjectEnvironmentCloneCheckpointObjectCopy(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointObjectCopy, bool, error)
	FinishProjectEnvironmentCloneCheckpointObjectCopy(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentCloneCheckpointObjectCopyObservation, ProjectEnvironmentCloneCheckpointReleaseObservation) error
}

func ProjectEnvironmentCloneCheckpointObjectCopyRequestID(p ProjectEnvironmentCloneCheckpointObjectCopyPlan) string {
	return "copy-" + p.Inventory.Target.OperationID + "-" + p.Inventory.Target.TargetBucketID
}
func ProjectEnvironmentCloneCheckpointObjectCopyPlanSHA256(p ProjectEnvironmentCloneCheckpointObjectCopyPlan) string {
	return cloneCheckpointSHA(p)
}
func checkpointObjectCopySHA(o ProjectEnvironmentCloneCheckpointObjectCopyObservation) string {
	o.ClosedThrough = time.Time{}
	return cloneCheckpointSHA(o)
}
func validateCheckpointObjectCopyObservation(in ProjectEnvironmentCloneCheckpointObjectCopy, o ProjectEnvironmentCloneCheckpointObjectCopyObservation, until time.Time) error {
	if in.State != "requested" && in.State != "copied" || in.RequestedAt.IsZero() || in.Plan.WriteUntil.IsZero() || o.RequestID != ProjectEnvironmentCloneCheckpointObjectCopyRequestID(in.Plan) || o.PlanSHA256 != cloneCheckpointSHA(in.Plan) || o.OwnerToken != in.Plan.Inventory.Target.OperationID || o.NativeResourceID != in.Plan.Inventory.Target.PhysicalName || !o.Complete || !o.MetadataComplete || !o.UnsupportedObjectsAbsent || !o.PinnedVersionsOnly || !o.Private || !o.NoCredentials || !o.WritersPermanentlyClosed || !o.TargetContentsUnchanged || o.ObjectCount != int64(len(in.Plan.Inventory.Objects)) || o.BytesCopied != in.Plan.Inventory.TotalBytes || o.copiedAtInvalid(in.RequestedAt, in.Plan.WriteUntil) || o.ClosedThrough.Before(until) {
		return ErrConflict
	}
	if in.State == "copied" && in.ObservationSHA256 != checkpointObjectCopySHA(o) {
		return ErrConflict
	}
	return nil
}
func (o ProjectEnvironmentCloneCheckpointObjectCopyObservation) copiedAtInvalid(requested, until time.Time) bool {
	return o.CopiedAt.Location() != time.UTC || o.CopiedAt.Nanosecond()%1000 != 0 || o.CopiedAt.Before(requested) || o.CopiedAt.After(until) || o.CopiedAt.After(time.Now())
}
