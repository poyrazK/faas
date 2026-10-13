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
	defer tx.Rollback(ctx)
	q := sqlc.New()
	acct, err := q.AccountByID(ctx, tx, mustPgUUID(account))
	if err != nil {
		return api.EventSubscriptionDeliveryControl{}, mapErr(err)
	}
	limits, valid := api.LimitsFor(api.Plan(acct.Plan))
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
	if limit > limits.EventDeliveries.PerConsumer {
		return api.EventSubscriptionDeliveryControl{}, ErrInvalidArgument
	}
	if err = q.EventSubscriptionControlLock(ctx, tx, canonicalMemUUID(sub)); err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	if err = q.EventSubscriptionCapacitySet(ctx, tx, sqlc.EventSubscriptionCapacitySetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), MaxPendingDeliveries: int32(limit), NowAt: pgtypeFromTime(time.Now().UTC())}); err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	out, err := getSubscriptionControl(ctx, q, tx, account, app, sub)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (m *MemStore) SetEventSubscriptionCapacity(ctx context.Context, account, app, sub string, limit int) (api.EventSubscriptionDeliveryControl, error) {
	if err := ctx.Err(); err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return api.EventSubscriptionDeliveryControl{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return api.EventSubscriptionDeliveryControl{}, ErrNotFound
	}
	c := m.eventSubscriptionControls[canonicalMemUUID(sub)]
	ceiling := m.eventDeliveryLimitsLocked(account).PerConsumer
	if limit < 0 || limit > ceiling {
		return api.EventSubscriptionDeliveryControl{}, ErrInvalidArgument
	}
	if m.eventSubscriptionControls == nil {
		m.eventSubscriptionControls = make(map[string]*memEventSubscriptionControl)
	}
	if c == nil {
		c = &memEventSubscriptionControl{AccountID: canonicalMemUUID(account), AppID: canonicalMemUUID(app)}
		m.eventSubscriptionControls[canonicalMemUUID(sub)] = c
	}
	c.MaxPendingDeliveries = limit
	c.UpdatedAt = time.Now().UTC()
	return m.eventSubscriptionControlResponseLocked(account, app, sub), nil
}
