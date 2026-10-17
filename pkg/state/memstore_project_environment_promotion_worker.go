package state

import (
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

// Candidate and rollback holds use the persisted promotion, never desired app
// settings. A failed/expired promotion cannot keep an unselected worker alive.
func (m *MemStore) promotionWorkerHoldsLocked(appID string) []string {
	var ids []string
	now := m.clock()
	for id, h := range m.promotionHealth {
		var worker *api.ProjectReleaseWorker
		for _, w := range api.ProjectPromotionWorkers(&h.Policy) {
			if w.AppID == appID {
				worker = w
				break
			}
		}
		if worker == nil {
			continue
		}
		p := m.projectEnvironmentPromotions[id]
		if h.Control != nil && p.TargetReleaseSetID != "" && p.RestoredTargetReleaseSetID == "" {
			baseline := m.projectReleaseSets[p.PreviousTargetReleaseSetID]
			if (baseline.Active || baseline.ExpiresAt != nil && now.Before(*baseline.ExpiresAt)) && h.Control.Status == "pending" && (h.Phase == "restoring" || h.Phase == "rolling_back") && now.Before(h.Control.Deadline) {
				ids = append(ids, worker.PreviousDeploymentID)
			}
			continue
		}
		if (h.Phase == "rolling_back" || p.RollbackStatus == "rolling_back") && p.TargetReleaseSetID != "" && p.RollbackReleaseSetID == "" && p.RollbackStatus != "rolled_back" {
			deadline := h.Deadline.Add(api.ProjectReleaseVerificationTimeout)
			if p.RollbackStartedAt != nil && p.RollbackStatus == "rolling_back" {
				deadline = p.RollbackStartedAt.Add(api.ProjectReleaseVerificationTimeout)
			}
			if now.Before(deadline) {
				ids = append(ids, worker.PreviousDeploymentID)
			}
		} else if p.Status == "running" && p.TargetReleaseSetID == "" && (h.Phase == "preflight" || h.Phase == "cutover") && now.Before(h.Deadline) {
			if pin := m.projectEnvironmentPromotionWorkloadSpecs[workloadSpecHeadKey(id, appID)]; pin.DeploymentID != "" {
				ids = append(ids, pin.DeploymentID)
			}
		}
	}
	return ids
}
func (m *MemStore) promotionWorkerHeldLocked(appID, depID string) bool {
	for _, id := range m.promotionWorkerHoldsLocked(appID) {
		if id == depID {
			return true
		}
	}
	return false
}
func (m *MemStore) observePromotionWorkerLocked(p ProjectEnvironmentPromotion, h *api.ProjectPromotionHealth, now time.Time) error {
	if len(api.ProjectPromotionWorkers(&h.Policy)) == 0 {
		return nil
	}
	c := m.promotionReleaseConfigurations[p.ID]
	p.Workers = true
	current, err := m.promotionReleaseInputsLocked(p)
	if err != nil {
		return err
	}
	if api.ProjectPromotionWorkerSetID(current.Worker, current.Workers) != api.ProjectPromotionWorkerSetID(c.Worker, c.Workers) {
		return ErrConflict
	}
	return visitPromotionWorkers(h, func(one *api.ProjectPromotionHealth) error { return m.observePromotionWorkerSingleLocked(p, one, now) })
}
func (m *MemStore) observePromotionWorkerSingleLocked(p ProjectEnvironmentPromotion, h *api.ProjectPromotionHealth, now time.Time) error {
	var err error
	pin := m.projectEnvironmentPromotionWorkloadSpecs[workloadSpecHeadKey(p.ID, h.Policy.Worker.AppID)]
	h.Worker = copyPromotionWorker(h.Policy.Worker)
	h.Worker.DeploymentID = pin.DeploymentID
	h.Worker.Status = "building"
	if pin.DeploymentID != "" {
		h.Worker.InstanceID, err = m.releaseWorkerReadyLocked(p.AccountID, h.Worker.AppID, pin.DeploymentID)
		if err != nil {
			return err
		}
	}
	if h.Worker.InstanceID != "" {
		h.Worker.Status = "ready"
		if p.TargetReleaseSetID != "" {
			h.Worker.Status = "active"
		}
	}
	if h.WorkerHealth == nil {
		h.WorkerHealth = &api.ProjectWorkerHealth{Status: "insufficient_evidence"}
	}
	if h.Phase != "finalizing" {
		m.readWorkerHealthLocked(promotionWorkerSample(h, now), now)
		if h.Phase == "restoring" && m.guardPromotionWorkerReadyLocked(p, h, true) != nil {
			h.WorkerHealth.Baseline = api.ProjectWorkerJobSample{}
		}
	}
	return nil
}
func (m *MemStore) guardPromotionWorkerReadyLocked(p ProjectEnvironmentPromotion, h *api.ProjectPromotionHealth, rollback bool) error {
	for _, one := range promotionWorkerHealthViews(h) {
		if err := m.guardPromotionWorkerSingleReadyLocked(p, one, rollback); err != nil {
			return err
		}
	}
	return nil
}
func (m *MemStore) guardPromotionWorkerSingleReadyLocked(p ProjectEnvironmentPromotion, h *api.ProjectPromotionHealth, rollback bool) error {
	if h == nil || h.Policy.Worker == nil {
		return nil
	}
	id := h.Policy.Worker.PreviousDeploymentID
	if !rollback {
		id = m.projectEnvironmentPromotionWorkloadSpecs[workloadSpecHeadKey(p.ID, h.Policy.Worker.AppID)].DeploymentID
	}
	ready, err := m.releaseWorkerReadyLocked(p.AccountID, h.Policy.Worker.AppID, id)
	if err != nil {
		return err
	}
	if ready == "" {
		return ErrPromotionWorkerNotReady
	}
	return nil
}
