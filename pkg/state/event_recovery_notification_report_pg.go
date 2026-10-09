package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) GetEventRecoveryNotifications(ctx context.Context, account, id string, now time.Time) (api.EventRecoveryNotifications, error) {
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	var out api.EventRecoveryNotifications
	if err := eventRecoveryIDs(account, id); err != nil {
		return out, err
	}
	if now.IsZero() {
		return out, ErrEventRecoveryQuery
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	job, err := getEventRecoveryMetadata(ctx, q, tx, account, id)
	if err != nil {
		return out, err
	}
	info, err := q.EventRecoveryNotificationEvidence(ctx, tx, sqlc.EventRecoveryNotificationEvidenceParams{AccountID: mustPgUUID(account), JobID: mustPgUUID(id)})
	if err != nil {
		return out, mapErr(err)
	}
	evidence := recoveryNotificationEvidence{Receipts: map[string]recoveryNotificationReceipt{}, Outbox: map[string]recoveryNotificationReceipt{}, Deliveries: map[string][]api.EventRecoveryNotificationReceiver{}, Available: map[string]bool{}, Truncated: map[string]bool{}}
	if err := json.Unmarshal(info.NotificationReceipts, &evidence.Receipts); err != nil {
		return out, err
	}
	if err := validateRecoveryNotificationReceipts(job.ID, evidence.Receipts); err != nil {
		return out, err
	}
	retained, err := q.EventRecoveryNotificationOutbox(ctx, tx, sqlc.EventRecoveryNotificationOutboxParams{JobID: mustPgUUID(id), AccountID: mustPgUUID(account), AppID: mustPgUUID(job.AppID)})
	if err != nil {
		return out, err
	}
	for _, row := range retained {
		if uuidString(row.ID) != recoveryNotificationEventID(job.ID, row.Event) {
			continue
		}
		receipt := recoveryNotificationReceipt{EventID: uuidString(row.ID), CapturedAt: timeFromPgtype(row.CreatedAt), RecipientWebhookIDs: []string{}}
		for _, recipient := range row.RecipientWebhookIds {
			receipt.RecipientWebhookIDs = append(receipt.RecipientWebhookIDs, uuidString(recipient))
		}
		evidence.Outbox[row.Event] = receipt
	}
	ids := map[string]bool{}
	for _, receipts := range []map[string]recoveryNotificationReceipt{evidence.Receipts, evidence.Outbox} {
		for _, receipt := range receipts {
			selected := append([]string{}, receipt.RecipientWebhookIDs...)
			sort.Strings(selected)
			for _, recipient := range selected[:min(len(selected), api.EventRecoveryNotificationReceiversMax)] {
				ids[recipient] = true
			}
		}
	}
	hooks := make([]pgtype.UUID, 0, len(ids))
	for recipient := range ids {
		hooks = append(hooks, mustPgUUID(recipient))
	}
	available, err := q.EventRecoveryNotificationReceivers(ctx, tx, sqlc.EventRecoveryNotificationReceiversParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(job.AppID), WebhookIds: hooks})
	if err != nil {
		return out, err
	}
	for _, hook := range available {
		evidence.Available[uuidString(hook)] = true
	}
	for _, event := range recoveryNotificationEvents {
		rows, err := q.EventRecoveryNotificationDeliveries(ctx, tx, sqlc.EventRecoveryNotificationDeliveriesParams{EventID: mustPgUUID(recoveryNotificationEventID(job.ID, event)), Event: event, AccountID: mustPgUUID(account), AppID: mustPgUUID(job.AppID), ReceiverLimit: api.EventRecoveryNotificationReceiversMax + 1})
		if err != nil {
			return out, err
		}
		evidence.Truncated[event] = len(rows) > api.EventRecoveryNotificationReceiversMax
		for _, row := range rows {
			evidence.Deliveries[event] = append(evidence.Deliveries[event], api.EventRecoveryNotificationReceiver{WebhookID: uuidString(row.WebhookID), DeliveryID: uuidString(row.ID), ReceiverAvailable: row.ReceiverAvailable, Status: row.Status, Attempt: int(row.Attempt), ReplayGeneration: int(row.ReplayGeneration), LastResponseCode: int(row.LastResponseCode), NextAttemptAt: timestamptzToTimePtr(row.NextAttemptAt), DeliveredAt: timestamptzToTimePtr(row.DeliveredAt)})
		}
	}
	out, err = recoveryNotificationReport(job, info.AppSlug, info.ExecutionNotificationCaptured, now, evidence)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
