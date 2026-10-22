package state

import (
	"context"
	"time"
)

// Immutable frozen database specification, independent of binding credentials.
type ProjectEnvironmentCloneCheckpointPostgresRuntimeSpec struct {
	Region                                  string
	PostgresMajor                           int
	ServiceClass, Availability              string
	ScaleToZero                             bool
	StorageLimitBytes, RestoreWindowSeconds int64
}

func checkpointPostgresRuntimeSpec(source ProjectEnvironmentClonePostgresBinding) ProjectEnvironmentCloneCheckpointPostgresRuntimeSpec {
	return ProjectEnvironmentCloneCheckpointPostgresRuntimeSpec{Region: source.Region, PostgresMajor: source.PostgresMajor, ServiceClass: source.ServiceClass, Availability: source.Availability, ScaleToZero: source.ScaleToZero, StorageLimitBytes: source.StorageLimitBytes, RestoreWindowSeconds: source.RestoreWindowSeconds}
}

type ProjectEnvironmentCloneCheckpointPostgresRuntimePlan struct {
	Verification       ProjectEnvironmentCloneCheckpointPostgresVerificationPlan
	VerificationSHA256 string
	Spec               ProjectEnvironmentCloneCheckpointPostgresRuntimeSpec
}
type ProjectEnvironmentCloneCheckpointPostgresRuntime struct {
	Plan              ProjectEnvironmentCloneCheckpointPostgresRuntimePlan
	State             string
	RequestedAt       time.Time
	ObservationSHA256 string
	Observation       ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation
}

// Original request evidence and independently current operator-only runtime
// health. Credentials and customer SQL access are not granted by this receipt.
type ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation struct {
	RequestID, PlanSHA256, OwnerID, NativeResourceID, DataResourceID string
	Spec                                                             ProjectEnvironmentCloneCheckpointPostgresRuntimeSpec
	RuntimeReady, DataAccessible, PrivateScope, ReadOnly             bool
	ReadyAt                                                          time.Time
	ReadyThrough                                                     time.Time // fresh horizon, excluded from terminal digest
}
type ProjectEnvironmentCloneCheckpointPostgresRuntimeStore interface {
	ReserveProjectEnvironmentCloneCheckpointPostgresRuntime(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentCloneCheckpointPostgresRuntime, error)
	ClaimProjectEnvironmentCloneCheckpointPostgresRuntime(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentCloneCheckpointPostgresVerificationObservation, ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointPostgresRuntime, bool, error)
	FinishProjectEnvironmentCloneCheckpointPostgresRuntime(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation, ProjectEnvironmentCloneCheckpointPostgresVerificationObservation, ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneDatabaseTarget, error)
}

func ProjectEnvironmentCloneCheckpointPostgresRuntimeRequestID(p ProjectEnvironmentCloneCheckpointPostgresRuntimePlan) string {
	return "runtime-" + p.Verification.Isolation.OperationID + "-" + p.Verification.Isolation.OwnerID
}
func ProjectEnvironmentCloneCheckpointPostgresRuntimePlanSHA256(p ProjectEnvironmentCloneCheckpointPostgresRuntimePlan) string {
	return cloneCheckpointSHA(p)
}
func checkpointPostgresRuntimeSHA(o ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation) string {
	o.ReadyThrough = time.Time{}
	return cloneCheckpointSHA(o)
}
func validateCheckpointPostgresRuntimeObservation(in ProjectEnvironmentCloneCheckpointPostgresRuntime, o ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation, until time.Time) error {
	p := in.Plan.Verification.Isolation
	if in.State != "requested" && in.State != "ready" || in.RequestedAt.IsZero() || o.RequestID != ProjectEnvironmentCloneCheckpointPostgresRuntimeRequestID(in.Plan) || o.PlanSHA256 != cloneCheckpointSHA(in.Plan) || o.OwnerID != p.OwnerID || o.NativeResourceID != p.NativeResourceID || !validCloneProviderIdentity(o.DataResourceID) || o.DataResourceID == p.SourceDataResourceID || o.Spec != in.Plan.Spec || !o.RuntimeReady || !o.DataAccessible || !o.PrivateScope || !o.ReadOnly || o.ReadyAt.Location() != time.UTC || o.ReadyAt.Nanosecond()%1000 != 0 || o.ReadyAt.Before(in.RequestedAt) || o.ReadyAt.Before(p.TargetCreatedAt) || o.ReadyAt.After(time.Now()) || o.ReadyThrough.Before(until) {
		return ErrConflict
	}
	if in.State == "ready" && in.ObservationSHA256 != checkpointPostgresRuntimeSHA(o) {
		return ErrConflict
	}
	return nil
}
