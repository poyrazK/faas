package state

// adr: 799
import (
	"context"
	"sort"
	"time"
)

var _ RegionalRuntimeBillingHistoryStore = (*MemStore)(nil)

// All committed instance writers use this helper. Writers which stage a row
// for a receipt check record the transition only after that check succeeds.
func (m *MemStore) putInstanceLocked(key string, ins Instance) {
	m.recordRegionalRuntimeBillingTransitionLocked(m.instances[key], ins)
	m.instances[key] = ins
}

func (m *MemStore) deleteInstanceLocked(key string) {
	if old, ok := m.instances[key]; ok {
		m.recordRegionalRuntimeBillingTransitionLocked(old, Instance{})
	}
	delete(m.instances, key)
}

func (m *MemStore) recordRegionalRuntimeBillingTransitionLocked(old, next Instance) {
	oldBillable, nextBillable := regionalBillingInstanceBillable(old), regionalBillingInstanceBillable(next)
	same := old.ID != "" && old.ID == next.ID && old.AppID == next.AppID &&
		old.DeploymentID == next.DeploymentID && old.NodeID == next.NodeID && old.WakeID == next.WakeID
	if oldBillable == nextBillable && (!oldBillable || same) {
		return
	}
	now := m.clock().UTC()
	if oldBillable {
		for id, row := range m.regionalRuntimeBillingRows {
			if row.InstanceID != regionalQuotaCanonicalID(old.ID) || row.EndedAt != nil || row.UnknownSince != nil {
				continue
			}
			closed := now
			if closed.Before(row.StartedAt) {
				closed = row.StartedAt
			}
			if same {
				row.EndedAt = &closed
			} else {
				row.UnknownSince = &closed
			}
			m.regionalRuntimeBillingRows[id] = row
		}
	}
	if nextBillable {
		start := now
		if old.ID == "" && !next.StartedAt.IsZero() {
			start = next.StartedAt.UTC()
		}
		m.regionalRuntimeBillingNext++
		r := RegionalRuntimeBillingInterval{ID: m.regionalRuntimeBillingNext,
			AppID: regionalQuotaCanonicalID(next.AppID), InstanceID: regionalQuotaCanonicalID(next.ID),
			DeploymentID: regionalQuotaCanonicalID(next.DeploymentID), NodeID: regionalQuotaCanonicalID(next.NodeID), RunID: regionalQuotaCanonicalID(next.WakeID),
			StartedAt: start, RecordedAt: now, CompleteStart: true}
		m.regionalRuntimeBillingRows[r.ID] = r
	}
}

func (m *MemStore) RegionalRuntimeBillingHistoryStart(ctx context.Context) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.regionalRuntimeBillingStart, nil
}

func (m *MemStore) ListRegionalRuntimeBillingIntervals(ctx context.Context, app string, minute time.Time, after int64, limit int) ([]RegionalRuntimeBillingInterval, error) {
	if !validRegionalBillingHistoryPage(app, minute, after, limit) {
		return nil, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]RegionalRuntimeBillingInterval, 0)
	for _, row := range m.regionalRuntimeBillingRows {
		if row.AppID == app && row.ID > after && row.StartedAt.Before(minute.Add(time.Minute)) &&
			(row.EndedAt == nil || row.EndedAt.After(minute)) {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	if len(rows) > limit {
		rows = rows[:limit]
	}
	for i := range rows {
		rows[i] = m.regionalBillingSourceLocked(rows[i])
	}
	return rows, nil
}

func (m *MemStore) regionalBillingSourceLocked(row RegionalRuntimeBillingInterval) RegionalRuntimeBillingInterval {
	row.EndedAt, row.UnknownSince = cloneTimePtr(row.EndedAt), cloneTimePtr(row.UnknownSince)
	for key, receipt := range m.regionalRuntimeCapacity {
		if receipt.IssuedAt == nil || !regionalBillingIntervalMatches(row, receipt.Permit) {
			continue
		}
		row.SourceMatches++
		if row.Permit != nil && row.Permit.ID < receipt.Permit.ID {
			continue
		}
		p := receipt.Permit
		row.Permit, row.IssuedAt, row.Launch = &p, cloneTimePtr(receipt.IssuedAt), nil
		if launch, ok := m.regionalRuntimeLaunchSources[key]; ok {
			row.Launch = &launch
		}
	}
	return row
}
