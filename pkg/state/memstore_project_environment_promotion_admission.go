package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) promotionAdmissionCountsLocked(account, id string) promotionAdmissionCounts {
	var counts promotionAdmissionCounts
	own := m.projectEnvironmentPromotions[id]
	for otherID, h := range m.promotionHealth {
		pending, _ := m.promotionWorkLocked(otherID)
		if !pending {
			continue
		}
		p := m.projectEnvironmentPromotions[otherID]
		if h.Phase != "queued" {
			counts.fleetActive++
			if p.AccountID == account {
				counts.accountActive++
			}
			continue
		}
		counts.fleetQueued++
		if p.AccountID != account {
			continue
		}
		counts.accountQueued++
		if p.CreatedAt.Before(own.CreatedAt) || p.CreatedAt.Equal(own.CreatedAt) && p.ID < own.ID {
			counts.older++
		}
	}
	return counts
}
func (m *MemStore) findPromotionApprovalLocked(p ProjectEnvironmentPromotion) (*ProjectEnvironmentApproval, error) {
	if p.Approval == nil {
		return nil, nil
	}
	for _, approval := range m.projectEnvironmentApprovals {
		if approval.AccountID == p.AccountID && approval.ProjectSlug == p.ProjectSlug && approval.EnvironmentSlug == p.ToEnvironment && approval.PlanTokenHash == p.Approval.PlanTokenHash && approval.ApprovalTokenHash == p.Approval.ApprovalTokenHash && approval.ConsumedAt == nil && approval.ExpiresAt.After(m.clock()) {
			return &approval, nil
		}
	}
	return nil, &PromotionApprovalError{}
}
func (m *MemStore) validateQueuedPromotionLocked(p ProjectEnvironmentPromotion) error {
	if p.TargetReleaseSetID != "" || p.TargetConfigVersion != 0 || p.RestoredTargetReleaseSetID != "" {
		return ErrConflict
	}
	for _, scope := range []struct{ environment, id string }{{p.FromEnvironment, p.SourceReleaseSetID}, {p.ToEnvironment, p.PreviousTargetReleaseSetID}} {
		if m.activeProjectReleaseSets[releaseKey(p.ProjectID, scope.environment)] != scope.id {
			return ErrConflict
		}
		graph, ok := m.projectReleaseSets[scope.id]
		if !ok || !graph.Active || time.Duration(graph.TTLSeconds)*time.Second < api.ProjectPromotionHealthMinRetention {
			return ErrConflict
		}
	}
	if err := m.verifyProjectEnvironmentPromotionQualificationLocked(p); err != nil {
		return err
	}
	captured, ok := m.promotionReleaseConfigurations[p.ID]
	if !ok {
		return ErrConflict
	}
	p.Workers, p.Migrate, p.ResolveServiceBindings = len(captured.workers()) > 0, captured.Migration != nil, captured.DestinationConfig != nil
	current, err := m.promotionReleaseInputsLocked(p)
	if err != nil {
		return err
	}
	view, err := current.prepare(p, m.runtimeAppValuesLocked)
	if err != nil {
		return err
	}
	if view == nil || view.Hash != captured.Hash {
		return ErrConflict
	}
	_, err = m.preparePromotionFeatureFlagsLocked(p, false, false)
	return err
}
func (m *MemStore) DispatchProjectEnvironmentPromotion(ctx context.Context, account, id, token string, generation int64) (*api.ProjectPromotionHealth, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, ok := m.projectEnvironmentPromotions[id]
	if !ok || p.AccountID != account {
		return nil, ErrNotFound
	}
	h, lease := m.promotionHealth[id], m.promotionHealthLeases[id]
	now := m.clock().UTC()
	if h == nil || h.Phase != "queued" || h.Admission == nil || h.Generation != generation || lease.token != token || !lease.until.After(now) || p.Status != "running" {
		return nil, ErrConflict
	}
	next := queuedPromotionDecision(h, "", now)
	if next.Generation == h.Generation {
		if m.promotionAdmissionCountsLocked(account, id).waitingReason() != "" {
			return clonePromotionHealth(h), nil
		}
		if err := m.validateQueuedPromotionLocked(p); err != nil {
			if !queueIntentConflict(err) {
				return nil, err
			}
			next = queuedPromotionDecision(h, "conflict", now)
		} else {
			now = m.clock().UTC()
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !lease.until.After(now) {
				return nil, ErrConflict
			}
			next = queuedPromotionDecision(h, "", now)
			if next.Generation == h.Generation {
				next = startQueuedPromotion(h, now)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !lease.until.After(m.clock()) {
		return nil, ErrConflict
	}

	m.promotionHealth[id] = next
	p.Status, p.VerificationStatus, p.Error = promotionHealthResult(next)
	p.VerificationError, p.UpdatedAt = p.Error, now
	if next.Admission.StartedAt != nil && p.VerificationStartedAt == nil {
		p.VerificationStartedAt = &now
	}
	if p.Status != "running" {
		p.CompletedAt, p.VerificationCompletedAt = &now, &now
	}
	m.projectEnvironmentPromotions[id] = p
	return clonePromotionHealth(next), nil
}
