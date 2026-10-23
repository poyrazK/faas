package apidserver

import (
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// Conversion prepares an app-readable envelope, never publication authority.
// The future atomic writer must authenticate current activation and persist the
// complete original binding set together. A private connection envelope cannot
// be copied into app_secrets: its namespace and payload are different.
func sealCheckpointPgAppSecret(member state.ProjectEnvironmentCloneCheckpointPgActivationBinding, in state.ProjectEnvironmentCloneCheckpointPgConnectionValue, credential state.ProjectEnvironmentCloneCheckpointPostgresCredential) (state.AppSecret, error) {
	var out state.AppSecret
	p := in.Plan
	c := p.Credential
	if in.State != "sealed" || credential.State != "prepared" || c.CredentialGeneration != 1 || c.TargetBindingID == "" || c.TargetBindingID == c.SourceBindingID || c.Runtime.Verification.Isolation.AccountID == "" || c.AppID == "" || c.Scope == "" || c.EnvironmentKey == "" ||
		state.ProjectEnvironmentCloneCheckpointPgConnectionPlanSHA256(member.Connection) != state.ProjectEnvironmentCloneCheckpointPgConnectionPlanSHA256(p) || member.ConnectionKeyID != in.KeyID || member.ConnectionCiphertextSHA256 != in.CiphertextSHA256 ||
		state.ProjectEnvironmentCloneCheckpointPostgresCredentialPlanSHA256(c) != state.ProjectEnvironmentCloneCheckpointPostgresCredentialPlanSHA256(credential.Plan) || p.CredentialCiphertextSHA256 != credential.CiphertextSHA256 || p.CredentialObservationSHA256 != credential.ObservationSHA256 ||
		len(in.Ciphertext) == 0 || len(in.Ciphertext) > api.PostgresCheckpointConnectionCiphertextMaxBytes || state.ProjectEnvironmentCloneCheckpointPostgresCredentialCiphertextSHA256(in.Ciphertext) != in.CiphertextSHA256 ||
		setSecretRecipient == nil || mfaIdentities == nil || hostHMACKey == nil {
		return out, errCloneCheckpointUnavailable
	}
	recipient := hostSecretRecipient()
	identities := mfaIdentities()
	key := hostHMACKeyBytes()
	matched := false
	for _, identity := range identities {
		if identity != nil && identity.Recipient().String() == in.KeyID {
			matched = true
		}
	}
	if recipient == nil || !matched || len(key) == 0 || !cloneInventoryCanOpen(recipient, identities) {
		return out, errCloneCheckpointUnavailable
	}
	namespace, raw, err := secretbox.OpenBytesMulti(identities, in.Ciphertext)
	if err != nil {
		return out, errCloneCheckpointUnavailable
	}
	defer clear(raw)
	var envelope checkpointPgConnectionEnvelope
	if namespace != checkpointPgConnectionNamespace || json.Unmarshal(raw, &envelope) != nil || envelope.PlanSHA256 != state.ProjectEnvironmentCloneCheckpointPgConnectionPlanSHA256(p) {
		clear(envelope.ConnectionValue)
		return out, errCloneCheckpointUnavailable
	}
	defer clear(envelope.ConnectionValue)
	password, err := openCheckpointPostgresCredential(credential)
	if err != nil {
		return out, errCloneCheckpointUnavailable
	}
	defer clear(password.value)
	expected := url.URL{Scheme: "postgresql", User: url.UserPassword(c.ProviderIdentityID, string(password.value)), Host: net.JoinHostPort(p.Endpoint.Host, strconv.Itoa(int(p.Endpoint.Port))), Path: "/" + p.Endpoint.Database, RawQuery: "sslmode=verify-full"}
	value := []byte(expected.String())
	defer clear(value)
	if subtle.ConstantTimeCompare(value, envelope.ConnectionValue) != 1 {
		return out, errCloneCheckpointUnavailable
	}
	ref, err := managedPostgresCredentialRef(managedpostgres.Binding{ID: c.TargetBindingID, CredentialGeneration: 1})
	if err != nil {
		return out, errCloneCheckpointUnavailable
	}
	// The checkpoint material above is apid's to open; the app secret is
	// vmmd's, so it is sealed to the runtime recipient (ADR 20261022111812).
	appRecipient := runtimeSecretRecipient()
	if appRecipient == nil {
		return out, errCloneCheckpointUnavailable
	}
	cipher, err := secretbox.SealOneBound(appRecipient, runtimeSecretBinding(c.Runtime.Verification.Isolation.AccountID), c.EnvironmentKey, string(value), api.PostgresCheckpointConnectionCiphertextMaxBytes)
	if err != nil || len(cipher) > api.PostgresCheckpointConnectionCiphertextMaxBytes {
		return out, errCloneCheckpointUnavailable
	}
	fingerprint, err := secretbox.ValueFingerprint(value, key)
	if err != nil {
		return out, errCloneCheckpointUnavailable
	}
	return state.AppSecret{AccountID: c.Runtime.Verification.Isolation.AccountID, AppID: c.AppID, Scope: c.Scope, Key: c.EnvironmentKey, Ciphertext: cipher, Kid: appRecipient.String(), ValueHash: fingerprint, SecretClass: state.SecretClassPersistent, SecretVersion: 1, ManagedPostgresBindingID: c.TargetBindingID, ManagedPostgresAccess: c.Access, ManagedCredentialRef: ref, ManagedCredentialGeneration: 1}, nil
}
