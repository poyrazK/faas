package state

import (
	"context"
	"time"
)

// Retirement pins the original preparation, including requests whose outcome is
// unknown. It contains no plaintext or encrypted password material.
type ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementPlan struct {
	Credential                                                       ProjectEnvironmentCloneCheckpointPostgresCredentialPlan
	PreparationState, CiphertextSHA256, PreparationObservationSHA256 string
	PreparationRequestedAt                                           time.Time
	ActivationRequestID                                              string `json:",omitempty"`
	ActivationPlanSHA256                                             string `json:",omitempty"`
}
type ProjectEnvironmentCloneCheckpointPostgresCredentialRetirement struct {
	Plan                     ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementPlan
	State, ObservationSHA256 string
	RequestedAt              time.Time
	Observation              ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation
}
type ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation struct {
	RequestID, PreparationRequestID, PlanSHA256, ProviderIdentityID, OwnerID, DataResourceID string
	PreparationFenced, IdentityAbsent, SessionsClosed, NoSourceAuthority, SQLScopeUnchanged  bool
	RetiredAt                                                                                time.Time
	ActivationFenced                                                                         bool `json:",omitempty"`
}
type ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementStore interface {
	ReserveProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentCloneCheckpointPostgresCredentialRetirement, error)
	ClaimProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentCloneCheckpointPostgresCredentialRetirement, bool, error)
	FinishProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation) error
}

func ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementRequestID(p ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementPlan) string {
	return "retire-" + ProjectEnvironmentCloneCheckpointPostgresCredentialRequestID(p.Credential)
}
func ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementPlanSHA256(p ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementPlan) string {
	return cloneCheckpointSHA(p)
}
func validateCheckpointPostgresCredentialRetirementObservation(in ProjectEnvironmentCloneCheckpointPostgresCredentialRetirement, o ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation) error {
	p := in.Plan
	if in.State != "requested" && in.State != "retired" || in.RequestedAt.IsZero() || o.RequestID != ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementRequestID(p) || o.PreparationRequestID != ProjectEnvironmentCloneCheckpointPostgresCredentialRequestID(p.Credential) || o.PlanSHA256 != cloneCheckpointSHA(p) || o.ProviderIdentityID != p.Credential.ProviderIdentityID || o.OwnerID != p.Credential.Runtime.Verification.Isolation.OwnerID || o.DataResourceID != p.Credential.DataResourceID || !o.PreparationFenced || p.ActivationRequestID != "" && !o.ActivationFenced || !o.IdentityAbsent || !o.SessionsClosed || !o.NoSourceAuthority || !o.SQLScopeUnchanged || o.RetiredAt.Location() != time.UTC || o.RetiredAt.Nanosecond()%1000 != 0 || o.RetiredAt.Before(in.RequestedAt) || o.RetiredAt.After(time.Now()) {
		return ErrConflict
	}
	if in.State == "retired" && in.ObservationSHA256 != cloneCheckpointSHA(o) {
		return ErrConflict
	}
	return nil
}
