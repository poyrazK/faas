package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ProcessNextWorkflowRecoveryAutomaticPolicy(ctx context.Context, now time.Time) (bool, error) {
	if now.IsZero() {
		return false, ErrEventRecoveryQuery
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	q := sqlc.New()
	next, err := q.WorkflowRecoveryAutomaticPolicyNext(ctx, s.pool, pgtypeFromTime(now))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("select automatic recovery policy: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin automatic recovery execution: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Match manual creation/pruning lock order before locking policy authority.
	if err = q.EventReplayBackfillLockAccountRange(ctx, tx, next.AccountID); err != nil {
		return false, fmt.Errorf("lock automatic recovery account: %w", err)
	}
	row, err := q.WorkflowRecoveryAutomaticPolicyClaim(ctx, tx, sqlc.WorkflowRecoveryAutomaticPolicyClaimParams{AppID: next.AppID, NowAt: pgtypeFromTime(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim automatic recovery policy: %w", err)
	}
	work, err := tx.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin automatic polling savepoint: %w", err)
	}
	var alertFailure *api.WorkflowRecoveryAutomaticPollingFailure
	err = processAutomaticPolicyTx(ctx, s, q, work, row, now)
	if err == nil {
		if err = work.Commit(ctx); err != nil {
			return false, fmt.Errorf("commit automatic polling savepoint: %w", err)
		}
	} else {
		code := automaticPollingFailureCode(err)
		if code == "" || ctx.Err() != nil {
			return false, err
		}
		if rollbackErr := work.Rollback(ctx); rollbackErr != nil {
			return false, fmt.Errorf("rollback automatic polling: %w", rollbackErr)
		}
		var previous *api.WorkflowRecoveryAutomaticPollingFailure
		if len(row.AutomaticPollFailure) > 0 {
			if decodeErr := json.Unmarshal(row.AutomaticPollFailure, &previous); decodeErr != nil {
				return false, fmt.Errorf("decode automatic polling backoff: %w", decodeErr)
			}
		}
		failure := nextAutomaticPollingFailure(previous, code, now)
		alertFailure = failure
		raw, encodeErr := json.Marshal(failure)
		if encodeErr != nil {
			return false, fmt.Errorf("encode automatic polling failure: %w", encodeErr)
		}
		if err = q.WorkflowRecoveryAutomaticPolicyFailure(ctx, tx, sqlc.WorkflowRecoveryAutomaticPolicyFailureParams{AppID: row.AppID, Failure: raw, NextAt: pgtypeFromTime(failure.RetryAt)}); err != nil {
			return false, fmt.Errorf("defer automatic polling: %w", err)
		}
	}
	previousNotification, decodeErr := pollingNotificationFromRaw(row.AutomaticPollNotification)
	if decodeErr != nil {
		return false, decodeErr
	}
	code := ""
	attempts := int64(0)
	var retry *time.Time
	if alertFailure != nil {
		code = alertFailure.Code
		attempts = alertFailure.Attempts
		retry = &alertFailure.RetryAt
	}
	app := App{ID: uuidString(row.AppID), AccountID: uuidString(row.AccountID), Slug: row.Slug}
	notification, captureErr := capturePollingAlertTx(ctx, q, tx, app, pollingAlertTransition(app, "automatic_policy", "", row.Version, code, attempts, retry, previousNotification, now))
	if captureErr != nil {
		return false, captureErr
	}
	if notification != nil {
		encoded, e := json.Marshal(notification)
		if e != nil {
			return false, fmt.Errorf("encode polling receipt: %w", e)
		}
		if e = q.WorkflowRecoveryAutomaticPollingNotificationSave(ctx, tx, sqlc.WorkflowRecoveryAutomaticPollingNotificationSaveParams{AppID: row.AppID, Notification: encoded}); e != nil {
			return false, fmt.Errorf("save polling receipt: %w", e)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit automatic polling: %w", err)
	}
	return true, nil
}

func processAutomaticPolicyTx(ctx context.Context, s *PgStore, q *sqlc.Queries, tx pgx.Tx, row sqlc.WorkflowRecoveryAutomaticPolicyClaimRow, now time.Time) error {
	p, err := pgPolicyResponse(uuidString(row.AppID), row.Version, row.Policy, row.UpdatedAt, row.ActorKind, row.ActorID, row.LastEvaluatedAt, row.Status, row.AutomaticFailures, row.AutomaticPausedAt, row.AutomaticPauseReason, row.AutomaticPollFailure, row.AutomaticPollNotification)
	if err != nil {
		return err
	}
	if err = (api.SetWorkflowRecoveryPolicyRequest{Policy: p.Policy}).Validate(); err != nil {
		return fmt.Errorf("%w: %w", errAutomaticPolicyAuthority, err)
	}
	if p.Policy.Mode == "automatic_initial" {
		now = now.UTC().Truncate(time.Microsecond)
	}
	if err = q.WorkflowRecoveryAutomaticObsolete(ctx, tx, sqlc.WorkflowRecoveryAutomaticObsoleteParams{AppID: row.AppID, PolicyVersion: p.Version}); err != nil {
		return fmt.Errorf("suppress old automatic plans: %w", err)
	}
	if err = q.WorkflowRecoveryAutomaticChargesPrune(ctx, tx, sqlc.WorkflowRecoveryAutomaticChargesPruneParams{AppID: row.AppID, BeforeAt: pgtypeFromTime(now.Add(-time.Duration(api.WorkflowRecoveryPolicyMaxCooldownSeconds) * time.Second))}); err != nil {
		return fmt.Errorf("prune automatic execution charges: %w", err)
	}
	ctx = automaticRecoveryActor(ctx, p)
	nextAt := now.Add(api.WorkflowRecoveryPolicyPollInterval)
	initial, initialErr := q.WorkflowInitialAutomaticOutstanding(ctx, tx, row.AppID)
	if initialErr == nil {
		if err = processInitialAutomaticResultTx(ctx, q, tx, &p, uuidString(row.AccountID), row.Slug, initial, now); err != nil {
			return err
		}
		if err = q.WorkflowRecoveryAutomaticPolicyAdvance(ctx, tx, sqlc.WorkflowRecoveryAutomaticPolicyAdvanceParams{AppID: row.AppID, NextAt: pgtypeFromTime(nextAt), Failures: int32(p.AutomaticExecution.ConsecutiveFailures), PausedAt: recoveryAutomaticPausedTime(p), PauseReason: p.AutomaticExecution.PauseReason}); err != nil {
			return fmt.Errorf("advance initial recovery policy: %w", err)
		}
		return nil
	}
	if !errors.Is(initialErr, pgx.ErrNoRows) {
		return fmt.Errorf("read outstanding initial recovery: %w", initialErr)
	}

	outstanding, err := q.WorkflowRecoveryAutomaticOutstanding(ctx, tx, row.AppID)
	if err == nil {
		e := api.WorkflowRecoveryAutomaticAction{ActionID: uuidString(outstanding.ActionID), Position: outstanding.Position, ChildJobID: uuidString(outstanding.ChildJobID)}
		result, reason, checkErr := recoveryAutomaticEvaluationResult(ctx, s, uuidString(row.AccountID), p.AppID, uuidString(outstanding.ReviewID), e, now)
		if checkErr != nil {
			return checkErr
		}
		if err = recordAutomaticEvaluation(ctx, q, tx, outstanding.ActionID, outstanding.LastEvaluation, reason, now, result); err != nil {
			return err
		}
		if result == nil {
			pending, pendingErr := q.WorkflowRecoveryAutomaticPending(ctx, tx, sqlc.WorkflowRecoveryAutomaticPendingParams{AppID: row.AppID, PolicyVersion: p.Version})
			if pendingErr == nil {
				if err = recordAutomaticEvaluation(ctx, q, tx, pending.ActionID, pending.LastEvaluation, "outstanding_retry", now, nil); err != nil {
					return err
				}
			} else if !errors.Is(pendingErr, pgx.ErrNoRows) {
				return fmt.Errorf("read automatic retry waiting for slot: %w", pendingErr)
			}
		}
		if checkErr == nil && result != nil {
			raw, e := automaticRecoveryRawResult(result)
			if e != nil {
				return e
			}
			if e = q.WorkflowRecoveryAutomaticResult(ctx, tx, sqlc.WorkflowRecoveryAutomaticResultParams{ActionID: outstanding.ActionID, Result: raw}); e != nil {
				return fmt.Errorf("retain automatic retry result: %w", e)
			}
			nextAt = now
			if outstanding.PolicyVersion == p.Version && recoveryAutomaticRecordFailure(&p, *result, now) {
				if e = enqueueAutomaticRecoveryPause(ctx, q, tx, row.AccountID, p, row.Slug); e != nil {
					return e
				}
			}
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("read outstanding automatic retry: %w", err)
	} else {
		if p.Policy.Mode == "automatic_initial" {
			err = executeInitialAutomaticPendingTx(ctx, q, tx, p, uuidString(row.AccountID), now)
		} else {
			nextAt, err = executeAutomaticRecoveryPending(ctx, s, q, tx, row.AccountID, p, now)
		}
		if err != nil {
			return err
		}
	}
	if err = q.WorkflowRecoveryAutomaticPolicyAdvance(ctx, tx, sqlc.WorkflowRecoveryAutomaticPolicyAdvanceParams{AppID: row.AppID, NextAt: pgtypeFromTime(nextAt), Failures: int32(p.AutomaticExecution.ConsecutiveFailures), PausedAt: recoveryAutomaticPausedTime(p), PauseReason: p.AutomaticExecution.PauseReason}); err != nil {
		return fmt.Errorf("advance automatic recovery policy: %w", err)
	}
	return nil
}
func recoveryAutomaticPausedTime(p api.WorkflowRecoveryPolicyResponse) pgtype.Timestamptz {
	if p.AutomaticExecution.PausedAt == nil {
		return pgtype.Timestamptz{}
	}
	return pgtypeFromTime(*p.AutomaticExecution.PausedAt)
}
func executeAutomaticRecoveryPending(ctx context.Context, s *PgStore, q *sqlc.Queries, tx pgx.Tx, account pgtype.UUID, p api.WorkflowRecoveryPolicyResponse, now time.Time) (time.Time, error) {
	later := now.Add(api.WorkflowRecoveryPolicyPollInterval)
	e, err := q.WorkflowRecoveryAutomaticPending(ctx, tx, sqlc.WorkflowRecoveryAutomaticPendingParams{AppID: mustPgUUID(p.AppID), PolicyVersion: p.Version})
	if errors.Is(err, pgx.ErrNoRows) {
		return later, nil
	}
	if err != nil {
		return later, fmt.Errorf("read pending automatic retry: %w", err)
	}
	evaluate := func(reason string) error {
		return recordAutomaticEvaluation(ctx, q, tx, e.ActionID, e.LastEvaluation, reason, now, nil)
	}
	suppress := func(reason string) (time.Time, error) {
		err := q.WorkflowRecoveryAutomaticSuppress(ctx, tx, sqlc.WorkflowRecoveryAutomaticSuppressParams{ActionID: e.ActionID, Reason: reason})
		if err != nil {
			return later, fmt.Errorf("suppress automatic retry: %w", err)
		}
		if err := evaluate("suppressed"); err != nil {
			return later, err
		}
		return now, nil
	}
	deferAction := func(reason string) (time.Time, error) {
		if err := q.WorkflowRecoveryAutomaticDefer(ctx, tx, sqlc.WorkflowRecoveryAutomaticDeferParams{ActionID: e.ActionID, Reason: reason}); err != nil {
			return later, fmt.Errorf("defer automatic retry: %w", err)
		}
		if err := evaluate(reason); err != nil {
			return later, err
		}
		return later, nil
	}
	var review api.WorkflowRecoveryPolicyReview
	if err = json.Unmarshal(e.Review, &review); err != nil {
		return later, fmt.Errorf("decode automatic review: %w", err)
	}
	if review.ReviewDecision != nil && review.ReviewDecision.Decision == "dismissed" {
		return suppress("dismissed")
	}
	action, err := recoveryAutomaticAction(review, api.WorkflowRecoveryAutomaticAction{ActionID: uuidString(e.ActionID), Position: e.Position}, p)
	if err != nil {
		return later, err
	}
	_, err = q.EventRecoveryByRequest(ctx, tx, sqlc.EventRecoveryByRequestParams{AccountID: account, AppID: mustPgUUID(p.AppID), RequestID: e.ActionID})
	if err == nil {
		return suppress("already_applied")
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return later, fmt.Errorf("read automatic action receipt: %w", err)
	}
	budget, err := q.WorkflowRecoveryAutomaticBudget(ctx, tx, sqlc.WorkflowRecoveryAutomaticBudgetParams{AppID: mustPgUUID(p.AppID), SinceAt: pgtypeFromTime(now.Add(-time.Hour))})
	if err != nil {
		return later, fmt.Errorf("read automatic execution budget: %w", err)
	}
	if budget.HourlyExecutions >= int64(p.Policy.HourlyExecutionBudget) {
		return deferAction("hourly_budget")
	}
	if budget.LastExecutionAt.Valid && budget.LastExecutionAt.Time.Add(time.Duration(p.Policy.CooldownSeconds)*time.Second).After(now) {
		return deferAction("cooldown")
	}
	job, err := getEventRecoveryMetadata(ctx, q, tx, uuidString(account), review.JobID)
	if automaticRecoveryChanged(err) {
		return suppress("target_unavailable")
	}
	if err != nil {
		return later, err
	}
	if _, err := q.GetNativeWorkflowRun(ctx, tx, mustPgUUID(action.RunID)); errors.Is(err, pgx.ErrNoRows) {
		return deferAction("evidence_unknown")
	} else if err != nil {
		return later, fmt.Errorf("read automatic retry run: %w", err)
	}
	candidates, err := pgRecoveryPolicyCandidates(ctx, q, tx, uuidString(account), p.Policy, job, now)
	if err != nil {
		return later, fmt.Errorf("recheck automatic retry: %w", err)
	}
	allowed, reason := recoveryAutomaticEligibility(action, candidates)
	if !allowed {
		if reason == "unknown" {
			return deferAction("evidence_unknown")
		}
		return suppress(reason)
	}
	child, err := createEventRecoveryTx(automaticRecoveryActor(ctx, p), tx, uuidString(account), p.AppID, *action.Retry)
	if automaticRecoveryDeferrable(err) {
		return deferAction("execution_quota")
	}
	if automaticRecoveryChanged(err) {
		return suppress("changed")
	}
	if err != nil {
		return later, fmt.Errorf("apply automatic retry: %w", err)
	}
	if err = q.WorkflowRecoveryAutomaticApply(ctx, tx, sqlc.WorkflowRecoveryAutomaticApplyParams{ActionID: e.ActionID, ChildJobID: mustPgUUID(child.ID), NowAt: pgtypeFromTime(now)}); err != nil {
		return later, fmt.Errorf("record automatic retry application: %w", err)
	}
	if err = evaluate("child_created"); err != nil {
		return later, err
	}
	return later, nil
}
func enqueueAutomaticRecoveryPause(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, account pgtype.UUID, p api.WorkflowRecoveryPolicyResponse, slug string) error {
	if err := appendRecoveryHistoryEvent(ctx, q, tx, p.AppID, recoveryAutomaticPauseHistoryEvent(p)); err != nil {
		return err
	}
	receivers, err := q.WorkflowRecoveryReviewNotificationReceivers(ctx, tx, sqlc.WorkflowRecoveryReviewNotificationReceiversParams{AccountID: account, AppID: mustPgUUID(p.AppID), Event: string(AppWebhookEventRecoveryPolicyPaused)})
	if err != nil {
		return fmt.Errorf("select automatic pause receivers: %w", err)
	}
	if len(receivers) == 0 {
		return nil
	}
	payload := recoveryPolicyPausedPayload(p, slug)
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode automatic pause notification: %w", err)
	}
	if err = q.WorkflowRecoveryReviewNotificationEnqueue(ctx, tx, sqlc.WorkflowRecoveryReviewNotificationEnqueueParams{EventID: mustPgUUID(payload.EventID), AccountID: account, AppID: mustPgUUID(p.AppID), Event: string(AppWebhookEventRecoveryPolicyPaused), ReviewID: mustPgUUID(payload.EventID), Payload: raw, ReceiverIds: receivers, NowAt: pgtypeFromTime(payload.PausedAt)}); err != nil {
		return fmt.Errorf("enqueue automatic pause notification: %w", err)
	}
	return nil
}

func recordAutomaticEvaluation(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, action pgtype.UUID, previous []byte, reason string, now time.Time, result *api.WorkflowRecoveryReviewActionStatus) error {
	raw, err := recoveryAutomaticEvaluationJSON(previous, reason, now)
	if err != nil {
		return err
	}
	if err = q.WorkflowRecoveryAutomaticEvaluate(ctx, tx, sqlc.WorkflowRecoveryAutomaticEvaluateParams{ActionID: action, Evaluation: raw}); err != nil {
		return fmt.Errorf("retain automatic retry evaluation: %w", err)
	}
	var prior *api.WorkflowRecoveryAutomaticEvaluation
	if len(previous) > 0 {
		if err = json.Unmarshal(previous, &prior); err != nil {
			return fmt.Errorf("decode prior recovery transition: %w", err)
		}
	}
	if prior == nil || prior.Reason != reason {
		return appendRecoveryRetryTransition(ctx, q, tx, action, reason, now, result)
	}
	return nil
}
