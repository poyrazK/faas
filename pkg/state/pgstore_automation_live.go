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
	"github.com/onebox-faas/faas/pkg/automationlive"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func liveRecordFromSQL(row sqlc.WorkflowAutomationLiveVerification) (automationLiveRecord, error) {
	r := automationLiveRecord{AppID: pgUUIDString(row.AppID), Name: row.Name, Key: row.IdempotencyKey, Fingerprint: row.RequestFingerprint, Test: row.LiveTest,
		View: api.AutomationLiveVerification{RunID: pgUUIDString(row.RunID), CaseName: row.CaseName, DeploymentID: pgUUIDString(row.DeploymentID),
			Version: row.AutomationVersion, PublishedVersion: row.PublishedVersion, InputsHash: row.InputsHash, CreatedAt: timeFromPgtype(row.CreatedAt)}}
	if len(row.Report) > 0 {
		r.View.Report = new(api.AutomationLiveReport)
		if err := json.Unmarshal(row.Report, r.View.Report); err != nil {
			return r, err
		}
	}
	return r, nil
}

func liveCurrentSQL(ctx context.Context, db sqlc.DBTX, appID, name string) (Automation, string, error) {
	q := sqlc.New()
	row, err := q.GetAutomationForLiveVerification(ctx, db, sqlc.GetAutomationForLiveVerificationParams{AppID: mustPgUUID(appID), Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return Automation{}, "", ErrNotFound
	}
	if err != nil {
		return Automation{}, "", err
	}
	dep, err := q.AutomationLiveDeployment(ctx, db, mustPgUUID(appID))
	if errors.Is(err, pgx.ErrNoRows) {
		return automationFromSQL(row), "", nil
	}
	return automationFromSQL(row), pgUUIDString(dep), err
}

func liveViewSQL(ctx context.Context, db sqlc.DBTX, row sqlc.WorkflowAutomationLiveVerification, current Automation, deploymentID string) (api.AutomationLiveVerification, error) {
	live, err := liveRecordFromSQL(row)
	if err != nil {
		return api.AutomationLiveVerification{}, err
	}
	run, err := sqlc.New().AutomationLiveRunState(ctx, db, row.RunID)
	if err != nil {
		return api.AutomationLiveVerification{}, err
	}
	return liveView(live, run.Status, timeFromPgtype(run.UpdatedAt), int(run.ResumeCount), current, deploymentID), nil
}

func (s *PgStore) StartAutomationLiveVerification(ctx context.Context, appID, name, key string, req api.StartAutomationLiveVerificationRequest, runtimeEnabled bool) (api.AutomationLiveVerification, bool, error) {
	fingerprint, err := validateLiveStart(&req, key)
	if err != nil {
		return api.AutomationLiveVerification{}, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.AutomationLiveVerification{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return api.AutomationLiveVerification{}, false, err
	}
	prior, err := q.GetAutomationLiveVerificationByKey(ctx, tx, sqlc.GetAutomationLiveVerificationByKeyParams{AppID: mustPgUUID(appID), Name: name, IdempotencyKey: key})
	if err == nil {
		if !bytes.Equal(prior.RequestFingerprint, fingerprint) {
			return api.AutomationLiveVerification{}, false, ErrWorkflowRunIdempotencyConflict
		}
		current, dep, err := liveCurrentSQL(ctx, tx, appID, name)
		if err != nil {
			return api.AutomationLiveVerification{}, false, err
		}
		view, err := liveViewSQL(ctx, tx, prior, current, dep)
		return view, true, err // Read-only replay; deferred rollback releases locks.
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return api.AutomationLiveVerification{}, false, err
	}
	row, err := startLiveVerificationTx(ctx, tx, appID, name, key, req, fingerprint, runtimeEnabled)
	if err != nil {
		return api.AutomationLiveVerification{}, false, err
	}
	current, dep, err := liveCurrentSQL(ctx, tx, appID, name)
	if err != nil {
		return api.AutomationLiveVerification{}, false, err
	}
	view, err := liveViewSQL(ctx, tx, row, current, dep)
	if err != nil {
		return api.AutomationLiveVerification{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.AutomationLiveVerification{}, false, err
	}
	return view, false, nil
}

func startLiveVerificationTx(ctx context.Context, tx pgx.Tx, appID, name, key string, req api.StartAutomationLiveVerificationRequest, fingerprint []byte, runtimeEnabled bool) (sqlc.WorkflowAutomationLiveVerification, error) {
	q := sqlc.New()
	target, err := q.LockEventWorkflowTarget(ctx, tx, mustPgUUID(appID))
	if errors.Is(err, pgx.ErrNoRows) || target.AppStatus == string(AppDeleted) {
		return sqlc.WorkflowAutomationLiveVerification{}, ErrNotFound
	}
	if err != nil {
		return sqlc.WorkflowAutomationLiveVerification{}, err
	}
	plan := api.Plan(target.Plan)
	if err := automationAccountGate(plan, (target.AccountStatus == "active" || target.AccountStatus == "past_due") && !target.AbuseHoldAt.Valid, "tests"); err != nil {
		return sqlc.WorkflowAutomationLiveVerification{}, err
	}
	if target.PlatformTenantRequired {
		return sqlc.WorkflowAutomationLiveVerification{}, api.ErrWorkflowTenantIdentityUnavailable()
	}
	if !runtimeEnabled || target.MaintenanceMode {
		return sqlc.WorkflowAutomationLiveVerification{}, ErrWorkflowEventTargetUnavailable
	}
	code, err := q.LockWorkflowScheduleTarget(ctx, tx, mustPgUUID(appID))
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.WorkflowAutomationLiveVerification{}, ErrWorkflowEventTargetUnavailable
	}
	if err != nil {
		return sqlc.WorkflowAutomationLiveVerification{}, err
	}
	automation, err := q.GetAutomationForLiveVerification(ctx, tx, sqlc.GetAutomationForLiveVerificationParams{AppID: mustPgUUID(appID), Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.WorkflowAutomationLiveVerification{}, ErrNotFound
	}
	if err != nil {
		return sqlc.WorkflowAutomationLiveVerification{}, err
	}
	run, live, err := prepareLiveVerification(appID, name, key, pgUUIDString(code.DeploymentID), automationFromSQL(automation), req, fingerprint)
	if err != nil {
		return sqlc.WorkflowAutomationLiveVerification{}, err
	}
	var def api.WorkflowSpec
	if err := json.Unmarshal(run.DefinitionSnapshot, &def); err != nil {
		return sqlc.WorkflowAutomationLiveVerification{}, err
	}
	if err := validateWorkflowOutboundTx(ctx, tx, appID, pgUUIDString(target.AccountID), def); err != nil {
		return sqlc.WorkflowAutomationLiveVerification{}, err
	}
	active, err := q.CountActiveWorkflowRunsForAdmission(ctx, tx, sqlc.CountActiveWorkflowRunsForAdmissionParams{AppID: mustPgUUID(appID), WorkflowName: "", ParkedMax: int64(plan.WorkflowMaxParkedRuns())})
	if err != nil {
		return sqlc.WorkflowAutomationLiveVerification{}, err
	}
	if active >= int64(plan.WorkflowMaxConcurrentRuns()) {
		return sqlc.WorkflowAutomationLiveVerification{}, api.ErrPlanWorkflowsQuota(plan, plan.WorkflowMaxConcurrentRuns(), int(active))
	}
	if err := insertWorkflowRun(ctx, tx, run); err != nil {
		return sqlc.WorkflowAutomationLiveVerification{}, err
	}
	return q.InsertAutomationLiveVerification(ctx, tx, sqlc.InsertAutomationLiveVerificationParams{RunID: mustPgUUID(run.ID), AppID: mustPgUUID(appID), Name: name,
		CaseName: req.CaseName, AutomationVersion: live.View.Version, PublishedVersion: live.View.PublishedVersion, DeploymentID: code.DeploymentID,
		InputsHash: live.View.InputsHash, LiveTest: live.Test, IdempotencyKey: key, RequestFingerprint: fingerprint})
}

func (s *PgStore) ListAutomationLiveVerifications(ctx context.Context, appID, name string) (api.ListAutomationLiveVerificationsResponse, error) {
	current, dep, err := liveCurrentSQL(ctx, s.pool, appID, name)
	if err != nil {
		return api.ListAutomationLiveVerificationsResponse{}, err
	}
	rows, err := sqlc.New().ListAutomationLiveVerifications(ctx, s.pool, sqlc.ListAutomationLiveVerificationsParams{AppID: mustPgUUID(appID), Name: name, Limit: api.AutomationLiveHistoryLimit})
	if err != nil {
		return api.ListAutomationLiveVerificationsResponse{}, err
	}
	result := api.ListAutomationLiveVerificationsResponse{DeploymentID: dep, PublishedVersion: current.PublishedVersion, Verifications: []api.AutomationLiveVerification{}}
	for _, row := range rows {
		view, err := liveViewSQL(ctx, s.pool, row, current, dep)
		if err != nil {
			return api.ListAutomationLiveVerificationsResponse{}, err
		}
		result.Verifications = append(result.Verifications, view)
	}
	return result, nil
}

func (s *PgStore) GetAutomationLiveVerification(ctx context.Context, appID, name, runID string) (api.AutomationLiveVerification, error) {
	if _, err := uuid.Parse(runID); err != nil {
		return api.AutomationLiveVerification{}, ErrNotFound
	}
	row, err := sqlc.New().GetAutomationLiveVerification(ctx, s.pool, sqlc.GetAutomationLiveVerificationParams{AppID: mustPgUUID(appID), Name: name, RunID: mustPgUUID(runID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.AutomationLiveVerification{}, ErrNotFound
	}
	if err != nil {
		return api.AutomationLiveVerification{}, err
	}
	current, dep, err := liveCurrentSQL(ctx, s.pool, appID, name)
	if err != nil {
		return api.AutomationLiveVerification{}, err
	}
	return liveViewSQL(ctx, s.pool, row, current, dep)
}

func (s *PgStore) CheckAutomationLiveVerification(ctx context.Context, appID, name, runID string) (api.AutomationLiveVerification, error) {
	if _, err := uuid.Parse(runID); err != nil {
		return api.AutomationLiveVerification{}, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.AutomationLiveVerification{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return api.AutomationLiveVerification{}, err
	}
	view, err := checkLiveVerificationTx(ctx, tx, appID, name, runID)
	if err != nil {
		return api.AutomationLiveVerification{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.AutomationLiveVerification{}, err
	}
	return view, nil
}

func checkLiveVerificationTx(ctx context.Context, tx pgx.Tx, appID, name, runID string) (api.AutomationLiveVerification, error) {
	q := sqlc.New()
	row, err := q.GetAutomationLiveVerification(ctx, tx, sqlc.GetAutomationLiveVerificationParams{AppID: mustPgUUID(appID), Name: name, RunID: mustPgUUID(runID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.AutomationLiveVerification{}, ErrNotFound
	}
	if err != nil {
		return api.AutomationLiveVerification{}, err
	}
	run, err := q.LockAutomationLiveRunState(ctx, tx, row.RunID)
	if err != nil {
		return api.AutomationLiveVerification{}, err
	}
	report, err := evaluateLiveReport(ctx, row.LiveTest, run.Status, run.CancelledAt.Valid, int(run.ResumeCount), timeFromPgtype(run.UpdatedAt), func(e api.AutomationLiveExpectation) ([]automationlive.ObservedStep, error) {
		index := pgtype.Int4{}
		if e.ItemIndex != nil {
			if *e.ItemIndex < 0 || *e.ItemIndex >= api.WorkflowForEachMaxItems {
				return nil, ErrAutomationInvalid
			}
			index = pgtype.Int4{Int32: int32(*e.ItemIndex), Valid: true}
		}
		steps, err := q.AutomationLiveStep(ctx, tx, sqlc.AutomationLiveStepParams{RunID: row.RunID, StepName: e.Step, LoopName: e.Loop, ItemIndex: index})
		if err != nil {
			return nil, err
		}
		observed := make([]automationlive.ObservedStep, 0, len(steps))
		for _, s := range steps {
			observed = append(observed, automationlive.ObservedStep{State: s.Status, Output: s.Output, AttemptCount: int(s.Attempt)})
		}
		return observed, nil
	})
	if err != nil {
		return api.AutomationLiveVerification{}, err
	}
	if report != nil {
		raw, err := json.Marshal(report)
		if err != nil {
			return api.AutomationLiveVerification{}, err
		}
		count, err := q.SaveAutomationLiveReport(ctx, tx, sqlc.SaveAutomationLiveReportParams{AppID: row.AppID, Name: name, RunID: row.RunID, Report: raw})
		if err != nil {
			return api.AutomationLiveVerification{}, err
		}
		if count != 1 {
			return api.AutomationLiveVerification{}, ErrNotFound
		}
		row.Report = raw
	}
	current, dep, err := liveCurrentSQL(ctx, tx, appID, name)
	if err != nil {
		return api.AutomationLiveVerification{}, err
	}
	view, err := liveViewSQL(ctx, tx, row, current, dep)
	if err != nil {
		return api.AutomationLiveVerification{}, err
	}
	return view, nil
}
