package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) PreviewEventRecoveryNotificationRetry(ctx context.Context, account, id string, now time.Time) (api.EventRecoveryNotificationRetryPreview, error) {
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	var out api.EventRecoveryNotificationRetryPreview
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
	report, err := getEventRecoveryNotifications(ctx, q, tx, account, id, now)
	if err != nil {
		return out, err
	}
	ids := []pgtype.UUID{}
	for _, notice := range report.Notifications {
		for _, r := range notice.Receivers {
			ids = append(ids, mustPgUUID(r.WebhookID))
		}
	}
	rows, err := q.EventRecoveryNotificationRetryHooks(ctx, tx, sqlc.EventRecoveryNotificationRetryHooksParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(report.AppID), WebhookIds: ids})
	if err != nil {
		return out, err
	}
	enabled := map[string]bool{}
	for _, r := range rows {
		enabled[uuidString(r.ID)] = r.Enabled
	}
	plan, err := q.EventRecoveryNotificationRetryPlan(ctx, tx, mustPgUUID(account))
	if err != nil {
		return out, mapErr(err)
	}
	out = recoveryNotificationRetryPreview(report, enabled, recoveryNotificationRetryAllowed(api.Plan(plan)))
	return out, tx.Commit(ctx)
}
func (s *PgStore) RetryEventRecoveryNotifications(ctx context.Context, account, id string, req api.EventRecoveryNotificationRetryRequest, now time.Time) (api.EventRecoveryNotificationRetryResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	var out api.EventRecoveryNotificationRetryResponse
	if err := validateRecoveryNotificationRetry(account, id, now, &req); err != nil {
		return out, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	owner, err := q.EventRecoveryNotificationRetryOwner(ctx, tx, sqlc.EventRecoveryNotificationRetryOwnerParams{JobID: mustPgUUID(id), AccountID: mustPgUUID(account)})
	if err != nil {
		return out, mapErr(err)
	}
	receipts := map[string]json.RawMessage{}
	if err := json.Unmarshal(owner.NotificationRetryReceipts, &receipts); err != nil {
		return out, err
	}
	if prior, ok, err := recoveryNotificationRetryPrior(receipts, req); ok || err != nil {
		return prior, err
	}
	if len(receipts) >= api.EventRecoveryNotificationRetryReceiptsMax {
		return out, ErrEventRecoveryNotificationRetryLimit
	}
	report, err := getEventRecoveryNotifications(ctx, q, tx, account, id, now)
	if err != nil {
		return out, err
	}
	plan, err := q.EventRecoveryNotificationRetryPlanLock(ctx, tx, mustPgUUID(account))
	if err != nil {
		return out, mapErr(err)
	}
	allowed := recoveryNotificationRetryAllowed(api.Plan(plan))
	out = api.EventRecoveryNotificationRetryResponse{JobID: report.JobID, AppID: report.AppID, RequestID: req.RequestID, DecidedAt: now, Results: []api.EventRecoveryNotificationRetryResult{}}
	for _, target := range req.Targets {
		result, err := retryRecoveryNotificationTarget(ctx, q, tx, account, report, target, allowed, now)
		if err != nil {
			return api.EventRecoveryNotificationRetryResponse{}, err
		}
		out.Results = append(out.Results, result)
	}
	actor := recoveryAuditActor(ctx, "notification_retry")
	raw, err := json.Marshal(recoveryNotificationRetryReceipt{Request: req, Response: out, ActorKind: actor.Kind, ActorID: actor.ID})
	if err != nil {
		return api.EventRecoveryNotificationRetryResponse{}, err
	}
	if err := q.EventRecoveryNotificationRetrySave(ctx, tx, sqlc.EventRecoveryNotificationRetrySaveParams{RequestID: req.RequestID, Receipt: raw, JobID: mustPgUUID(id), AccountID: mustPgUUID(account)}); err != nil {
		return api.EventRecoveryNotificationRetryResponse{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.EventRecoveryNotificationRetryResponse{}, err
	}
	return out, nil
}
func retryRecoveryNotificationTarget(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, account string, report api.EventRecoveryNotifications, target api.EventRecoveryNotificationRetryTarget, allowed bool, now time.Time) (api.EventRecoveryNotificationRetryResult, error) {
	result := api.EventRecoveryNotificationRetryResult{Target: target, State: "skipped"}
	_, event, reason := recoveryNotificationRetryReceiver(report, target)
	if reason != "" {
		result.Reason = reason
		return result, nil
	}
	enabled, err := q.EventRecoveryNotificationRetryHookLock(ctx, db, sqlc.EventRecoveryNotificationRetryHookLockParams{WebhookID: mustPgUUID(target.WebhookID), AccountID: mustPgUUID(account), AppID: mustPgUUID(report.AppID)})
	if errors.Is(err, pgx.ErrNoRows) {
		result.Reason = "receiver_unavailable"
		return result, nil
	}
	if err != nil {
		return result, err
	}
	row, err := q.EventRecoveryNotificationRetryDeliveryLock(ctx, db, sqlc.EventRecoveryNotificationRetryDeliveryLockParams{DeliveryID: mustPgUUID(target.DeliveryID), WebhookID: mustPgUUID(target.WebhookID), AccountID: mustPgUUID(account), AppID: mustPgUUID(report.AppID), Event: event, EventID: mustPgUUID(recoveryNotificationEventID(report.JobID, event))})
	if errors.Is(err, pgx.ErrNoRows) {
		result.Reason = "delivery_unavailable"
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.Reason = recoveryNotificationRetryReason(target.DeliveryID, row.Status, int(row.ReplayGeneration), true, enabled, allowed, target.ExpectedReplayGeneration)
	if result.Reason != "" {
		return result, nil
	}
	changed, err := q.EventRecoveryNotificationRetryReset(ctx, db, sqlc.EventRecoveryNotificationRetryResetParams{NowAt: pgtypeFromTime(now), DeliveryID: mustPgUUID(target.DeliveryID), AccountID: mustPgUUID(account), WebhookID: mustPgUUID(target.WebhookID), AppID: mustPgUUID(report.AppID), Event: event, EventID: mustPgUUID(recoveryNotificationEventID(report.JobID, event)), ExpectedGeneration: int32(*target.ExpectedReplayGeneration)})
	if err != nil {
		return result, err
	}
	if changed != 1 {
		return result, ErrEventRecoveryRequestConflict
	}
	n := int(row.ReplayGeneration) + 1
	result.State = "queued"
	result.ReplayGeneration = &n
	return result, nil
}

func (s *PgStore) GetEventRecoveryNotificationRetryHistory(ctx context.Context, account, id string, now time.Time) (api.EventRecoveryNotificationRetryHistory, error) {
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	var out api.EventRecoveryNotificationRetryHistory
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
	owner, err := q.EventRecoveryNotificationRetryHistoryOwner(ctx, tx, sqlc.EventRecoveryNotificationRetryHistoryOwnerParams{JobID: mustPgUUID(id), AccountID: mustPgUUID(account)})
	if err != nil {
		return out, mapErr(err)
	}
	receipts, err := recoveryNotificationRetryReceipts(owner.NotificationRetryReceipts)
	if err != nil {
		return out, err
	}
	out, err = recoveryNotificationRetryHistory(uuidString(mustPgUUID(id)), uuidString(owner.AppID), now, receipts)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) GetEventRecoveryNotificationRetryDecision(ctx context.Context, account, id, requestID string, now time.Time) (api.EventRecoveryNotificationRetryDecisionDetail, error) {
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	var out api.EventRecoveryNotificationRetryDecisionDetail
	if err := eventRecoveryIDs(account, id); err != nil {
		return out, err
	}
	if now.IsZero() {
		return out, ErrEventRecoveryQuery
	}
	parsed, err := uuid.Parse(requestID)
	if err != nil || parsed == uuid.Nil || parsed.String() != requestID {
		return out, ErrEventRecoveryQuery
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	owner, err := q.EventRecoveryNotificationRetryHistoryOwner(ctx, tx, sqlc.EventRecoveryNotificationRetryHistoryOwnerParams{JobID: mustPgUUID(id), AccountID: mustPgUUID(account)})
	if err != nil {
		return out, mapErr(err)
	}
	receipts, err := recoveryNotificationRetryReceipts(owner.NotificationRetryReceipts)
	if err != nil {
		return out, err
	}
	raw, ok := receipts[requestID]
	if !ok {
		return out, ErrEventRecoveryNotificationRetryDecisionNotFound
	}
	var saved recoveryNotificationRetryReceipt
	if err := json.Unmarshal(raw, &saved); err != nil {
		return out, err
	}
	report, err := getEventRecoveryNotifications(ctx, q, tx, account, id, now)
	if err != nil {
		return out, err
	}
	out, err = recoveryNotificationRetryDecisionDetail(uuidString(mustPgUUID(id)), uuidString(owner.AppID), requestID, now, saved, report)
	if err != nil {
		return out, err
	}
	for i := range out.Decisions {
		row := &out.Decisions[i]
		if row.State != "queued" || row.ReplayGeneration == nil {
			continue
		}
		_, event, reason := recoveryNotificationRetryReceiver(report, row.Target)
		if reason != "" {
			continue
		}
		evidence, err := q.EventRecoveryNotificationRetryGenerationOutcome(ctx, tx, sqlc.EventRecoveryNotificationRetryGenerationOutcomeParams{
			DeliveryID: mustPgUUID(row.Target.DeliveryID), WebhookID: mustPgUUID(row.Target.WebhookID), AccountID: mustPgUUID(account), AppID: owner.AppID,
			Event: event, EventID: mustPgUUID(recoveryNotificationEventID(out.JobID, event)), Generation: int32(*row.ReplayGeneration),
		})
		if err != nil {
			return out, err
		}
		var completed *time.Time
		if evidence.CompletedAt.Valid {
			t := evidence.CompletedAt.Time
			completed = &t
		}
		recoveryNotificationRetryOutcome(row, int(evidence.RetainedCount), int(evidence.HighestAttempt), evidence.TerminalOutcome, completed)
	}
	return out, tx.Commit(ctx)
}
