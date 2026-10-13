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

func (s *PgStore) ProcessNextWorkflowRecoveryHealthAlert(ctx context.Context, now time.Time) (bool, error) {
	if now.IsZero() {
		return false, ErrEventRecoveryQuery
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return false, fmt.Errorf("begin recovery health alert: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	claim, err := q.WorkflowRecoveryHealthClaim(ctx, tx, pgtypeFromTime(now))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim recovery health policy: %w", err)
	}
	queue, _, err := pgWorkflowRecoveryAutomaticQueue(ctx, q, tx, uuidString(claim.AccountID), uuidString(claim.AppID), now, false)
	if err != nil {
		return false, err
	}
	seconds := api.WorkflowRecoveryHealthDefaultStallSeconds
	if queue.Policy.Policy.HealthAlerts != nil {
		seconds = queue.Policy.Policy.HealthAlerts.StallAfterSeconds
	}
	health := api.RecoveryPolicyHealth(queue, seconds)
	next, event := recoveryHealthTransition(queue.HealthIncident, health, recoveryHealthAlertEnabled(queue.Policy.Policy))
	if event != "" {
		eligible, err := q.WorkflowRecoveryReviewNotificationReceivers(ctx, tx, sqlc.WorkflowRecoveryReviewNotificationReceiversParams{AccountID: claim.AccountID, AppID: claim.AppID, Event: string(event)})
		if err != nil {
			return false, fmt.Errorf("read recovery health receivers: %w", err)
		}
		receivers := []pgtype.UUID{}
		ids := []string{}
		for _, id := range eligible {
			if recoveryHealthReceiverAllowed(next, event, uuidString(id)) {
				receivers = append(receivers, id)
				ids = append(ids, uuidString(id))
			}
		}
		payload := recoveryHealthPayload(next, health, claim.Slug, event)
		recoveryHealthCapture(next, payload, event, ids)
		if len(ids) > 0 {
			raw, err := json.Marshal(payload)
			if err != nil {
				return false, fmt.Errorf("encode recovery health payload: %w", err)
			}
			if err = q.WorkflowRecoveryReviewNotificationEnqueue(ctx, tx, sqlc.WorkflowRecoveryReviewNotificationEnqueueParams{EventID: mustPgUUID(payload.EventID), AccountID: claim.AccountID, AppID: claim.AppID, Event: string(event), ReviewID: mustPgUUID(payload.EventID), Payload: raw, ReceiverIds: receivers, NowAt: pgtypeFromTime(now)}); err != nil {
				return false, fmt.Errorf("enqueue recovery health alert: %w", err)
			}
		}
	}
	for _, e := range recoveryHealthHistoryEvents(queue.HealthIncident, next, queue.Policy.Version) {
		if err = appendRecoveryHistoryEvent(ctx, q, tx, uuidString(claim.AppID), e); err != nil {
			return false, err
		}
	}
	if next != nil {
		raw, err := json.Marshal(next)
		if err != nil {
			return false, fmt.Errorf("encode recovery health incident: %w", err)
		}
		if err = q.WorkflowRecoveryHealthIncidentSet(ctx, tx, sqlc.WorkflowRecoveryHealthIncidentSetParams{AppID: claim.AppID, Incident: raw}); err != nil {
			return false, fmt.Errorf("retain recovery health incident: %w", err)
		}
	}
	if err = q.WorkflowRecoveryHealthAdvance(ctx, tx, sqlc.WorkflowRecoveryHealthAdvanceParams{AppID: claim.AppID, NextAt: pgtypeFromTime(now.Add(api.WorkflowRecoveryPolicyPollInterval))}); err != nil {
		return false, fmt.Errorf("defer recovery health monitor: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit recovery health alert: %w", err)
	}
	return true, nil
}
