package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementStore = (*PgStore)(nil)

func (s *PgStore) checkpointCredentialRetirementTx(ctx context.Context, l ProjectEnvironmentCloneLease, id string, work func(context.Context, pgx.Tx, ProjectEnvironmentCloneOperation, ProjectEnvironmentCloneCheckpointPostgresCredential) error) error {
	if !validCloneLeaseIdentity(l) || !validCloneCredentialSourceID(id) {
		return ErrInvalidArgument
	}
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := lockCloneWorkloadOperationTx(ctx, tx, l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID)
	if err != nil {
		return err
	}
	if op.Status != CloneOperationCompensating {
		return ErrConflict
	}
	if err = authorizeCloneObjectMutationTx(ctx, tx, op, &l); err != nil {
		return err
	}
	credential, err := readCheckpointPostgresCredentialTx(ctx, tx, op, id)
	if err != nil {
		return err
	}
	// Authenticate frozen provenance even after native fork deletion. Live health
	// and retained capture horizons are not prerequisites for revoking authority.
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return err
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return err
	}
	appID, source, err := capturedClonePostgresBinding(views, id)
	if err != nil {
		return err
	}
	runtime, err := readCheckpointPostgresRuntimeTx(ctx, tx, op, source.DatabaseID)
	if err != nil {
		return err
	}
	p := credential.Plan
	if runtime.State != "ready" || !reflect.DeepEqual(p.Runtime, runtime.Plan) || p.RuntimeSHA256 != runtime.ObservationSHA256 || p.DataResourceID != runtime.Observation.DataResourceID || p.AppID != appID || p.Scope != op.TargetEnvironment || p.EnvironmentKey != source.EnvironmentKey || p.Access != source.Access || p.CredentialGeneration != 1 || !validCloneCredentialSourceID(p.TargetBindingID) || p.TargetBindingID == id || p.ProviderIdentityID != "stage-"+p.TargetBindingID {
		return ErrConflict
	}
	published, err := sqlc.New().CloneCheckpointPostgresCredentialPublished(ctx, tx, mustPgUUID(p.TargetBindingID))
	if err != nil {
		return mapErr(err)
	}
	if !published.Valid || published.Bool {
		return ErrConflict
	}
	if err = work(ctx, tx, op, credential); err != nil {
		return err
	}
	if err = authorizeCloneObjectMutationTx(ctx, tx, op, &l); err != nil {
		return err
	}
	return mapErr(tx.Commit(ctx))
}
func checkpointCredentialRetirementPlanTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, in ProjectEnvironmentCloneCheckpointPostgresCredential) (ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementPlan, error) {
	p := ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementPlan{Credential: in.Plan, PreparationState: in.State, CiphertextSHA256: in.CiphertextSHA256, PreparationObservationSHA256: in.ObservationSHA256, PreparationRequestedAt: in.RequestedAt}
	activation, err := readCheckpointPgActivationTx(ctx, tx, op)
	if errors.Is(err, ErrNotFound) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	found := false
	for _, b := range activation.Plan.Bindings {
		if b.Connection.Credential.SourceBindingID == in.Plan.SourceBindingID {
			if found || !reflect.DeepEqual(b.Connection.Credential, in.Plan) || b.Connection.CredentialCiphertextSHA256 != in.CiphertextSHA256 || b.Connection.CredentialObservationSHA256 != in.ObservationSHA256 {
				return p, ErrConflict
			}
			found = true
		}
	}
	if !found {
		return p, ErrConflict
	}
	p.ActivationRequestID = ProjectEnvironmentCloneCheckpointPgActivationRequestID(activation.Plan)
	p.ActivationPlanSHA256 = cloneCheckpointSHA(activation.Plan)
	return p, nil
}
func checkpointCredentialRetirementFromSQL(r sqlc.ProjectEnvironmentCloneCheckpointPgCredentialRetirement, p ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementPlan) (ProjectEnvironmentCloneCheckpointPostgresCredentialRetirement, error) {
	in := ProjectEnvironmentCloneCheckpointPostgresCredentialRetirement{State: r.State, RequestedAt: r.RequestedAt.Time.UTC(), ObservationSHA256: r.ObservationSha256.String}
	if json.Unmarshal(r.Plan, &in.Plan) != nil || pgUUIDString(r.OperationID) != p.Credential.Runtime.Verification.Isolation.OperationID || pgUUIDString(r.SourceBindingID) != p.Credential.SourceBindingID || !reflect.DeepEqual(in.Plan, p) {
		return in, ErrConflict
	}
	if in.State == "retired" && (json.Unmarshal(r.Observation, &in.Observation) != nil || validateCheckpointPostgresCredentialRetirementObservation(in, in.Observation) != nil) {
		return in, ErrConflict
	}
	return in, nil
}
func readCheckpointCredentialRetirementTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, credential ProjectEnvironmentCloneCheckpointPostgresCredential) (ProjectEnvironmentCloneCheckpointPostgresCredentialRetirement, error) {
	r, err := sqlc.New().ReadCloneCheckpointPostgresCredentialRetirement(ctx, tx, sqlc.ReadCloneCheckpointPostgresCredentialRetirementParams{OperationID: mustPgUUID(op.ID), SourceBindingID: mustPgUUID(credential.Plan.SourceBindingID)})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointPostgresCredentialRetirement{}, mapErr(err)
	}
	p, err := checkpointCredentialRetirementPlanTx(ctx, tx, op, credential)
	if err != nil {
		return ProjectEnvironmentCloneCheckpointPostgresCredentialRetirement{}, err
	}
	return checkpointCredentialRetirementFromSQL(r, p)
}
func (s *PgStore) ReserveProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(ctx context.Context, l ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentCloneCheckpointPostgresCredentialRetirement, error) {
	var out ProjectEnvironmentCloneCheckpointPostgresCredentialRetirement
	err := s.checkpointCredentialRetirementTx(ctx, l, id, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, c ProjectEnvironmentCloneCheckpointPostgresCredential) error {
		var err error
		out, err = readCheckpointCredentialRetirementTx(ctx, tx, op, c)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		p, err := checkpointCredentialRetirementPlanTx(ctx, tx, op, c)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(p)
		if err != nil {
			return err
		}
		row, err := sqlc.New().InsertCloneCheckpointPostgresCredentialRetirement(ctx, tx, sqlc.InsertCloneCheckpointPostgresCredentialRetirementParams{OperationID: mustPgUUID(op.ID), SourceBindingID: mustPgUUID(id), Plan: encoded, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointCredentialRetirementFromSQL(row, p)
		return err
	})
	return out, err
}
func (s *PgStore) ClaimProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(ctx context.Context, l ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentCloneCheckpointPostgresCredentialRetirement, bool, error) {
	var out ProjectEnvironmentCloneCheckpointPostgresCredentialRetirement
	dispatch := false
	err := s.checkpointCredentialRetirementTx(ctx, l, id, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, c ProjectEnvironmentCloneCheckpointPostgresCredential) error {
		var err error
		out, err = readCheckpointCredentialRetirementTx(ctx, tx, op, c)
		if err != nil {
			return err
		}
		if out.State != "planned" {
			return nil
		}
		row, err := sqlc.New().ClaimCloneCheckpointPostgresCredentialRetirement(ctx, tx, sqlc.ClaimCloneCheckpointPostgresCredentialRetirementParams{OperationID: mustPgUUID(op.ID), SourceBindingID: mustPgUUID(id), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointCredentialRetirementFromSQL(row, out.Plan)
		dispatch = err == nil
		return err
	})
	return out, dispatch && err == nil, err
}
func (s *PgStore) FinishProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(ctx context.Context, l ProjectEnvironmentCloneLease, id string, o ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation) error {
	return s.checkpointCredentialRetirementTx(ctx, l, id, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, c ProjectEnvironmentCloneCheckpointPostgresCredential) error {
		in, err := readCheckpointCredentialRetirementTx(ctx, tx, op, c)
		if err != nil {
			return err
		}
		if err = validateCheckpointPostgresCredentialRetirementObservation(in, o); err != nil {
			return err
		}
		if in.State == "retired" {
			return nil
		}
		encoded, err := json.Marshal(o)
		if err != nil {
			return err
		}
		n, err := sqlc.New().FinishCloneCheckpointPostgresCredentialRetirement(ctx, tx, sqlc.FinishCloneCheckpointPostgresCredentialRetirementParams{OperationID: mustPgUUID(op.ID), SourceBindingID: mustPgUUID(id), Observation: encoded, ObservationSha256: cloneCheckpointSHA(o), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
}
