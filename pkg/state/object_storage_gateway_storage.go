package state

import (
	"context"
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ObjectStorageGatewayStorageStore meters stored bytes for gateway-safety
// accounting (ADR-20261024132844). Each call opens this UTC month's row for
// every live bucket and accrues every open row of this or last month up to
// now, the end of its month or the bucket's deletion, at the bucket's last
// inventoried bytes. It is safe to call from several apid processes at once.
type ObjectStorageGatewayStorageStore interface {
	AccrueObjectStorageGatewayStorage(context.Context) error
}

var _ ObjectStorageGatewayStorageStore = (*PgStore)(nil)
var _ ObjectStorageGatewayStorageStore = (*MemStore)(nil)

func (s *pgCore) AccrueObjectStorageGatewayStorage(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	if _, err = q.ObjectStorageGatewayStorageOpen(ctx, tx); err != nil {
		return mapErr(err)
	}
	if _, err = q.ObjectStorageGatewayStorageAdvance(ctx, tx); err != nil {
		return mapErr(err)
	}
	return tx.Commit(ctx)
}

type memGatewayStorage struct {
	from, through time.Time
	byteHours     float64
}

func (m *MemStore) AccrueObjectStorageGatewayStorage(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return err
	}
	if m.objectGatewayStorage == nil {
		m.objectGatewayStorage = map[string]*memGatewayStorage{}
	}
	now := m.clock().UTC()
	period := ObjectStoragePeriod(now)
	previous := period.AddDate(0, -1, 0)
	for id, b := range m.objectBuckets {
		key := objectProviderRequestKey(id, period)
		if b.State == "deleted" || m.objectGatewayStorage[key] != nil {
			continue
		}
		start := now
		if m.objectGatewayStorage[objectProviderRequestKey(id, previous)] != nil {
			start = period
		}
		m.objectGatewayStorage[key] = &memGatewayStorage{from: start, through: start}
	}
	for id, b := range m.objectBuckets {
		for _, p := range []time.Time{previous, period} {
			g := m.objectGatewayStorage[objectProviderRequestKey(id, p)]
			if g == nil {
				continue
			}
			until := minTime(now, p.AddDate(0, 1, 0))
			if b.State == "deleted" {
				until = minTime(until, maxTime(b.UpdatedAt, g.through))
			}
			if !until.After(g.through) {
				continue
			}
			bytes := float64(max(m.objectUsage[id].ObservedBytes, 0))
			g.byteHours = math.Min(g.byteHours+bytes*until.Sub(g.through).Hours(), float64(api.MaxObjectStoragePolicyValue))
			g.through = until
		}
	}
	return nil
}
