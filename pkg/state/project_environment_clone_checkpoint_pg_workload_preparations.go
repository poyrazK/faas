package state

import (
	"reflect"
	"time"
)

// The caller must authenticate the original activation graph and lock its live
// publication rows in the enclosing transaction. This projection reuses the
// committed envelopes; it neither opens credentials nor creates a legacy restore
// ledger. In particular it cannot supply resource publication authority.
func checkpointPgWorkloadPreparations(op ProjectEnvironmentCloneOperation, activation ProjectEnvironmentCloneCheckpointPgActivation, publication ProjectEnvironmentCloneCheckpointPgPublication) (map[string]ProjectEnvironmentClonePostgresBindingPreparation, error) {
	if activation.State != "active" || publication.State != "published" || publication.Plan.OperationID != op.ID ||
		activation.Plan.OperationID != op.ID || activation.Plan.AccountID != op.AccountID || activation.Plan.ProjectID != op.ProjectID || activation.Plan.Scope != op.TargetEnvironment || activation.Plan.SourceRevisionHash != op.SourceRevisionHash ||
		publication.PlanSHA256 != cloneCheckpointSHA(publication.Plan) || publication.Plan.ActivationPlanSHA256 != cloneCheckpointSHA(activation.Plan) ||
		publication.Plan.ActivationObservationSHA256 != activation.ObservationSHA256 || len(publication.Plan.Secrets) != len(activation.Plan.Bindings) {
		return nil, ErrProjectEnvironmentCloneManagedValueProof
	}
	result := make(map[string]ProjectEnvironmentClonePostgresBindingPreparation, len(activation.Plan.Bindings))
	for i, member := range activation.Plan.Bindings {
		c := member.Connection.Credential
		p := c.Runtime.Verification.Isolation
		secret, err := checkpointPgPublicationSecret(c, publication.Plan.Secrets[i])
		if err != nil || !reflect.DeepEqual(secret, publication.Plan.Secrets[i]) || c.SourceBindingID == "" {
			return nil, ErrProjectEnvironmentCloneManagedValueProof
		}
		if _, duplicate := result[c.SourceBindingID]; duplicate {
			return nil, ErrProjectEnvironmentCloneManagedValueProof
		}
		prepared, _, err := normalizeClonePostgresPreparation(ProjectEnvironmentClonePostgresBindingPreparation{
			Binding: ProjectEnvironmentClonePostgresBindingTarget{
				OperationID: p.OperationID, SourceBindingID: c.SourceBindingID, ID: c.TargetBindingID,
				AccountID: p.AccountID, AppID: c.AppID, DatabaseID: p.OwnerID,
				Scope: c.Scope, EnvironmentKey: c.EnvironmentKey, Access: c.Access,
				BackendID: p.BackendID, BackendFingerprint: p.BackendFingerprint,
				DatabaseProviderResourceID: p.NativeResourceID, CapturePoint: p.CapturePoint.Format(time.RFC3339Nano),
				CredentialGeneration: c.CredentialGeneration, State: "ready",
				ProviderIdentityID: c.ProviderIdentityID, CredentialRef: secret.ManagedCredentialRef,
			}, Secret: secret,
		})
		if err != nil || validateClonePreparedPostgresIdentity(op, c.SourceBindingID, c.AppID, prepared) != nil {
			return nil, ErrProjectEnvironmentCloneManagedValueProof
		}
		result[c.SourceBindingID] = prepared
	}
	return result, nil
}
