package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Private prepublication intent; source credential material is never an input.
type ProjectEnvironmentCloneCheckpointPostgresCredentialPlan struct {
	Runtime                                                                                    ProjectEnvironmentCloneCheckpointPostgresRuntimePlan
	RuntimeSHA256, DataResourceID                                                              string
	SourceBindingID, TargetBindingID, ProviderIdentityID, AppID, Scope, EnvironmentKey, Access string
	CredentialGeneration                                                                       int64
}
type ProjectEnvironmentCloneCheckpointPostgresCredential struct {
	Plan                                              ProjectEnvironmentCloneCheckpointPostgresCredentialPlan
	State, KeyID, CiphertextSHA256, ObservationSHA256 string
	Ciphertext                                        []byte
	RequestedAt                                       time.Time
}
type ProjectEnvironmentCloneCheckpointPostgresCredentialRequest struct {
	Plan             ProjectEnvironmentCloneCheckpointPostgresCredentialPlan
	CiphertextSHA256 string
}
type ProjectEnvironmentCloneCheckpointPostgresCredentialProof struct {
	Runtime      ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation
	Verification ProjectEnvironmentCloneCheckpointPostgresVerificationObservation
	Isolation    ProjectEnvironmentCloneCheckpointPostgresIsolationObservation
	Retention    ProjectEnvironmentCloneCheckpointReleaseObservation
}
type ProjectEnvironmentCloneCheckpointPostgresCredentialObservation struct {
	RequestID, PlanSHA256, CiphertextSHA256, ProviderIdentityID, OwnerID, DataResourceID, Access string
	IdentityDisabled, PrivateScope, NoSourceAuthority, SQLScopeUnchanged                         bool
	CreatedAt                                                                                    time.Time
	DisabledThrough                                                                              time.Time // independent current horizon, excluded terminal digest
}
type ProjectEnvironmentCloneCheckpointPostgresCredentialStore interface {
	ReserveProjectEnvironmentCloneCheckpointPostgresCredential(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentCloneCheckpointPostgresCredential, error)
	SealProjectEnvironmentCloneCheckpointPostgresCredential(context.Context, ProjectEnvironmentCloneLease, string, string, []byte) (ProjectEnvironmentCloneCheckpointPostgresCredential, error)
	ClaimProjectEnvironmentCloneCheckpointPostgresCredential(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentCloneCheckpointPostgresCredentialProof) (ProjectEnvironmentCloneCheckpointPostgresCredential, bool, error)
	FinishProjectEnvironmentCloneCheckpointPostgresCredential(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentCloneCheckpointPostgresCredentialObservation, ProjectEnvironmentCloneCheckpointPostgresCredentialProof) error
}

func ProjectEnvironmentCloneCheckpointPostgresCredentialRequestID(p ProjectEnvironmentCloneCheckpointPostgresCredentialPlan) string {
	return "credential-" + p.Runtime.Verification.Isolation.OperationID + "-" + p.TargetBindingID
}
func ProjectEnvironmentCloneCheckpointPostgresCredentialPlanSHA256(p ProjectEnvironmentCloneCheckpointPostgresCredentialPlan) string {
	return cloneCheckpointSHA(p)
}
func ProjectEnvironmentCloneCheckpointPostgresCredentialCiphertextSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func checkpointPostgresCredentialSHA(o ProjectEnvironmentCloneCheckpointPostgresCredentialObservation) string {
	o.DisabledThrough = time.Time{}
	return cloneCheckpointSHA(o)
}
func validateCheckpointPostgresCredentialObservation(in ProjectEnvironmentCloneCheckpointPostgresCredential, o ProjectEnvironmentCloneCheckpointPostgresCredentialObservation, until time.Time) error {
	p := in.Plan
	if in.State != "requested" && in.State != "prepared" || in.RequestedAt.IsZero() || o.RequestID != ProjectEnvironmentCloneCheckpointPostgresCredentialRequestID(p) || o.PlanSHA256 != cloneCheckpointSHA(p) || o.CiphertextSHA256 != in.CiphertextSHA256 || o.ProviderIdentityID != p.ProviderIdentityID || o.OwnerID != p.Runtime.Verification.Isolation.OwnerID || o.DataResourceID != p.DataResourceID || o.Access != p.Access || !o.IdentityDisabled || !o.PrivateScope || !o.NoSourceAuthority || !o.SQLScopeUnchanged || o.CreatedAt.Location() != time.UTC || o.CreatedAt.Nanosecond()%1000 != 0 || o.CreatedAt.Before(in.RequestedAt) || o.CreatedAt.After(time.Now()) || o.DisabledThrough.Before(until) {
		return ErrConflict
	}
	if in.State == "prepared" && in.ObservationSHA256 != checkpointPostgresCredentialSHA(o) {
		return ErrConflict
	}
	return nil
}
