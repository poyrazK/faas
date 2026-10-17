package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectrecovery"
)

var _ ObjectNodeSettlementStore = (*MemStore)(nil)

func (m *MemStore) nodeSettlementEnrollmentLocked(id, kind string, b ObjectBucket, enabled bool) (ObjectNodeSettlement, error) {
	if !enabled {
		return ObjectNodeSettlement{}, nil
	}
	if _, exists := m.objectNodeSettlements[id]; exists {
		return ObjectNodeSettlement{}, ErrConflict
	}
	return newObjectNodeSettlement(id, kind, b, m.clock().UTC())
}

func (m *MemStore) prepareUploadNodeSettlementLocked(c ObjectUploadCompletion, dispatched bool) (func(), error) {
	return m.prepareNodeSettlementLocked(c.ID, func(bucket string) (objectrecovery.Settlement, bool, error) {
		if !dispatched {
			return objectrecovery.Settlement{}, false, nil
		}
		return UploadNodeSettlement(bucket, c)
	})
}

func (m *MemStore) prepareMultipartNodeSettlementLocked(u ObjectMultipartUpload) (func(), error) {
	return m.prepareNodeSettlementLocked(u.ID, func(bucket string) (objectrecovery.Settlement, bool, error) {
		return MultipartNodeSettlement(bucket, u)
	})
}

func (m *MemStore) enrollObjectNodeSettlementLocked(job ObjectNodeSettlement) {
	if job.ID == "" {
		return
	}
	if m.objectNodeSettlements == nil {
		m.objectNodeSettlements = map[string]ObjectNodeSettlement{}
	}
	m.objectNodeSettlements[job.ID] = job
}

// Prepare before fallible event/version publication and commit with the owner
// under the same mutex. A failed settlement leaves a waiting enrollment intact.
func (m *MemStore) prepareNodeSettlementLocked(id string, build func(string) (objectrecovery.Settlement, bool, error)) (func(), error) {
	job, enrolled := m.objectNodeSettlements[id]
	if !enrolled {
		return func() {}, nil
	}
	a, ready, err := build(job.PhysicalName)
	if err != nil {
		return nil, err
	}
	if !ready {
		return func() { delete(m.objectNodeSettlements, id) }, nil
	}
	job, err = readyObjectNodeSettlement(job, a, m.clock().UTC())
	if err != nil {
		return nil, err
	}
	return func() { m.objectNodeSettlements[id] = job }, nil
}

func (m *MemStore) ClaimObjectNodeSettlements(ctx context.Context, token string, limit int32) ([]ObjectNodeSettlement, error) {
	if !validNodeSettlementClaim(token, limit) {
		return nil, ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock().UTC()
	jobs := []ObjectNodeSettlement{}
	for _, job := range m.objectNodeSettlements {
		if !job.ReadyAt.IsZero() && !job.RetryAt.After(now) && !job.LeaseUntil.After(now) {
			jobs = append(jobs, job)
		}
	}
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].RetryAt.Before(jobs[j].RetryAt) || jobs[i].RetryAt.Equal(jobs[j].RetryAt) && jobs[i].ID < jobs[j].ID
	})
	jobs = jobs[:min(int(limit), len(jobs))]
	for i := range jobs {
		jobs[i].LeaseToken, jobs[i].LeaseUntil = token, now.Add(api.ObjectNodeSettlementLease)
		if jobs[i].Attempts < api.ObjectNodeSettlementMaxAttempts {
			jobs[i].Attempts++
		}
		m.objectNodeSettlements[jobs[i].ID] = jobs[i]
	}
	return jobs, nil
}

func (m *MemStore) FinishObjectNodeSettlement(ctx context.Context, id, token string) error {
	if !validNodeSettlementClaim(token, 1) || !validNodeSettlementEnrollment(id, true) {
		return ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.objectNodeSettlements[id]
	if !ok || token == "" || job.LeaseToken != token || !job.LeaseUntil.After(m.clock()) {
		return ErrConflict
	}
	delete(m.objectNodeSettlements, id)
	return nil
}

func (m *MemStore) RetryObjectNodeSettlement(ctx context.Context, id, token, code string, delay time.Duration) error {
	if !validNodeSettlementClaim(token, 1) || !validNodeSettlementEnrollment(id, true) || !validNodeSettlementRetry(code, delay) {
		return ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.objectNodeSettlements[id]
	now := m.clock().UTC()
	if !ok || token == "" || job.LeaseToken != token || !job.LeaseUntil.After(now) {
		return ErrConflict
	}
	job.LeaseToken, job.LeaseUntil = "", time.Time{}
	job.LastError, job.RetryAt = code, now.Add(delay)
	m.objectNodeSettlements[id] = job
	return nil
}

func (m *MemStore) ObjectNodeSettlementBacklog(ctx context.Context) (ObjectNodeSettlementBacklog, error) {
	if err := ctx.Err(); err != nil {
		return ObjectNodeSettlementBacklog{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var stats ObjectNodeSettlementBacklog
	for _, job := range m.objectNodeSettlements {
		if job.ReadyAt.IsZero() {
			stats.Waiting++
		} else {
			stats.Ready++
			if stats.OldestReadyAt.IsZero() || job.ReadyAt.Before(stats.OldestReadyAt) {
				stats.OldestReadyAt = job.ReadyAt
			}
		}
	}
	return stats, nil
}
