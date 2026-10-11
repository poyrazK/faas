package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneCheckpointWorkloadRetirementStore = (*PgStore)(nil)

func checkpointWorkloadRetirementPlanTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) (ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan, error) {
	var p ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan
	// Published releases require a separate authenticated withdrawal protocol.
	if op.TargetReleaseSetID != "" {
		return p, ErrConflict
	}
	source, err := readCheckpointSourceCompensationTx(ctx, tx, op)
	if err != nil {
		return p, checkpointPgCompensationDependencyError(err)
	}
	if source.State != "compensated" {
		return p, ErrConflict
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return p, err
	}
	p = ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan{OperationID: op.ID, AccountID: op.AccountID, ProjectID: op.ProjectID, SourceEnvironment: op.SourceEnvironment, TargetEnvironment: op.TargetEnvironment, SourceRevisionHash: op.SourceRevisionHash, SourceCompensationSHA256: source.ObservationSHA256, SourceCompensatedAt: source.Observation.CompensatedAt, Workloads: []ProjectEnvironmentCloneCheckpointRetirementWorkload{}}
	heads, err := checkpointWorkloadRetirementMaterializationTx(ctx, tx, op, &p)
	if err != nil {
		return p, err
	}
	var targets []projectCloneWorkloadRecord
	for _, r := range records {
		w := ProjectEnvironmentCloneCheckpointRetirementWorkload{AppID: r.AppID, SourceDeploymentID: r.SourceDeploymentID, SourceHash: r.SourceHash, TargetDeploymentID: r.TargetDeploymentID, TargetSettingsHash: r.TargetSettingsHash}
		if p.EnvironmentID != "" {
			head, ok := heads[r.AppID]
			if !ok {
				return p, ErrConflict
			}
			w.SpecID, w.SpecHash = head.SpecID, head.Hash
		}
		p.Workloads = append(p.Workloads, w)
		if r.TargetDeploymentID != "" {
			targets = append(targets, r)
		}
	}
	if p.EnvironmentID != "" && len(heads) != len(records) {
		return p, ErrConflict
	}
	if err = validateCheckpointWorkloadRetirementPlan(p); err != nil {
		return p, err
	}
	return p, checkpointWorkloadRetirementDeploymentsTx(ctx, tx, op, p, targets)
}

func checkpointWorkloadRetirementMaterializationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, p *ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) (map[string]projectCloneMaterializedWorkload, error) {
	row, err := sqlc.New().ReadCloneCheckpointWorkloadRetirementMaterialization(ctx, tx, mustPgUUID(op.ID))
	if errors.Is(mapErr(err), ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, mapErr(err)
	}
	env, err := replayCheckpointWorkloadMaterializationTx(ctx, tx, op, row)
	if err != nil {
		return nil, checkpointPgCompensationDependencyError(err)
	}
	var heads map[string]projectCloneMaterializedWorkload
	if err = json.Unmarshal(row.WorkloadSettings, &heads); err != nil {
		return nil, ErrConflict
	}
	ownership, err := verifyCloneConfigurationOwnershipTx(ctx, tx, op, env.ID)
	if err != nil {
		return nil, err
	}
	p.EnvironmentID = env.ID
	p.MaterializationSHA256 = cloneCheckpointSHA(struct {
		EnvironmentID          string
		WorkloadSettings       json.RawMessage
		CreatedAt              time.Time
		ConfigurationOwnership string `json:",omitempty"`
	}{env.ID, row.WorkloadSettings, row.CreatedAt.Time.UTC(), ownership})
	return heads, nil
}

func checkpointWorkloadRetirementDeploymentsTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, p ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan, records []projectCloneWorkloadRecord) error {
	orphan, err := sqlc.New().CloneCheckpointWorkloadRetirementHasOrphanDeployments(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return mapErr(err)
	}
	if orphan {
		return ErrConflict
	}
	removed, err := verifyCloneDeploymentCleanupTx(ctx, tx, op, p)
	if err != nil || removed {
		return err
	}
	_, cleanupErr := sqlc.New().ReadCloneWorkloadCleanup(ctx, tx, mustPgUUID(op.ID))
	if cleanupErr != nil && !errors.Is(mapErr(cleanupErr), ErrNotFound) {
		return mapErr(cleanupErr)
	}
	proofs, err := cloneWorkloadProofsWithRetiredSettingsTx(ctx, tx, op, records, cleanupErr == nil)
	if err != nil {
		return checkpointPgCompensationDependencyError(err)
	}
	for i, r := range records {
		d, err := deploymentByIDDB(ctx, tx, r.TargetDeploymentID)
		if err != nil {
			return checkpointPgCompensationDependencyError(err)
		}
		proof := proofs[i]
		if d.AppID != r.AppID || d.Scope != op.TargetEnvironment || d.Reason != "environment-clone:"+op.ID || proof.artifactHash != r.SourceHash || proof.headHash != r.TargetSettingsHash || proof.pinHash != r.TargetSettingsHash {
			return ErrConflict
		}
	}
	return nil
}

func checkpointWorkloadRetirementFromSQL(row sqlc.ProjectEnvironmentCloneCheckpointWorkloadRetirement) (ProjectEnvironmentCloneCheckpointWorkloadRetirement, error) {
	in := ProjectEnvironmentCloneCheckpointWorkloadRetirement{State: row.State, RequestedAt: row.RequestedAt.Time.UTC(), ObservationSHA256: row.ObservationSha256.String}
	if len(row.Plan) > api.CheckpointWorkloadRetirementMaxBytes || json.Unmarshal(row.Plan, &in.Plan) != nil || pgUUIDString(row.OperationID) != in.Plan.OperationID || validateCheckpointWorkloadRetirementPlan(in.Plan) != nil || !checkpointObjectRetirementJournalShape(in.State, "retired", in.RequestedAt, in.ObservationSHA256) {
		return in, ErrConflict
	}
	if (in.State != "planned") != row.RequestedAt.Valid || (in.State == "retired") != row.RetiredAt.Valid || (in.State == "retired") != row.ObservationSha256.Valid || (in.State == "retired") != (len(row.Observation) > 0) {
		return in, ErrConflict
	}
	if in.State == "retired" && (len(row.Observation) > api.CheckpointWorkloadRetirementMaxBytes || json.Unmarshal(row.Observation, &in.Observation) != nil || validateCheckpointWorkloadRetirementObservation(in, in.Observation) != nil || !in.Observation.RetiredAt.Equal(row.RetiredAt.Time)) {
		return in, ErrConflict
	}
	return in, nil
}

func readCheckpointWorkloadRetirementTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) (ProjectEnvironmentCloneCheckpointWorkloadRetirement, error) {
	row, err := sqlc.New().ReadCloneCheckpointWorkloadRetirement(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return ProjectEnvironmentCloneCheckpointWorkloadRetirement{}, mapErr(err)
	}
	in, err := checkpointWorkloadRetirementFromSQL(row)
	if err != nil {
		return in, err
	}
	p, err := checkpointWorkloadRetirementPlanTx(ctx, tx, op)
	if err == nil && !reflect.DeepEqual(in.Plan, p) {
		err = ErrConflict
	}
	if err == nil && in.State == "retired" {
		err = checkpointWorkloadSchedulerClosedTx(ctx, tx, op.ID)
	}
	return in, err
}

func checkpointWorkloadSchedulerClosedTx(ctx context.Context, tx pgx.Tx, operationID string) error {
	closed, err := sqlc.New().CloneCheckpointWorkloadSchedulerClosed(ctx, tx, mustPgUUID(operationID))
	if err != nil {
		return mapErr(err)
	}
	if !closed.Valid || !closed.Bool {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx context.Context, l ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointWorkloadRetirement, error) {
	var out ProjectEnvironmentCloneCheckpointWorkloadRetirement
	err := s.checkpointSourceCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointWorkloadRetirementTx(ctx, tx, op)
		if err == nil || !errors.Is(err, ErrNotFound) {
			return err
		}
		p, err := checkpointWorkloadRetirementPlanTx(ctx, tx, op)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(p)
		if err != nil {
			return err
		}
		row, err := sqlc.New().InsertCloneCheckpointWorkloadRetirement(ctx, tx, sqlc.InsertCloneCheckpointWorkloadRetirementParams{OperationID: mustPgUUID(op.ID), Plan: raw, ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointWorkloadRetirementFromSQL(row)
		return err
	})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointWorkloadRetirement{}, err
	}
	return out, nil
}
func (s *PgStore) ProjectEnvironmentCloneCheckpointWorkloadRetirementForLease(ctx context.Context, l ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointWorkloadRetirement, error) {
	var out ProjectEnvironmentCloneCheckpointWorkloadRetirement
	err := s.checkpointSourceCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out, err = readCheckpointWorkloadRetirementTx(ctx, tx, op)
		return err
	})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointWorkloadRetirement{}, err
	}
	return out, nil
}
func (s *PgStore) ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx context.Context, l ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointWorkloadRetirement, bool, error) {
	var out ProjectEnvironmentCloneCheckpointWorkloadRetirement
	dispatch := false
	err := s.checkpointSourceCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		// Serialize catalogue admissions before authenticating the original plan.
		// The durable requested journal then rejects delayed writers after commit.
		if err := sqlc.New().LockCloneCheckpointSchedulerAdmission(ctx, tx); err != nil {
			return mapErr(err)
		}
		var err error
		out, err = readCheckpointWorkloadRetirementTx(ctx, tx, op)
		if err != nil || out.State != "planned" {
			return err
		}
		row, err := sqlc.New().ClaimCloneCheckpointWorkloadRetirement(ctx, tx, sqlc.ClaimCloneCheckpointWorkloadRetirementParams{OperationID: mustPgUUID(op.ID), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		out, err = checkpointWorkloadRetirementFromSQL(row)
		dispatch = err == nil
		return err
	})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointWorkloadRetirement{}, false, err
	}
	return out, dispatch, nil
}
func (s *PgStore) FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx context.Context, l ProjectEnvironmentCloneLease, o ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) error {
	return s.checkpointSourceCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		// Hold all admission/dispatch writer fences through receipt acceptance.
		if err := sqlc.New().LockCloneCheckpointSchedulerAdmission(ctx, tx); err != nil {
			return mapErr(err)
		}
		in, err := readCheckpointWorkloadRetirementTx(ctx, tx, op)
		if err != nil {
			return err
		}
		if err = validateCheckpointWorkloadRetirementObservation(in, o); err != nil {
			return err
		}
		if err = checkpointWorkloadSchedulerClosedTx(ctx, tx, op.ID); err != nil {
			return err
		}
		if in.State == "retired" {
			return nil
		}
		raw, err := json.Marshal(o)
		if err != nil {
			return err
		}
		q := sqlc.New()
		if err = cloneCheckpointReleaseWriteResult(q.FinishCloneCheckpointWorkloadRetirement(ctx, tx, sqlc.FinishCloneCheckpointWorkloadRetirementParams{OperationID: mustPgUUID(op.ID), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token), Observation: raw, ObservationSha256: cloneCheckpointSHA(o), RetiredAt: pgtype.Timestamptz{Time: o.RetiredAt, Valid: true}})); err != nil {
			return err
		}
		_, err = readCheckpointWorkloadRetirementTx(ctx, tx, op)
		return err
	})
}

// Only native retirement replay can use the original materialization after
// deletion. Configuration ownership verification authenticates the complete
// cleanup chain and exact absence before saved spec identities are used.
func replayCheckpointWorkloadMaterializationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, row sqlc.ProjectEnvironmentCloneMaterialization) (ProjectEnvironment, error) {
	_, err := sqlc.New().ReadCloneWorkloadCleanup(ctx, tx, mustPgUUID(op.ID))
	if errors.Is(mapErr(err), ErrNotFound) {
		return replayCloneMaterializationTx(ctx, tx, op)
	}
	if err != nil {
		return ProjectEnvironment{}, mapErr(err)
	}
	_, err = sqlc.New().ReadCloneEnvironmentCleanup(ctx, tx, mustPgUUID(op.ID))
	if err == nil {
		// The caller verifies the complete ownership/cleanup chain and exact
		// absence before reconstructing the original native plan from this ID.
		return ProjectEnvironment{ID: pgUUIDString(row.EnvironmentID)}, nil
	}
	if !errors.Is(mapErr(err), ErrNotFound) {
		return ProjectEnvironment{}, mapErr(err)
	}

	env, err := sqlc.New().LockProjectEnvironmentCloneMaterializedEnvironment(ctx, tx, sqlc.LockProjectEnvironmentCloneMaterializedEnvironmentParams{ID: row.EnvironmentID, AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID), Slug: op.TargetEnvironment})
	if err != nil {
		return ProjectEnvironment{}, checkpointPgCompensationDependencyError(mapErr(err))
	}
	if env.Protected {
		return ProjectEnvironment{}, ErrConflict
	}
	return ProjectEnvironment{ID: pgUUIDString(env.ID)}, nil
}
