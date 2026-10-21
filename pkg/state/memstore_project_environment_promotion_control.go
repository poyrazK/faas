package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) RequestProjectPromotionControl(_ context.Context, accountID, promotionID, action string, req api.ProjectPromotionControlRequest) (*api.ProjectPromotionControl, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projectEnvironmentPromotions[promotionID]
	if !ok || p.AccountID != accountID {
		return nil, ErrNotFound
	}
	if !req.Valid() || action != "abort" && action != "recover" {
		return nil, ErrInvalidArgument
	}
	if old, ok := m.promotionControls[promotionID][req.RequestID]; ok {
		if old.Request != req || old.Action != action {
			return nil, ErrConflict
		}
		return clonePromotionControl(&old), nil
	}
	next, err := requestPromotionControl(p, m.promotionHealth[promotionID], m.activeProjectReleaseSets[releaseKey(p.ProjectID, p.ToEnvironment)], action, req, m.clock().UTC())
	if err != nil {
		return nil, err
	}
	if next.Phase == "aborted" {
		for _, pin := range m.projectEnvironmentPromotionWorkloadSpecs {
			if pin.PromotionID != promotionID {
				continue
			}
			d := m.deployments[pin.DeploymentID]
			selected := false
			for _, graph := range m.projectReleaseSets {
				if graph.Active && releaseMemberForApp(graph, d.AppID) == d.ID {
					selected = true
					break
				}
			}
			if !selected && d.TrafficPercentExplicit && d.TrafficPercent == 0 && (d.Status == DeployPending || d.Status == DeployLive) {
				d.Status = DeploySuperseded
				m.deployments[d.ID] = d
			}
		}
	}
	m.savePromotionControlLocked(promotionID, next.Control)
	m.promotionHealth[promotionID] = next
	p.Status, p.VerificationStatus, p.Error = promotionHealthResult(next)
	p.VerificationError = p.Error
	p.UpdatedAt = m.clock().UTC()
	p.CompletedAt, p.VerificationCompletedAt = nil, nil
	if p.Status != "running" {
		p.CompletedAt, p.VerificationCompletedAt = &p.UpdatedAt, &p.UpdatedAt
	}
	m.projectEnvironmentPromotions[promotionID] = p
	return clonePromotionControl(next.Control), nil
}

func (m *MemStore) savePromotionControlLocked(id string, c *api.ProjectPromotionControl) {
	if c == nil {
		return
	}
	if m.promotionControls == nil {
		m.promotionControls = map[string]map[string]api.ProjectPromotionControl{}
	}
	if m.promotionControls[id] == nil {
		m.promotionControls[id] = map[string]api.ProjectPromotionControl{}
	}
	m.promotionControls[id][c.Request.RequestID] = *clonePromotionControl(c)
}
