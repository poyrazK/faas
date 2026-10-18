package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// WorkflowInitialManualCompletionStore observes already approved preview-mode
// jobs independently of automatic execution authority and scheduler budgets.
type WorkflowInitialManualCompletionStore interface {
	ProcessNextWorkflowInitialManualCompletion(context.Context, time.Time) (bool, error)
}

// Observation never grants submission authority or changes automatic accounting.
func (m *MemStore) ProcessNextWorkflowInitialManualCompletion(ctx context.Context, now time.Time) (bool, error) {
	if now.IsZero() {
		return false, ErrEventRecoveryQuery
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	var chosen *api.WorkflowInitialRecoveryReview
	for _, r := range m.initialRecoveryReviews {
		app, ok := m.eventSubscriptionAppLocked(r.AppID)
		if !ok || app.Status == AppDeleted || r.Policy.Mode != "preview" || !initialReviewUnresolved(r) || m.initialCompletionNext[r.ID].After(now) {
			continue
		}
		if chosen == nil || m.initialCompletionNext[r.ID].Before(m.initialCompletionNext[chosen.ID]) || m.initialCompletionNext[r.ID].Equal(m.initialCompletionNext[chosen.ID]) && r.ID < chosen.ID {
			copied := recoveryPolicyClone(r)
			chosen = &copied
		}
	}
	if chosen == nil {
		return false, nil
	}
	r := *chosen
	app, _ := m.eventSubscriptionAppLocked(r.AppID)
	if err := validateManualObservationIdentity(r); err != nil {
		r.ObservationFailure = nextManualObservationFailure(r.ObservationFailure, "invalid_recovery_identity", now)
		n, captureErr := m.capturePollingAlertLocked(app, pollingAlertTransition(app, "manual_initial", r.ID, r.PolicyVersion, r.ObservationFailure.Code, r.ObservationFailure.Attempts, &r.ObservationFailure.RetryAt, r.PollingNotification, now))
		if captureErr != nil {
			return false, captureErr
		}
		if n != nil {
			r.PollingNotification = n
		}
		if m.initialCompletionNext == nil {
			m.initialCompletionNext = map[string]time.Time{}
		}
		m.initialCompletionNext[r.ID] = r.ObservationFailure.RetryAt
		m.initialRecoveryReviews[r.ID] = recoveryPolicyClone(r)
		return true, nil
	}
	r.ObservationFailure = nil
	now = now.UTC().Truncate(time.Microsecond)
	reason := "evidence_unknown"
	var result *api.WorkflowRecoveryReviewActionStatus

	if job := m.eventRecoveryJobs[r.JobID]; job != nil && len(job.Items) == 1 && sameMemUUID(job.Job.AppID, r.AppID) && sameMemUUID(job.AccountID, app.AccountID) {
		item := job.Items[0]
		if item.WorkflowRunID == r.Target.RunID && item.ExpectedResumeCount != nil && *item.ExpectedResumeCount == 0 {
			result, reason = initialAutomaticOutcome(r, job.Job, item.EventRecoveryItem, m.workflowRecoveryExecutionLocked(job, item, now), now)
			if result == nil && job.Job.State == "paused" {
				reason = "job_paused"
			}
		}
	}
	if result != nil {
		r.Result = result
		ctx = WithEventRecoveryActor(ctx, "system", "initial_completion_observer")
		initialReviewTransition(ctx, &r, "submitted", "result_recorded", now)
		m.recordInitialLifecycleLocked(r)

	}
	if err := m.captureInitialManualAttentionLocked(app.AccountID, &r, reason, now); err != nil {
		return false, err
	}
	n, captureErr := m.capturePollingAlertLocked(app, pollingAlertTransition(app, "manual_initial", r.ID, r.PolicyVersion, "", 0, nil, r.PollingNotification, now))
	if captureErr != nil {
		return false, captureErr
	}
	if n != nil {
		r.PollingNotification = n
	}
	m.initialRecoveryReviews[r.ID] = recoveryPolicyClone(r)
	if result != nil {
		delete(m.initialCompletionNext, r.ID)
		return true, nil
	}
	if m.initialCompletionNext == nil {
		m.initialCompletionNext = map[string]time.Time{}
	}
	m.initialCompletionNext[r.ID] = now.Add(api.WorkflowRecoveryPolicyPollInterval)
	return true, nil
}

func (s *PgStore) ProcessNextWorkflowInitialManualCompletion(ctx context.Context, now time.Time) (bool, error) {
	if now.IsZero() {
		return false, ErrEventRecoveryQuery
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	now = now.UTC().Truncate(time.Microsecond)
	q := sqlc.New()
	next, err := q.WorkflowInitialManualCompletionNext(ctx, s.pool, pgtypeFromTime(now))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("select manual initial completion: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin manual initial completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = q.EventReplayBackfillLockAccountRange(ctx, tx, next.AccountID); err != nil {
		return false, fmt.Errorf("lock manual initial completion account: %w", err)
	}
	// Match account -> policy -> review order used by manual decisions and pruning.
	if _, err = q.WorkflowInitialReviewPolicy(ctx, tx, sqlc.WorkflowInitialReviewPolicyParams{AppID: next.AppID, AccountID: next.AccountID}); err != nil {
		return false, fmt.Errorf("lock manual initial completion policy: %w", err)
	}
	raw, err := q.WorkflowInitialManualCompletionClaim(ctx, tx, sqlc.WorkflowInitialManualCompletionClaimParams{ID: next.ID, NowAt: pgtypeFromTime(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim manual initial completion: %w", err)
	}
	work, err := tx.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin manual observation savepoint: %w", err)
	}
	var alertFailure *api.WorkflowInitialObservationFailure
	err = observeManualInitialReviewTx(ctx, q, work, raw, uuidString(next.AccountID), uuidString(next.ID), uuidString(next.AppID), now)
	if err == nil {
		if err = work.Commit(ctx); err != nil {
			return false, fmt.Errorf("commit manual observation savepoint: %w", err)
		}
	} else {
		code := manualObservationFailureCode(err)
		if code == "" || ctx.Err() != nil {
			return false, err
		}
		if rollbackErr := work.Rollback(ctx); rollbackErr != nil {
			return false, fmt.Errorf("rollback manual observation: %w", rollbackErr)
		}
		previous := manualObservationFailureFromRaw(raw)
		failure := nextManualObservationFailure(previous, code, now)
		alertFailure = failure
		encoded, encodeErr := json.Marshal(failure)
		if encodeErr != nil {
			return false, fmt.Errorf("encode manual observation failure: %w", encodeErr)
		}
		if err = q.WorkflowInitialManualCompletionFailure(ctx, tx, sqlc.WorkflowInitialManualCompletionFailureParams{ID: next.ID, Failure: encoded, NextAt: pgtypeFromTime(failure.RetryAt)}); err != nil {
			return false, fmt.Errorf("defer failed manual observation: %w", err)
		}
	}
	previousNotification, decodeErr := manualPollingNotificationFromRaw(raw)
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
	app := App{ID: uuidString(next.AppID), AccountID: uuidString(next.AccountID), Slug: next.Slug}
	notification, captureErr := capturePollingAlertTx(ctx, q, tx, app, pollingAlertTransition(app, "manual_initial", uuidString(next.ID), next.PolicyVersion, code, attempts, retry, previousNotification, now))
	if captureErr != nil {
		return false, captureErr
	}
	if notification != nil {
		encoded, e := json.Marshal(notification)
		if e != nil {
			return false, fmt.Errorf("encode manual polling receipt: %w", e)
		}
		if e = q.WorkflowInitialPollingNotificationSave(ctx, tx, sqlc.WorkflowInitialPollingNotificationSaveParams{ID: next.ID, Notification: encoded}); e != nil {
			return false, fmt.Errorf("save manual polling receipt: %w", e)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit manual initial completion: %w", err)
	}
	return true, nil
}

func observeManualInitialReviewTx(ctx context.Context, q *sqlc.Queries, work pgx.Tx, raw []byte, account, id, app string, now time.Time) error {
	r, err := decodeInitialReview(raw)
	if err != nil {
		return err
	}
	if err = validateClaimedManualObservation(r, id, app); err != nil {
		return err
	}
	r.ObservationFailure = nil
	result, reason, err := observeInitialAutomaticTx(ctx, q, work, r, account, now)
	if err != nil {
		return err
	}
	if result != nil {
		r.Result = result
		ctx = WithEventRecoveryActor(ctx, "system", "initial_completion_observer")
		initialReviewTransition(ctx, &r, "submitted", "result_recorded", now)
		if err = appendRecoveryHistoryEvent(ctx, q, work, r.AppID, initialLifecycleHistory(r)); err != nil {
			return err
		}
	}
	if err = captureInitialManualAttentionTx(ctx, q, work, account, &r, reason, now); err != nil {
		return err
	}
	if err = initialReviewSaveTx(ctx, q, work, r); err != nil {
		return err
	}
	if err = q.WorkflowInitialManualCompletionAdvance(ctx, work, sqlc.WorkflowInitialManualCompletionAdvanceParams{ID: mustPgUUID(r.ID), NextAt: pgtypeFromTime(now.Add(api.WorkflowRecoveryPolicyPollInterval))}); err != nil {
		return fmt.Errorf("advance manual initial completion: %w", err)
	}

	return nil
}
