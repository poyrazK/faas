package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) GetWorkflowRecoveryPolicyHealth(ctx context.Context, account, app string, seconds int) (api.WorkflowRecoveryPolicyHealth, error) {
	var out api.WorkflowRecoveryPolicyHealth
	if err := eventRecoveryIDs(account, app); err != nil {
		return out, err
	}
	if err := api.ValidateRecoveryHealthThreshold(seconds); err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, fmt.Errorf("begin recovery health snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	q := sqlc.New()
	queue, _, err := pgWorkflowRecoveryAutomaticQueue(ctx, q, tx, account, app, now, false)
	if err != nil {
		return out, err
	}
	if err = pgRecoveryQueueEvidence(ctx, q, tx, account, app, &queue); err != nil {
		return out, err
	}
	scope, err := q.WorkflowRecoveryOverviewApp(ctx, tx, sqlc.WorkflowRecoveryOverviewAppParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app)})
	if err != nil {
		return out, err
	}
	raw, err := q.WorkflowRecoveryOverviewManual(ctx, tx, sqlc.WorkflowRecoveryOverviewManualParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), NowAt: pgtypeFromTime(now), PolicyEnabled: queue.Policy.Policy.Enabled, PolicyVersion: queue.Policy.Version, SampleLimit: api.WorkflowRecoveryOverviewItemsMax})
	if err != nil {
		return out, fmt.Errorf("read manual recovery health: %w", err)
	}
	var manual recoveryOverviewManual
	if err = json.Unmarshal(raw, &manual); err != nil {
		return out, fmt.Errorf("decode manual recovery health: %w", err)
	}
	raw, err = q.WorkflowRecoveryOverviewRetry(ctx, tx, sqlc.WorkflowRecoveryOverviewRetryParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), NowAt: pgtypeFromTime(now), PolicyEnabled: queue.Policy.Policy.Enabled, PolicyVersion: queue.Policy.Version, SampleLimit: api.WorkflowRecoveryOverviewItemsMax, EscapedSlug: url.PathEscape(scope)})
	if err != nil {
		return out, fmt.Errorf("read manual retry health: %w", err)
	}
	if err = json.Unmarshal(raw, &manual); err != nil {
		return out, fmt.Errorf("decode manual retry health: %w", err)
	}
	alert, err := pgRecoveryOverviewAlert(ctx, q, tx, account, app, now)
	if err != nil {
		return out, err
	}
	out = recoveryOverview(api.RecoveryPolicyHealth(queue, seconds), manual, alert, scope)
	if err = tx.Commit(ctx); err != nil {
		return out, fmt.Errorf("commit recovery health snapshot: %w", err)
	}
	return out, nil
}

func pgRecoveryOverviewAlert(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, account, app string, now time.Time) (*api.WorkflowRecoveryOverviewAlert, error) {
	raw, err := q.WorkflowRecoveryOverviewLastAlert(ctx, tx, sqlc.WorkflowRecoveryOverviewLastAlertParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), NowAt: pgtypeFromTime(now), RetainedAfter: pgtypeFromTime(now.Add(-api.WorkflowRecoveryHistoryRetention))})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read last polling alert: %w", err)
	}
	n, err := pollingNotificationFromRaw(raw)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, nil
	}
	// Invalid retained identities must not panic or escape the scoped lookup.
	if _, e := uuid.Parse(n.Payload.EventID); e != nil {
		return nil, fmt.Errorf("%w: invalid polling alert identity", ErrEventRecoveryQuery)
	}
	ids := make([]pgtype.UUID, 0, len(n.ReceiverWebhookIDs))
	for _, id := range n.ReceiverWebhookIDs {
		if _, e := uuid.Parse(id); e != nil {
			return nil, fmt.Errorf("%w: invalid polling receiver identity", ErrEventRecoveryQuery)
		}
		ids = append(ids, mustPgUUID(id))
	}
	raw, err = q.WorkflowRecoveryOverviewDelivery(ctx, tx, sqlc.WorkflowRecoveryOverviewDeliveryParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), EventID: mustPgUUID(n.Payload.EventID), Event: string(AppWebhookEventRecoveryPollingStatus), ReceiverIds: ids})
	if err != nil {
		return nil, fmt.Errorf("read polling alert delivery: %w", err)
	}
	a := &api.WorkflowRecoveryOverviewAlert{Notification: *n}
	if err = json.Unmarshal(raw, a); err != nil {
		return nil, fmt.Errorf("decode polling alert delivery: %w", err)
	}
	recoveryOverviewDelivery(a)
	return a, nil
}
