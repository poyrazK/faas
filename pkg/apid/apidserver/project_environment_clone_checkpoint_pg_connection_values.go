package apidserver

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// Qualified adapters attest the pinned operator-only target endpoint and its TLS
// server identity without changing grants, logins or the original SQL roster.
// Provider-returned URIs, passwords and connection options are never inputs.
type cloneCheckpointPgConnectionDriver interface {
	cloneCheckpointPostgresCredentialDriver
	ObserveCheckpointPgEndpoint(context.Context, state.ProjectEnvironmentCloneCheckpointPostgresCredentialRequest, time.Time) (state.ProjectEnvironmentCloneCheckpointPgEndpointObservation, error)
}

const checkpointPgConnectionNamespace = "checkpoint-postgres-connection"

type checkpointPgConnectionEnvelope struct {
	PlanSHA256      string
	ConnectionValue []byte
}

func (s *server) prepareCheckpointPgConnectionValues(ctx context.Context, l state.ProjectEnvironmentCloneLease, d cloneCheckpointPgConnectionDriver) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(state.ProjectEnvironmentCloneCheckpointPgConnectionStore)
	credentials, credentialOK := s.store.(state.ProjectEnvironmentCloneCheckpointPostgresCredentialStore)
	leases, leaseOK := s.store.(state.ProjectEnvironmentCloneWorkerLeaseStore)
	captures, captureOK := s.store.(state.ProjectEnvironmentCloneBindingCaptureStore)
	if !ok || !credentialOK || !leaseOK || !captureOK || d == nil || s.cloneWorkerAdmission == nil {
		return l, errCloneCheckpointUnavailable
	}
	if l.Operation.Status != state.CloneOperationCapturing {
		return l, state.ErrConflict
	}
	views, err := captures.ProjectEnvironmentCloneBindings(ctx, l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID)
	if err != nil {
		return l, err
	}
	for _, view := range views {
		for _, binding := range view.Postgres {
			if err = s.cloneWorkerAdmission(ctx); err != nil {
				return l, err
			}
			l, err = renewCloneObjectWorkerLease(ctx, leases, l, state.CloneOperationCapturing)
			if err != nil {
				return l, err
			}
			if err = s.prepareCheckpointPgConnectionSource(ctx, l, d, store, credentials, binding.ID); err != nil {
				return l, err
			}
		}
	}
	return l, nil
}
func (s *server) checkpointPgConnectionProof(ctx context.Context, l state.ProjectEnvironmentCloneLease, d cloneCheckpointPgConnectionDriver, c state.ProjectEnvironmentCloneCheckpointPostgresCredential) (state.ProjectEnvironmentCloneCheckpointPgConnectionProof, error) {
	var proof state.ProjectEnvironmentCloneCheckpointPgConnectionProof
	var err error
	proof.Prerequisites, err = s.checkpointCredentialProof(ctx, l, d, c.Plan)
	if err != nil {
		return proof, err
	}
	r := state.ProjectEnvironmentCloneCheckpointPostgresCredentialRequest{Plan: c.Plan, CiphertextSHA256: c.CiphertextSHA256}
	proof.Credential, err = d.ObserveCheckpointPostgresCredential(ctx, r, l.ExpiresAt)
	if err != nil {
		return proof, err
	}
	proof.Endpoint, err = d.ObserveCheckpointPgEndpoint(ctx, r, l.ExpiresAt)
	return proof, err
}
func (s *server) prepareCheckpointPgConnectionSource(ctx context.Context, l state.ProjectEnvironmentCloneLease, d cloneCheckpointPgConnectionDriver, store state.ProjectEnvironmentCloneCheckpointPgConnectionStore, credentials state.ProjectEnvironmentCloneCheckpointPostgresCredentialStore, id string) error {
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	c, err := credentials.ReserveProjectEnvironmentCloneCheckpointPostgresCredential(ctx, l, id)
	if err != nil {
		return err
	}
	if c.State != "prepared" {
		return state.ErrConflict
	}
	proof, err := s.checkpointPgConnectionProof(ctx, l, d, c)
	if err != nil {
		return err
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	in, err := store.ReserveProjectEnvironmentCloneCheckpointPgConnectionValue(ctx, l, id, proof)
	if err != nil {
		return err
	}
	if in.State == "sealed" {
		return nil
	}
	kid, cipher, err := sealCheckpointPgConnectionValue(in.Plan, c)
	if err != nil {
		return err
	}
	proof, err = s.checkpointPgConnectionProof(ctx, l, d, c)
	if err != nil {
		return err
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return err
	}
	_, err = store.SealProjectEnvironmentCloneCheckpointPgConnectionValue(ctx, l, id, kid, cipher, proof)
	return err
}
func sealCheckpointPgConnectionValue(p state.ProjectEnvironmentCloneCheckpointPgConnectionPlan, c state.ProjectEnvironmentCloneCheckpointPostgresCredential) (string, []byte, error) {
	if p.CredentialObservationSHA256 != c.ObservationSHA256 || p.CredentialCiphertextSHA256 != c.CiphertextSHA256 || state.ProjectEnvironmentCloneCheckpointPostgresCredentialPlanSHA256(p.Credential) != state.ProjectEnvironmentCloneCheckpointPostgresCredentialPlanSHA256(c.Plan) || setSecretRecipient == nil || mfaIdentities == nil {
		return "", nil, errCloneCheckpointUnavailable
	}
	recipient := cloneSealRecipient()
	if recipient == nil || !cloneInventoryCanOpen(recipient, accountIdentities()) {
		return "", nil, errCloneCheckpointUnavailable
	}
	password, err := openCheckpointPostgresCredential(c)
	if err != nil {
		return "", nil, err
	}
	defer clear(password.value)
	connection := url.URL{Scheme: "postgresql", User: url.UserPassword(p.Credential.ProviderIdentityID, string(password.value)), Host: net.JoinHostPort(p.Endpoint.Host, strconv.Itoa(int(p.Endpoint.Port))), Path: "/" + p.Endpoint.Database, RawQuery: "sslmode=verify-full"}
	value := []byte(connection.String())
	defer clear(value)
	raw, err := json.Marshal(checkpointPgConnectionEnvelope{PlanSHA256: state.ProjectEnvironmentCloneCheckpointPgConnectionPlanSHA256(p), ConnectionValue: value})
	if err != nil {
		return "", nil, errCloneCheckpointUnavailable
	}
	defer clear(raw)
	cipher, err := secretbox.SealBytes(recipient, checkpointPgConnectionNamespace, raw, api.PostgresCheckpointConnectionCiphertextMaxBytes)
	if err != nil || len(cipher) > api.PostgresCheckpointConnectionCiphertextMaxBytes {
		return "", nil, errCloneCheckpointUnavailable
	}
	return recipient.String(), cipher, nil
}
