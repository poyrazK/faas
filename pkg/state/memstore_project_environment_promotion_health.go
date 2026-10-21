package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type promotionHealthLease struct {
	token string
	until time.Time
}

func (m *MemStore) ClaimProjectEnvironmentPromotionHealth(_ context.Context, accountID, promotionID, token string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projectEnvironmentPromotions[promotionID]
	if !ok || p.AccountID != accountID {
		return false, ErrNotFound
	}
	pending, _ := m.promotionWorkLocked(promotionID)
	if !pending || m.promotionHealthLeases[promotionID].until.After(m.clock()) || !m.promotionSlotsLocked(accountID) {
		return false, nil
	}
	m.claimPromotionHealthLocked(promotionID, token)
	return true, nil
}

func (m *MemStore) ReleaseProjectEnvironmentPromotionHealth(_ context.Context, promotionID, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	lease := m.promotionHealthLeases[promotionID]
	if lease.token == token && lease.until.After(m.clock()) {
		if h := m.promotionHealth[promotionID]; h != nil {
			schedule := m.promotionScheduleLocked(promotionID)
			var recovery *api.ProjectPromotionMigrationRecovery
			if r, ok := m.latestPromotionRecoveryLocked(promotionID); ok {
				recovery = copyPromotionRecovery(r)
			}
			schedule.nextCheckAt = nextPromotionHealthCheck(h, recovery, m.clock().UTC())
			m.promotionHealthSchedules[promotionID] = schedule
		}
		delete(m.promotionHealthLeases, promotionID)
	}
	return nil
}

func (m *MemStore) ProjectEnvironmentPromotionHealth(_ context.Context, accountID, promotionID string) (*api.ProjectPromotionHealth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projectEnvironmentPromotions[promotionID]
	if !ok || p.AccountID != accountID {
		return nil, ErrNotFound
	}
	h := clonePromotionHealth(m.promotionHealth[promotionID])
	if h != nil {
		if r, ok := m.latestPromotionRecoveryLocked(promotionID); ok {
			h.Recovery = copyPromotionRecovery(r)
		}
	}
	return h, nil
}

func (m *MemStore) PendingProjectEnvironmentPromotionHealth(_ context.Context) ([]PromotionHealthJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	jobs := m.promotionDueLocked(false)
	if len(jobs) > api.ProjectPromotionHealthBatchSize {
		jobs = jobs[:api.ProjectPromotionHealthBatchSize]
	}
	return jobs, nil
}

func (m *MemStore) AdvanceProjectEnvironmentPromotionHealth(_ context.Context, accountID, promotionID string, o PromotionHealthObservation) (*api.ProjectPromotionHealth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projectEnvironmentPromotions[promotionID]
	if !ok || p.AccountID != accountID {
		return nil, ErrNotFound
	}
	if !m.promotionObservationLeaseLocked(promotionID, o, m.clock()) {
		return nil, ErrConflict
	}
	h := m.promotionHealth[promotionID]
	if h == nil {
		return nil, ErrNotFound
	}
	active := m.activeProjectReleaseSets[releaseKey(p.ProjectID, p.ToEnvironment)]
	expected := p.PreviousTargetReleaseSetID
	if p.TargetReleaseSetID != "" {
		expected = p.TargetReleaseSetID
	}
	if p.RestoredTargetReleaseSetID != "" {
		expected = p.RestoredTargetReleaseSetID
	}
	if active != expected {
		o.Action = "conflict"
	}
	if p.Status != "running" && promotionHealthPending(h.Phase) {
		o.Action = "fail"
	}
	if h.Phase != "queued" && len(api.ProjectPromotionWorkers(&h.Policy)) > 0 && h.Generation == o.Generation && (o.Action == "" || o.Action == "complete") {
		h = clonePromotionHealth(h)
		if err := m.observePromotionWorkerLocked(p, h, m.clock()); err != nil {
			if !errors.Is(err, ErrConflict) {
				return nil, err
			}
			o.Action = "conflict"
		}
		if o.Action == "complete" && !promotionWorkersReady(h) {
			o.Action = ""
		}
	}
	if o.Action == "complete" {
		if m.guardPromotionMigrationPublishLocked(p) != nil {
			o.Action = "conflict"
		}
		release := m.projectReleaseSets[active]
		if err := m.validateProjectEnvironmentPromotionConfigLocked(p, false); err != nil {
			o.Action = "conflict"
		}
		if err := m.guardPromotionReleaseConfigurationLocked(p, release.Members, false, true); err != nil {
			o.Action = "conflict"
		}
		if _, err := m.promotionWorkloadActivationsLocked(p, release.Members, false, true); err != nil {
			o.Action = "conflict"
		}
		if _, err := m.preparePromotionFeatureFlagsLocked(p, false, true); err != nil {
			o.Action = "conflict"
		}
	}
	next, err := decidePromotionHealth(h, p, o, m.clock().UTC())
	if err != nil {
		return nil, err
	}
	if next.Generation == h.Generation {
		return clonePromotionHealth(h), nil
	}
	m.savePromotionControlLocked(promotionID, next.Control)
	m.promotionHealth[promotionID] = next
	p.Status, p.VerificationStatus, p.Error = promotionHealthResult(next)
	p.VerificationError = p.Error
	p.UpdatedAt = m.clock().UTC()
	if p.VerificationStartedAt == nil && (next.Admission == nil || next.Admission.StartedAt != nil) {
		p.VerificationStartedAt = &p.UpdatedAt
	}
	if p.Status != "running" {
		p.CompletedAt = &p.UpdatedAt
		p.VerificationCompletedAt = &p.UpdatedAt
	}
	m.projectEnvironmentPromotions[promotionID] = p
	return clonePromotionHealth(next), nil
}

func (m *MemStore) guardPromotionHealthPublishLocked(p ProjectEnvironmentPromotion) error {
	h := m.promotionHealth[p.ID]
	if h == nil || p.TargetReleaseSetID != "" {
		return nil
	}
	if err := m.guardPromotionMigrationPublishLocked(p); err != nil {
		return err
	}
	if err := m.guardPromotionWorkerReadyLocked(p, h, false); err != nil {
		return err
	}
	now := m.clock().UTC()
	if p.Status != "running" || h.Phase != "cutover" || !now.Before(h.Deadline) || h.Verdict != "healthy" || !promotionHealthBaselineHealthy(h, now) {
		return ErrConflict
	}
	return nil
}
