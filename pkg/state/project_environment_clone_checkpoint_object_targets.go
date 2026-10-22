package state

import (
	"context"
	"time"
)

// Immutable field order is part of the native request digest. No credentials or
// worker tokens belong in this original operation-owned creation plan.
type ProjectEnvironmentCloneCheckpointObjectTargetPlan struct {
	OperationID, AccountID, AppID, SourceBucketID, TargetBucketID string
	BackendID, BackendFingerprint, Region, PhysicalName           string
	ManifestSHA256, CheckpointID                                  string
}

type ProjectEnvironmentCloneCheckpointObjectTarget struct {
	Plan              ProjectEnvironmentCloneCheckpointObjectTargetPlan
	State             string
	RequestedAt       time.Time
	ObservationSHA256 string
}

type ProjectEnvironmentCloneCheckpointObjectTargetObservation struct {
	RequestID, PlanSHA256, OwnerToken, NativeResourceID, State string
	Private, Empty                                             bool
	CreatedAt                                                  time.Time
}

type ProjectEnvironmentCloneCheckpointObjectTargetStore interface {
	ReserveProjectEnvironmentCloneCheckpointObjectTarget(context.Context, ProjectEnvironmentCloneLease, string, string, string) (ProjectEnvironmentCloneCheckpointObjectTarget, error)
	ClaimProjectEnvironmentCloneCheckpointObjectTarget(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointObjectTarget, bool, error)
	FinishProjectEnvironmentCloneCheckpointObjectTarget(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentCloneCheckpointObjectTargetObservation, ProjectEnvironmentCloneCheckpointReleaseObservation) (ObjectBucket, error)
}

func ProjectEnvironmentCloneCheckpointObjectTargetRequestID(plan ProjectEnvironmentCloneCheckpointObjectTargetPlan) string {
	return "target-" + plan.OperationID + "-" + plan.TargetBucketID
}

func ProjectEnvironmentCloneCheckpointObjectTargetPlanSHA256(plan ProjectEnvironmentCloneCheckpointObjectTargetPlan) string {
	return cloneCheckpointSHA(plan)
}

func validateCloneCheckpointObjectTargetObservation(in ProjectEnvironmentCloneCheckpointObjectTarget, observed ProjectEnvironmentCloneCheckpointObjectTargetObservation) error {
	if in.State != "requested" && in.State != "ready" || in.RequestedAt.IsZero() ||
		observed.RequestID != ProjectEnvironmentCloneCheckpointObjectTargetRequestID(in.Plan) || observed.PlanSHA256 != cloneCheckpointSHA(in.Plan) ||
		observed.OwnerToken != in.Plan.OperationID || observed.NativeResourceID != in.Plan.PhysicalName || observed.State != "ready" || !observed.Private || !observed.Empty ||
		observed.CreatedAt.Location() != time.UTC || observed.CreatedAt.Nanosecond()%1000 != 0 || observed.CreatedAt.Before(in.RequestedAt) || observed.CreatedAt.After(time.Now()) {
		return ErrConflict
	}
	if in.State == "ready" && in.ObservationSHA256 != cloneCheckpointSHA(observed) {
		return ErrConflict
	}
	return nil
}
