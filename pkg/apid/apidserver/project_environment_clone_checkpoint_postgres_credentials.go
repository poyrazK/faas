package apidserver

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// Qualified adapters create only the named fresh inactive identity, scoped to
// this exact dataset/access. It must have no source/inherited authority and may
// not change the original fork SQL roster, open logins/writers, create grants or
// publish app secrets. Providers unable to keep inactive credential ownership
// separate from the original SQL scope cannot qualify. Observe authenticates the
// original request and current disabled state, without reissuing or repairing.
// Plaintext is synchronous, never logged or retained. No built-in driver exists.
type cloneCheckpointPostgresCredentialDriver interface {
	cloneCheckpointPostgresRuntimeDriver
	PrepareCheckpointPostgresCredential(context.Context, state.ProjectEnvironmentCloneCheckpointPostgresCredentialRequest, cloneCheckpointCredentialPassword) error
	ObserveCheckpointPostgresCredential(context.Context, state.ProjectEnvironmentCloneCheckpointPostgresCredentialRequest, time.Time) (state.ProjectEnvironmentCloneCheckpointPostgresCredentialObservation, error)
}
type cloneCheckpointCredentialPassword struct{ value []byte }

func (cloneCheckpointCredentialPassword) String() string   { return "[REDACTED]" }
func (cloneCheckpointCredentialPassword) GoString() string { return "[REDACTED]" }

const checkpointPostgresCredentialNamespace = "checkpoint-postgres-credential"

type checkpointPostgresCredentialEnvelope struct {
	PlanSHA256 string
	Password   []byte
}

func (s *server) prepareCheckpointPostgresCredentials(ctx context.Context, l state.ProjectEnvironmentCloneLease, d cloneCheckpointPostgresCredentialDriver) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(state.ProjectEnvironmentCloneCheckpointPostgresCredentialStore)
	leases, leaseOK := s.store.(state.ProjectEnvironmentCloneWorkerLeaseStore)
	releases, releaseOK := s.store.(state.ProjectEnvironmentCloneCheckpointReleaseStore)
	captures, captureOK := s.store.(state.ProjectEnvironmentCloneBindingCaptureStore)
	if !ok || !leaseOK || !releaseOK || !captureOK || d == nil || s.cloneWorkerAdmission == nil {
		return l, errCloneCheckpointUnavailable
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return l, err
	}
	var err error
	l, err = renewCloneObjectWorkerLease(ctx, leases, l, state.CloneOperationCapturing)
	if err != nil {
		return l, err
	}
	l, err = s.authenticateCheckpointRestoreRetention(ctx, l, d, releases)
	if err != nil {
		return l, err
	}
	views, err := captures.ProjectEnvironmentCloneBindings(ctx, l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID)
	if err != nil {
		return l, err
	}
	for _, view := range views {
		for _, binding := range view.Postgres {
			l, err = renewCloneObjectWorkerLease(ctx, leases, l, state.CloneOperationCapturing)
			if err != nil {
				return l, err
			}
			if err = s.prepareCheckpointPostgresCredentialSource(ctx, l, d, store, binding.ID); err != nil {
				return l, err
			}
		}
	}
	return l, nil
}
func (s *server) prepareCheckpointPostgresCredentialSource(ctx context.Context, l state.ProjectEnvironmentCloneLease, d cloneCheckpointPostgresCredentialDriver, store state.ProjectEnvironmentCloneCheckpointPostgresCredentialStore, id string) error {
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	in, err := store.ReserveProjectEnvironmentCloneCheckpointPostgresCredential(ctx, l, id)
	if err != nil {
		return err
	}
	if in.State == "planned" {
		kid, cipher, err := sealCheckpointPostgresCredential(in.Plan)
		if err != nil {
			return err
		}
		if err = s.cloneWorkerAdmission(ctx); err != nil {
			return err
		}
		in, err = store.SealProjectEnvironmentCloneCheckpointPostgresCredential(ctx, l, id, kid, cipher)
		if err != nil {
			return err
		}
	}
	var password cloneCheckpointCredentialPassword
	if in.State == "sealed" {
		password, err = openCheckpointPostgresCredential(in)
		if err != nil {
			return err
		}
	}
	defer func() { clear(password.value) }()
	proof, err := s.checkpointCredentialProof(ctx, l, d, in.Plan)
	if err != nil {
		return err
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	in, dispatch, err := store.ClaimProjectEnvironmentCloneCheckpointPostgresCredential(ctx, l, id, proof)
	if err != nil {
		return err
	}
	request := state.ProjectEnvironmentCloneCheckpointPostgresCredentialRequest{Plan: in.Plan, CiphertextSHA256: in.CiphertextSHA256}
	if dispatch {
		if err = s.cloneWorkerAdmission(ctx); err != nil {
			return err
		}
		if err = d.PrepareCheckpointPostgresCredential(ctx, request, password); err != nil {
			// Provider errors may echo credential input; never propagate them.
			return errCloneCheckpointUnavailable
		}
	}
	clear(password.value)
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	observed, err := d.ObserveCheckpointPostgresCredential(ctx, request, l.ExpiresAt)
	if err != nil {
		return err
	}
	proof, err = s.checkpointCredentialProof(ctx, l, d, in.Plan)
	if err != nil {
		return err
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	return store.FinishProjectEnvironmentCloneCheckpointPostgresCredential(ctx, l, id, observed, proof)
}
func (s *server) checkpointCredentialProof(ctx context.Context, l state.ProjectEnvironmentCloneLease, d cloneCheckpointPostgresCredentialDriver, p state.ProjectEnvironmentCloneCheckpointPostgresCredentialPlan) (state.ProjectEnvironmentCloneCheckpointPostgresCredentialProof, error) {
	var proof state.ProjectEnvironmentCloneCheckpointPostgresCredentialProof
	var err error
	proof.Runtime, err = d.ObserveCheckpointPostgresRuntime(ctx, p.Runtime, l.ExpiresAt)
	if err != nil {
		return proof, err
	}
	proof.Verification, err = d.ObserveCheckpointPostgresVerification(ctx, p.Runtime.Verification)
	if err != nil {
		return proof, err
	}
	proof.Isolation, err = d.ObserveCheckpointPostgresIsolation(ctx, p.Runtime.Verification.Isolation, l.ExpiresAt)
	if err != nil {
		return proof, err
	}
	proof.Retention, err = s.checkpointTargetRetention(ctx, l, d)
	return proof, err
}
func sealCheckpointPostgresCredential(p state.ProjectEnvironmentCloneCheckpointPostgresCredentialPlan) (string, []byte, error) {
	if setSecretRecipient == nil || mfaIdentities == nil {
		return "", nil, errCloneCheckpointUnavailable
	}
	recipient := hostSecretRecipient()
	if recipient == nil || !cloneInventoryCanOpen(recipient, mfaIdentities()) {
		return "", nil, errCloneCheckpointUnavailable
	}
	seed := make([]byte, api.PostgresCheckpointCredentialPasswordBytes)
	defer clear(seed)
	if _, err := rand.Read(seed); err != nil {
		return "", nil, errCloneCheckpointUnavailable
	}
	password := make([]byte, base64.RawURLEncoding.EncodedLen(len(seed)))
	defer clear(password)
	base64.RawURLEncoding.Encode(password, seed)
	raw, err := json.Marshal(checkpointPostgresCredentialEnvelope{PlanSHA256: state.ProjectEnvironmentCloneCheckpointPostgresCredentialPlanSHA256(p), Password: password})
	if err != nil {
		return "", nil, errCloneCheckpointUnavailable
	}
	defer clear(raw)
	cipher, err := secretbox.SealBytes(recipient, checkpointPostgresCredentialNamespace, raw, api.PostgresCheckpointCredentialCiphertextMaxBytes)
	if err != nil || len(cipher) > api.PostgresCheckpointCredentialCiphertextMaxBytes {
		return "", nil, errCloneCheckpointUnavailable
	}
	return recipient.String(), cipher, nil
}
func openCheckpointPostgresCredential(in state.ProjectEnvironmentCloneCheckpointPostgresCredential) (cloneCheckpointCredentialPassword, error) {
	var out cloneCheckpointCredentialPassword
	if mfaIdentities == nil || len(in.Ciphertext) == 0 || len(in.Ciphertext) > api.PostgresCheckpointCredentialCiphertextMaxBytes || state.ProjectEnvironmentCloneCheckpointPostgresCredentialCiphertextSHA256(in.Ciphertext) != in.CiphertextSHA256 {
		return out, errCloneCheckpointUnavailable
	}
	identities := mfaIdentities()
	match := false
	for _, id := range identities {
		if id != nil && id.Recipient().String() == in.KeyID {
			match = true
		}
	}
	if !match {
		return out, errCloneCheckpointUnavailable
	}
	namespace, raw, err := secretbox.OpenBytesMulti(identities, in.Ciphertext)
	if err != nil {
		return out, errCloneCheckpointUnavailable
	}
	defer clear(raw)
	var envelope checkpointPostgresCredentialEnvelope
	if namespace != checkpointPostgresCredentialNamespace || json.Unmarshal(raw, &envelope) != nil || envelope.PlanSHA256 != state.ProjectEnvironmentCloneCheckpointPostgresCredentialPlanSHA256(in.Plan) {
		clear(envelope.Password)
		return out, errCloneCheckpointUnavailable
	}
	seed, err := base64.RawURLEncoding.DecodeString(string(envelope.Password))
	defer clear(seed)
	if err != nil || len(seed) != api.PostgresCheckpointCredentialPasswordBytes {
		clear(envelope.Password)
		return out, errCloneCheckpointUnavailable
	}
	return cloneCheckpointCredentialPassword{value: envelope.Password}, nil
}
