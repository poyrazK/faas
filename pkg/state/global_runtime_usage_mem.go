package state

// adr: 798
import (
	"context"
	"time"
)

// Binding is startup wiring, before the store accepts usage. A portable store
// without a directory has no global obligations and cannot import signed usage.
func (m *MemStore) BindGlobalRuntimeUsageAuthority(authority GlobalRuntimeUsageAuthority) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalRuntimeUsageAuthority = authority
}

func (m *MemStore) applyGlobalRuntimeUsage(ctx context.Context, change globalRuntimeUsageMutation) (GlobalRuntimeUsageReceipt, error) {
	if err := ctx.Err(); err != nil {
		return GlobalRuntimeUsageReceipt{}, err
	}
	m.mu.Lock()
	authority := m.globalRuntimeUsageAuthority
	m.mu.Unlock()
	if authority == nil {
		return GlobalRuntimeUsageReceipt{}, ErrConflict
	}
	b, err := authority.GlobalRuntimeUsageBinding(ctx, change.usage.Grant)
	if err != nil {
		return GlobalRuntimeUsageReceipt{}, err
	}
	if err = checkGlobalRuntimeUsageBinding(change.usage, b); err != nil {
		return GlobalRuntimeUsageReceipt{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.accounts[b.AccountID]; !exists {
		return GlobalRuntimeUsageReceipt{}, ErrNotFound
	}
	minute := time.UnixMicro(change.usage.MinuteMicros).UTC()
	key := globalRuntimeUsageKey(b.Grant, minute)
	if old, exists := m.globalRuntimeUsageReceipts[key]; exists {
		if old.Usage != change.usage {
			return GlobalRuntimeUsageReceipt{}, ErrConflict
		}
		return old, nil // Retain the first signature, even after key rotation.
	}
	r := GlobalRuntimeUsageReceipt{Usage: change.usage, Envelope: change.envelope, AccountID: b.AccountID, AppSlug: b.AppSlug, BillingInstanceID: globalRuntimeBillingID(b.Grant), ImportedAt: time.Now().UTC().Truncate(time.Microsecond)}
	for _, row := range m.usage {
		if row.InstanceID == r.BillingInstanceID && row.Minute.Equal(minute) {
			return GlobalRuntimeUsageReceipt{}, ErrConflict
		}
	}
	if err := ctx.Err(); err != nil {
		return GlobalRuntimeUsageReceipt{}, err
	}
	if m.globalRuntimeUsageReceipts == nil {
		m.globalRuntimeUsageReceipts = make(map[string]GlobalRuntimeUsageReceipt)
	}
	m.globalRuntimeUsageReceipts[key] = r
	row := usageMinute{AccountID: b.AccountID, AppID: b.Grant.GlobalAppID, MeterKind: "app", InstanceID: r.BillingInstanceID, Minute: minute, MBSeconds: r.Usage.MBSeconds}
	m.usage = append(m.usage, row)
	m.recomputeMonthLocked(row.AccountID, row.AppID, minute)
	m.retainFinancialUsageLocked(usageMinute{}, row)
	return r, nil
}

func (m *MemStore) GlobalRuntimeUsage(ctx context.Context, app, allocation, cell string, minute time.Time) (GlobalRuntimeUsageReceipt, error) {
	if !validRegionalReleaseIDs(app, allocation, cell) || !minute.Equal(minute.Truncate(time.Minute)) || minute.UnixMicro() < 1 {
		return GlobalRuntimeUsageReceipt{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return GlobalRuntimeUsageReceipt{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.globalRuntimeUsageReceipts {
		g := r.Usage.Grant
		if g.GlobalAppID == app && g.AllocationID == allocation && g.Binding.CellID == cell && r.Usage.MinuteMicros == minute.UnixMicro() {
			return r, nil
		}
	}
	return GlobalRuntimeUsageReceipt{}, ErrNotFound
}

func (m *MemStore) GlobalRuntimeBillingReady(ctx context.Context, account string, start, end time.Time) (bool, error) {
	if !validFinancialWindow(account, start, end) || !start.Equal(start.Truncate(time.Minute)) || !end.Equal(end.Truncate(time.Minute)) {
		return false, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	authority := m.globalRuntimeUsageAuthority
	m.mu.Unlock()
	if authority == nil {
		return true, nil
	}
	obligations, err := authority.GlobalRuntimeUsageObligations(ctx, account, start, end)
	if err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range obligations {
		from, through := globalRuntimeUsageWindow(b, start.UTC(), end.UTC())
		for minute := from; minute.Before(through); minute = minute.Add(time.Minute) {
			if _, found := m.globalRuntimeUsageReceipts[globalRuntimeUsageKey(b.Grant, minute)]; !found {
				return false, nil
			}
		}
	}
	return true, ctx.Err()
}
