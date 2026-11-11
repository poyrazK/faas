package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/storedworkflowlive"
)

func liveRecordFromSQL(row sqlc.StoredWorkflowLiveVerification) (storedWorkflowLiveRecord, error) {
	r := storedWorkflowLiveRecord{AppID: pgUUIDString(row.AppID), Name: row.Name, Key: row.IdempotencyKey, Fingerprint: row.RequestFingerprint, Test: row.LiveTest,
		View: api.StoredWorkflowLiveVerification{RunID: pgUUIDString(row.RunID), CaseName: row.CaseName, DeploymentID: pgUUIDString(row.DeploymentID),
			Version: row.WorkflowVersion, PublishedVersion: row.PublishedVersion, InputsHash: row.InputsHash, CreatedAt: timeFromPgtype(row.CreatedAt)}}
	if len(row.Report) > 0 {
		r.View.Report = new(api.StoredWorkflowLiveReport)
		if err := json.Unmarshal(row.Report, r.View.Report); err != nil {
			return r, err
		}
	}
	return r, nil
}

func liveCurrentSQL(ctx context.Context, db sqlc.DBTX, appID, name string) (StoredWorkflow, string, error) {
	q := sqlc.New()
	row, err := q.GetStoredWorkflowForLiveVerification(ctx, db, sqlc.GetStoredWorkflowForLiveVerificationParams{AppID: mustPgUUID(appID), Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredWorkflow{}, "", ErrNotFound
	}
	if err != nil {
		return StoredWorkflow{}, "", err
	}
	dep, err := q.StoredWorkflowLiveDeployment(ctx, db, mustPgUUID(appID))
	if errors.Is(err, pgx.ErrNoRows) {
		return storedWorkflowFromSQL(row), "", nil
	}
	return storedWorkflowFromSQL(row), pgUUIDString(dep), err
}

func liveViewSQL(ctx context.Context, db sqlc.DBTX, row sqlc.StoredWorkflowLiveVerification, current StoredWorkflow, deploymentID string) (api.StoredWorkflowLiveVerification, error) {
	live, err := liveRecordFromSQL(row)
	if err != nil {
		return api.StoredWorkflowLiveVerification{}, err
	}
	run, err := sqlc.New().StoredWorkflowLiveRunState(ctx, db, row.RunID)
	if err != nil {
		return api.StoredWorkflowLiveVerification{}, err
	}
	return liveView(live, run.Status, timeFromPgtype(run.UpdatedAt), int(run.ResumeCount), current, deploymentID), nil
}

func (s *PgStore) StartStoredWorkflowLiveVerification(ctx context.Context, appID, name, key string, req api.StartStoredWorkflowLiveVerificationRequest, runtimeEnabled bool) (api.StoredWorkflowLiveVerification, bool, error) {
	fingerprint, err := validateLiveStart(&req, key)
	if err != nil {
		return api.StoredWorkflowLiveVerification{}, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.StoredWorkflowLiveVerification{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return api.StoredWorkflowLiveVerification{}, false, err
	}
	prior, err := q.GetStoredWorkflowLiveVerificationByKey(ctx, tx, sqlc.GetStoredWorkflowLiveVerificationByKeyParams{AppID: mustPgUUID(appID), Name: name, IdempotencyKey: key})
	if err == nil {
		if !bytes.Equal(prior.RequestFingerprint, fingerprint) {
			return api.StoredWorkflowLiveVerification{}, false, ErrWorkflowRunIdempotencyConflict
		}
		current, dep, err := liveCurrentSQL(ctx, tx, appID, name)
		if err != nil {
			return api.StoredWorkflowLiveVerification{}, false, err
		}
		view, err := liveViewSQL(ctx, tx, prior, current, dep)
		return view, true, err // Read-only replay; deferred rollback releases locks.
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return api.StoredWorkflowLiveVerification{}, false, err
	}
	row, err := startLiveVerificationTx(ctx, tx, appID, name, key, req, fingerprint, runtimeEnabled)
	if err != nil {
		return api.StoredWorkflowLiveVerification{}, false, err
	}
	current, dep, err := liveCurrentSQL(ctx, tx, appID, name)
	if err != nil {
		return api.StoredWorkflowLiveVerification{}, false, err
	}
	view, err := liveViewSQL(ctx, tx, row, current, dep)
	if err != nil {
		return api.StoredWorkflowLiveVerification{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.StoredWorkflowLiveVerification{}, false, err
	}
	return view, false, nil
}

func startLiveVerificationTx(ctx context.Context, tx pgx.Tx, appID, name, key string, req api.StartStoredWorkflowLiveVerificationRequest, fingerprint []byte, runtimeEnabled bool) (sqlc.StoredWorkflowLiveVerification, error) {
	q := sqlc.New()
	target, err := q.LockEventWorkflowTarget(ctx, tx, mustPgUUID(appID))
	if errors.Is(err, pgx.ErrNoRows) || target.AppStatus == string(AppDeleted) {
		return sqlc.StoredWorkflowLiveVerification{}, ErrNotFound
	}
	if err != nil {
		return sqlc.StoredWorkflowLiveVerification{}, err
	}
	plan := api.Plan(target.Plan)
	if err := storedWorkflowAccountGate(plan, (target.AccountStatus == "active" || target.AccountStatus == "past_due") && !target.AbuseHoldAt.Valid, "tests"); err != nil {
		return sqlc.StoredWorkflowLiveVerification{}, err
	}
	if target.PlatformTenantRequired {
		return sqlc.StoredWorkflowLiveVerification{}, api.ErrWorkflowTenantIdentityUnavailable()
	}
	if !runtimeEnabled || target.MaintenanceMode {
		return sqlc.StoredWorkflowLiveVerification{}, ErrWorkflowEventTargetUnavailable
	}
	code, err := q.LockWorkflowScheduleTarget(ctx, tx, mustPgUUID(appID))
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.StoredWorkflowLiveVerification{}, ErrWorkflowEventTargetUnavailable
	}
	if err != nil {
		return sqlc.StoredWorkflowLiveVerification{}, err
	}
	storedWorkflow, err := q.GetStoredWorkflowForLiveVerification(ctx, tx, sqlc.GetStoredWorkflowForLiveVerificationParams{AppID: mustPgUUID(appID), Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.StoredWorkflowLiveVerification{}, ErrNotFound
	}
	if err != nil {
		return sqlc.StoredWorkflowLiveVerification{}, err
	}
	run, live, err := prepareLiveVerification(appID, name, key, pgUUIDString(code.DeploymentID), storedWorkflowFromSQL(storedWorkflow), req, fingerprint)
	if err != nil {
		return sqlc.StoredWorkflowLiveVerification{}, err
	}
	var def api.WorkflowSpec
	if err := json.Unmarshal(run.DefinitionSnapshot, &def); err != nil {
		return sqlc.StoredWorkflowLiveVerification{}, err
	}
	if err := validateWorkflowOutboundTx(ctx, tx, appID, pgUUIDString(target.AccountID), def); err != nil {
		return sqlc.StoredWorkflowLiveVerification{}, err
	}
	active, err := q.CountActiveWorkflowRunsForAdmission(ctx, tx, sqlc.CountActiveWorkflowRunsForAdmissionParams{AppID: mustPgUUID(appID), WorkflowName: "", ParkedMax: int64(plan.WorkflowMaxParkedRuns())})
	if err != nil {
		return sqlc.StoredWorkflowLiveVerification{}, err
	}
	if active >= int64(plan.WorkflowMaxConcurrentRuns()) {
		return sqlc.StoredWorkflowLiveVerification{}, api.ErrPlanWorkflowsQuota(plan, plan.WorkflowMaxConcurrentRuns(), int(active))
	}
	if err := insertWorkflowRun(ctx, tx, run); err != nil {
		return sqlc.StoredWorkflowLiveVerification{}, err
	}
	return q.InsertStoredWorkflowLiveVerification(ctx, tx, sqlc.InsertStoredWorkflowLiveVerificationParams{RunID: mustPgUUID(run.ID), AppID: mustPgUUID(appID), Name: name,
		CaseName: req.CaseName, WorkflowVersion: live.View.Version, PublishedVersion: live.View.PublishedVersion, DeploymentID: code.DeploymentID,
		InputsHash: live.View.InputsHash, LiveTest: live.Test, IdempotencyKey: key, RequestFingerprint: fingerprint})
}

func (s *PgStore) ListStoredWorkflowLiveVerifications(ctx context.Context, appID, name string) (api.ListStoredWorkflowLiveVerificationsResponse, error) {
	current, dep, err := liveCurrentSQL(ctx, s.pool, appID, name)
	if err != nil {
		return api.ListStoredWorkflowLiveVerificationsResponse{}, err
	}
	rows, err := sqlc.New().ListStoredWorkflowLiveVerifications(ctx, s.pool, sqlc.ListStoredWorkflowLiveVerificationsParams{AppID: mustPgUUID(appID), Name: name, Limit: api.StoredWorkflowLiveHistoryLimit})
	if err != nil {
		return api.ListStoredWorkflowLiveVerificationsResponse{}, err
	}
	result := api.ListStoredWorkflowLiveVerificationsResponse{DeploymentID: dep, PublishedVersion: current.PublishedVersion, Verifications: []api.StoredWorkflowLiveVerification{}}
	for _, row := range rows {
		view, err := liveViewSQL(ctx, s.pool, row, current, dep)
		if err != nil {
			return api.ListStoredWorkflowLiveVerificationsResponse{}, err
		}
		result.Verifications = append(result.Verifications, view)
	}
	return result, nil
}

func (s *PgStore) GetStoredWorkflowLiveVerification(ctx context.Context, appID, name, runID string) (api.StoredWorkflowLiveVerification, error) {
	if _, err := uuid.Parse(runID); err != nil {
		return api.StoredWorkflowLiveVerification{}, ErrNotFound
	}
	row, err := sqlc.New().GetStoredWorkflowLiveVerification(ctx, s.pool, sqlc.GetStoredWorkflowLiveVerificationParams{AppID: mustPgUUID(appID), Name: name, RunID: mustPgUUID(runID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.StoredWorkflowLiveVerification{}, ErrNotFound
	}
	if err != nil {
		return api.StoredWorkflowLiveVerification{}, err
	}
	current, dep, err := liveCurrentSQL(ctx, s.pool, appID, name)
	if err != nil {
		return api.StoredWorkflowLiveVerification{}, err
	}
	return liveViewSQL(ctx, s.pool, row, current, dep)
}

func (s *PgStore) CheckStoredWorkflowLiveVerification(ctx context.Context, appID, name, runID string) (api.StoredWorkflowLiveVerification, error) {
	if _, err := uuid.Parse(runID); err != nil {
		return api.StoredWorkflowLiveVerification{}, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.StoredWorkflowLiveVerification{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return api.StoredWorkflowLiveVerification{}, err
	}
	view, err := checkLiveVerificationTx(ctx, tx, appID, name, runID)
	if err != nil {
		return api.StoredWorkflowLiveVerification{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.StoredWorkflowLiveVerification{}, err
	}
	return view, nil
}

func checkLiveVerificationTx(ctx context.Context, tx pgx.Tx, appID, name, runID string) (api.StoredWorkflowLiveVerification, error) {
	q := sqlc.New()
	row, err := q.GetStoredWorkflowLiveVerification(ctx, tx, sqlc.GetStoredWorkflowLiveVerificationParams{AppID: mustPgUUID(appID), Name: name, RunID: mustPgUUID(runID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.StoredWorkflowLiveVerification{}, ErrNotFound
	}
	if err != nil {
		return api.StoredWorkflowLiveVerification{}, err
	}
	run, err := q.LockStoredWorkflowLiveRunState(ctx, tx, row.RunID)
	if err != nil {
		return api.StoredWorkflowLiveVerification{}, err
	}
	report, err := evaluateLiveReport(ctx, row.LiveTest, run.Status, run.CancelledAt.Valid, int(run.ResumeCount), timeFromPgtype(run.UpdatedAt), func(e api.StoredWorkflowLiveExpectation) ([]storedworkflowlive.ObservedStep, error) {
		index := pgtype.Int4{}
		if e.ItemIndex != nil {
			if *e.ItemIndex < 0 || *e.ItemIndex >= api.WorkflowForEachMaxItems {
				return nil, ErrStoredWorkflowInvalid
			}
			index = pgtype.Int4{Int32: int32(*e.ItemIndex), Valid: true}
		}
		steps, err := q.StoredWorkflowLiveStep(ctx, tx, sqlc.StoredWorkflowLiveStepParams{RunID: row.RunID, StepName: e.Step, LoopName: e.Loop, ItemIndex: index})
		if err != nil {
			return nil, err
		}
		observed := make([]storedworkflowlive.ObservedStep, 0, len(steps))
		for _, s := range steps {
			observed = append(observed, storedworkflowlive.ObservedStep{State: s.Status, Output: s.Output, AttemptCount: int(s.Attempt)})
		}
		return observed, nil
	})
	if err != nil {
		return api.StoredWorkflowLiveVerification{}, err
	}
	if report != nil {
		raw, err := json.Marshal(report)
		if err != nil {
			return api.StoredWorkflowLiveVerification{}, err
		}
		count, err := q.SaveStoredWorkflowLiveReport(ctx, tx, sqlc.SaveStoredWorkflowLiveReportParams{AppID: row.AppID, Name: name, RunID: row.RunID, Report: raw})
		if err != nil {
			return api.StoredWorkflowLiveVerification{}, err
		}
		if count != 1 {
			return api.StoredWorkflowLiveVerification{}, ErrNotFound
		}
		row.Report = raw
	}
	current, dep, err := liveCurrentSQL(ctx, tx, appID, name)
	if err != nil {
		return api.StoredWorkflowLiveVerification{}, err
	}
	view, err := liveViewSQL(ctx, tx, row, current, dep)
	if err != nil {
		return api.StoredWorkflowLiveVerification{}, err
	}
	return view, nil
}
