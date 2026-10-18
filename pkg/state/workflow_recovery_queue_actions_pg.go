package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

func pgAutomaticAction(row sqlc.WorkflowRecoveryPolicyExecution) (api.WorkflowRecoveryAutomaticAction, error) {
	e := api.WorkflowRecoveryAutomaticAction{ActionID: uuidString(row.ActionID), Position: row.Position, Status: row.Status, Reason: row.Reason, AppliedAt: timestamptzToTimePtr(row.AppliedAt)}
	if row.ChildJobID.Valid {
		e.ChildJobID = uuidString(row.ChildJobID)
	}
	for _, field := range []struct {
		raw    []byte
		target any
	}{{row.LastEvaluation, &e.LastEvaluation}, {row.Suppression, &e.Suppression}, {row.BlockedNotification, &e.BlockedNotification}, {row.Result, &e.Result}} {
		if len(field.raw) > 0 {
			if err := json.Unmarshal(field.raw, field.target); err != nil {
				return e, fmt.Errorf("decode automatic retry evidence: %w", err)
			}
		}
	}
	return e, nil
}
func (s *PgStore) SuppressWorkflowRecoveryAutomaticAction(ctx context.Context, account, app, review, action string, r api.SuppressWorkflowRecoveryAutomaticActionRequest) (api.WorkflowRecoveryAutomaticAction, error) {
	var out api.WorkflowRecoveryAutomaticAction
	if err := validateAutomaticSuppression(account, app, review, action, r); err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, fmt.Errorf("begin automatic suppression: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.EventRecoveryApp(ctx, tx, sqlc.EventRecoveryAppParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app)}); errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	} else if err != nil {
		return out, fmt.Errorf("scope automatic suppression: %w", err)
	}
	p, err := q.WorkflowRecoveryAutomaticOperatorPolicy(ctx, tx, sqlc.WorkflowRecoveryAutomaticOperatorPolicyParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app)})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, fmt.Errorf("lock suppression policy: %w", err)
	}
	if p.Version != r.ExpectedPolicyVersion {
		return out, ErrWorkflowRecoveryPolicyConflict
	}
	if sameMemUUID(review, action) {
		initial, e := initialReviewGetTx(ctx, q, tx, account, app, review, time.Now().UTC())
		if e == nil {
			if initial.PolicyVersion != r.ExpectedPolicyVersion {
				return out, ErrWorkflowRecoveryPolicyConflict
			}
			authority, e := pgPolicyResponse(uuidString(p.AppID), p.Version, p.Policy, p.UpdatedAt, p.ActorKind, p.ActorID, p.LastEvaluatedAt, p.Status, p.AutomaticFailures, p.AutomaticPausedAt, p.AutomaticPauseReason, p.AutomaticPollFailure, p.AutomaticPollNotification)
			if e != nil {
				return out, e
			}
			decided, e := decideInitialReviewTx(ctx, q, tx, account, app, review, authority, api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "cancel", Reason: r.Reason}, time.Now().UTC())
			if e != nil {
				return out, e
			}
			return initialAutomaticSuppressionAction(decided), tx.Commit(ctx)
		}
		if !errors.Is(e, ErrNotFound) {
			return out, e
		}
	}
	row, err := q.WorkflowRecoveryAutomaticOperatorAction(ctx, tx, sqlc.WorkflowRecoveryAutomaticOperatorActionParams{AppID: mustPgUUID(app), ReviewID: mustPgUUID(review), ActionID: mustPgUUID(action)})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, fmt.Errorf("lock automatic retry: %w", err)
	}
	if row.PolicyVersion != r.ExpectedPolicyVersion {
		return out, ErrWorkflowRecoveryPolicyConflict
	}
	out, err = pgAutomaticAction(row)
	if err != nil {
		return out, err
	}
	if automaticSuppressionReplay(out, r) {
		return out, tx.Commit(ctx)
	}
	if out.Status != "pending" {
		return api.WorkflowRecoveryAutomaticAction{}, ErrEventRecoveryChanged
	}
	actor := recoveryAuditActor(ctx, "automatic_retry_suppressed")
	out.Status = "suppressed"
	out.Reason = "operator_suppressed"
	out.Suppression = &api.WorkflowRecoveryAutomaticSuppression{Reason: r.Reason, ActorKind: actor.Kind, ActorID: actor.ID, SuppressedAt: time.Now().UTC()}
	raw, err := json.Marshal(out.Suppression)
	if err != nil {
		return out, fmt.Errorf("encode automatic suppression: %w", err)
	}
	if err = q.WorkflowRecoveryAutomaticOperatorSuppress(ctx, tx, sqlc.WorkflowRecoveryAutomaticOperatorSuppressParams{ActionID: row.ActionID, Suppression: raw}); err != nil {
		return out, fmt.Errorf("retain automatic suppression: %w", err)
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) ProcessNextWorkflowRecoveryAutomaticBlockedNotification(ctx context.Context, now time.Time) (bool, error) {
	if now.IsZero() {
		return false, ErrEventRecoveryQuery
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	q := sqlc.New()
	next, err := q.WorkflowRecoveryAutomaticBlockedNext(ctx, s.pool, pgtypeFromTime(now))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("select blocked automatic retry: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin blocked retry notification: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	p, err := q.WorkflowRecoveryAutomaticOperatorPolicy(ctx, tx, sqlc.WorkflowRecoveryAutomaticOperatorPolicyParams{AccountID: next.AccountID, AppID: next.AppID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock blocked retry policy: %w", err)
	}
	row, err := q.WorkflowRecoveryAutomaticOperatorAction(ctx, tx, sqlc.WorkflowRecoveryAutomaticOperatorActionParams{AppID: next.AppID, ReviewID: next.ReviewID, ActionID: next.ActionID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock blocked retry action: %w", err)
	}
	policy, err := pgPolicyResponse(uuidString(p.AppID), p.Version, p.Policy, p.UpdatedAt, p.ActorKind, p.ActorID, p.LastEvaluatedAt, p.Status, p.AutomaticFailures, p.AutomaticPausedAt, p.AutomaticPauseReason, p.AutomaticPollFailure, p.AutomaticPollNotification)
	if err != nil {
		return false, err
	}
	action, err := pgAutomaticAction(row)
	if err != nil {
		return false, err
	}
	if !automaticBlockedEligible(policy, action, row.PolicyVersion) || row.NextBlockedAt.Time.After(now) {
		return false, nil
	}
	if err = q.WorkflowRecoveryAutomaticBlockedDefer(ctx, tx, sqlc.WorkflowRecoveryAutomaticBlockedDeferParams{ActionID: row.ActionID, NextAt: pgtypeFromTime(now.Add(api.WorkflowRecoveryPolicyPollInterval))}); err != nil {
		return false, fmt.Errorf("defer blocked retry capture: %w", err)
	}
	retained, current, frame, err := retryEvidenceScopeTx(ctx, q, tx, uuidString(next.AccountID), uuidString(next.AppID), uuidString(next.ReviewID), action.ActionID, now, false)
	if err != nil {
		return false, err
	}
	if retained.ReviewDecision != nil && retained.ReviewDecision.Decision == "dismissed" {
		return true, tx.Commit(ctx)
	}
	var diagnosis api.WorkflowRetryEvidenceDiagnosis
	if action.Status == "pending" {
		diagnosis, err = retryParentEvidenceTx(ctx, q, tx, uuidString(next.AccountID), retained, current, frame, now)
		if err != nil {
			return false, err
		}
		live, liveErr := q.GetNativeWorkflowRun(ctx, tx, mustPgUUID(frame.Target.RunID))
		if errors.Is(liveErr, pgx.ErrNoRows) {
			diagnosis.Reason, diagnosis.Repairable = "run_not_retained", false
		} else if liveErr != nil {
			return false, fmt.Errorf("inspect blocked retry live generation: %w", liveErr)
		} else if live.ResumeCount != int32(frame.Target.ExpectedResumeCount) {
			diagnosis.Reason, diagnosis.Repairable = "generation_superseded", false
		}
	} else {
		var observed api.WorkflowInitialEvidenceDiagnosis
		observed, err = retryEvidenceTx(ctx, q, tx, uuidString(next.AccountID), retained, current, frame, now)
		diagnosis = retryEvidenceDiagnosis(observed, action.ActionID)
		diagnosis.EvidenceKind = "child"
		diagnosis.ItemPosition = 1
	}
	if err != nil {
		return false, err
	}
	transition := automaticEvidenceTransition(uuidString(next.AppID), p.Slug, retained.ID, retained.PolicyVersion, action, diagnosis, now)
	if transition == nil {
		return true, tx.Commit(ctx)
	}
	receivers, err := q.WorkflowRecoveryReviewNotificationReceivers(ctx, tx, sqlc.WorkflowRecoveryReviewNotificationReceiversParams{AccountID: next.AccountID, AppID: next.AppID, Event: string(AppWebhookEventRecoveryQueueBlocked)})
	if err != nil {
		return false, fmt.Errorf("select blocked retry receivers: %w", err)
	}
	ids := []string{}
	for _, id := range receivers {
		ids = append(ids, uuidString(id))
	}
	payload := *transition
	receipt, err := json.Marshal(automaticEvidenceReceipt(payload, action.BlockedNotification, ids))
	if err != nil {
		return false, fmt.Errorf("encode blocked retry receipt: %w", err)
	}
	if err = q.WorkflowRecoveryAutomaticBlockedCapture(ctx, tx, sqlc.WorkflowRecoveryAutomaticBlockedCaptureParams{ActionID: row.ActionID, Receipt: receipt}); err != nil {
		return false, fmt.Errorf("retain blocked retry capture: %w", err)
	}
	if err = appendRecoveryHistoryEvent(ctx, q, tx, uuidString(next.AppID), retryAlertHistory(retained, payload)); err != nil {
		return false, err
	}
	if len(receivers) > 0 {
		raw, err := json.Marshal(payload)
		if err != nil {
			return false, fmt.Errorf("encode blocked retry payload: %w", err)
		}
		if err = q.WorkflowRecoveryReviewNotificationEnqueue(ctx, tx, sqlc.WorkflowRecoveryReviewNotificationEnqueueParams{EventID: mustPgUUID(payload.EventID), AccountID: next.AccountID, AppID: next.AppID, Event: string(AppWebhookEventRecoveryQueueBlocked), ReviewID: mustPgUUID(payload.EventID), Payload: raw, ReceiverIds: receivers, NowAt: pgtypeFromTime(now)}); err != nil {
			return false, fmt.Errorf("enqueue blocked retry notification: %w", err)
		}
	}
	return true, tx.Commit(ctx)
}
