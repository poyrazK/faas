package state

import (
	"context"
	"time"
)

// Immutable request input binds original retained data and an already isolated
// private owner. Digests contain no customer names, values or credentials.
type ProjectEnvironmentCloneCheckpointPostgresVerificationPlan struct {
	Isolation           ProjectEnvironmentCloneCheckpointPostgresIsolationPlan
	IsolationSHA256     string
	MaxReadBytesPerSide int64
}
type ProjectEnvironmentCloneCheckpointPostgresVerification struct {
	Plan              ProjectEnvironmentCloneCheckpointPostgresVerificationPlan
	State             string
	RequestedAt       time.Time
	ObservationSHA256 string
}

// A qualified comparator must cover every supported database, schema, table,
// sequence and large object, including empty objects. A complete equal inventory
// binds the canonical logical content, not physical page layout. It must refuse
// unsupported objects and execute no data/authentication mutation or source read.
type ProjectEnvironmentCloneCheckpointPostgresVerificationObservation struct {
	RequestID, PlanSHA256, SourceSHA256, TargetSHA256 string
	Complete, UnsupportedObjectsAbsent, ReadOnly      bool
	SourceReadBytes, TargetReadBytes                  int64
	ComparedAt                                        time.Time
}
type ProjectEnvironmentCloneCheckpointPostgresVerificationStore interface {
	ReserveProjectEnvironmentCloneCheckpointPostgresVerification(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentCloneCheckpointPostgresVerification, error)
	ClaimProjectEnvironmentCloneCheckpointPostgresVerification(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointPostgresVerification, bool, error)
	FinishProjectEnvironmentCloneCheckpointPostgresVerification(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentCloneCheckpointPostgresVerificationObservation, ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, ProjectEnvironmentCloneCheckpointReleaseObservation) error
}

func ProjectEnvironmentCloneCheckpointPostgresVerificationRequestID(p ProjectEnvironmentCloneCheckpointPostgresVerificationPlan) string {
	return "verify-" + p.Isolation.OperationID + "-" + p.Isolation.OwnerID
}
func ProjectEnvironmentCloneCheckpointPostgresVerificationPlanSHA256(p ProjectEnvironmentCloneCheckpointPostgresVerificationPlan) string {
	return cloneCheckpointSHA(p)
}
func validateCheckpointPostgresVerificationObservation(in ProjectEnvironmentCloneCheckpointPostgresVerification, o ProjectEnvironmentCloneCheckpointPostgresVerificationObservation) error {
	if in.State != "requested" && in.State != "verified" || in.RequestedAt.IsZero() || o.RequestID != ProjectEnvironmentCloneCheckpointPostgresVerificationRequestID(in.Plan) || o.PlanSHA256 != cloneCheckpointSHA(in.Plan) || !validCloneObjectSHA256(o.SourceSHA256) || o.SourceSHA256 != o.TargetSHA256 || !o.Complete || !o.UnsupportedObjectsAbsent || !o.ReadOnly || o.SourceReadBytes < 0 || o.TargetReadBytes < 0 || o.SourceReadBytes > in.Plan.MaxReadBytesPerSide || o.TargetReadBytes > in.Plan.MaxReadBytesPerSide || o.ComparedAt.Location() != time.UTC || o.ComparedAt.Nanosecond()%1000 != 0 || o.ComparedAt.Before(in.RequestedAt) || o.ComparedAt.After(time.Now()) {
		return ErrConflict
	}
	if in.State == "verified" && in.ObservationSHA256 != cloneCheckpointSHA(o) {
		return ErrConflict
	}
	return nil
}
