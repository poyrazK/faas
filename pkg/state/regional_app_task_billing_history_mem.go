package state

// adr: 800
import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimequota"
)

var _ RegionalAppTaskBillingHistoryStore = (*MemStore)(nil)

func (m *MemStore) putAppTaskLocked(key string, task AppTask) {
	m.recordRegionalAppTaskBillingTransitionLocked(m.appTasks[key], task)
	m.appTasks[key] = task
}

func (m *MemStore) deleteAppTaskLocked(key string) {
	if old, ok := m.appTasks[key]; ok {
		m.recordRegionalAppTaskBillingTransitionLocked(old, AppTask{})
	}
	delete(m.appTasks, key)
}

func (m *MemStore) recordRegionalAppTaskBillingTransitionLocked(old, next AppTask) {
	oldBillable, nextBillable := regionalAppTaskBillable(old), regionalAppTaskBillable(next)
	sameIdentity := old.ID != "" && old.ID == next.ID && old.AppID == next.AppID && old.DeploymentID == next.DeploymentID
	sameRun := sameIdentity && appTaskStringValue(old.LeaseToken) == appTaskStringValue(next.LeaseToken) && appTaskStringValue(old.LeaseOwner) == appTaskStringValue(next.LeaseOwner)
	if oldBillable == nextBillable && (!oldBillable || sameRun) {
		return
	}
	now := m.clock().UTC()
	if oldBillable {
		end := next.FinishedAt
		closed := sameIdentity && next.Status.Terminal() && end != nil && old.LeaseExpiresAt != nil && end.Before(*old.LeaseExpiresAt) &&
			appTaskStringValue(next.FailureCode) != "lease_expired"
		for id, row := range m.regionalAppTaskBillingRows {
			if row.TaskID != regionalQuotaCanonicalID(old.ID) || row.EndedAt != nil || row.UnknownSince != nil {
				continue
			}
			at := now
			if closed && !end.Before(row.StartedAt) {
				at = end.UTC()
				row.EndedAt = &at
			} else {
				if at.Before(row.StartedAt) {
					at = row.StartedAt
				}
				row.UnknownSince = &at
			}
			m.regionalAppTaskBillingRows[id] = row
		}
	}
	if nextBillable {
		start := next.UpdatedAt.UTC()
		if start.IsZero() {
			start = now
		}
		m.regionalAppTaskBillingNext++
		row := RegionalAppTaskBillingInterval{ID: m.regionalAppTaskBillingNext, AppID: regionalQuotaCanonicalID(next.AppID),
			TaskID: regionalQuotaCanonicalID(next.ID), DeploymentID: regionalQuotaCanonicalID(next.DeploymentID), RunID: regionalQuotaCanonicalID(appTaskStringValue(next.LeaseToken)),
			LeaseOwner: appTaskStringValue(next.LeaseOwner), StartedAt: start, RecordedAt: now, CompleteStart: true}
		m.regionalAppTaskBillingRows[row.ID] = row
	}
}

func appTaskStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (m *MemStore) RegionalAppTaskBillingHistoryStart(ctx context.Context) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.regionalAppTaskBillingStart, nil
}

func (m *MemStore) ListRegionalAppTaskBillingIntervals(ctx context.Context, app string, minute time.Time, after int64, limit int) ([]RegionalAppTaskBillingInterval, error) {
	if !validRegionalBillingHistoryPage(app, minute, after, limit) {
		return nil, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]RegionalAppTaskBillingInterval, 0)
	for _, row := range m.regionalAppTaskBillingRows {
		if row.AppID == app && row.ID > after && row.StartedAt.Before(minute.Add(time.Minute)) && (row.EndedAt == nil || row.EndedAt.After(minute)) {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	if len(rows) > limit {
		rows = rows[:limit]
	}
	for i := range rows {
		var err error
		rows[i], err = m.regionalAppTaskBillingSourceLocked(rows[i])
		if err != nil {
			return nil, err
		}
	}
	return rows, nil
}

func (m *MemStore) regionalAppTaskBillingSourceLocked(row RegionalAppTaskBillingInterval) (RegionalAppTaskBillingInterval, error) {
	row.EndedAt, row.UnknownSince = cloneTimePtr(row.EndedAt), cloneTimePtr(row.UnknownSince)
	for key, receipt := range m.regionalRuntimeCapacity {
		p := receipt.Permit
		if receipt.IssuedAt == nil || p.Kind != runtimequota.CapacityAppTask || p.Grant.Binding.RegionalAppID != row.AppID || p.DeploymentID != row.DeploymentID || p.RunID != row.RunID {
			continue
		}
		row.SourceMatches++
		if row.Permit != nil && row.Permit.ID < p.ID {
			continue
		}
		row.Permit, row.IssuedAt, row.Launch = &p, cloneTimePtr(receipt.IssuedAt), nil
		if launch, ok := m.regionalRuntimeLaunchSources[key]; ok {
			row.Launch = &launch
		}
	}
	if row.Permit != nil && (row.Permit.Validate() != nil || !regionalAppTaskBillingPermitMatches(row, *row.Permit) ||
		(row.Launch != nil && !regionalAppTaskBillingLaunchMatches(row, *row.Launch))) {
		return RegionalAppTaskBillingInterval{}, ErrConflict
	}
	return row, nil
}
