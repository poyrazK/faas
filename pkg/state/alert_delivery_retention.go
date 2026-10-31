package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// AlertDeliveryPurger deletes settled alert deliveries past their retention
// (ADR-20261029203000). Optional on Store; callers type-assert.
type AlertDeliveryPurger interface {
	// PurgeAlertDeliveriesBefore deletes up to maxRows settled deliveries
	// fired before cutoff, oldest first, keeping each rule's newest delivery
	// and every delivery with a rollback action. It returns how many went.
	PurgeAlertDeliveriesBefore(ctx context.Context, cutoff time.Time, maxRows int) (int64, error)
}

// PurgeAlertDeliveriesBefore runs one batch of PurgeAlertDeliveriesBefore.
func (s *PgStore) PurgeAlertDeliveriesBefore(ctx context.Context, cutoff time.Time, maxRows int) (int64, error) {
	n, err := sqlc.New().PurgeAlertDeliveriesBefore(ctx, s.pool, sqlc.PurgeAlertDeliveriesBeforeParams{
		Before: pgtype.Timestamptz{Time: cutoff, Valid: true}, MaxRows: int32(maxRows),
	})
	if err != nil {
		return 0, fmt.Errorf("state: purge alert deliveries: %w", err)
	}
	return n, nil
}

// PurgeAlertDeliveriesBefore mirrors PgStore.
func (m *MemStore) PurgeAlertDeliveriesBefore(_ context.Context, cutoff time.Time, maxRows int) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	newest := map[string]time.Time{}
	for _, d := range m.alertDeliveries {
		if d.FiredAt.After(newest[d.RuleID]) {
			newest[d.RuleID] = d.FiredAt
		}
	}
	type dueDelivery struct {
		key     string // the map is keyed by idempotency key
		firedAt time.Time
	}
	var due []dueDelivery
	for key, d := range m.alertDeliveries {
		_, rollback := m.alertRollbacks[alertRollbackFireID(d.ID)]
		if d.FiredAt.Before(cutoff) && d.Status != AlertDeliveryPending && d.FiredAt.Before(newest[d.RuleID]) && !rollback {
			due = append(due, dueDelivery{key: key, firedAt: d.FiredAt})
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].firedAt.Before(due[j].firedAt) })
	due = due[:min(len(due), maxRows)]
	for _, d := range due {
		delete(m.alertDeliveries, d.key)
	}
	return int64(len(due)), nil
}

var (
	_ AlertDeliveryPurger = (*PgStore)(nil)
	_ AlertDeliveryPurger = (*MemStore)(nil)
)
