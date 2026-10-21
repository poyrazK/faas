package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) admitPromotionMigrationLocked(p ProjectEnvironmentPromotion, c *promotionReleaseConfiguration) error {
	if err := validatePromotionMigrationAdmission(p, c); err != nil {
		return err
	}
	if c == nil || c.Migration == nil {
		return nil
	}
	if !m.migrationPublicationAllowedLocked(p.ProjectID) {
		return ErrConflict
	}
	for id, other := range m.projectEnvironmentPromotions {
		if id != p.ID && other.ProjectID == p.ProjectID && (m.pendingPromotionMigrationOwnerLocked(id) || m.promotionRecoveryPendingLocked(id)) {
			return ErrConflict
		}
	}
	for _, entry := range m.projectSubmissions {
		if pendingProjectMigration(entry.Payload, p.ProjectID) {
			return ErrConflict
		}
	}
	for _, task := range m.appTasks {
		if task.Kind == AppTaskKindRelease && m.apps[task.AppID].ProjectID == p.ProjectID && (!task.Status.Terminal() || task.StartedAt != nil && task.ExitCode == nil && !m.promotionMigrationTaskResolvedLocked(task.ID)) {
			return ErrConflict
		}
	}
	if m.promotionMigrations == nil {
		m.promotionMigrations = map[string]promotionMigrationCapture{}
	}
	return nil
}

func (m *MemStore) ReconcileProjectEnvironmentPromotionMigration(_ context.Context, accountID, promotionID string) (*api.ProjectPromotionHealth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projectEnvironmentPromotions[promotionID]
	h := m.promotionHealth[promotionID]
	if !ok || p.AccountID != accountID {
		return nil, ErrNotFound
	}
	if h == nil || h.Policy.Migration == nil || h.Phase != "migrating" {
		return clonePromotionHealth(h), nil
	}
	c := m.promotionReleaseConfigurations[promotionID]
	if c.authenticate() != nil || c.Migration == nil || *c.Migration != *h.Policy.Migration {
		return nil, ErrConflict
	}
	members := []ProjectReleaseMember{}
	for _, w := range c.Workloads {
		pin := m.projectEnvironmentPromotionWorkloadSpecs[workloadSpecHeadKey(p.ID, w.AppID)]
		if pin.DeploymentID == "" {
			return clonePromotionHealth(h), nil
		}
		members = append(members, ProjectReleaseMember{w.AppID, pin.DeploymentID})
	}
	if err := m.guardPromotionReleaseConfigurationLocked(p, members, false, false); err != nil {
		return nil, err
	}
	deploymentID := m.projectEnvironmentPromotionWorkloadSpecs[workloadSpecHeadKey(p.ID, c.Migration.AppID)].DeploymentID
	d := m.deployments[deploymentID]
	pin := m.promotionMigrations[p.ID]
	var task *AppTask
	if pin.TaskID != "" {
		t, found := m.appTasks[pin.TaskID]
		if !found {
			return nil, ErrConflict
		}
		task = &t
	} else if m.clock().Before(h.StartedAt.Add(api.ProjectPromotionMigrationStartTimeout)) {
		snapshot, err := m.runtimeAppValuesLocked(p.AccountID, d.AppID, d.ID)
		if err != nil {
			return nil, err
		}
		fence, err := NewRuntimeAppConfigFence(snapshot)
		if err != nil {
			return nil, err
		}
		params, err := promotionMigrationTaskParams(p, h, d)
		if err != nil {
			return nil, err
		}
		params.promotionMigrationID = p.ID
		t, err := m.createAppTaskLocked(params)
		if err != nil {
			return nil, err
		}
		pin.TaskID, pin.RuntimeFingerprint = t.ID, fence.Fingerprint
		m.promotionMigrations[p.ID] = pin
		task = &t
	}
	next := promotionMigrationOutcome(p.AccountID, h, d, task, m.clock().UTC())

	if next.Phase == "preflight" && verifyPromotionMigrationTask(p.AccountID, h, d, *task, pin) != nil {
		return nil, ErrConflict
	}
	if next.Generation == h.Generation {
		return clonePromotionHealth(next), nil
	}
	m.promotionHealth[p.ID] = next
	p.Status, p.VerificationStatus, p.Error = promotionHealthResult(next)
	p.UpdatedAt = m.clock().UTC()
	if p.Status != "running" {
		p.CompletedAt = &p.UpdatedAt
	}
	m.projectEnvironmentPromotions[p.ID] = p
	return clonePromotionHealth(next), nil
}

func (m *MemStore) promotionMigrationRuntimeLocked(task AppTask) (string, promotionMigrationCapture, bool) {
	for id, pin := range m.promotionMigrations {
		if pin.TaskID == task.ID {
			return id, pin, true
		}
	}
	return "", promotionMigrationCapture{}, false
}

func (m *MemStore) guardPromotionMigrationRuntimeLocked(task AppTask, requirePrepared bool) error {
	id, pin, found := m.promotionMigrationRuntimeLocked(task)
	if !found {
		return nil
	}
	h, p := m.promotionHealth[id], m.projectEnvironmentPromotions[id]
	if h == nil || p.Status != "running" || h.Phase != "migrating" || !m.clock().Before(h.Deadline) || task.DeploymentScope != p.ToEnvironment {
		return ErrConflict
	}
	c := m.promotionReleaseConfigurations[id]
	members := []ProjectReleaseMember{}
	for _, w := range c.Workloads {
		member := m.projectEnvironmentPromotionWorkloadSpecs[workloadSpecHeadKey(id, w.AppID)]
		members = append(members, ProjectReleaseMember{AppID: w.AppID, DeploymentID: member.DeploymentID})
	}
	if err := m.guardPromotionReleaseConfigurationLocked(p, members, false, false); err != nil {
		return err
	}
	if requirePrepared && (task.LeaseToken == nil || pin.PreparedLeaseToken != *task.LeaseToken) {
		return ErrConflict
	}
	snapshot, err := m.runtimeAppValuesLocked(task.AccountID, task.AppID, task.DeploymentID)
	if err != nil {
		return err
	}
	fence, err := NewRuntimeAppConfigFence(snapshot)
	if err != nil || fence.Fingerprint != pin.RuntimeFingerprint {
		return ErrConflict
	}
	return nil
}

func (m *MemStore) bindPromotionMigrationRuntimeLocked(task, passed AppTask, fence RuntimeAppConfigFence) (bool, error) {
	id, pin, found := m.promotionMigrationRuntimeLocked(task)
	if !found {
		return false, nil
	}
	if task.Status != AppTaskRestoring || task.LeaseToken == nil || passed.LeaseToken == nil || *task.LeaseToken != *passed.LeaseToken || task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(m.clock()) || !validRuntimeAppConfigFence(fence) || fence.Fingerprint != pin.RuntimeFingerprint {
		return true, ErrConflict
	}
	if err := m.guardPromotionMigrationRuntimeLocked(task, false); err != nil {
		return true, err
	}
	pin.PreparedLeaseToken = *task.LeaseToken
	m.promotionMigrations[id] = pin
	return true, nil
}

func (m *MemStore) guardPromotionMigrationPublishLocked(p ProjectEnvironmentPromotion) error {
	h := m.promotionHealth[p.ID]
	if h == nil || h.Policy.Migration == nil {
		return nil
	}
	pin := m.promotionMigrations[p.ID]
	task, ok := m.appTasks[pin.TaskID]
	if !ok {
		return ErrConflict
	}
	d := m.deployments[task.DeploymentID]
	if err := verifyPromotionMigrationTask(p.AccountID, h, d, task, pin); err != nil {
		r, ok := m.latestPromotionRecoveryLocked(p.ID)
		if !ok || !promotionRecoveredMigrationEligible(p.AccountID, h, d, task, pin, r, m.promotionMigrationRuntimes[p.ID]) {
			return err
		}
	}
	snapshot, err := m.runtimeAppValuesLocked(task.AccountID, task.AppID, task.DeploymentID)
	if err != nil {
		return err
	}
	fence, err := NewRuntimeAppConfigFence(snapshot)
	if err != nil || fence.Fingerprint != pin.RuntimeFingerprint {
		return ErrConflict
	}
	return nil
}

func (m *MemStore) promotionMigrationTaskAllowedLocked(params CreateAppTaskParams) bool {
	if params.Kind != AppTaskKindRelease {
		return true
	}
	for id, p := range m.projectEnvironmentPromotions {
		h := m.promotionHealth[id]
		pin := m.promotionMigrations[id]
		previous, hasTask := m.appTasks[pin.TaskID]
		uncertain := hasTask && previous.StartedAt != nil && previous.ExitCode == nil && !m.promotionMigrationTaskResolvedLocked(previous.ID)
		if p.ProjectID == m.apps[params.AppID].ProjectID && (uncertain || p.Status == "running" && h != nil && h.Policy.Migration != nil || m.promotionRecoveryPendingLocked(id)) {
			// Creation is exclusive to the migration reconciler; its journal is
			// linked before the scheduler can observe the task under this lock.
			return params.promotionMigrationID == id
		}
	}
	return true
}

// Keep project submission admission symmetric with promotion admission.
func (m *MemStore) pendingPromotionMigrationLocked(projectID string) bool {
	for id, p := range m.projectEnvironmentPromotions {
		if p.ProjectID == projectID {
			pin := m.promotionMigrations[id]
			task, ok := m.appTasks[pin.TaskID]
			if ok && task.StartedAt != nil && task.ExitCode == nil && !m.promotionMigrationTaskResolvedLocked(task.ID) {
				return true
			}
		}
		if p.ProjectID == projectID && (m.pendingPromotionMigrationOwnerLocked(id) || m.promotionRecoveryPendingLocked(id)) {
			return true
		}
	}
	return false
}

func (m *MemStore) promotionMigrationClaimAllowedLocked(task AppTask) bool {
	if task.Kind != AppTaskKindRelease {
		return true
	}
	if _, _, owned := m.promotionMigrationRuntimeLocked(task); owned {
		return m.guardPromotionMigrationRuntimeLocked(task, false) == nil
	}
	return !m.pendingPromotionMigrationLocked(m.apps[task.AppID].ProjectID)
}
