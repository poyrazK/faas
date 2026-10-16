package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventSubscriptionCapacityStore interface {
	SetEventSubscriptionCapacity(context.Context, string, string, string, int) (api.EventSubscriptionDeliveryControl, error)
}

type EventSubscriptionCapacityGuardedStore interface {
	SetEventSubscriptionCapacityGuarded(context.Context, string, string, string, api.EventSubscriptionCapacityRequest) (api.EventSubscriptionDeliveryControl, error)
}

var ErrEventSubscriptionCapacityChanged = errors.New("state: subscription capacity configuration changed")

var _ EventSubscriptionCapacityGuardedStore = (*PgStore)(nil)
var _ EventSubscriptionCapacityGuardedStore = (*MemStore)(nil)

var _ EventSubscriptionCapacityStore = (*PgStore)(nil)
var _ EventSubscriptionCapacityStore = (*MemStore)(nil)

func effectiveEventConsumerCapacity(plan, configured int) int {
	if configured > 0 && configured < plan {
		return configured
	}
	return plan
}
func populateEventSubscriptionCapacity(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, account, app, sub string, out *api.EventSubscriptionDeliveryControl) error {
	acct, err := q.AccountByID(ctx, db, mustPgUUID(account))
	if err != nil {
		return mapErr(err)
	}
	limits, ok := api.LimitsFor(api.Plan(acct.Plan))
	if !ok {
		return ErrInvalidArgument
	}
	out.EffectiveMaxPendingDeliveries = effectiveEventConsumerCapacity(limits.EventDeliveries.PerConsumer, out.MaxPendingDeliveries)
	counts, err := q.EventDeliveryCounts(ctx, db, sqlc.EventDeliveryCountsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: canonicalMemUUID(sub)})
	out.LiveDeliveries = counts.ConsumerCount
	return err
}
func (s *PgStore) SetEventSubscriptionCapacity(ctx context.Context, account, app, sub string, limit int) (api.EventSubscriptionDeliveryControl, error) {
	return s.setEventSubscriptionCapacity(ctx, account, app, sub, limit, nil, "")
}
func (s *PgStore) SetEventSubscriptionCapacityGuarded(ctx context.Context, account, app, sub string, req api.EventSubscriptionCapacityRequest) (api.EventSubscriptionDeliveryControl, error) {
	if err := req.Validate(); err != nil {
		return api.EventSubscriptionDeliveryControl{}, ErrInvalidArgument
	}
	return s.setEventSubscriptionCapacity(ctx, account, app, sub, *req.MaxPendingDeliveries, req.Expected, req.CommandID)
}
func (s *PgStore) setEventSubscriptionCapacity(ctx context.Context, account, app, sub string, limit int, expected *api.EventSubscriptionCapacityGuard, command string) (api.EventSubscriptionDeliveryControl, error) {
	req, payload, normalizeErr := normalizeCapacityCommandRequest(api.EventSubscriptionCapacityRequest{MaxPendingDeliveries: &limit, Expected: expected, CommandID: command})
	if normalizeErr != nil {
		return api.EventSubscriptionDeliveryControl{}, normalizeErr
	}
	expected, command = req.Expected, req.CommandID
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	if limit < 0 {
		return api.EventSubscriptionDeliveryControl{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	plan, err := q.EventSubscriptionCapacityAccount(ctx, tx, mustPgUUID(account))
	if err != nil {
		return api.EventSubscriptionDeliveryControl{}, mapErr(err)
	}
	limits, valid := api.LimitsFor(api.Plan(plan))
	if !valid {
		return api.EventSubscriptionDeliveryControl{}, ErrInvalidArgument
	}
	// Serialize changes with routing and replay admission before taking app or
	// subscription locks. Lowering does not evict already admitted live work.
	if err = lockEventCapacity(ctx, tx, account, limits.EventDeliveries); err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	if _, err = q.EventSubscriptionControlTarget(ctx, tx, sqlc.EventSubscriptionControlTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)}); errors.Is(err, pgx.ErrNoRows) {
		return api.EventSubscriptionDeliveryControl{}, ErrNotFound
	} else if err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	if err = q.EventSubscriptionControlLock(ctx, tx, canonicalMemUUID(sub)); err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	priorReceipt, lookupErr := lookupCapacityCommandTx(ctx, q, tx, account, app, sub, req, payload, now)
	if lookupErr != nil {
		return api.EventSubscriptionDeliveryControl{}, lookupErr
	}
	if priorReceipt != nil {
		out, readErr := getSubscriptionControl(ctx, q, tx, account, app, sub)
		if readErr != nil {
			return out, readErr
		}
		out.CapacityCommandReceipt = priorReceipt
		return out, tx.Commit(ctx)
	}
	previous := 0
	if expected != nil || command != "" {
		configured, readErr := q.EventSubscriptionCapacityOverride(ctx, tx, sqlc.EventSubscriptionCapacityOverrideParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: canonicalMemUUID(sub)})
		if readErr != nil {
			return api.EventSubscriptionDeliveryControl{}, readErr
		}
		previous = int(configured)
		if expected != nil && !eventSubscriptionCapacityGuardMatches(expected, int(configured), limits.EventDeliveries.PerConsumer) {
			return api.EventSubscriptionDeliveryControl{}, ErrEventSubscriptionCapacityChanged
		}
	}
	if limit > limits.EventDeliveries.PerConsumer {
		return api.EventSubscriptionDeliveryControl{}, ErrInvalidArgument
	}
	if command != "" {
		count, countErr := q.EventSubscriptionCapacityCommandsCount(ctx, tx, sqlc.EventSubscriptionCapacityCommandsCountParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), NowAt: pgtypeFromTime(now)})
		if countErr != nil {
			return api.EventSubscriptionDeliveryControl{}, countErr
		}
		if count >= api.EventSubscriptionCapacityCommandMaxReceipts {
			return api.EventSubscriptionDeliveryControl{}, ErrSubscriptionCapacityCommandLimit
		}
	}
	receipt, receiptJSON, receiptErr := capacityCommandReceipt(req, app, sub, previous, limits.EventDeliveries.PerConsumer, now)
	if receiptErr != nil {
		return api.EventSubscriptionDeliveryControl{}, receiptErr
	}
	if err = q.EventSubscriptionCapacitySet(ctx, tx, sqlc.EventSubscriptionCapacitySetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), MaxPendingDeliveries: int32(limit), NowAt: pgtypeFromTime(now)}); err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	if receipt != nil {
		if err = q.EventSubscriptionCapacityCommandInsert(ctx, tx, sqlc.EventSubscriptionCapacityCommandInsertParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), CommandID: mustPgUUID(command), Request: payload, Receipt: receiptJSON, RecordedAt: pgtypeFromTime(receipt.RecordedAt), RetainUntil: pgtypeFromTime(receipt.RetainUntil)}); err != nil {
			return api.EventSubscriptionDeliveryControl{}, err
		}
	}
	out, err := getSubscriptionControl(ctx, q, tx, account, app, sub)
	if err != nil {
		return out, err
	}
	out.CapacityCommandReceipt = receipt
	return out, tx.Commit(ctx)
}
func (m *MemStore) SetEventSubscriptionCapacity(ctx context.Context, account, app, sub string, limit int) (api.EventSubscriptionDeliveryControl, error) {
	return m.setEventSubscriptionCapacity(ctx, account, app, sub, limit, nil, "")
}
func (m *MemStore) SetEventSubscriptionCapacityGuarded(ctx context.Context, account, app, sub string, req api.EventSubscriptionCapacityRequest) (api.EventSubscriptionDeliveryControl, error) {
	if err := req.Validate(); err != nil {
		return api.EventSubscriptionDeliveryControl{}, ErrInvalidArgument
	}
	return m.setEventSubscriptionCapacity(ctx, account, app, sub, *req.MaxPendingDeliveries, req.Expected, req.CommandID)
}
func (m *MemStore) setEventSubscriptionCapacity(ctx context.Context, account, app, sub string, limit int, expected *api.EventSubscriptionCapacityGuard, command string) (api.EventSubscriptionDeliveryControl, error) {
	if err := ctx.Err(); err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	req, payload, normalizeErr := normalizeCapacityCommandRequest(api.EventSubscriptionCapacityRequest{MaxPendingDeliveries: &limit, Expected: expected, CommandID: command})
	if normalizeErr != nil {
		return api.EventSubscriptionDeliveryControl{}, normalizeErr
	}
	expected, command = req.Expected, req.CommandID
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return api.EventSubscriptionDeliveryControl{}, ErrNotFound
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	priorReceipt, lookupErr := m.lookupCapacityCommandLocked(account, app, sub, req, payload, now)
	if lookupErr != nil {
		return api.EventSubscriptionDeliveryControl{}, lookupErr
	}
	if priorReceipt != nil {
		out := m.eventSubscriptionControlResponseLocked(account, app, sub)
		out.CapacityCommandReceipt = priorReceipt
		return out, nil
	}
	c := m.eventSubscriptionControls[canonicalMemUUID(sub)]
	ceiling := m.eventDeliveryLimitsLocked(account).PerConsumer
	configured := 0
	if c != nil {
		configured = c.MaxPendingDeliveries
	}
	if expected != nil && !eventSubscriptionCapacityGuardMatches(expected, configured, ceiling) {
		return api.EventSubscriptionDeliveryControl{}, ErrEventSubscriptionCapacityChanged
	}
	if limit < 0 || limit > ceiling {
		return api.EventSubscriptionDeliveryControl{}, ErrInvalidArgument
	}
	if command != "" && m.capacityCommandCountLocked(account, app, sub, now) >= api.EventSubscriptionCapacityCommandMaxReceipts {
		return api.EventSubscriptionDeliveryControl{}, ErrSubscriptionCapacityCommandLimit
	}
	receipt, receiptJSON, receiptErr := capacityCommandReceipt(req, app, sub, configured, ceiling, now)
	if receiptErr != nil {
		return api.EventSubscriptionDeliveryControl{}, receiptErr
	}
	if err := ctx.Err(); err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	if m.eventSubscriptionControls == nil {
		m.eventSubscriptionControls = make(map[string]*memEventSubscriptionControl)
	}
	if c == nil {
		c = &memEventSubscriptionControl{AccountID: canonicalMemUUID(account), AppID: canonicalMemUUID(app)}
		m.eventSubscriptionControls[canonicalMemUUID(sub)] = c
	}
	c.MaxPendingDeliveries = limit
	c.UpdatedAt = now
	if receipt != nil {
		for key, row := range m.eventSubscriptionCapacityCommands {
			if !row.RetainUntil.After(now) && sameMemUUID(row.AccountID, account) && sameMemUUID(row.AppID, app) && sameMemUUID(row.SubscriptionID, sub) {
				delete(m.eventSubscriptionCapacityCommands, key)
			}
		}
		if m.eventSubscriptionCapacityCommands == nil {
			m.eventSubscriptionCapacityCommands = make(map[string]memSubscriptionCapacityCommand)
		}
		m.eventSubscriptionCapacityCommands[canonicalMemUUID(sub)+"/"+command] = memSubscriptionCapacityCommand{AccountID: canonicalMemUUID(account), AppID: canonicalMemUUID(app), SubscriptionID: canonicalMemUUID(sub), Request: payload, Receipt: receiptJSON, RetainUntil: receipt.RetainUntil}
	}
	out := m.eventSubscriptionControlResponseLocked(account, app, sub)
	out.CapacityCommandReceipt = receipt
	return out, nil
}

func eventSubscriptionCapacityGuardMatches(expected *api.EventSubscriptionCapacityGuard, configured, ceiling int) bool {
	return expected.MaxPendingDeliveries != nil && expected.PlanMaxPendingDeliveries != nil && *expected.MaxPendingDeliveries == configured && *expected.PlanMaxPendingDeliveries == ceiling
}
