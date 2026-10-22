package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneCheckpointPgActivationStore = (*PgStore)(nil)

func checkpointPgActivationFromSQL(r sqlc.ProjectEnvironmentCloneCheckpointPgActivation, op ProjectEnvironmentCloneOperation) (ProjectEnvironmentCloneCheckpointPgActivation, error) {
	in := ProjectEnvironmentCloneCheckpointPgActivation{State: r.State, RequestedAt: r.RequestedAt.Time.UTC(), ObservationSHA256: r.ObservationSha256.String}
	if json.Unmarshal(r.Plan, &in.Plan) != nil || pgUUIDString(r.OperationID) != op.ID || in.Plan.OperationID != op.ID || in.Plan.AccountID != op.AccountID || in.Plan.ProjectID != op.ProjectID || in.Plan.Scope != op.TargetEnvironment || in.Plan.SourceRevisionHash != op.SourceRevisionHash || len(in.Plan.Bindings) > api.PostgresCheckpointCredentialsPerAccountMax {
		return in, ErrConflict
	}
	if in.State == "active" && (json.Unmarshal(r.Observation, &in.Observation) != nil || validateCheckpointPgActivationObservation(in, in.Observation, in.Observation.HealthyThrough) != nil) {
		return in, ErrConflict
	}
	return in, nil
}
func readCheckpointPgActivationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) (ProjectEnvironmentCloneCheckpointPgActivation, error) {
	row, err := sqlc.New().ReadCloneCheckpointPgActivation(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return ProjectEnvironmentCloneCheckpointPgActivation{}, mapErr(err)
	}
	in, err := checkpointPgActivationFromSQL(row, op)
	if err != nil || len(in.Plan.Bindings) != 0 {
		return in, err
	}
	// An empty journal is authority only for an originally empty frozen roster.
	// Check it here as well as at reservation: withdrawal uses static replay.
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return in, err
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return in, err
	}
	for _, view := range views {
		if len(view.Postgres) != 0 {
			return in, ErrConflict
		}
	}
	return in, nil
}
func checkpointPgActivationPlanTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, l ProjectEnvironmentCloneLease, proofs []ProjectEnvironmentCloneCheckpointPgConnectionProof, attached, publishedOwned bool) (ProjectEnvironmentCloneCheckpointPgActivationPlan, error) {
	out := ProjectEnvironmentCloneCheckpointPgActivationPlan{OperationID: op.ID, AccountID: op.AccountID, ProjectID: op.ProjectID, Scope: op.TargetEnvironment, SourceRevisionHash: op.SourceRevisionHash}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return out, err
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return out, err
	}
	ids := []string{}
	for _, view := range views {
		for _, b := range view.Postgres {
			ids = append(ids, b.ID)
		}
	}
	sort.Strings(ids)
	if len(ids) > api.PostgresCheckpointCredentialsPerAccountMax || proofs != nil && len(proofs) != len(ids) {
		return out, ErrConflict
	}
	byID := map[string]ProjectEnvironmentCloneCheckpointPgConnectionProof{}
	for _, proof := range proofs {
		id := proof.Credential.RequestID
		if _, exists := byID[id]; exists {
			return out, ErrConflict
		}
		byID[id] = proof
	}
	q := sqlc.New()
	for _, id := range ids {
		c, err := readCheckpointPostgresCredentialTx(ctx, tx, op, id)
		if err != nil {
			return out, err
		}
		if c.State != "prepared" {
			return out, ErrConflict
		}
		if err = validateCheckpointPostgresCredentialTx(ctx, tx, op, l, c, nil); err != nil {
			return out, err
		}
		r, err := q.ReadCloneCheckpointPgConnectionValue(ctx, tx, sqlc.ReadCloneCheckpointPgConnectionValueParams{OperationID: mustPgUUID(op.ID), SourceBindingID: mustPgUUID(id)})
		if err != nil {
			return out, mapErr(err)
		}
		var p ProjectEnvironmentCloneCheckpointPgConnectionPlan
		if json.Unmarshal(r.Plan, &p) != nil || !reflect.DeepEqual(p.Credential, c.Plan) || p.CredentialObservationSHA256 != c.ObservationSHA256 || p.CredentialCiphertextSHA256 != c.CiphertextSHA256 {
			return out, ErrConflict
		}
		value, err := checkpointPgConnectionFromSQL(r, p)
		if err != nil {
			return out, err
		}
		if value.State != "sealed" || attached && pgUUIDString(r.ActivationOperationID) != op.ID || !attached && r.ActivationOperationID.Valid {
			return out, ErrConflict
		}
		published, err := q.CloneCheckpointPostgresCredentialPublished(ctx, tx, mustPgUUID(c.Plan.TargetBindingID))
		if err != nil {
			return out, mapErr(err)
		}
		if !published.Valid || published.Bool && !publishedOwned {
			return out, ErrConflict
		}
		if proofs != nil {
			proof, ok := byID[ProjectEnvironmentCloneCheckpointPostgresCredentialRequestID(c.Plan)]
			if !ok {
				return out, ErrConflict
			}
			fresh, err := checkpointPgConnectionPlanTx(ctx, tx, op, l, id, proof)
			if err != nil {
				return out, err
			}
			if !reflect.DeepEqual(fresh, p) {
				return out, ErrConflict
			}
		}
		out.Bindings = append(out.Bindings, ProjectEnvironmentCloneCheckpointPgActivationBinding{Connection: p, ConnectionKeyID: value.KeyID, ConnectionCiphertextSHA256: value.CiphertextSHA256})
	}
	return out, nil
}
func validateCheckpointPgActivationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, l ProjectEnvironmentCloneLease, in ProjectEnvironmentCloneCheckpointPgActivation, proofs []ProjectEnvironmentCloneCheckpointPgConnectionProof) error {
	owned, err := checkpointPgPublicationOwnedTx(ctx, tx, op)
	if err != nil {
		return err
	}
	p, err := checkpointPgActivationPlanTx(ctx, tx, op, l, proofs, true, owned)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(p, in.Plan) {
		return ErrConflict
	}
	return nil
}
func (s *PgStore) ProjectEnvironmentCloneCheckpointPgActivationForLease(ctx context.Context, l ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointPgActivation, error) {
	var out ProjectEnvironmentCloneCheckpointPgActivation
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointPgActivationTx(ctx, tx, op)
		if err != nil {
			return err
		}
		return validateCheckpointPgActivationTx(ctx, tx, op, l, out, nil)
	})
	return out, err
}
func (s *PgStore) ReserveProjectEnvironmentCloneCheckpointPgActivation(ctx context.Context, l ProjectEnvironmentCloneLease, proofs []ProjectEnvironmentCloneCheckpointPgConnectionProof) (ProjectEnvironmentCloneCheckpointPgActivation, error) {
	var out ProjectEnvironmentCloneCheckpointPgActivation
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointPgActivationTx(ctx, tx, op)
		if err == nil {
			if out.State != "planned" {
				return validateCheckpointPgActivationTx(ctx, tx, op, l, out, nil)
			}
			if proofs == nil {
				return ErrConflict
			}
			return validateCheckpointPgActivationTx(ctx, tx, op, l, out, proofs)
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		if proofs == nil {
			return ErrConflict
		}
		p, err := checkpointPgActivationPlanTx(ctx, tx, op, l, proofs, false, false)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(p)
		if err != nil {
			return err
		}
		if len(encoded) > api.PostgresCheckpointActivationPlanMaxBytes {
			return ErrQuotaExceeded
		}
		q := sqlc.New()
		row, err := q.InsertCloneCheckpointPgActivation(ctx, tx, sqlc.InsertCloneCheckpointPgActivationParams{OperationID: mustPgUUID(op.ID), Plan: encoded, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		for _, b := range p.Bindings {
			n, err := q.AttachCloneCheckpointPgActivationConnection(ctx, tx, sqlc.AttachCloneCheckpointPgActivationConnectionParams{OperationID: mustPgUUID(op.ID), SourceBindingID: mustPgUUID(b.Connection.Credential.SourceBindingID), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
			if err != nil {
				return mapErr(err)
			}
			if n != 1 {
				return ErrConflict
			}
		}
		out, err = checkpointPgActivationFromSQL(row, op)
		return err
	})
	return out, err
}
func (s *PgStore) ClaimProjectEnvironmentCloneCheckpointPgActivation(ctx context.Context, l ProjectEnvironmentCloneLease, proofs []ProjectEnvironmentCloneCheckpointPgConnectionProof) (ProjectEnvironmentCloneCheckpointPgActivation, bool, error) {
	var out ProjectEnvironmentCloneCheckpointPgActivation
	dispatch := false
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointPgActivationTx(ctx, tx, op)
		if err != nil {
			return err
		}
		if out.State != "planned" {
			return validateCheckpointPgActivationTx(ctx, tx, op, l, out, nil)
		}
		if proofs == nil {
			return ErrConflict
		}
		if err = validateCheckpointPgActivationTx(ctx, tx, op, l, out, proofs); err != nil {
			return err
		}
		row, err := sqlc.New().ClaimCloneCheckpointPgActivation(ctx, tx, sqlc.ClaimCloneCheckpointPgActivationParams{OperationID: mustPgUUID(op.ID), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointPgActivationFromSQL(row, op)
		dispatch = err == nil
		return err
	})
	return out, dispatch && err == nil, err
}
func (s *PgStore) FinishProjectEnvironmentCloneCheckpointPgActivation(ctx context.Context, l ProjectEnvironmentCloneLease, o ProjectEnvironmentCloneCheckpointPgActivationObservation, retention ProjectEnvironmentCloneCheckpointReleaseObservation) error {
	return s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		in, err := readCheckpointPgActivationTx(ctx, tx, op)
		if err != nil {
			return err
		}
		if err = validateCheckpointPgActivationTx(ctx, tx, op, l, in, nil); err != nil {
			return err
		}
		if err = validateCheckpointPgActivationObservation(in, o, l.ExpiresAt); err != nil {
			return err
		}
		release, err := readCloneCheckpointReleaseTx(ctx, tx, op)
		if err != nil {
			return err
		}
		if err = validateCloneCheckpointReleaseObservation(release, retention, l.ExpiresAt); err != nil {
			return err
		}
		if in.State == "active" {
			return nil
		}
		encoded, err := json.Marshal(o)
		if err != nil {
			return err
		}
		n, err := sqlc.New().FinishCloneCheckpointPgActivation(ctx, tx, sqlc.FinishCloneCheckpointPgActivationParams{OperationID: mustPgUUID(op.ID), Observation: encoded, ObservationSha256: checkpointPgActivationObservationSHA(o), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
}
