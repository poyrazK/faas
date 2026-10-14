package state

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

func captureInitialAutomaticBlockTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, account string, r *api.WorkflowInitialRecoveryReview, reason string, now time.Time) error {
	if !initialAttentionNeedsDiagnosis(*r, reason) {
		return nil
	}
	d, err := initialEvidenceTx(ctx, q, tx, account, *r, now)
	if err != nil {
		return err
	}
	payload := initialAutomaticBlockTransition(r, reason, now, d)
	if payload == nil {
		return nil
	}
	receipt, err := captureInitialRecoveryAttentionTx(ctx, q, tx, account, r, payload, r.AutomaticBlockSequence, now)
	if err != nil {
		return err
	}
	r.AutomaticBlockNotification = receipt
	return nil
}

func captureInitialRecoveryAttentionTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, account string, r *api.WorkflowInitialRecoveryReview, payload *api.WorkflowRecoveryQueueBlockedWebhookPayload, sequence int64, now time.Time) (*api.WorkflowRecoveryAutomaticBlockedNotification, error) {
	ids, err := q.WorkflowRecoveryReviewNotificationReceivers(ctx, tx, sqlc.WorkflowRecoveryReviewNotificationReceiversParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(r.AppID), Event: string(AppWebhookEventRecoveryQueueBlocked)})
	if err != nil {
		return nil, fmt.Errorf("select initial recovery attention receivers: %w", err)
	}
	receivers := []string{}
	for _, id := range ids {
		receivers = append(receivers, uuidString(id))
	}
	receipt := automaticBlockedReceipt(*payload, receivers)
	receipt.Sequence = sequence
	if err = appendRecoveryHistoryEvent(ctx, q, tx, r.AppID, initialAlertHistory(*r, *payload)); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return &receipt, nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode initial recovery attention: %w", err)
	}
	if err = q.WorkflowRecoveryReviewNotificationEnqueue(ctx, tx, sqlc.WorkflowRecoveryReviewNotificationEnqueueParams{EventID: mustPgUUID(payload.EventID), AccountID: mustPgUUID(account), AppID: mustPgUUID(r.AppID), Event: string(AppWebhookEventRecoveryQueueBlocked), ReviewID: mustPgUUID(payload.EventID), Payload: raw, ReceiverIds: ids, NowAt: pgtypeFromTime(now)}); err != nil {
		return nil, fmt.Errorf("enqueue initial recovery attention: %w", err)
	}
	return &receipt, nil
}
