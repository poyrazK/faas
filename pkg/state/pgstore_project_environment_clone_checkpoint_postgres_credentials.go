package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneCheckpointPostgresCredentialStore = (*PgStore)(nil)

func checkpointPostgresCredentialFromSQL(r sqlc.ProjectEnvironmentCloneCheckpointPostgresCredential) (ProjectEnvironmentCloneCheckpointPostgresCredential, error) {
	in := ProjectEnvironmentCloneCheckpointPostgresCredential{State: r.State, KeyID: r.KeyID.String, Ciphertext: append([]byte(nil), r.Ciphertext...), CiphertextSHA256: r.CiphertextSha256.String, ObservationSHA256: r.ObservationSha256.String, RequestedAt: r.RequestedAt.Time.UTC()}
	if json.Unmarshal(r.Plan, &in.Plan) != nil || in.Plan.Runtime.Verification.Isolation.OperationID != pgUUIDString(r.OperationID) || in.Plan.Runtime.Verification.Isolation.SourceDatabaseID != pgUUIDString(r.SourceDatabaseID) || in.Plan.Runtime.Verification.Isolation.AccountID != pgUUIDString(r.AccountID) || in.Plan.SourceBindingID != pgUUIDString(r.SourceBindingID) || in.Plan.TargetBindingID != pgUUIDString(r.TargetBindingID) {
		return in, ErrConflict
	}
	if in.State != "planned" {
		if len(in.Ciphertext) == 0 || len(in.Ciphertext) > api.PostgresCheckpointCredentialCiphertextMaxBytes || ProjectEnvironmentCloneCheckpointPostgresCredentialCiphertextSHA256(in.Ciphertext) != in.CiphertextSHA256 {
			return in, ErrConflict
		}
		if _, err := age.ParseX25519Recipient(in.KeyID); err != nil {
			return in, ErrConflict
		}
	}
	return in, nil
}
func readCheckpointPostgresCredentialTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, id string) (ProjectEnvironmentCloneCheckpointPostgresCredential, error) {
	r, err := sqlc.New().ReadCloneCheckpointPostgresCredential(ctx, tx, sqlc.ReadCloneCheckpointPostgresCredentialParams{OperationID: mustPgUUID(op.ID), SourceBindingID: mustPgUUID(id)})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointPostgresCredential{}, mapErr(err)
	}
	return checkpointPostgresCredentialFromSQL(r)
}
func checkpointPostgresCredentialPlanTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, l ProjectEnvironmentCloneLease, id, targetID string, proof *ProjectEnvironmentCloneCheckpointPostgresCredentialProof) (ProjectEnvironmentCloneCheckpointPostgresCredentialPlan, error) {
	var out ProjectEnvironmentCloneCheckpointPostgresCredentialPlan
	if !validCloneCredentialSourceID(targetID) || targetID == id {
		return out, ErrConflict
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return out, err
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return out, err
	}
	appID, source, err := capturedClonePostgresBinding(views, id)
	if err != nil {
		return out, err
	}
	runtime, err := readCheckpointPostgresRuntimeTx(ctx, tx, op, source.DatabaseID)
	if err != nil {
		return out, err
	}
	if runtime.State != "ready" {
		return out, ErrConflict
	}
	var v *ProjectEnvironmentCloneCheckpointPostgresVerificationObservation
	var c *ProjectEnvironmentCloneCheckpointPostgresIsolationObservation
	var r *ProjectEnvironmentCloneCheckpointReleaseObservation
	if proof != nil {
		v = &proof.Verification
		c = &proof.Isolation
		r = &proof.Retention
	}
	if err = validateCheckpointPostgresRuntimeTx(ctx, tx, op, l, runtime, v, c, r); err != nil {
		return out, err
	}
	if proof != nil {
		if err = validateCheckpointPostgresRuntimeObservation(runtime, proof.Runtime, l.ExpiresAt); err != nil {
			return out, err
		}
	}
	return ProjectEnvironmentCloneCheckpointPostgresCredentialPlan{Runtime: runtime.Plan, RuntimeSHA256: runtime.ObservationSHA256, DataResourceID: runtime.Observation.DataResourceID, SourceBindingID: id, TargetBindingID: targetID, ProviderIdentityID: "stage-" + targetID, AppID: appID, Scope: op.TargetEnvironment, EnvironmentKey: source.EnvironmentKey, Access: source.Access, CredentialGeneration: 1}, nil
}
func validateCheckpointPostgresCredentialTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, l ProjectEnvironmentCloneLease, in ProjectEnvironmentCloneCheckpointPostgresCredential, proof *ProjectEnvironmentCloneCheckpointPostgresCredentialProof) error {
	p, err := checkpointPostgresCredentialPlanTx(ctx, tx, op, l, in.Plan.SourceBindingID, in.Plan.TargetBindingID, proof)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(p, in.Plan) {
		return ErrConflict
	}
	return nil
}
func (s *PgStore) ReserveProjectEnvironmentCloneCheckpointPostgresCredential(ctx context.Context, l ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentCloneCheckpointPostgresCredential, error) {
	var out ProjectEnvironmentCloneCheckpointPostgresCredential
	if !validCloneCredentialSourceID(id) {
		return out, ErrInvalidArgument
	}
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		q := sqlc.New()
		if _, err := q.LockProjectEnvironmentCloneDatabaseAccount(ctx, tx, mustPgUUID(op.AccountID)); err != nil {
			return mapErr(err)
		}
		var err error
		out, err = readCheckpointPostgresCredentialTx(ctx, tx, op, id)
		if err == nil {
			return validateCheckpointPostgresCredentialTx(ctx, tx, op, l, out, nil)
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		p, err := checkpointPostgresCredentialPlanTx(ctx, tx, op, l, id, uuid.NewString(), nil)
		if err != nil {
			return err
		}
		count, err := q.CountCloneCheckpointPostgresCredentials(ctx, tx, mustPgUUID(op.AccountID))
		if err != nil {
			return mapErr(err)
		}
		if count >= api.PostgresCheckpointCredentialsPerAccountMax {
			return ErrQuotaExceeded
		}
		encoded, err := json.Marshal(p)
		if err != nil {
			return err
		}
		row, err := q.InsertCloneCheckpointPostgresCredential(ctx, tx, sqlc.InsertCloneCheckpointPostgresCredentialParams{OperationID: mustPgUUID(op.ID), SourceBindingID: mustPgUUID(id), SourceDatabaseID: mustPgUUID(p.Runtime.Verification.Isolation.SourceDatabaseID), TargetBindingID: mustPgUUID(p.TargetBindingID), Plan: encoded, ExpectedRevision: l.Operation.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointPostgresCredentialFromSQL(row)
		return err
	})
	return out, err
}
func (s *PgStore) SealProjectEnvironmentCloneCheckpointPostgresCredential(ctx context.Context, l ProjectEnvironmentCloneLease, id, kid string, ciphertext []byte) (ProjectEnvironmentCloneCheckpointPostgresCredential, error) {
	var out ProjectEnvironmentCloneCheckpointPostgresCredential
	if !validCloneCredentialSourceID(id) || len(ciphertext) == 0 || len(ciphertext) > api.PostgresCheckpointCredentialCiphertextMaxBytes {
		return out, ErrInvalidArgument
	}
	if _, err := age.ParseX25519Recipient(kid); err != nil {
		return out, ErrInvalidArgument
	}
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointPostgresCredentialTx(ctx, tx, op, id)
		if err != nil {
			return err
		}
		if err = validateCheckpointPostgresCredentialTx(ctx, tx, op, l, out, nil); err != nil {
			return err
		}
		if out.State != "planned" {
			if out.KeyID != kid || !bytes.Equal(out.Ciphertext, ciphertext) {
				return ErrConflict
			}
			return nil
		}
		row, err := sqlc.New().SealCloneCheckpointPostgresCredential(ctx, tx, sqlc.SealCloneCheckpointPostgresCredentialParams{OperationID: mustPgUUID(op.ID), SourceBindingID: mustPgUUID(id), KeyID: kid, Ciphertext: ciphertext, CiphertextSha256: ProjectEnvironmentCloneCheckpointPostgresCredentialCiphertextSHA256(ciphertext), ExpectedRevision: l.Operation.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointPostgresCredentialFromSQL(row)
		return err
	})
	return out, err
}
func (s *PgStore) ClaimProjectEnvironmentCloneCheckpointPostgresCredential(ctx context.Context, l ProjectEnvironmentCloneLease, id string, proof ProjectEnvironmentCloneCheckpointPostgresCredentialProof) (ProjectEnvironmentCloneCheckpointPostgresCredential, bool, error) {
	var out ProjectEnvironmentCloneCheckpointPostgresCredential
	dispatch := false
	if !validCloneCredentialSourceID(id) {
		return out, false, ErrInvalidArgument
	}
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointPostgresCredentialTx(ctx, tx, op, id)
		if err != nil {
			return err
		}
		if err = validateCheckpointPostgresCredentialTx(ctx, tx, op, l, out, &proof); err != nil {
			return err
		}
		if out.State == "planned" {
			return ErrConflict
		}
		if out.State != "sealed" {
			return nil
		}
		row, err := sqlc.New().ClaimCloneCheckpointPostgresCredential(ctx, tx, sqlc.ClaimCloneCheckpointPostgresCredentialParams{OperationID: mustPgUUID(op.ID), SourceBindingID: mustPgUUID(id), ExpectedRevision: l.Operation.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointPostgresCredentialFromSQL(row)
		dispatch = err == nil
		return err
	})
	return out, dispatch && err == nil, err
}
func (s *PgStore) FinishProjectEnvironmentCloneCheckpointPostgresCredential(ctx context.Context, l ProjectEnvironmentCloneLease, id string, o ProjectEnvironmentCloneCheckpointPostgresCredentialObservation, proof ProjectEnvironmentCloneCheckpointPostgresCredentialProof) error {
	if !validCloneCredentialSourceID(id) {
		return ErrInvalidArgument
	}
	return s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		in, err := readCheckpointPostgresCredentialTx(ctx, tx, op, id)
		if err != nil {
			return err
		}
		if err = validateCheckpointPostgresCredentialTx(ctx, tx, op, l, in, &proof); err != nil {
			return err
		}
		if err = validateCheckpointPostgresCredentialObservation(in, o, l.ExpiresAt); err != nil {
			return err
		}
		if in.State == "prepared" {
			return nil
		}
		count, err := sqlc.New().FinishCloneCheckpointPostgresCredential(ctx, tx, sqlc.FinishCloneCheckpointPostgresCredentialParams{OperationID: mustPgUUID(op.ID), SourceBindingID: mustPgUUID(id), ObservationSha256: checkpointPostgresCredentialSHA(o), ExpectedRevision: l.Operation.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		if count != 1 {
			return ErrConflict
		}
		return nil
	})
}
