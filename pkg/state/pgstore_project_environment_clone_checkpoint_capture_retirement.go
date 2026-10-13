package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneCheckpointCaptureRetirementStore = (*PgStore)(nil)

// Adopted object captures require original unused-reservation completion or the
// authenticated native target/copy retirement proof. PostgreSQL restore/isolation
// intents require target compensation and credential retirement proofs; copy
// readers/targets require their permanently fenced retirement proofs.
func checkpointCaptureRetirementPlanTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) (ProjectEnvironmentCloneCheckpointCaptureRetirementPlan, error) {
	var p ProjectEnvironmentCloneCheckpointCaptureRetirementPlan
	if len(op.Resources) < 1 || op.Resources[0].Kind != "data_checkpoint" {
		return p, ErrConflict
	}
	var err error
	p.SourceCompensation, err = readCheckpointSourceCompensationTx(ctx, tx, op)
	if errors.Is(err, ErrNotFound) {
		return p, ErrConflict
	}
	if err != nil {
		return p, err
	}
	if err = validateCheckpointCaptureRetirementPlan(p); err != nil {
		return p, err
	}
	if len(op.Resources) <= 2 {
		adopted, err := sqlc.New().CloneCheckpointCaptureRetirementHasAdoption(ctx, tx, mustPgUUID(op.ID))
		if err != nil {
			return p, mapErr(err)
		}
		if !adopted.Valid || adopted.Bool {
			return p, ErrConflict
		}
		return p, nil
	}
	return p, checkpointAdoptedCaptureReadyTx(ctx, tx, op, p)
}

func checkpointCaptureRetirementFromSQL(r sqlc.ProjectEnvironmentCloneCheckpointCaptureRetirement) (ProjectEnvironmentCloneCheckpointCaptureRetirement, error) {
	in := ProjectEnvironmentCloneCheckpointCaptureRetirement{State: r.State, RequestedAt: r.RequestedAt.Time.UTC(), ObservationSHA256: r.ObservationSha256.String}
	if len(r.Plan) > api.CheckpointCaptureRetirementMaxBytes || json.Unmarshal(r.Plan, &in.Plan) != nil || pgUUIDString(r.OperationID) != in.Plan.SourceCompensation.Plan.Capture.OperationID || validateCheckpointCaptureRetirementPlan(in.Plan) != nil || !checkpointObjectRetirementJournalShape(in.State, "retired", in.RequestedAt, in.ObservationSHA256) {
		return in, ErrConflict
	}
	if (in.State != "planned") != r.RequestedAt.Valid || (in.State == "retired") != r.RetiredAt.Valid || (in.State == "retired") != r.ObservationSha256.Valid || (in.State == "retired") != (len(r.Observation) > 0) {
		return in, ErrConflict
	}
	if in.State == "retired" && (len(r.Observation) > api.CheckpointCaptureRetirementMaxBytes || json.Unmarshal(r.Observation, &in.Observation) != nil || validateCheckpointCaptureRetirementObservation(in, in.Observation) != nil || !in.Observation.RetiredAt.Equal(r.RetiredAt.Time)) {
		return in, ErrConflict
	}
	return in, nil
}

func readCheckpointCaptureRetirementTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) (ProjectEnvironmentCloneCheckpointCaptureRetirement, error) {
	r, err := sqlc.New().ReadCloneCheckpointCaptureRetirement(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return ProjectEnvironmentCloneCheckpointCaptureRetirement{}, mapErr(err)
	}
	in, err := checkpointCaptureRetirementFromSQL(r)
	if err != nil {
		return in, err
	}
	p, err := checkpointCaptureRetirementPlanTx(ctx, tx, op)
	if err != nil {
		return in, err
	}
	if !reflect.DeepEqual(p, in.Plan) {
		return in, ErrConflict
	}
	return in, nil
}

func (s *PgStore) ReserveProjectEnvironmentCloneCheckpointCaptureRetirement(ctx context.Context, l ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointCaptureRetirement, error) {
	var out ProjectEnvironmentCloneCheckpointCaptureRetirement
	err := s.checkpointSourceCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointCaptureRetirementTx(ctx, tx, op)
		if err == nil || !errors.Is(err, ErrNotFound) {
			return err
		}
		p, err := checkpointCaptureRetirementPlanTx(ctx, tx, op)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(p)
		if err != nil {
			return ErrInvalidArgument
		}
		if len(raw) > api.CheckpointCaptureRetirementMaxBytes {
			return ErrQuotaExceeded
		}
		r, err := sqlc.New().InsertCloneCheckpointCaptureRetirement(ctx, tx, sqlc.InsertCloneCheckpointCaptureRetirementParams{OperationID: mustPgUUID(op.ID), Plan: raw, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointCaptureRetirementFromSQL(r)
		return err
	})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointCaptureRetirement{}, err
	}
	return out, nil
}

func (s *PgStore) ProjectEnvironmentCloneCheckpointCaptureRetirementForLease(ctx context.Context, l ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointCaptureRetirement, error) {
	var out ProjectEnvironmentCloneCheckpointCaptureRetirement
	err := s.checkpointSourceCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointCaptureRetirementTx(ctx, tx, op)
		return err
	})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointCaptureRetirement{}, err
	}
	return out, nil
}

func (s *PgStore) ClaimProjectEnvironmentCloneCheckpointCaptureRetirement(ctx context.Context, l ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointCaptureRetirement, bool, error) {
	var out ProjectEnvironmentCloneCheckpointCaptureRetirement
	dispatch := false
	err := s.checkpointSourceCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointCaptureRetirementTx(ctx, tx, op)
		if err != nil || out.State != "planned" {
			return err
		}
		r, err := sqlc.New().ClaimCloneCheckpointCaptureRetirement(ctx, tx, sqlc.ClaimCloneCheckpointCaptureRetirementParams{OperationID: mustPgUUID(op.ID), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointCaptureRetirementFromSQL(r)
		dispatch = err == nil
		return err
	})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointCaptureRetirement{}, false, err
	}
	return out, dispatch, nil
}

func (s *PgStore) FinishProjectEnvironmentCloneCheckpointCaptureRetirement(ctx context.Context, l ProjectEnvironmentCloneLease, o ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) error {
	return s.checkpointSourceCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		in, err := readCheckpointCaptureRetirementTx(ctx, tx, op)
		if err != nil {
			return err
		}
		if err = validateCheckpointCaptureRetirementObservation(in, o); err != nil {
			return err
		}
		if in.State == "retired" {
			return nil
		}
		raw, err := json.Marshal(o)
		if err != nil {
			return ErrInvalidArgument
		}
		if len(raw) > api.CheckpointCaptureRetirementMaxBytes {
			return ErrQuotaExceeded
		}
		q := sqlc.New()
		digest := cloneCheckpointSHA(o)
		if err = cloneCheckpointReleaseWriteResult(q.FinishCloneCheckpointCaptureRetirement(ctx, tx, sqlc.FinishCloneCheckpointCaptureRetirementParams{OperationID: mustPgUUID(op.ID), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token), Observation: raw, ObservationSha256: digest, RetiredAt: pgtype.Timestamptz{Time: o.RetiredAt, Valid: true}})); err != nil {
			return err
		}
		if len(op.Resources) > 2 {
			for _, source := range o.Sources {
				if err = cloneCheckpointReleaseWriteResult(q.InsertCloneCheckpointCaptureCatalogueRetirement(ctx, tx, sqlc.InsertCloneCheckpointCaptureCatalogueRetirementParams{OperationID: mustPgUUID(op.ID), SourceKind: source.Kind, SourceID: mustPgUUID(source.ID), CaptureIdentity: source.CaptureIdentity, SourceResourceID: source.SourceResourceID, ObservationSha256: digest, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})); err != nil {
					return err
				}
				if source.Kind == "managed_postgres" {
					if err = cloneCheckpointReleaseWriteResult(q.RetireCloneCheckpointCaptureSnapshot(ctx, tx, sqlc.RetireCloneCheckpointCaptureSnapshotParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(source.ID), ObservationSha256: digest, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})); err != nil {
						return err
					}
				}
			}
		}
		_, err = readCheckpointCaptureRetirementTx(ctx, tx, op)
		return err
	})
}
