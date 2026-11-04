package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const (
	automationFailurePolicyItemTimeout = 3 * time.Second
	automationFailurePolicyPageBudget  = 3 * time.Second
)

func failurePolicyResponsePG(ctx context.Context, db sqlc.DBTX, appID, name string, now time.Time) (api.AutomationFailurePolicyResponse, error) {
	q := sqlc.New()
	raw, err := q.GetAutomationFailurePolicy(ctx, db, sqlc.GetAutomationFailurePolicyParams{AppID: mustPgUUID(appID), Name: name, HistoryLimit: api.AutomationFailurePolicyHistoryLimit})
	var out api.AutomationFailurePolicyResponse
	if err != nil {
		return out, mapErr(err)
	}
	if err = json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	since := now.Add(-time.Duration(out.Policy.WindowSeconds) * time.Second)
	if out.MonitoringSince != nil && out.MonitoringSince.After(since) {
		since = *out.MonitoringSince
	}
	signals, err := q.AutomationFailureSignals(ctx, db, sqlc.AutomationFailureSignalsParams{AppID: mustPgUUID(appID), Name: name, SinceAt: objectUsageTime(since), NowAt: objectUsageTime(now)})
	if err != nil {
		return out, err
	}
	out.ObservedFailures = signals.Failures
	out.ObservedCompletedRuns = signals.CompletedRuns
	out.PendingRuns = signals.PendingRuns
	out.RunningRuns = signals.RunningRuns
	out.WaitingRuns = signals.WaitingRuns
	out.RetainedEvents, err = q.AutomationFailureRetainedEvents(ctx, db, sqlc.AutomationFailureRetainedEventsParams{AppID: appID, Name: name})
	return out, err
}

// Call with the app admission lock held. An unlatched threshold must still
// stop automatic admissions when the background evaluator is slow or failing.
func automationFailureAdmissionStatePG(ctx context.Context, tx pgx.Tx, appID, name string, now time.Time) (api.AutomationFailurePolicyResponse, error) {
	q := sqlc.New()
	raw, err := q.GetAutomationFailurePolicy(ctx, tx, sqlc.GetAutomationFailurePolicyParams{AppID: mustPgUUID(appID), Name: name, HistoryLimit: 0})
	var out api.AutomationFailurePolicyResponse
	if err != nil {
		return out, mapErr(err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	if out.Paused || !out.Policy.Enabled {
		return out, nil
	}
	since := now.Add(-time.Duration(out.Policy.WindowSeconds) * time.Second)
	if out.MonitoringSince != nil && out.MonitoringSince.After(since) {
		since = *out.MonitoringSince
	}
	signals, err := q.AutomationFailureSignals(ctx, tx, sqlc.AutomationFailureSignalsParams{AppID: mustPgUUID(appID), Name: name, SinceAt: objectUsageTime(since), NowAt: objectUsageTime(now)})
	if err != nil {
		return out, err
	}
	out.ObservedFailures, out.ObservedCompletedRuns = signals.Failures, signals.CompletedRuns
	return out, nil
}

// The caller must commit a newly latched pause even when it defers an event.
func automationFailureBlocksAdmissionPG(ctx context.Context, tx pgx.Tx, appID, name string, now time.Time) (blocked, latched bool, err error) {
	out, err := automationFailureAdmissionStatePG(ctx, tx, appID, name, now)
	if err != nil {
		return false, false, err
	}
	if out.Paused {
		return true, false, nil
	}
	if !automationFailureBlocksAdmission(out) {
		return false, false, nil
	}
	q := sqlc.New()
	if err := q.EnsureAutomationFailureGuard(ctx, tx, sqlc.EnsureAutomationFailureGuardParams{AppID: mustPgUUID(appID), Name: name}); err != nil {
		return false, false, err
	}
	if err := q.LatchAutomationFailurePause(ctx, tx, sqlc.LatchAutomationFailurePauseParams{AppID: mustPgUUID(appID), Name: name, NowAt: objectUsageTime(now), Failures: out.ObservedFailures, CompletedRuns: out.ObservedCompletedRuns, PolicyVersion: out.Policy.Version}); err != nil {
		return false, false, err
	}
	return true, true, nil
}
func failurePolicyTargetPG(ctx context.Context, db sqlc.DBTX, appID, name string) error {
	exists, err := sqlc.New().AutomationFailureTargetExists(ctx, db, sqlc.AutomationFailureTargetExistsParams{AppID: mustPgUUID(appID), Name: name})
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}
func (s *PgStore) GetAutomationFailurePolicy(ctx context.Context, appID, name string) (api.AutomationFailurePolicyResponse, error) {
	if err := failurePolicyTargetPG(ctx, s.pool, appID, name); err != nil {
		return api.AutomationFailurePolicyResponse{}, err
	}
	return failurePolicyResponsePG(ctx, s.pool, appID, name, time.Now().UTC())
}
func (s *PgStore) SetAutomationFailurePolicy(ctx context.Context, appID, name string, r api.SetAutomationFailurePolicyRequest) (api.AutomationFailurePolicyResponse, error) {
	if !validAutomationFailurePolicy(name, r) {
		return api.AutomationFailurePolicyResponse{}, ErrAutomationInvalid
	}
	var out api.AutomationFailurePolicyResponse
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err = q.LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return out, err
	}
	target, err := q.LockEventWorkflowTarget(ctx, tx, mustPgUUID(appID))
	if err != nil {
		return out, mapErr(err)
	}
	if target.AppStatus == string(AppDeleted) {
		return out, ErrNotFound
	}
	if (target.AccountStatus != "active" && target.AccountStatus != "past_due") || target.AbuseHoldAt.Valid || *r.Enabled && !api.Plan(target.Plan).WorkflowsAllowed() {
		return out, ErrWorkflowEventTargetUnavailable
	}
	if err = failurePolicyTargetPG(ctx, tx, appID, name); err != nil {
		return out, err
	}
	out, err = failurePolicyResponsePG(ctx, tx, appID, name, time.Now().UTC())
	if err != nil {
		return out, err
	}
	if out.Policy.Version != r.ExpectedVersion {
		return out, ErrAutomationFailurePolicyConflict
	}
	err = q.UpsertAutomationFailurePolicy(ctx, tx, sqlc.UpsertAutomationFailurePolicyParams{AppID: mustPgUUID(appID), Name: name, Version: out.Policy.Version + 1, Enabled: *r.Enabled, FailureThreshold: int32(r.FailureThreshold), MinCompletedRuns: int32(r.MinCompletedRuns), WindowSeconds: int32(r.WindowSeconds)})
	if err != nil {
		return out, err
	}
	if err = q.EnsureAutomationFailureGuard(ctx, tx, sqlc.EnsureAutomationFailureGuardParams{AppID: mustPgUUID(appID), Name: name}); err != nil {
		return out, err
	}
	out, err = failurePolicyResponsePG(ctx, tx, appID, name, time.Now().UTC())
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) ResumeAutomationFailurePause(ctx context.Context, appID, name, actor string, r api.ResumeAutomationFailurePauseRequest) (api.AutomationFailurePolicyResponse, error) {
	var out api.AutomationFailurePolicyResponse
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err = q.LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return out, err
	}
	target, err := q.LockEventWorkflowTarget(ctx, tx, mustPgUUID(appID))
	if err != nil {
		return out, mapErr(err)
	}
	if target.AppStatus == string(AppDeleted) || pgUUIDString(target.AccountID) != actor || actor == "" {
		return out, ErrNotFound
	}
	if err = failurePolicyTargetPG(ctx, tx, appID, name); err != nil {
		return out, err
	}
	out, err = failurePolicyResponsePG(ctx, tx, appID, name, time.Now().UTC())
	if err != nil {
		return out, err
	}
	if !out.Paused || out.Generation != r.ExpectedGeneration {
		return out, ErrAutomationFailurePolicyConflict
	}
	if err = q.ResumeAutomationFailurePause(ctx, tx, sqlc.ResumeAutomationFailurePauseParams{AppID: mustPgUUID(appID), Name: name, ActorAccountID: mustPgUUID(actor), Failures: out.ObservedFailures, CompletedRuns: out.ObservedCompletedRuns, PolicyVersion: out.Policy.Version}); err != nil {
		return out, err
	}
	if err = q.DeleteAutomationScheduleCursor(ctx, tx, sqlc.DeleteAutomationScheduleCursorParams{AppID: mustPgUUID(appID), WorkflowName: name}); err != nil {
		return out, err
	}
	if err = q.RearmTenantAutomationSchedules(ctx, tx, sqlc.RearmTenantAutomationSchedulesParams{AppID: mustPgUUID(appID), WorkflowName: name}); err != nil {
		return out, err
	}
	out, err = failurePolicyResponsePG(ctx, tx, appID, name, time.Now().UTC())
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) ListAutomationFailurePauses(ctx context.Context, appID string) ([]string, error) {
	return sqlc.New().ListAutomationFailurePauses(ctx, s.pool, mustPgUUID(appID))
}
func (s *PgStore) EvaluateAutomationFailurePolicies(ctx context.Context, owner, after string, limit int, now time.Time) (string, error) {
	if limit < 1 || limit > api.AutomationFailurePolicyBatch {
		return after, ErrAutomationInvalid
	}
	rows, err := sqlc.New().ListAutomationFailurePolicyCandidates(ctx, s.pool, sqlc.ListAutomationFailurePolicyCandidatesParams{OwnerNodeID: mustPgUUID(owner), AfterKey: after, BatchLimit: int32(limit)})
	if err != nil {
		return after, err
	}
	var failures []error
	started := time.Now()
	processed := 0
	last := after
	for _, row := range rows {
		itemCtx, cancel := context.WithTimeout(ctx, automationFailurePolicyItemTimeout)
		err = s.evaluateAutomationFailurePolicy(itemCtx, row.AppID, row.Name, now)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				if processed == 0 {
					return after, ctx.Err()
				}
				return last, &AutomationFailurePolicyBatchError{Err: errors.Join(append(failures, ctx.Err())...)}
			}
			failures = append(failures, err)
		}
		processed++
		last = row.AppID + "/" + row.Name
		if processed < len(rows) && time.Since(started) >= automationFailurePolicyPageBudget {
			break
		}
	}
	next := ""
	if processed < len(rows) || len(rows) == limit {
		next = last
	}
	if len(failures) > 0 {
		return next, &AutomationFailurePolicyBatchError{Err: errors.Join(failures...)}
	}
	return next, nil
}
func (s *PgStore) evaluateAutomationFailurePolicy(ctx context.Context, appID, name string, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err = q.LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return err
	}
	if err = failurePolicyTargetPG(ctx, tx, appID, name); errors.Is(err, ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	if err = q.EnsureAutomationFailureGuard(ctx, tx, sqlc.EnsureAutomationFailureGuardParams{AppID: mustPgUUID(appID), Name: name}); err != nil {
		return err
	}
	out, err := automationFailureAdmissionStatePG(ctx, tx, appID, name, now)
	if err != nil {
		return err
	}
	if out.Paused || !out.Policy.Enabled || out.ObservedFailures < int64(out.Policy.FailureThreshold) || out.ObservedCompletedRuns < int64(out.Policy.MinCompletedRuns) {
		return tx.Commit(ctx)
	}
	if err = q.LatchAutomationFailurePause(ctx, tx, sqlc.LatchAutomationFailurePauseParams{AppID: mustPgUUID(appID), Name: name, NowAt: objectUsageTime(now), Failures: out.ObservedFailures, CompletedRuns: out.ObservedCompletedRuns, PolicyVersion: out.Policy.Version}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
