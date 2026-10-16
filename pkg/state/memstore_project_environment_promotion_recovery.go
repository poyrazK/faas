package state

import (
	"context"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) latestPromotionRecoveryLocked(id string) (promotionRecoveryRecord, bool) {
	var latest promotionRecoveryRecord
	found := false
	for _, r := range m.promotionRecoveries[id] {
		if !found || r.Receipt.Request.ExpectedGeneration > latest.Receipt.Request.ExpectedGeneration {
			latest = r
			found = true
		}
	}
	return latest, found
}
func (m *MemStore) ProjectPromotionMigrationRecovery(_ context.Context, account, id string) (*api.ProjectPromotionMigrationRecovery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.projectEnvironmentPromotions[id].AccountID != account {
		return nil, ErrNotFound
	}
	r, ok := m.latestPromotionRecoveryLocked(id)
	if !ok {
		return nil, nil
	}
	return copyPromotionRecovery(r), nil
}
func (m *MemStore) promotionMigrationTaskResolvedLocked(taskID string) bool {
	for id, pin := range m.promotionMigrations {
		if pin.TaskID == taskID {
			r, ok := m.latestPromotionRecoveryLocked(id)
			return ok && (r.Receipt.Status == "resumed" || r.Receipt.Status == "resolved") && m.promotionMigrationRuntimes[id].DestroyedAt != nil
		}
	}
	return false
}
func (m *MemStore) recoveryMembersLocked(p ProjectEnvironmentPromotion) []ProjectReleaseMember {
	members := []ProjectReleaseMember{}
	for _, w := range m.promotionReleaseConfigurations[p.ID].Workloads {
		candidate := m.projectEnvironmentPromotionWorkloadSpecs[workloadSpecHeadKey(p.ID, w.AppID)]
		members = append(members, ProjectReleaseMember{AppID: w.AppID, DeploymentID: candidate.DeploymentID})
	}
	return members
}
func (m *MemStore) recoveryResumeGuardLocked(p ProjectEnvironmentPromotion, r promotionRecoveryRecord) error {
	if err := m.guardPromotionReleaseConfigurationLocked(p, m.recoveryMembersLocked(p), false, false); err != nil {
		return err
	}
	task := m.appTasks[m.promotionMigrations[p.ID].TaskID]
	values, err := m.runtimeAppValuesLocked(p.AccountID, task.AppID, task.DeploymentID)
	if err != nil {
		return err
	}
	fence, err := NewRuntimeAppConfigFence(values)
	if err != nil || fence.Fingerprint != r.RuntimeFingerprint {
		return ErrConflict
	}
	return nil
}
func (m *MemStore) RequestProjectPromotionMigrationRecovery(_ context.Context, account, id string, req api.ProjectPromotionMigrationRecoveryRequest) (*api.ProjectPromotionMigrationRecovery, error) {
	if !req.Valid() {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projectEnvironmentPromotions[id]
	if !ok || p.AccountID != account {
		return nil, ErrNotFound
	}
	if r, ok := m.promotionRecoveries[id][req.RequestID]; ok {
		if !promotionRecoveryMatches(r, req) {
			return nil, ErrConflict
		}
		return copyPromotionRecovery(r), nil
	}
	if previous, ok := m.latestPromotionRecoveryLocked(id); ok && previous.Receipt.Status != "blocked" {
		return nil, ErrConflict
	}
	h := m.promotionHealth[id]
	pin := m.promotionMigrations[id]
	task := m.appTasks[pin.TaskID]
	if h == nil || h.Generation != req.ExpectedGeneration || pin.TaskID != req.ExpectedTaskID || !promotionRecoveryTaskEligible(p, h, task, pin, m.promotionMigrationRuntimes[id]) {
		return nil, ErrConflict
	}
	if !m.promotionRecoveryLaneAllowedLocked(p, task.ID) {
		return nil, ErrConflict
	}
	r := newPromotionRecovery(account, req, pin, m.clock().UTC())
	if req.Action == "resume" {
		if err := m.recoveryResumeGuardLocked(p, r); err != nil {
			return nil, err
		}
	} else {
		other := m.projectEnvironmentPromotions[req.ReplacementPromotionID]
		if !promotionRecoveryReplacementEligible(p, other, m.promotionHealth[other.ID]) || m.activeProjectReleaseSets[releaseKey(p.ProjectID, p.ToEnvironment)] != other.TargetReleaseSetID {
			return nil, ErrConflict
		}
	}
	if m.promotionRecoveries == nil {
		m.promotionRecoveries = map[string]map[string]promotionRecoveryRecord{}
	}
	if m.promotionRecoveries[id] == nil {
		m.promotionRecoveries[id] = map[string]promotionRecoveryRecord{}
	}
	m.promotionRecoveries[id][req.RequestID] = r
	next := clonePromotionHealth(h)
	next.Generation++
	m.promotionHealth[id] = next
	return copyPromotionRecovery(r), nil
}
func (m *MemStore) FinalizeProjectPromotionMigrationRecovery(_ context.Context, account, id, request string, e ProjectMigrationRecoveryEvidence) (*api.ProjectPromotionMigrationRecovery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.projectEnvironmentPromotions[id]
	if p.AccountID != account {
		return nil, ErrNotFound
	}
	r, ok := m.promotionRecoveries[id][request]
	if !ok {
		return nil, ErrNotFound
	}
	if promotionRecoveryTerminal(r.Receipt.Status) {
		return copyPromotionRecovery(r), nil
	}
	pin := m.promotionMigrations[id]
	task := m.appTasks[pin.TaskID]
	runtime := m.promotionMigrationRuntimes[id]
	h := m.promotionHealth[id]
	if r.Receipt.Status != "verifying" || !m.clock().Before(r.Receipt.Deadline) || runtime.DestroyedAt == nil || !promotionRecoveryTaskEligible(p, h, task, pin, runtime) {
		return nil, ErrConflict
	}
	if r.Receipt.Request.Action == "resume" {
		if err := m.recoveryResumeGuardLocked(p, r); err != nil {
			return nil, err
		}
		r.Receipt.Status = "resumed"
		next := promotionRecoveryResumeHealth(h, m.clock().UTC())
		m.promotionHealth[id] = next
		p.Status, p.VerificationStatus, p.Error = promotionHealthResult(next)
		p.CompletedAt = nil
		m.projectEnvironmentPromotions[id] = p
	} else {
		other := m.projectEnvironmentPromotions[r.Receipt.Request.ReplacementPromotionID]
		otherHealth := m.promotionHealth[other.ID]
		if !promotionRecoveryReplacementEligible(p, other, otherHealth) || m.activeProjectReleaseSets[releaseKey(p.ProjectID, p.ToEnvironment)] != other.TargetReleaseSetID || !promotionRecoveryEvidenceEligible(otherHealth, e, m.clock()) {
			return nil, ErrConflict
		}
		graph := m.projectReleaseSets[other.TargetReleaseSetID]
		if len(e.RuntimeFingerprints) != len(graph.Members) || m.guardPromotionReleaseConfigurationLocked(other, graph.Members, false, true) != nil || m.validateProjectEnvironmentPromotionConfigLocked(other, false) != nil {
			return nil, ErrConflict
		}
		for _, member := range graph.Members {
			snapshot, err := m.runtimeAppValuesLocked(account, member.AppID, member.DeploymentID)
			if err != nil {
				return nil, err
			}
			fence, err := NewRuntimeAppConfigFence(snapshot)
			if err != nil || fence.Fingerprint != e.RuntimeFingerprints[member.AppID] {
				return nil, ErrConflict
			}
		}
		if len(api.ProjectPromotionWorkers(&otherHealth.Policy)) > 0 {
			now := m.clock().UTC()
			observed := promotionRecoveryWorkerObservation(otherHealth)
			if err := m.observePromotionWorkerLocked(other, observed, now); err != nil {
				return nil, err
			}
			var proofs []*promotionRecoveryWorkerProof
			for _, one := range promotionWorkerHealthViews(observed) {
				sample := promotionRecoveryWorkerSample(one, r.Receipt.RequestedAt)
				m.readWorkerHealthLocked(sample, now)
				proof, err := promotionRecoveryWorkerProofFor(one, sample, r.Receipt.RequestedAt, now)
				if err != nil {
					return nil, err
				}
				proofs = append(proofs, proof)
			}
			if otherHealth.Policy.Worker != nil {
				r.WorkerProof = proofs[0]
			} else {
				r.WorkerProofs = proofs
			}
		}
		r.Receipt.Status = "resolved"
		r.RuntimeFingerprints = e.RuntimeFingerprints
	}
	now := m.clock().UTC()
	r.Receipt.ResolvedAt = &now
	m.promotionRecoveries[id][request] = r
	return copyPromotionRecovery(r), nil
}
func (m *MemStore) BlockProjectPromotionMigrationRecovery(_ context.Context, account, id, request, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.projectEnvironmentPromotions[id].AccountID != account {
		return ErrNotFound
	}
	r, ok := m.promotionRecoveries[id][request]
	if !ok {
		return ErrNotFound
	}
	if promotionRecoveryTerminal(r.Receipt.Status) {
		return nil
	}
	r.Receipt.Status, r.Receipt.Reason = "blocked", reason
	m.promotionRecoveries[id][request] = r
	return nil
}
func (m *MemStore) RegisterPromotionMigrationRuntime(_ context.Context, passed AppTask, node string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, pin, found := m.promotionMigrationRuntimeLocked(passed)
	if !found {
		return "", nil
	}
	task := m.appTasks[passed.ID]
	if task.Status != AppTaskRestoring || task.LeaseToken == nil || passed.LeaseToken == nil || *task.LeaseToken != *passed.LeaseToken || *task.LeaseToken != pin.PreparedLeaseToken || node == "" || task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(m.clock()) {
		return "", ErrConflict
	}
	if err := m.guardPromotionMigrationRuntimeLocked(task, true); err != nil {
		return "", err
	}
	if m.promotionMigrationRuntimes == nil {
		m.promotionMigrationRuntimes = map[string]promotionMigrationRuntime{}
	}
	runtime := promotionMigrationRuntime{NodeID: node, LeaseToken: *task.LeaseToken, InstanceID: promotionMigrationInstanceID(task.ID, *task.LeaseToken)}
	previous := m.promotionMigrationRuntimes[id]
	if previous.LeaseToken == runtime.LeaseToken && previous.NodeID != node {
		return "", ErrConflict
	}
	m.promotionMigrationRuntimes[id] = runtime
	return runtime.InstanceID, nil
}
func (m *MemStore) PendingPromotionMigrationQuiescence(_ context.Context) ([]PromotionMigrationQuiescenceJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	jobs := []PromotionMigrationQuiescenceJob{}
	for id := range m.promotionRecoveries {
		r, ok := m.latestPromotionRecoveryLocked(id)
		if !ok || r.Receipt.Status != "quiescing" {
			continue
		}
		p := m.projectEnvironmentPromotions[id]
		pin := m.promotionMigrations[id]
		task := m.appTasks[pin.TaskID]
		runtime := m.promotionMigrationRuntimes[id]
		if promotionRecoveryTaskEligible(p, m.promotionHealth[id], task, pin, runtime) {
			jobs = append(jobs, PromotionMigrationQuiescenceJob{AccountID: p.AccountID, PromotionID: id, RequestID: r.Receipt.Request.RequestID, TaskID: task.ID, NodeID: runtime.NodeID, InstanceID: runtime.InstanceID, LeaseToken: runtime.LeaseToken})
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].PromotionID < jobs[j].PromotionID })
	return jobs[:min(len(jobs), api.ProjectPromotionHealthBatchSize)], nil
}
func (m *MemStore) AcknowledgePromotionMigrationQuiescence(_ context.Context, job PromotionMigrationQuiescenceJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.projectEnvironmentPromotions[job.PromotionID]
	r, ok := m.promotionRecoveries[job.PromotionID][job.RequestID]
	if !ok || p.AccountID != job.AccountID {
		return ErrNotFound
	}
	if r.Receipt.Status != "quiescing" {
		return nil
	}
	pin := m.promotionMigrations[p.ID]
	runtime := m.promotionMigrationRuntimes[p.ID]
	if pin.TaskID != job.TaskID || runtime.NodeID != job.NodeID || runtime.InstanceID != job.InstanceID || runtime.LeaseToken != job.LeaseToken || !promotionRecoveryTaskEligible(p, m.promotionHealth[p.ID], m.appTasks[pin.TaskID], pin, runtime) {
		return ErrConflict
	}
	now := m.clock().UTC()
	if runtime.DestroyedAt == nil {
		runtime.DestroyedAt = &now
	}
	m.promotionMigrationRuntimes[p.ID] = runtime
	r.Receipt.Status = "verifying"
	m.promotionRecoveries[p.ID][job.RequestID] = r
	return nil
}

func (m *MemStore) promotionRecoveryPendingLocked(id string) bool {
	r, ok := m.latestPromotionRecoveryLocked(id)
	return ok && (r.Receipt.Status == "quiescing" || r.Receipt.Status == "verifying")
}
func (m *MemStore) promotionRecoveryLaneAllowedLocked(p ProjectEnvironmentPromotion, taskID string) bool {
	for id, other := range m.projectEnvironmentPromotions {
		if id != p.ID && other.ProjectID == p.ProjectID && (m.pendingPromotionMigrationOwnerLocked(id) || m.promotionRecoveryPendingLocked(id)) {
			return false
		}
	}
	for _, t := range m.appTasks {
		if t.ID != taskID && t.Kind == AppTaskKindRelease && m.apps[t.AppID].ProjectID == p.ProjectID && (!t.Status.Terminal() || t.StartedAt != nil && t.ExitCode == nil && !m.promotionMigrationTaskResolvedLocked(t.ID)) {
			return false
		}
	}
	for _, s := range m.projectSubmissions {
		if pendingProjectMigration(s.Payload, p.ProjectID) {
			return false
		}
	}
	return m.migrationPublicationAllowedLocked(p.ProjectID)
}
func (m *MemStore) pendingPromotionMigrationOwnerLocked(id string) bool {
	p := m.projectEnvironmentPromotions[id]
	return p.Status == "running" && m.promotionHealth[id] != nil && m.promotionHealth[id].Policy.Migration != nil
}
