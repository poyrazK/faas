package state

// adr: 801
import (
	"bytes"
	"context"
	"time"
)

type regionalRuntimeFloorMemoryRow struct {
	ID                    int64
	AppID, ObjectID, Kind string
	StartedAt             time.Time
	EndedAt               *time.Time
	Claims                []byte
}

func (m *MemStore) putAppLocked(id string, app App) {
	old, exists := m.apps[id]
	m.recordRegionalFloorLocked(RegionalFloorApp, old.ID, old.ID, exists, app.ID, app.ID, regionalFloorAppClaims(app), true)
	m.apps[id] = app
}

func (m *MemStore) deleteAppLocked(id string) {
	if old, exists := m.apps[id]; exists {
		m.recordRegionalFloorLocked(RegionalFloorApp, old.ID, old.ID, true, "", "", nil, false)
	}
	delete(m.apps, id)
}

func (m *MemStore) recordRegionalDeploymentFloorLocked(old Deployment, exists bool, next Deployment, present bool) {
	m.recordRegionalFloorLocked(RegionalFloorDeployment, old.AppID, old.ID, exists, next.AppID, next.ID, regionalFloorDeploymentClaims(next), present)
}

func (m *MemStore) putDeploymentFloorLocked(id string, dep Deployment) {
	old, exists := m.deployments[id]
	m.recordRegionalDeploymentFloorLocked(old, exists, dep, true)
	m.deployments[id] = dep
}

func (m *MemStore) deleteDeploymentLocked(id string) {
	if old, exists := m.deployments[id]; exists {
		m.recordRegionalDeploymentFloorLocked(old, true, Deployment{}, false)
	}
	delete(m.deployments, id)
}

func (m *MemStore) recordRegionalFloorLocked(kind, oldApp, oldObject string, exists bool, nextApp, nextObject string, nextClaims []byte, present bool) {
	key := kind + "\x00" + regionalQuotaCanonicalID(oldObject)
	index, known := m.regionalRuntimeFloorHeads[key]
	if known && present && oldApp == nextApp && oldObject == nextObject && bytes.Equal(m.regionalRuntimeFloorRows[index].Claims, nextClaims) {
		return
	}
	now := m.clock().UTC()
	if exists && known {
		row := m.regionalRuntimeFloorRows[index]
		if now.Before(row.StartedAt) {
			now = row.StartedAt
		}
		at := now
		row.EndedAt = &at
		m.regionalRuntimeFloorRows[index] = row
		delete(m.regionalRuntimeFloorHeads, key)
	}
	app, object := regionalQuotaCanonicalID(nextApp), regionalQuotaCanonicalID(nextObject)
	if !present || app == "" || object == "" {
		return
	}
	m.regionalRuntimeFloorNext++
	if m.regionalRuntimeFloorHeads == nil {
		m.regionalRuntimeFloorHeads = map[string]int{}
	}
	m.regionalRuntimeFloorHeads[kind+"\x00"+object] = len(m.regionalRuntimeFloorRows)
	m.regionalRuntimeFloorRows = append(m.regionalRuntimeFloorRows, regionalRuntimeFloorMemoryRow{ID: m.regionalRuntimeFloorNext, AppID: app, ObjectID: object, Kind: kind, StartedAt: now, Claims: append([]byte(nil), nextClaims...)})
}

// Rollback of a compound memory mutation restores closed prior intervals as
// well as new rows. Claims and time values are immutable; IDs are not reused.
func (m *MemStore) regionalFloorRollbackLocked() func() {
	rows := append([]regionalRuntimeFloorMemoryRow(nil), m.regionalRuntimeFloorRows...)
	heads := make(map[string]int, len(m.regionalRuntimeFloorHeads))
	for k, v := range m.regionalRuntimeFloorHeads {
		heads[k] = v
	}
	return func() { m.regionalRuntimeFloorRows = rows; m.regionalRuntimeFloorHeads = heads }
}

func (m *MemStore) RegionalRuntimeFloorHistoryStart(ctx context.Context) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.regionalRuntimeFloorStart, nil
}

func (m *MemStore) ListRegionalRuntimeFloorIntervals(ctx context.Context, app string, minute time.Time, after int64, limit int) ([]RegionalRuntimeFloorInterval, error) {
	if !validRegionalBillingHistoryPage(app, minute, after, limit) {
		return nil, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RegionalRuntimeFloorInterval, 0)
	for _, row := range m.regionalRuntimeFloorRows {
		if row.AppID != app || row.ID <= after || !row.StartedAt.Before(minute.Add(time.Minute)) || (row.EndedAt != nil && !row.EndedAt.After(minute)) {
			continue
		}
		r, err := regionalRuntimeFloorDecode(row.ID, row.AppID, row.ObjectID, row.Kind, row.StartedAt, row.EndedAt, row.Claims)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

var _ RegionalRuntimeFloorHistoryStore = (*MemStore)(nil)
