package state

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

var _ EnvironmentGitOpsWorkloadRetirementStore = (*MemStore)(nil)
var _ EnvironmentWorkloadRetirementAdmissionStore = (*MemStore)(nil)

func (m *MemStore) workloadAdmissionRetiredLocked(appID, scope string) bool {
	for _, memory := range m.environmentGitOps {
		if workloadRetirementAdmissionScope(memory.source.EnvironmentSlug) != workloadRetirementAdmissionScope(scope) {
			continue
		}
		for _, r := range memory.retirements {
			if r.AppID == appID && r.BlocksAdmission() {
				return true
			}
		}
	}
	return false
}
func (m *MemStore) EnvironmentWorkloadAdmissionRetired(ctx context.Context, appID, scope string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.workloadAdmissionRetiredLocked(appID, scope), nil
}
func (m *MemStore) ListEnvironmentGitOpsWorkloadRetirements(ctx context.Context, accountID, sourceID string) ([]EnvironmentWorkloadRetirement, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, ok := m.environmentGitOps[sourceID]
	if !ok || memory.source.AccountID != accountID {
		return nil, ErrNotFound
	}
	out := []EnvironmentWorkloadRetirement{}
	for _, r := range memory.retirements {
		out = append(out, cloneWorkloadRetirement(r))
	}
	slices.SortFunc(out, func(a, b EnvironmentWorkloadRetirement) int { return strings.Compare(a.Resource, b.Resource) })
	return out, nil
}
func (m *MemStore) retirementInventoryLocked(memory *environmentGitOpsMemory, resource string) workloadRetirementInventory {
	appID := memory.resources[resource]
	out := workloadRetirementInventory{JobID: m.appEnvironmentWorkloadIntents[environmentWorkloadIntentKey{appID, memory.source.EnvironmentID}].JobID}
	for _, d := range m.deployments {
		if d.AppID != appID || normalizedDeploymentScope(d.Scope) != normalizedDeploymentScope(memory.source.EnvironmentSlug) {
			continue
		}
		var frozen EnvironmentWorkloadRuntime
		if json.Unmarshal([]byte(d.EnvironmentWorkloadRuntime), &frozen) == nil && frozen.SourceID == memory.source.ID {
			out.DeploymentIDs = append(out.DeploymentIDs, d.ID)
			mode := frozen.Baseline.ExecutionMode
			if raw, present := frozen.Runtime["execution_mode"]; present {
				_ = json.Unmarshal(raw, &mode)
			}
			if !d.EnvironmentWorkloadHeld() && mode != api.ExecutionModeJob && (out.RouteDeploymentID == "" || d.ID < out.RouteDeploymentID) {
				out.RouteDeploymentID = d.ID
			}

		} else if d.Status == DeployLive {
			out.UnmanagedExecution = true
		}
	}
	ownedBindings := map[string]bool{}
	for key, id := range memory.queues {
		if strings.HasPrefix(key, resource+"#") {
			out.QueueBindingIDs = append(out.QueueBindingIDs, id)
			ownedBindings[id] = true
		}
	}
	for _, b := range m.queueBindings {
		if b.AppID == appID && b.EnvironmentID == memory.source.EnvironmentID && !ownedBindings[b.ID] {
			out.UnmanagedBinding = true
		}
	}
	slices.Sort(out.DeploymentIDs)
	slices.Sort(out.QueueBindingIDs)
	return out
}
func (m *MemStore) BeginEnvironmentGitOpsWorkloadRetirements(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) ([]EnvironmentGitOpsStep, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, err := m.gitOpsLeaseLocked(lease, time.Now())
	if err != nil {
		return nil, err
	}
	desired, err := desiredEnvironmentRevision(lease.Revision)
	if err != nil {
		return nil, err
	}
	observed, err := compileGitOpsObservation(m.gitOpsSnapshotLocked(memory), desired)
	if err != nil {
		return nil, err
	}
	plan, err := environmentGitOpsPlan(memory.source, lease.Revision, desired, observed, false)
	if err != nil {
		return nil, err
	}
	if memory.source.Spec.Mode != "enforce" || !plan.CanApply() || plan.Hash != reviewed.Hash {
		return nil, ErrConflict
	}
	journal := maps.Clone(memory.retirements)
	if journal == nil {
		journal = map[string]EnvironmentWorkloadRetirement{}
	}
	steps := []EnvironmentGitOpsStep{}
	for resource, r := range journal {
		_, wanted := desired.Definition.Workloads[strings.TrimPrefix(resource, "workload/")]
		if r.Phase == "planned" && r.Generation != memory.source.Generation {
			r.Phase = "cancelled"
			journal[resource] = r
			steps = append(steps, EnvironmentGitOpsStep{Resource: resource, Path: "presence", Action: "cancel_retirement", Status: "applied"})
			continue
		}
		if !wanted && r.Phase == "recovering" && memory.source.Generation > r.RecoveryGeneration {
			r.Phase = "retired"
			r.RecoveryRevisionID = ""
			r.RecoveryGeneration = 0
			journal[resource] = r
			steps = append(steps, EnvironmentGitOpsStep{Resource: resource, Path: "presence", Action: "abandon_recovery", Status: "retired"})
			continue
		}
		if wanted && (r.Phase == "retired" || r.Phase == "recovering") && (r.RecoveryGeneration != memory.source.Generation || r.RecoveryRevisionID != lease.Revision.ID) {
			if !workloadRetirementRecoveryMatches(desired.Definition.WorkloadRecoveries[strings.TrimPrefix(resource, "workload/")], r.AppID) {
				return nil, ErrConflict
			}
			r.Phase = "recovering"
			r.RecoveryRevisionID = lease.Revision.ID
			r.RecoveryGeneration = memory.source.Generation
			journal[resource] = r
			steps = append(steps, EnvironmentGitOpsStep{Resource: resource, Path: "presence", Action: "recover", Status: "preparing"})
		}
	}
	if len(steps) == 0 {
		for _, resource := range workloadRetirementResources(plan) {
			inventory := m.retirementInventoryLocked(memory, resource)
			if inventory.UnmanagedExecution || inventory.UnmanagedBinding {
				return nil, ErrConflict
			}
			r := EnvironmentWorkloadRetirement{ID: newID(), SourceID: memory.source.ID, AccountID: memory.source.AccountID, ProjectID: memory.source.ProjectID, EnvironmentID: memory.source.EnvironmentID, AppID: memory.resources[resource], Resource: resource, RevisionID: lease.Revision.ID, Generation: memory.source.Generation, PlanHash: plan.Hash, Phase: "planned", DeploymentIDs: inventory.DeploymentIDs, QueueBindingIDs: inventory.QueueBindingIDs, JobID: inventory.JobID, RequestedAt: time.Now().UTC()}
			r.RouteDeploymentID = inventory.RouteDeploymentID
			journal[resource] = r
			steps = append(steps, EnvironmentGitOpsStep{Resource: resource, Path: "presence", Action: "retire", Status: "planned"})
		}
	}
	if len(steps) > 0 {
		memory.retirements = journal
		touchGitOpsMemoryIntent(memory)
	}
	return steps, nil
}

func (m *MemStore) PendingEnvironmentGitOpsWorkloadRetirements(ctx context.Context, lease EnvironmentGitOpsLease, nodes []string) ([]EnvironmentWorkloadRetirement, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	nodes, err := canonicalGitOpsEffectNames(nodes)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, err := m.gitOpsLeaseLocked(lease, time.Now())
	if err != nil {
		return nil, err
	}
	if !slices.Equal(nodes, m.environmentServingGatewayNamesLocked()) {
		return nil, ErrConflict
	}
	out := []EnvironmentWorkloadRetirement{}
	for resource, r := range memory.retirements {
		if r.Phase != "retiring" {
			continue
		}
		if r.RouteDeploymentID != "" && (r.RouteGeneration == 0 || !slices.Equal(r.ExpectedGateways, nodes)) {
			m.deploymentRouteGeneration++
			r.RouteGeneration = m.deploymentRouteGeneration
			r.ExpectedGateways = slices.Clone(nodes)
			r.AcknowledgedGateways = nil
			memory.retirements[resource] = r
		}
		out = append(out, cloneWorkloadRetirement(r))
	}
	slices.SortFunc(out, func(a, b EnvironmentWorkloadRetirement) int { return strings.Compare(a.Resource, b.Resource) })
	return out, nil
}
func (m *MemStore) AcknowledgeEnvironmentGitOpsWorkloadRetirement(ctx context.Context, lease EnvironmentGitOpsLease, id string, generation int64, node string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, err := m.gitOpsLeaseLocked(lease, time.Now())
	if err != nil {
		return err
	}
	for resource, r := range memory.retirements {
		if r.ID != id {
			continue
		}
		if r.Phase != "retiring" || r.RouteGeneration != generation || generation <= 0 || !slices.Contains(r.ExpectedGateways, node) {
			return ErrConflict
		}
		if !slices.Contains(r.AcknowledgedGateways, node) {
			r.AcknowledgedGateways = append(r.AcknowledgedGateways, node)
			slices.Sort(r.AcknowledgedGateways)
		}
		memory.retirements[resource] = r
		return nil
	}
	return ErrNotFound
}
func (m *MemStore) retirementRoutesWithdrawnLocked(r EnvironmentWorkloadRetirement) bool {
	return r.RouteDeploymentID == "" || r.RouteGeneration > 0 && len(r.ExpectedGateways) > 0 && slices.Equal(r.ExpectedGateways, m.environmentServingGatewayNamesLocked()) && slices.Equal(r.ExpectedGateways, r.AcknowledgedGateways)
}
func (m *MemStore) retirementDrainedLocked(r EnvironmentWorkloadRetirement) bool {
	if !m.retirementRoutesWithdrawnLocked(r) {
		return false
	}
	if r.JobID != "" {
		job, exists := m.jobs[r.JobID]
		if !exists || job.Status != "paused" && job.Status != "deleted" {
			return false
		}
	}
	for _, id := range r.QueueBindingIDs {
		binding, exists := m.queueBindings[id]
		if !exists || binding.AppID != r.AppID || binding.EnvironmentID != r.EnvironmentID || binding.Enabled || binding.RetiredAt == nil {
			return false
		}
	}
	for _, consumer := range m.triggers {
		if !consumer.Enabled || !consumer.QueueBindingID.Valid {
			continue
		}
		for _, id := range r.QueueBindingIDs {
			if consumer.QueueBindingID.String() == canonicalMemUUID(id) {
				return false
			}
		}
	}
	for _, d := range m.deployments {
		if slices.Contains(r.DeploymentIDs, d.ID) && d.Status != DeploySuperseded && d.Status != DeployCancelled && d.Status != DeployFailed {
			return false
		}
	}
	for _, i := range m.instances {
		if (slices.Contains(r.DeploymentIDs, i.DeploymentID) || r.JobID != "" && i.JobID == r.JobID) && i.State != "parked" && i.State != "stopped" && i.State != "failed" {
			return false
		}
	}
	for _, execution := range m.qualificationExecutions {
		if slices.Contains(r.DeploymentIDs, execution.Execution.DeploymentID) && execution.RetiredAt == nil {
			return false
		}
	}
	for _, i := range m.invocations {
		if i.AppID == r.AppID && (i.EnvironmentID == r.EnvironmentID || workloadRetirementAdmissionScope(i.DeploymentScope) == workloadRetirementAdmissionScope(m.environmentGitOps[r.SourceID].source.EnvironmentSlug)) && i.State == InvocationDispatching {
			return false
		}
	}
	for _, receipt := range m.records {
		if receipt.State != "claimed" || !receipt.ClaimExpiresAt.Valid || !receipt.ClaimExpiresAt.Time.After(time.Now()) {
			continue
		}
		consumer := m.triggers[receipt.TriggerID.String()]
		for _, id := range r.QueueBindingIDs {
			if consumer.QueueBindingID.Valid && consumer.QueueBindingID.String() == canonicalMemUUID(id) {
				return false
			}
		}
	}
	if r.JobID != "" {
		for id, run := range m.jobRuns {
			if run.JobID != r.JobID {
				continue
			}
			for _, task := range m.jobTasks[id] {
				if task.Status == "claimed" {
					return false
				}
			}
		}
	}
	return true
}
func (m *MemStore) ReconcileEnvironmentGitOpsWorkloadRetirements(ctx context.Context, lease EnvironmentGitOpsLease) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, err := m.gitOpsLeaseLocked(lease, time.Now())
	if err != nil {
		return false, err
	}
	ready := true
	for resource, r := range memory.retirements {
		if r.Phase == "planned" || r.Phase == "recovering" {
			ready = false
			continue
		}
		if r.Phase != "retiring" {
			continue
		}
		if m.retirementRoutesWithdrawnLocked(r) {
			for _, id := range r.DeploymentIDs {
				d, ok := m.deployments[id]
				if !ok || d.Status == DeployFailed || d.Status == DeploySuperseded || d.Status == DeployCancelled {
					continue
				}
				if d.Status == DeployLive {
					d.Status = DeploySuperseded
				} else {
					d.Status = DeployCancelled
				}
				d.TrafficPercent = 0
				d.TrafficPercentExplicit = true
				m.deployments[id] = d
			}
		}
		if !m.retirementDrainedLocked(r) {
			ready = false
			continue
		}
		now := time.Now().UTC()
		r.Phase = "retired"
		r.RetiredAt = &now
		memory.retirements[resource] = r
	}
	return ready, nil
}

func (m *MemStore) workloadReleaseExclusionsLocked(source EnvironmentGitSource) map[string]bool {
	out := map[string]bool{}
	for _, memory := range m.environmentGitOps {
		if memory.source.AccountID != source.AccountID || memory.source.ProjectID != source.ProjectID || memory.source.EnvironmentSlug != source.EnvironmentSlug {
			continue
		}
		for _, r := range memory.retirements {
			if r.Phase == "retiring" || r.Phase == "retired" || r.Phase == "planned" && !memory.source.Detached && r.Generation == memory.source.Generation {
				out[r.AppID] = true
			}
		}
	}
	return out
}
func (m *MemStore) activateWorkloadRetirementsLocked(memory *environmentGitOpsMemory, lease EnvironmentGitOpsLease) error {
	for resource, r := range memory.retirements {
		if r.Phase == "recovering" && r.RecoveryGeneration == lease.Source.Generation && r.RecoveryRevisionID == lease.Revision.ID {
			now := time.Now().UTC()
			r.Phase = "recovered"
			r.RecoveredAt = &now
			memory.retirements[resource] = r
			continue
		}
		if r.Phase != "planned" {
			continue
		}
		if r.Generation != lease.Source.Generation || r.RevisionID != lease.Revision.ID {
			return ErrConflict
		}
		if !workloadRetirementInventoryMatches(r, m.retirementInventoryLocked(memory, resource)) {
			return ErrConflict
		}
		for _, id := range r.DeploymentIDs {
			d, ok := m.deployments[id]
			if ok && d.Status == DeployLive {
				d.TrafficPercent = 0
				d.TrafficPercentExplicit = true
				m.deployments[id] = d
			}
		}
		for _, id := range r.QueueBindingIDs {
			if _, err := m.mutateQueueBindingConsumerLocked(lease.Source.AccountID, r.AppID, id, nil, nil, true, true); err != nil {
				return err
			}
		}
		if r.JobID != "" {
			job, ok := m.jobs[r.JobID]
			if !ok {
				return ErrConflict
			}
			job.Status = "paused"
			job.UpdatedAt = time.Now().UTC()
			m.jobs[r.JobID] = job
		}
		r.Phase = "retiring"
		memory.retirements[resource] = r
	}
	return nil
}
func (m *MemStore) ActivateEnvironmentGitOpsEmptyRetirement(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, err := m.gitOpsLeaseLocked(lease, time.Now())
	if err != nil {
		return false, err
	}
	desired, err := desiredEnvironmentRevision(lease.Revision)
	if err != nil {
		return false, err
	}
	if desired.Definition.Workloads == nil || len(desired.Definition.Workloads) != 0 {
		return false, ErrConflict
	}
	observed, err := compileGitOpsObservation(m.gitOpsSnapshotLocked(memory), desired)
	if err != nil {
		return false, err
	}
	plan, err := environmentGitOpsPlan(memory.source, lease.Revision, desired, observed, false)
	if err != nil {
		return false, err
	}
	if !plan.CanApply() || plan.HasDrift() || plan.Hash != reviewed.Hash {
		return false, ErrConflict
	}
	needed := false
	for _, r := range memory.retirements {
		if r.Phase == "planned" {
			needed = true
		}
	}
	if !needed {
		return true, nil
	}
	members, _, ttl, err := m.environmentGitOpsReleaseMembersLocked(memory.source, EnvironmentWorkloadGraph{}, m.activeProjectReleaseSets[releaseKey(memory.source.ProjectID, memory.source.EnvironmentSlug)])
	if err != nil {
		return false, err
	}
	rollback := m.stageWorkloadRetirementCutoverLocked(memory)
	defer func() { rollback() }()
	if err := m.activateWorkloadRetirementsLocked(memory, lease); err != nil {
		return false, err
	}
	if _, err := m.publishProjectReleaseSetLocked(memory.source.AccountID, memory.source.ProjectID, memory.source.EnvironmentSlug, ttl, members, true); err != nil {
		return false, err
	}
	rollback = func() {}
	return true, nil
}

// The memory store mirrors the PostgreSQL transaction, including failure after
// a queue projection has changed but before a release can be published.
func (m *MemStore) stageWorkloadRetirementCutoverLocked(memory *environmentGitOpsMemory) func() {
	deployments, jobs, bindings, triggers, journal := m.deployments, m.jobs, m.queueBindings, m.triggers, memory.retirements
	m.deployments = maps.Clone(deployments)
	m.jobs = maps.Clone(jobs)
	m.queueBindings = maps.Clone(bindings)
	m.triggers = maps.Clone(triggers)
	memory.retirements = maps.Clone(journal)
	return func() {
		m.deployments = deployments
		m.jobs = jobs
		m.queueBindings = bindings
		m.triggers = triggers
		memory.retirements = journal
	}
}

func (m *MemStore) retirementJobBlockedLocked(jobID string) bool {
	for _, memory := range m.environmentGitOps {
		for _, r := range memory.retirements {
			if r.JobID == jobID && jobID != "" && r.BlocksAdmission() {
				return true
			}
		}
	}
	return false
}

func (m *MemStore) workloadRetirementRetainsDeploymentLocked(id string) bool {
	deployment, exists := m.deployments[id]
	if !exists {
		return false
	}
	app := m.apps[deployment.AppID]
	if app.ID == "" || app.Status == AppDeleted {
		return false
	}
	for _, memory := range m.environmentGitOps {
		for _, r := range memory.retirements {
			if r.Phase != "cancelled" && r.Phase != "recovered" && r.AppID == deployment.AppID && normalizedDeploymentScope(memory.source.EnvironmentSlug) == normalizedDeploymentScope(deployment.Scope) && slices.Contains(r.DeploymentIDs, id) {
				return true
			}
		}
	}
	return false
}
