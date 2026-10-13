package state

import (
	"context"
	"time"
)

// Field order is immutable request digest input. These plans contain no SQL
// passwords, source credentials, worker tokens or replacement source definitions.
type ProjectEnvironmentCloneCheckpointPostgresIsolationPlan struct {
	OperationID, AccountID, SourceDatabaseID, OwnerID, NativeResourceID                   string
	BackendID, BackendFingerprint, CheckpointID, ProviderSnapshotID, SourceDataResourceID string
	CapturePoint, SnapshotCreatedAt, TargetCreatedAt                                      time.Time
}
type ProjectEnvironmentCloneCheckpointPostgresIsolation struct {
	Plan              ProjectEnvironmentCloneCheckpointPostgresIsolationPlan
	State             string
	RequestedAt       time.Time
	ObservationSHA256 string
}
type ProjectEnvironmentCloneCheckpointPostgresIsolationObservation struct {
	RequestID, PlanSHA256, OwnerID, NativeResourceID, RosterSHA256                 string
	AllWritersClosed, InheritedLoginsDisabled, ScheduledJobsDisabled, PrivateScope bool
	ClosedAt                                                                       time.Time
	// Independently observed current closure through the work lease, excluded
	// from the immutable original terminal digest to allow worker handoff.
	ClosedThrough time.Time
}
type ProjectEnvironmentCloneCheckpointPostgresIsolationStore interface {
	ReserveProjectEnvironmentCloneCheckpointPostgresIsolation(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentCloneCheckpointPostgresIsolation, error)
	ClaimProjectEnvironmentCloneCheckpointPostgresIsolation(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointPostgresIsolation, bool, error)
	FinishProjectEnvironmentCloneCheckpointPostgresIsolation(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneDatabaseTarget, error)
}

func ProjectEnvironmentCloneCheckpointPostgresIsolationRequestID(plan ProjectEnvironmentCloneCheckpointPostgresIsolationPlan) string {
	return "isolate-" + plan.OperationID + "-" + plan.OwnerID
}
func ProjectEnvironmentCloneCheckpointPostgresIsolationPlanSHA256(plan ProjectEnvironmentCloneCheckpointPostgresIsolationPlan) string {
	return cloneCheckpointSHA(plan)
}
func checkpointPostgresIsolationSHA(o ProjectEnvironmentCloneCheckpointPostgresIsolationObservation) string {
	o.ClosedThrough = time.Time{}
	return cloneCheckpointSHA(o)
}
func validateCheckpointPostgresIsolationObservation(in ProjectEnvironmentCloneCheckpointPostgresIsolation, o ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, until time.Time) error {
	if in.State != "requested" && in.State != "adopted" || in.RequestedAt.IsZero() || o.RequestID != ProjectEnvironmentCloneCheckpointPostgresIsolationRequestID(in.Plan) || o.PlanSHA256 != cloneCheckpointSHA(in.Plan) || o.OwnerID != in.Plan.OwnerID || o.NativeResourceID != in.Plan.NativeResourceID || !validCloneObjectSHA256(o.RosterSHA256) || !o.AllWritersClosed || !o.InheritedLoginsDisabled || !o.ScheduledJobsDisabled || !o.PrivateScope || o.ClosedAt.Location() != time.UTC || o.ClosedAt.Nanosecond()%1000 != 0 || o.ClosedAt.Before(in.RequestedAt) || o.ClosedAt.Before(in.Plan.TargetCreatedAt) || o.ClosedAt.After(time.Now()) || o.ClosedThrough.Before(until) {
		return ErrConflict
	}
	if in.State == "adopted" && in.ObservationSHA256 != checkpointPostgresIsolationSHA(o) {
		return ErrConflict
	}
	return nil
}
