package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneCheckpointPgConnectionStore = (*PgStore)(nil)

func checkpointPgConnectionPlanTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, l ProjectEnvironmentCloneLease, id string, proof ProjectEnvironmentCloneCheckpointPgConnectionProof) (ProjectEnvironmentCloneCheckpointPgConnectionPlan, error) {
	var out ProjectEnvironmentCloneCheckpointPgConnectionPlan
	credential, err := readCheckpointPostgresCredentialTx(ctx, tx, op, id)
	if err != nil {
		return out, err
	}
	if err = validateCheckpointPostgresCredentialTx(ctx, tx, op, l, credential, &proof.Prerequisites); err != nil {
		return out, err
	}
	if err = validateCheckpointPostgresCredentialObservation(credential, proof.Credential, l.ExpiresAt); err != nil {
		return out, err
	}
	if err = validateCheckpointPgEndpoint(credential, proof.Endpoint, l.ExpiresAt); err != nil {
		return out, err
	}
	out = ProjectEnvironmentCloneCheckpointPgConnectionPlan{Credential: credential.Plan, CredentialObservationSHA256: credential.ObservationSHA256, CredentialCiphertextSHA256: credential.CiphertextSHA256, Endpoint: proof.Endpoint}
	out.Endpoint.ValidThrough = time.Time{}
	return out, nil
}
func checkpointPgConnectionFromSQL(r sqlc.ProjectEnvironmentCloneCheckpointPgConnectionValue, p ProjectEnvironmentCloneCheckpointPgConnectionPlan) (ProjectEnvironmentCloneCheckpointPgConnectionValue, error) {
	in := ProjectEnvironmentCloneCheckpointPgConnectionValue{State: r.State, KeyID: r.KeyID.String, CiphertextSHA256: r.CiphertextSha256.String, Ciphertext: append([]byte(nil), r.Ciphertext...)}
	if json.Unmarshal(r.Plan, &in.Plan) != nil || !reflect.DeepEqual(in.Plan, p) || pgUUIDString(r.OperationID) != p.Credential.Runtime.Verification.Isolation.OperationID || pgUUIDString(r.SourceBindingID) != p.Credential.SourceBindingID {
		return in, ErrConflict
	}
	if in.State == "sealed" {
		if len(in.Ciphertext) == 0 || len(in.Ciphertext) > api.PostgresCheckpointConnectionCiphertextMaxBytes || ProjectEnvironmentCloneCheckpointPostgresCredentialCiphertextSHA256(in.Ciphertext) != in.CiphertextSHA256 {
			return in, ErrConflict
		}
		if _, err := age.ParseX25519Recipient(in.KeyID); err != nil {
			return in, ErrConflict
		}
	}
	return in, nil
}
func (s *PgStore) ReserveProjectEnvironmentCloneCheckpointPgConnectionValue(ctx context.Context, l ProjectEnvironmentCloneLease, id string, proof ProjectEnvironmentCloneCheckpointPgConnectionProof) (ProjectEnvironmentCloneCheckpointPgConnectionValue, error) {
	var out ProjectEnvironmentCloneCheckpointPgConnectionValue
	if !validCloneCredentialSourceID(id) {
		return out, ErrInvalidArgument
	}
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		p, err := checkpointPgConnectionPlanTx(ctx, tx, op, l, id, proof)
		if err != nil {
			return err
		}
		q := sqlc.New()
		row, err := q.ReadCloneCheckpointPgConnectionValue(ctx, tx, sqlc.ReadCloneCheckpointPgConnectionValueParams{OperationID: mustPgUUID(op.ID), SourceBindingID: mustPgUUID(id)})
		if err == nil {
			out, err = checkpointPgConnectionFromSQL(row, p)
			return err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return mapErr(err)
		}
		encoded, err := json.Marshal(p)
		if err != nil {
			return err
		}
		row, err = q.InsertCloneCheckpointPgConnectionValue(ctx, tx, sqlc.InsertCloneCheckpointPgConnectionValueParams{OperationID: mustPgUUID(op.ID), SourceBindingID: mustPgUUID(id), Plan: encoded, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointPgConnectionFromSQL(row, p)
		return err
	})
	return out, err
}
func (s *PgStore) SealProjectEnvironmentCloneCheckpointPgConnectionValue(ctx context.Context, l ProjectEnvironmentCloneLease, id, kid string, cipher []byte, proof ProjectEnvironmentCloneCheckpointPgConnectionProof) (ProjectEnvironmentCloneCheckpointPgConnectionValue, error) {
	var out ProjectEnvironmentCloneCheckpointPgConnectionValue
	if !validCloneCredentialSourceID(id) || len(cipher) == 0 || len(cipher) > api.PostgresCheckpointConnectionCiphertextMaxBytes {
		return out, ErrInvalidArgument
	}
	if _, err := age.ParseX25519Recipient(kid); err != nil {
		return out, ErrInvalidArgument
	}
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		p, err := checkpointPgConnectionPlanTx(ctx, tx, op, l, id, proof)
		if err != nil {
			return err
		}
		q := sqlc.New()
		row, err := q.ReadCloneCheckpointPgConnectionValue(ctx, tx, sqlc.ReadCloneCheckpointPgConnectionValueParams{OperationID: mustPgUUID(op.ID), SourceBindingID: mustPgUUID(id)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointPgConnectionFromSQL(row, p)
		if err != nil {
			return err
		}
		if out.State == "sealed" {
			if out.KeyID != kid || !bytes.Equal(out.Ciphertext, cipher) {
				return ErrConflict
			}
			return nil
		}
		row, err = q.SealCloneCheckpointPgConnectionValue(ctx, tx, sqlc.SealCloneCheckpointPgConnectionValueParams{OperationID: mustPgUUID(op.ID), SourceBindingID: mustPgUUID(id), KeyID: kid, Ciphertext: cipher, CiphertextSha256: ProjectEnvironmentCloneCheckpointPostgresCredentialCiphertextSHA256(cipher), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointPgConnectionFromSQL(row, p)
		return err
	})
	return out, err
}
