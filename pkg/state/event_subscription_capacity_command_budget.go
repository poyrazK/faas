package state

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func capacityCommandBudget(app, sub string, now time.Time, count int64, earliest *time.Time) (*api.EventSubscriptionCapacityCommandBudget, error) {
	limit := api.EventSubscriptionCapacityCommandMaxReceipts
	if count < 0 || count > int64(limit) || (count == 0) != (earliest == nil) || (earliest != nil && (!earliest.After(now))) {
		return nil, fmt.Errorf("invalid capacity command budget metadata")
	}
	out := &api.EventSubscriptionCapacityCommandBudget{AppID: canonicalMemUUID(app), SubscriptionID: canonicalMemUUID(sub), ObservedAt: now, Complete: true, RetainedReceipts: int(count), Limit: limit, RemainingSlots: limit - int(count), Full: count == int64(limit)}
	if earliest != nil {
		value := earliest.UTC()
		out.EarliestExpiryAt = &value
	}
	out.Advice = "New command receipt slots were available at this observation; concurrent commands can consume them."
	if out.Full {
		out.Advice = "Receipt budget is full; known unchanged-input command repeats remain available. Expiry can free slots. Commands without UUIDs have no idempotent receipt evidence."
	}
	return out, nil
}
func readCapacityCommandBudget(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, account, app, sub string, now time.Time) (*api.EventSubscriptionCapacityCommandBudget, error) {
	row, err := q.EventSubscriptionCapacityCommandBudget(ctx, db, sqlc.EventSubscriptionCapacityCommandBudgetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), ObservedAt: pgtypeFromTime(now)})
	if err != nil {
		return nil, err
	}
	return capacityCommandBudget(app, sub, now, row.RetainedReceipts, timestamptzToTimePtr(row.EarliestExpiryAt))
}
func (m *MemStore) capacityCommandBudgetLocked(ctx context.Context, account, app, sub string, now time.Time) (*api.EventSubscriptionCapacityCommandBudget, error) {
	var count int64
	var earliest *time.Time
	for _, row := range m.eventSubscriptionCapacityCommands {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !sameMemUUID(row.AccountID, account) || !sameMemUUID(row.AppID, app) || !sameMemUUID(row.SubscriptionID, sub) || !row.RetainUntil.After(now) {
			continue
		}
		count++
		if earliest == nil || row.RetainUntil.Before(*earliest) {
			value := row.RetainUntil
			earliest = &value
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return capacityCommandBudget(app, sub, now, count, earliest)
}
