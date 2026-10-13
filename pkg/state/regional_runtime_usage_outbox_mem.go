package state

// adr: 802
import (
	"context"
	"sort"
	"time"
)

var _ RegionalRuntimeUsageOutboxStore = (*MemStore)(nil)

func (m *MemStore) queueRegionalRuntimeUsage(ctx context.Context, q regionalRuntimeUsageQueued) (RegionalRuntimeUsageOutboxItem, error) {
	if err := ctx.Err(); err != nil {
		return RegionalRuntimeUsageOutboxItem{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	u := q.usage
	r, ok := m.regionalRuntimeQuotas[u.Grant.Binding.RegionalAppID+"/"+u.Grant.AllocationID]
	if !ok {
		return RegionalRuntimeUsageOutboxItem{}, ErrNotFound
	}
	if err := checkRegionalUsageOrigin(u, r); err != nil {
		return RegionalRuntimeUsageOutboxItem{}, err
	}
	key := regionalUsageOutboxKey(u)
	if old, ok := m.regionalRuntimeUsageOutbox[key]; ok {
		if old.Usage != u {
			return RegionalRuntimeUsageOutboxItem{}, ErrConflict
		}
		return cloneRegionalUsageOutbox(old), nil
	}
	m.regionalRuntimeUsageNext++
	out := RegionalRuntimeUsageOutboxItem{ID: m.regionalRuntimeUsageNext, Usage: u, Envelope: q.envelope, CreatedAt: regionalCapacityNow()}
	if m.regionalRuntimeUsageOutbox == nil {
		m.regionalRuntimeUsageOutbox = map[string]RegionalRuntimeUsageOutboxItem{}
	}
	m.regionalRuntimeUsageOutbox[key] = out
	return cloneRegionalUsageOutbox(out), nil
}

func (m *MemStore) ackRegionalRuntimeUsage(ctx context.Context, q regionalRuntimeUsageImported) (RegionalRuntimeUsageOutboxItem, error) {
	if err := ctx.Err(); err != nil {
		return RegionalRuntimeUsageOutboxItem{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := regionalUsageOutboxKey(q.ack.Usage)
	old, ok := m.regionalRuntimeUsageOutbox[key]
	if !ok {
		return RegionalRuntimeUsageOutboxItem{}, ErrNotFound
	}
	if old.Usage != q.ack.Usage || (old.Ack != nil && *old.Ack != q.ack) {
		return RegionalRuntimeUsageOutboxItem{}, ErrConflict
	}
	if old.Ack == nil {
		old.Ack = &q.ack
		old.AckEnvelope = &q.envelope
		m.regionalRuntimeUsageOutbox[key] = old
	}
	return cloneRegionalUsageOutbox(old), nil
}

func (m *MemStore) RegionalRuntimeUsageOutbox(ctx context.Context, app, allocation string, minute time.Time) (RegionalRuntimeUsageOutboxItem, error) {
	if !validRegionalBillingHistoryPage(app, minute, 0, 1) || !validRegionalReleaseIDs(allocation, allocation, allocation) {
		return RegionalRuntimeUsageOutboxItem{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return RegionalRuntimeUsageOutboxItem{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.regionalRuntimeUsageOutbox {
		if r.Usage.Grant.Binding.RegionalAppID == app && r.Usage.Grant.AllocationID == allocation && r.Usage.MinuteMicros == minute.UnixMicro() {
			return cloneRegionalUsageOutbox(r), nil
		}
	}
	return RegionalRuntimeUsageOutboxItem{}, ErrNotFound
}

func (m *MemStore) ListPendingRegionalRuntimeUsage(ctx context.Context, cell string, after int64, limit int) ([]RegionalRuntimeUsageOutboxItem, error) {
	if !validRegionalUsagePage(cell, after, limit) {
		return nil, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RegionalRuntimeUsageOutboxItem, 0)
	for _, r := range m.regionalRuntimeUsageOutbox {
		if r.ID > after && r.Ack == nil && (cell == "" || r.Usage.Grant.Binding.CellID == cell) {
			out = append(out, cloneRegionalUsageOutbox(r))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
