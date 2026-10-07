package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) lifecycleMultipartUploadsLocked(j ObjectLifecycleScan) []ObjectMultipartUpload {
	rows := []ObjectMultipartUpload{}
	for _, u := range m.objectMultipartUploads {
		if lifecycleMultipartCandidate(j, u) {
			u.Parts, u.Metadata = cloneMultipartParts(u.Parts), cloneObjectMultipartMetadata(u.Metadata)
			rows = append(rows, cloneObjectMultipartUpload(u))
		}
	}
	sort.Slice(rows, func(i, k int) bool { return rows[i].ID < rows[k].ID })
	if len(rows) > int(api.ObjectLifecycleMultipartPageSize) {
		rows = rows[:api.ObjectLifecycleMultipartPageSize]
	}
	return rows
}

func (m *MemStore) ListObjectLifecycleMultipartUploads(_ context.Context, id, token string) ([]ObjectMultipartUpload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.objectLifecycleScans[id]
	if !ok {
		return nil, ErrNotFound
	}
	p, err := m.ownedLifecyclePolicyLocked(j.AccountID, j.AppID, j.BucketID)
	if err != nil || p.Revision != j.Revision || j.Phase != "multipart" || !validLifecycleScanLease(j, token, m.clock()) {
		return nil, ErrConflict
	}
	return m.lifecycleMultipartUploadsLocked(j), nil
}

// Admission and its checkpoint share the same mutex. The provider is called
// later by multipart recovery; reservation bytes are unchanged here.
func (m *MemStore) CheckpointObjectLifecycleMultipartUpload(_ context.Context, id, token string, expected ObjectMultipartUpload) (ObjectLifecycleScan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var next ObjectMultipartUpload
	j, err := m.mutateLifecycleScanLocked(id, func(j ObjectLifecycleScan) (ObjectLifecycleScan, error) {
		now := m.clock()
		if j.Phase != "multipart" || !validLifecycleScanLease(j, token, now) {
			return j, ErrConflict
		}
		rows := m.lifecycleMultipartUploadsLocked(j)
		if expected.ID == "" {
			if len(rows) != 0 {
				return j, ErrConflict
			}
			j.State, j.FinishedAt, j.UpdatedAt, j.RetryAt = "completed", &now, now, now
			j.Token, j.LeaseUntil = "", time.Time{}
			return j, nil
		}
		if len(rows) != 0 && rows[0].ID < expected.ID {
			return j, ErrConflict
		}
		old, ok := m.objectMultipartUploads[expected.ID]
		if !ok {
			return j, ErrNotFound
		}
		var e error
		j, next, e = checkpointLifecycleMultipart(j, token, old, expected, now)
		return j, e
	})
	if err == nil {
		if next.ID != "" {
			m.objectMultipartUploads[next.ID] = next
		}
		if j.State == "completed" {
			p := m.objectLifecyclePolicies[j.BucketID]
			p.NextScanAt = m.clock().Add(api.ObjectLifecycleSweepInterval)
			m.objectLifecyclePolicies[j.BucketID] = p
		}
	}
	return j, err
}
