package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) promotionScheduleLocked(id string) promotionHealthSchedule {
	if m.promotionHealthSchedules == nil {
		m.promotionHealthSchedules = map[string]promotionHealthSchedule{}
	}
	h := m.promotionHealth[id]
	s, found := m.promotionHealthSchedules[id]
	if !found {
		s.nextCheckAt = m.projectEnvironmentPromotions[id].CreatedAt
	}
	r, _ := m.latestPromotionRecoveryLocked(id)
	if s.generation != h.Generation || s.recoveryID != r.Receipt.Request.RequestID || s.recoveryStatus != r.Receipt.Status {
		if m.clock().Before(s.nextCheckAt) {
			s.nextCheckAt = m.clock().UTC()
		}
		s.generation, s.recoveryID, s.recoveryStatus = h.Generation, r.Receipt.Request.RequestID, r.Receipt.Status
	}
	m.promotionHealthSchedules[id] = s
	return s
}

func (m *MemStore) promotionWorkLocked(id string) (pending, urgent bool) {
	p, h := m.projectEnvironmentPromotions[id], m.promotionHealth[id]
	if h == nil {
		return false, false
	}
	r, has := m.latestPromotionRecoveryLocked(id)
	recovery := has && !promotionRecoveryTerminal(r.Receipt.Status)
	return p.Status == "running" && promotionHealthPending(h.Phase) || p.Status == "failed" && recovery, h.Phase == "restoring" || h.Phase == "rolling_back" || recovery
}

func (m *MemStore) promotionDueLocked(prefer bool) []PromotionHealthJob {
	var jobs []PromotionHealthJob
	now := m.clock()
	for id := range m.promotionHealth {
		pending, _ := m.promotionWorkLocked(id)
		if !pending || m.promotionScheduleLocked(id).nextCheckAt.After(m.clock()) || m.promotionHealthLeases[id].until.After(now) {
			continue
		}
		p := m.projectEnvironmentPromotions[id]
		jobs = append(jobs, PromotionHealthJob{id, p.AccountID, p.ProjectSlug, p.ToEnvironment})
	}
	sort.Slice(jobs, func(i, j int) bool {
		a, b := jobs[i].PromotionID, jobs[j].PromotionID
		if prefer {
			_, urgentA := m.promotionWorkLocked(a)
			_, urgentB := m.promotionWorkLocked(b)
			if urgentA != urgentB {
				return urgentA
			}
		}
		sa, sb := m.promotionHealthSchedules[a], m.promotionHealthSchedules[b]
		if !sa.nextCheckAt.Equal(sb.nextCheckAt) {
			return sa.nextCheckAt.Before(sb.nextCheckAt)
		}
		if !sa.lastClaimedAt.Equal(sb.lastClaimedAt) {
			return sa.lastClaimedAt.Before(sb.lastClaimedAt)
		}
		return a < b
	})
	return jobs
}

func (m *MemStore) promotionSlotsLocked(account string) bool {
	total, own := 0, 0
	for id, lease := range m.promotionHealthLeases {
		if lease.until.After(m.clock()) {
			total++
			if m.projectEnvironmentPromotions[id].AccountID == account {
				own++
			}
		}
	}
	return total < api.ProjectPromotionHealthConcurrency && own < api.ProjectPromotionHealthAccountConcurrency
}

func (m *MemStore) claimPromotionHealthLocked(id, token string) {
	if m.promotionHealthLeases == nil {
		m.promotionHealthLeases = map[string]promotionHealthLease{}
	}
	now := m.clock().UTC()
	m.promotionHealthLeases[id] = promotionHealthLease{token, now.Add(api.ProjectPromotionHealthLeaseDuration)}
	s := m.promotionScheduleLocked(id)
	s.nextCheckAt, s.lastClaimedAt = now.Add(api.ProjectPromotionHealthLeaseDuration), now
	m.promotionHealthSchedules[id] = s
}

func (m *MemStore) ClaimNextProjectEnvironmentPromotionHealth(ctx context.Context, token string, preferRecovery bool) (*PromotionHealthJob, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, job := range m.promotionDueLocked(preferRecovery) {
		if !m.promotionSlotsLocked(job.AccountID) {
			continue
		}
		m.claimPromotionHealthLocked(job.PromotionID, token)
		return &job, nil
	}
	return nil, nil
}
func (m *MemStore) ProjectEnvironmentPromotionHealthLeaseCurrent(_ context.Context, account, id, token string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lease := m.promotionHealthLeases[id]
	return m.projectEnvironmentPromotions[id].AccountID == account && lease.token == token && lease.until.After(m.clock()), nil
}
func (m *MemStore) promotionObservationLeaseLocked(id string, o PromotionHealthObservation, now time.Time) bool {
	lease := m.promotionHealthLeases[id]
	return o.LeaseToken == "" || lease.token == o.LeaseToken && lease.until.After(now)
}
