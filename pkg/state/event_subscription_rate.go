package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrEventSubscriptionRateCommandLimit = errors.New("state: subscription rate command retention full")
var ErrEventSubscriptionRateCommandConflict = errors.New("state: subscription rate command UUID reused")

type EventSubscriptionRateStore interface {
	SetEventSubscriptionRateLimit(context.Context, string, string, string, api.EventSubscriptionRateRequest) (api.EventSubscriptionRateCommandReceipt, error)
	GetEventSubscriptionRateCommand(context.Context, string, string, string, string) (api.EventSubscriptionRateCommandReceipt, error)
	ListEventSubscriptionRateCommands(context.Context, string, string, string, int64) (api.EventSubscriptionRateCommands, error)
}

var _ EventSubscriptionRateStore = (*PgStore)(nil)
var _ EventSubscriptionRateStore = (*MemStore)(nil)

// Integer millisecond credit preserves the refill remainder between polls.
func subscriptionRateStatus(enabled bool, revision, interval, burst, credit int64, anchor, now time.Time) (api.EventSubscriptionRateLimit, int64, time.Time) {
	future := anchor.After(now)
	capacity := interval * burst
	elapsed := min(max(int64(0), now.Sub(anchor).Milliseconds()), capacity)
	credit = min(capacity, credit+elapsed)
	if credit == capacity && !future {
		anchor = now
	} else {
		anchor = anchor.Add(time.Duration(elapsed) * time.Millisecond)
	}
	out := api.EventSubscriptionRateLimit{Enabled: enabled, Revision: revision, IntervalMillis: interval, Burst: burst, ObservedAt: now}
	if enabled {
		out.AvailableTokens = credit / interval
		out.RateLimited = out.AvailableTokens == 0 || future
		if out.RateLimited {
			next := anchor.Add(time.Duration(max(int64(1), interval-credit)) * time.Millisecond)
			out.NextAvailableAt = &next
		}
	}
	return out, credit, anchor
}
func subscriptionRateSQL(row sqlc.EventSubscriptionDeliveryControl, now time.Time) (api.EventSubscriptionRateLimit, int64, time.Time) {
	return subscriptionRateStatus(row.RateEnabled, row.RateRevision, row.RateIntervalMs, row.RateBurst, row.RateCreditMs, timeFromPgtype(row.RateRefilledAt), now)
}
func normalizeSubscriptionRate(req api.EventSubscriptionRateRequest) (api.EventSubscriptionRateRequest, []byte, error) {
	if req.Validate() != nil {
		return req, nil, ErrInvalidArgument
	}
	req.CommandID = strings.ToLower(req.CommandID)
	raw, err := json.Marshal(req)
	var copy api.EventSubscriptionRateRequest
	if err == nil {
		err = json.Unmarshal(raw, &copy)
	}
	return copy, raw, err
}
func rateCommandReceipt(req api.EventSubscriptionRateRequest, app, sub string, sequence int64, status api.EventSubscriptionRateLimit, applied bool, now time.Time) api.EventSubscriptionRateCommandReceipt {
	outcome := "applied"
	if !applied {
		outcome = "rejected"
	}
	return api.EventSubscriptionRateCommandReceipt{CommandID: req.CommandID, AppID: canonicalMemUUID(app), SubscriptionID: canonicalMemUUID(sub), Sequence: sequence, Outcome: outcome, Result: status, RecordedAt: now, RetainUntil: now.Add(api.EventSubscriptionRateCommandRetention)}
}
func (s *PgStore) SetEventSubscriptionRateLimit(ctx context.Context, account, app, sub string, req api.EventSubscriptionRateRequest) (api.EventSubscriptionRateCommandReceipt, error) {
	var zero api.EventSubscriptionRateCommandReceipt
	req, payload, err := normalizeSubscriptionRate(req)
	if err != nil {
		return zero, err
	}
	if err = validSubscriptionControlIDs(account, app, sub); err != nil {
		return zero, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.EventSubscriptionControlTarget(ctx, tx, sqlc.EventSubscriptionControlTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)}); err != nil {
		return zero, mapErr(err)
	}
	if err = q.EventSubscriptionControlLock(ctx, tx, canonicalMemUUID(sub)); err != nil {
		return zero, err
	}
	stamp, err := q.EventSubscriptionRateClock(ctx, tx)
	if err != nil {
		return zero, err
	}
	now := timeFromPgtype(stamp)
	saved, err := q.EventSubscriptionRateCommandGet(ctx, tx, sqlc.EventSubscriptionRateCommandGetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), CommandID: mustPgUUID(req.CommandID), NowAt: stamp})
	if err == nil {
		var original api.EventSubscriptionRateRequest
		if json.Unmarshal(saved.Request, &original) != nil {
			return zero, ErrConflict
		}
		_, raw, decodeErr := normalizeSubscriptionRate(original)
		if decodeErr != nil || !bytes.Equal(raw, payload) {
			return zero, ErrEventSubscriptionRateCommandConflict
		}
		if err = json.Unmarshal(saved.Receipt, &zero); err != nil {
			return zero, err
		}
		return zero, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return zero, err
	}
	if err = q.EventSubscriptionRateInitialize(ctx, tx, sqlc.EventSubscriptionRateInitializeParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)}); err != nil {
		return zero, err
	}
	if err = q.EventSubscriptionRateCommandsExpire(ctx, tx, sqlc.EventSubscriptionRateCommandsExpireParams{SubscriptionID: mustPgUUID(sub), NowAt: stamp}); err != nil {
		return zero, err
	}
	count, err := q.EventSubscriptionRateCommandsCount(ctx, tx, sqlc.EventSubscriptionRateCommandsCountParams{SubscriptionID: mustPgUUID(sub), NowAt: stamp})
	if err != nil {
		return zero, err
	}
	if count >= api.EventSubscriptionRateCommandMaxReceipts {
		return zero, ErrEventSubscriptionRateCommandLimit
	}
	previous, err := q.EventSubscriptionControlGet(ctx, tx, sqlc.EventSubscriptionControlGetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)})
	if err != nil {
		return zero, err
	}
	_, oldCredit, _ := subscriptionRateSQL(previous, now)
	if previous.RateEnabled && timeFromPgtype(previous.RateRefilledAt).After(now) {
		return zero, ErrConflict
	}
	applied := previous.RateRevision == *req.ExpectedRevision
	credit := req.IntervalMillis * req.Burst
	if previous.RateEnabled && *req.Enabled {
		credit = min(credit, oldCredit*req.IntervalMillis/previous.RateIntervalMs)
	}
	current, err := q.EventSubscriptionRateConfigure(ctx, tx, sqlc.EventSubscriptionRateConfigureParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), Applied: applied, Enabled: *req.Enabled, IntervalMs: req.IntervalMillis, Burst: req.Burst, CreditMs: credit, NowAt: stamp})
	if err != nil {
		return zero, err
	}
	status, _, _ := subscriptionRateSQL(current, now)
	receipt := rateCommandReceipt(req, app, sub, current.RateSequence, status, applied, now)
	raw, err := json.Marshal(receipt)
	if err != nil {
		return zero, err
	}
	if err = q.EventSubscriptionRateCommandInsert(ctx, tx, sqlc.EventSubscriptionRateCommandInsertParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), CommandID: mustPgUUID(req.CommandID), Sequence: receipt.Sequence, Request: payload, Receipt: raw, NowAt: stamp, RetainUntil: pgtypeFromTime(receipt.RetainUntil)}); err != nil {
		return zero, err
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, err
	}
	return receipt, nil
}
func (s *PgStore) GetEventSubscriptionRateCommand(ctx context.Context, account, app, sub, command string) (api.EventSubscriptionRateCommandReceipt, error) {
	var out api.EventSubscriptionRateCommandReceipt
	if validSubscriptionControlIDs(account, app, sub) != nil || !validRateCommandID(command) {
		return out, ErrInvalidArgument
	}
	q := sqlc.New()
	if _, err := q.EventConsumerPressureTarget(ctx, s.pool, sqlc.EventConsumerPressureTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)}); err != nil {
		return out, mapErr(err)
	}
	now, err := q.EventSubscriptionRateClock(ctx, s.pool)
	if err != nil {
		return out, err
	}
	row, err := q.EventSubscriptionRateCommandGet(ctx, s.pool, sqlc.EventSubscriptionRateCommandGetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), CommandID: mustPgUUID(command), NowAt: now})
	if err != nil {
		return out, mapErr(err)
	}
	err = json.Unmarshal(row.Receipt, &out)
	return out, err
}
func validRateCommandID(command string) bool {
	zero := int64(0)
	enabled := false
	return (api.EventSubscriptionRateRequest{Enabled: &enabled, CommandID: command, ExpectedRevision: &zero, IntervalMillis: 1000, Burst: 1}).Validate() == nil
}
func (s *PgStore) ListEventSubscriptionRateCommands(ctx context.Context, account, app, sub string, after int64) (api.EventSubscriptionRateCommands, error) {
	out := api.EventSubscriptionRateCommands{Receipts: []api.EventSubscriptionRateCommandReceipt{}}
	if validSubscriptionControlIDs(account, app, sub) != nil || after < 0 {
		return out, ErrInvalidArgument
	}
	q := sqlc.New()
	if _, err := q.EventConsumerPressureTarget(ctx, s.pool, sqlc.EventConsumerPressureTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)}); err != nil {
		return out, mapErr(err)
	}
	now, err := q.EventSubscriptionRateClock(ctx, s.pool)
	if err != nil {
		return out, err
	}
	rows, err := q.EventSubscriptionRateCommandsList(ctx, s.pool, sqlc.EventSubscriptionRateCommandsListParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), AfterSequence: after, NowAt: now, PageLimit: int32(api.EventSubscriptionRateCommandPageSize + 1)})
	if err != nil {
		return out, err
	}
	for i, raw := range rows {
		if i == api.EventSubscriptionRateCommandPageSize {
			out.NextAfter = out.Receipts[api.EventSubscriptionRateCommandPageSize-1].Sequence
			break
		}
		var receipt api.EventSubscriptionRateCommandReceipt
		if err = json.Unmarshal(raw, &receipt); err != nil {
			return out, err
		}
		out.Receipts = append(out.Receipts, receipt)
	}
	return out, nil
}

// Short rechecks let a relaxed configuration affect an already deferred row.
func subscriptionRateRetry(now, next time.Time) time.Time {
	retry := now.Add(api.EventSubscriptionControlRetryDelay)
	if next.Before(retry) {
		return next
	}
	return retry
}
