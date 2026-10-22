package state

import (
	"context"
	"time"
)

// The complete frozen binding set is activated as one named intent. Preparing a
// subset would invalidate the common SQL closure proof before its peers can claim.
type ProjectEnvironmentCloneCheckpointPgActivationBinding struct {
	Connection                                  ProjectEnvironmentCloneCheckpointPgConnectionPlan
	ConnectionKeyID, ConnectionCiphertextSHA256 string
}
type ProjectEnvironmentCloneCheckpointPgActivationPlan struct {
	OperationID, AccountID, ProjectID, Scope, SourceRevisionHash string
	Bindings                                                     []ProjectEnvironmentCloneCheckpointPgActivationBinding
}
type ProjectEnvironmentCloneCheckpointPgActivation struct {
	Plan                     ProjectEnvironmentCloneCheckpointPgActivationPlan
	State, ObservationSHA256 string
	RequestedAt              time.Time
	Observation              ProjectEnvironmentCloneCheckpointPgActivationObservation
}
type ProjectEnvironmentCloneCheckpointPgActivationObservation struct {
	RequestID, PlanSHA256, AllowedBindingsSHA256                                                                                                                         string
	AllIdentitiesActive, ExactGrantScope, NoSourceAuthority, InheritedAuthorityAbsent, ScheduledJobsDisabled, PrivateScope, EndpointsPinned, TLSVerified, DatasetsPinned bool
	ActivatedAt, HealthyThrough                                                                                                                                          time.Time
}
type ProjectEnvironmentCloneCheckpointPgActivationStore interface {
	ProjectEnvironmentCloneCheckpointPgActivationForLease(context.Context, ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointPgActivation, error)
	ReserveProjectEnvironmentCloneCheckpointPgActivation(context.Context, ProjectEnvironmentCloneLease, []ProjectEnvironmentCloneCheckpointPgConnectionProof) (ProjectEnvironmentCloneCheckpointPgActivation, error)
	ClaimProjectEnvironmentCloneCheckpointPgActivation(context.Context, ProjectEnvironmentCloneLease, []ProjectEnvironmentCloneCheckpointPgConnectionProof) (ProjectEnvironmentCloneCheckpointPgActivation, bool, error)
	FinishProjectEnvironmentCloneCheckpointPgActivation(context.Context, ProjectEnvironmentCloneLease, ProjectEnvironmentCloneCheckpointPgActivationObservation, ProjectEnvironmentCloneCheckpointReleaseObservation) error
}

func ProjectEnvironmentCloneCheckpointPgActivationRequestID(p ProjectEnvironmentCloneCheckpointPgActivationPlan) string {
	return "activate-" + p.OperationID
}
func ProjectEnvironmentCloneCheckpointPgActivationPlanSHA256(p ProjectEnvironmentCloneCheckpointPgActivationPlan) string {
	return cloneCheckpointSHA(p)
}
func ProjectEnvironmentCloneCheckpointPgActivationBindingsSHA256(p ProjectEnvironmentCloneCheckpointPgActivationPlan) string {
	return cloneCheckpointSHA(p.Bindings)
}
func checkpointPgActivationObservationSHA(o ProjectEnvironmentCloneCheckpointPgActivationObservation) string {
	o.HealthyThrough = time.Time{}
	return cloneCheckpointSHA(o)
}
func validateCheckpointPgActivationObservation(in ProjectEnvironmentCloneCheckpointPgActivation, o ProjectEnvironmentCloneCheckpointPgActivationObservation, until time.Time) error {
	if in.State != "requested" && in.State != "active" || in.RequestedAt.IsZero() || o.RequestID != ProjectEnvironmentCloneCheckpointPgActivationRequestID(in.Plan) || o.PlanSHA256 != cloneCheckpointSHA(in.Plan) || o.AllowedBindingsSHA256 != cloneCheckpointSHA(in.Plan.Bindings) || !o.AllIdentitiesActive || !o.ExactGrantScope || !o.NoSourceAuthority || !o.InheritedAuthorityAbsent || !o.ScheduledJobsDisabled || !o.PrivateScope || !o.EndpointsPinned || !o.TLSVerified || !o.DatasetsPinned || o.ActivatedAt.Location() != time.UTC || o.ActivatedAt.Nanosecond()%1000 != 0 || o.ActivatedAt.Before(in.RequestedAt) || o.ActivatedAt.After(time.Now()) || o.HealthyThrough.Before(until) {
		return ErrConflict
	}
	if in.State == "active" && in.ObservationSHA256 != checkpointPgActivationObservationSHA(o) {
		return ErrConflict
	}
	return nil
}
