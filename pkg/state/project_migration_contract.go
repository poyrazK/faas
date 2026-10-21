package state

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type ProjectMigrationContractEvidence struct {
	StartedAt          time.Time
	RuntimeFingerprint string
	Evidence           []api.ProjectReleaseProbeEvidence
}
type ProjectMigrationContractStore interface {
	StageProjectMigrationContract(context.Context, string, string, string, string) error
	AdmitProjectMigrationContract(context.Context, string, string, api.ProjectReleaseControlRequest, ProjectMigrationContractEvidence) (*api.ProjectMigrationContract, error)
	ProjectMigrationContractStatus(context.Context, string, string) (*api.ProjectMigrationContract, error)
}
type ProjectMigrationFenceReader interface {
	ProjectMigrationDeploymentRetired(context.Context, string) (bool, error)
}
type projectMigrationContract struct {
	Recovery                                                                *migrationRecoveryRecord
	ExpansionTaskID, RuntimeFingerprint, PreparedLeaseToken                 string
	ProjectID, AppID, CommandHash, TaskID, RequestID, ReleaseID, ConfigHash string
	Command                                                                 []string
	Generation                                                              uint64
	ConfigVersion                                                           int64
	RetiredIDs                                                              []string
}

func contractPlanReason(safe *api.ProjectSafeRelease) string {
	if safe == nil || safe.ReleaseCommand == nil || len(safe.ReleaseCommand.ContractCommandHash) != 64 || !safe.ReleaseCommand.VerifyBaseline || safe.Configuration == nil || safe.Verification == nil || !safe.Verification.Progressive || safe.Verification.Stabilization == nil || !safe.Verification.Stabilization.Enabled {
		return "contract_not_planned"
	}
	if safe.Status != "succeeded" || safe.ReleaseCommand.Status != "succeeded" {
		return "rollout_not_succeeded"
	}
	v := safe.Verification.Stabilization
	if v.Status != "completed" {
		return "stabilization_pending"
	}
	if v.Health != "healthy" {
		return "stabilization_health_unknown"
	}
	if v.Deadline == nil || time.Now().Before(*v.Deadline) {
		return "stabilization_pending"
	}
	return ""
}
func validContractRequest(req api.ProjectReleaseControlRequest) bool {
	return api.ValidDeploymentAttemptID(req.RequestID) && req.ExpectedGeneration > 0 && req.ExpectedGeneration <= math.MaxInt64
}
func validContractEvidence(safe *api.ProjectSafeRelease, e ProjectMigrationContractEvidence) bool {
	now := time.Now().UTC()
	return !e.StartedAt.IsZero() && !e.StartedAt.After(now) && now.Sub(e.StartedAt) <= api.ProjectMigrationBaselineEvidenceMaxAge && !e.StartedAt.Before(*safe.Verification.Stabilization.Deadline) && migrationBaselineVerdict(safe.Verification.Probes, e.Evidence) == "healthy"
}
func contractTaskResult(c projectMigrationContract, task AppTask, found, allowed bool) (result *api.ProjectMigrationContract) {
	defer func() { result = annotateMigrationRecovery(c, result) }()
	result = &api.ProjectMigrationContract{Status: "unknown", TaskID: c.TaskID, RequestID: c.RequestID, ExpectedGeneration: c.Generation, RetiredDeployments: len(c.RetiredIDs)}
	if !found {
		result.Reason = "task_receipt_missing"
		return result
	}
	if task.ExitCode != nil {
		exit := *task.ExitCode
		result.ExitCode = &exit
	}
	if task.FailureCode != nil {
		result.FailureCode = *task.FailureCode
	}
	result.Status = string(task.Status)
	if task.Status == AppTaskSucceeded && (task.ExitCode == nil || *task.ExitCode != 0) {
		result.Status = "uncertain"
	}
	if task.Status.Terminal() && task.StartedAt != nil && task.ExitCode == nil {
		result.Status = "uncertain"
	}
	if !allowed && (task.Status == AppTaskQueued || task.Status == AppTaskRestoring) {
		result.Status = "blocked"
		result.Reason = "production_or_configuration_changed"
	}
	return result
}
func (m *MemStore) migrationDeploymentRetiredLocked(id string) bool {
	for _, c := range m.migrationContracts {
		for _, retired := range c.RetiredIDs {
			if id == retired {
				return true
			}
		}
	}
	return false
}
func (m *MemStore) ProjectMigrationDeploymentRetired(_ context.Context, id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.migrationDeploymentRetiredLocked(id), nil
}
func (m *MemStore) migrationPublicationAllowedLocked(project string) bool {
	for _, c := range m.migrationContracts {
		if c.ProjectID == project && c.TaskID != "" {
			task, found := m.appTasks[c.TaskID]
			if !found || !task.Status.Terminal() {
				return false
			}
		}
	}
	return true
}
func (m *MemStore) migrationTaskAllowedLocked(task AppTask) bool {
	if !m.promotionMigrationClaimAllowedLocked(task) {
		return false
	}
	if m.migrationDeploymentRetiredLocked(task.DeploymentID) {
		return false
	}
	project := m.apps[task.AppID].ProjectID
	for _, c := range m.migrationContracts {
		if c.ProjectID != project || c.TaskID == "" {
			continue
		}
		owner, found := m.appTasks[c.TaskID]
		if c.TaskID != task.ID {
			if !found || !owner.Status.Terminal() {
				return false
			}
			continue
		}
		if m.activeProjectReleaseSets[releaseKey(project, "production")] != c.ReleaseID {
			return false
		}
		head := m.projectEnvironmentConfigLatestLocked(project, "production")
		if head.Version != c.ConfigVersion || head.ConfigHash != c.ConfigHash {
			return false
		}
	}
	return true
}
func (m *MemStore) contractBlockerLocked(project, attempt string, safe *api.ProjectSafeRelease) string {
	return m.contractBlockerExceptTaskLocked(project, attempt, safe, "")
}

func (m *MemStore) contractBlockerExceptTaskLocked(project, attempt string, safe *api.ProjectSafeRelease, ignoredTask string) string {
	if m.pendingPromotionMigrationLocked(project) {
		return "pending_submission"
	}
	if m.activeProjectReleaseSets[releaseKey(project, "production")] != safe.ReleaseID {
		return "production_changed"
	}
	head := m.projectEnvironmentConfigLatestLocked(project, "production")
	if head.Version != safe.Configuration.TargetVersion || head.ConfigHash != safe.Configuration.TargetHash {
		return "configuration_changed"
	}
	now := time.Now()
	for _, c := range m.projectCanaries {
		if c.ProjectID == project && c.Policy.Status == "running" {
			return "rollout_pending"
		}
	}
	for _, r := range m.projectReleaseSets {
		if r.ProjectID == project && r.EnvironmentSlug == "production" && r.ID != safe.ReleaseID && m.releaseUsableLocked(r) {
			return "retained_releases"
		}
	}
	current := map[string]bool{}
	for _, member := range m.projectReleaseSets[safe.ReleaseID].Members {
		current[member.DeploymentID] = true
	}
	for id, dep := range m.deployments {
		if m.apps[dep.AppID].ProjectID != project || normalizedDeploymentScope(dep.Scope) != "production" || current[id] {
			continue
		}
		if m.revisionPins[id].After(now) || m.operationCodePins[id].After(now) || m.workflowCodePins[id].After(now) || m.durableWorkRetainsDeploymentLocked(id) {
			return "retained_code_pins"
		}
		if dep.TrafficPercent > 0 {
			return "old_revision_traffic"
		}
		for _, ins := range m.instances {
			if ins.DeploymentID == id && ins.State != "stopped" && ins.State != "failed" && ins.State != "parked" {
				return "old_instances"
			}
		}
		if dep.Status != DeployLive && dep.Status != DeployFailed && dep.Status != DeploySuperseded && string(dep.Status) != "cancelled" {
			return "pending_source"
		}
	}
	for _, inv := range m.invocations {
		if m.apps[inv.AppID].ProjectID == project && normalizedDeploymentScope(inv.DeploymentScope) == "production" && (inv.State == InvocationPending || inv.State == InvocationDispatching) {
			return "outstanding_jobs"
		}
	}
	for _, task := range m.appTasks {
		if m.apps[task.AppID].ProjectID == project && !m.migrationTaskExecutionResolvedLocked(task.ID) && task.ID != ignoredTask && task.StartedAt != nil && task.ExitCode == nil && !current[task.DeploymentID] {
			return "old_task_outcome_uncertain"
		}
	}
	for _, task := range m.appTasks {
		if m.apps[task.AppID].ProjectID == project && !task.Status.Terminal() {
			return "outstanding_tasks"
		}
	}
	for _, entry := range m.projectSubmissions {
		var r api.ProjectSubmissionReceipt
		if json.Unmarshal(entry.Payload, &r) == nil && r.Apply.ProjectID == project && r.AttemptID != attempt && (entry.Status == "processing" || r.Apply.SafeRelease != nil && pendingSafeProjectRelease(r.Apply.SafeRelease.Status)) {
			return "pending_submission"
		}
	}
	return ""
}
func (m *MemStore) StageProjectMigrationContract(_ context.Context, account, attempt, project, command string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := account + "\x00" + attempt
	entry, found := m.projectSubmissions[key]
	if !found {
		return ErrNotFound
	}
	var r api.ProjectSubmissionReceipt
	if json.Unmarshal(entry.Payload, &r) != nil || entry.Status != "processing" || r.Apply.ProjectID != project || r.Apply.SafeRelease == nil || r.Apply.SafeRelease.ReleaseCommand == nil {
		return ErrConflict
	}
	policy := r.Apply.SafeRelease.ReleaseCommand
	hash := api.ProjectReleaseCommandHash([]string{command}, true)
	if policy.ContractCommandHash != hash || !policy.VerifyBaseline {
		return ErrConflict
	}
	if _, problem := (api.CreateAppTaskRequest{Command: []string{command}, CommandShell: true}).Resolve(); problem != nil {
		return ErrInvalidArgument
	}
	if m.migrationContracts == nil {
		m.migrationContracts = map[string]projectMigrationContract{}
	}
	if prior, ok := m.migrationContracts[key]; ok {
		if prior.CommandHash != hash || prior.TaskID != "" {
			return ErrConflict
		}
		return nil
	}
	m.migrationContracts[key] = projectMigrationContract{ProjectID: project, AppID: policy.AppID, Command: []string{command}, CommandHash: hash}
	return nil
}
func (m *MemStore) ProjectMigrationContractStatus(_ context.Context, account, attempt string) (*api.ProjectMigrationContract, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := account + "\x00" + attempt
	c, found := m.migrationContracts[key]
	if !found {
		return nil, ErrNotFound
	}
	if c.TaskID != "" {
		task, ok := m.appTasks[c.TaskID]
		return contractTaskResult(c, task, ok, m.migrationTaskAllowedLocked(task)), nil
	}
	var r api.ProjectSubmissionReceipt
	if err := json.Unmarshal(m.projectSubmissions[key].Payload, &r); err != nil {
		return nil, err
	}
	reason := contractPlanReason(r.Apply.SafeRelease)
	if reason == "" && c.RuntimeFingerprint == "" {
		reason = "expansion_runtime_unattested"
	}
	if reason == "" {
		deployment := ""
		for _, b := range r.Apply.Builds {
			if b.AppID == c.AppID {
				deployment = b.DeploymentID
			}
		}
		snapshot, readErr := m.runtimeAppValuesLocked(account, c.AppID, deployment)
		if readErr != nil {
			reason = "runtime_inputs_changed"
		} else {
			fence, fenceErr := NewRuntimeAppConfigFence(snapshot)
			if fenceErr != nil || fence.Fingerprint != c.RuntimeFingerprint {
				reason = "runtime_inputs_changed"
			}
		}
	}
	if reason == "" {
		reason = m.contractBlockerLocked(c.ProjectID, attempt, r.Apply.SafeRelease)
	}
	if reason != "" {
		return &api.ProjectMigrationContract{Status: "blocked", Reason: reason}, nil
	}
	return &api.ProjectMigrationContract{Status: "ready", Reason: "fresh_health_check_required"}, nil
}
func (m *MemStore) AdmitProjectMigrationContract(_ context.Context, account, attempt string, req api.ProjectReleaseControlRequest, evidence ProjectMigrationContractEvidence) (*api.ProjectMigrationContract, error) {
	if !validContractRequest(req) {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := account + "\x00" + attempt
	c, found := m.migrationContracts[key]
	if !found {
		return nil, ErrNotFound
	}
	if c.TaskID != "" {
		if c.RequestID != req.RequestID || c.Generation != req.ExpectedGeneration {
			return nil, ErrConflict
		}
		task, ok := m.appTasks[c.TaskID]
		return contractTaskResult(c, task, ok, m.migrationTaskAllowedLocked(task)), nil
	}
	var r api.ProjectSubmissionReceipt
	if err := json.Unmarshal(m.projectSubmissions[key].Payload, &r); err != nil {
		return nil, err
	}
	safe := r.Apply.SafeRelease
	if contractPlanReason(safe) != "" || safe.Verification.Generation != req.ExpectedGeneration || !validContractEvidence(safe, evidence) || c.CommandHash != safe.ReleaseCommand.ContractCommandHash || m.contractBlockerLocked(c.ProjectID, attempt, safe) != "" {
		return nil, ErrConflict
	}
	if _, err := m.guardReleaseConfigurationLocked(account, attempt, c.ProjectID, safe, true); err != nil {
		return nil, err
	}
	current := map[string]bool{}
	deployment := ""
	for _, member := range m.projectReleaseSets[safe.ReleaseID].Members {
		current[member.DeploymentID] = true
		if member.AppID == c.AppID {
			deployment = member.DeploymentID
		}
	}
	for id, dep := range m.deployments {
		if m.apps[dep.AppID].ProjectID == c.ProjectID && normalizedDeploymentScope(dep.Scope) == "production" && !current[id] {
			c.RetiredIDs = append(c.RetiredIDs, id)
		}
	}
	if m.deployments[deployment].Status != DeployLive {
		return nil, ErrConflict
	}
	snapshot, err := m.runtimeAppValuesLocked(account, c.AppID, deployment)
	if err != nil {
		return nil, err
	}
	fence, err := NewRuntimeAppConfigFence(snapshot)
	if err != nil || c.RuntimeFingerprint == "" || fence.Fingerprint != c.RuntimeFingerprint || evidence.RuntimeFingerprint != c.RuntimeFingerprint || c.ExpansionTaskID != safe.ReleaseCommand.TaskID {
		return nil, ErrConflict
	}
	startDeadline := time.Now().UTC().Add(api.ProjectMigrationContractStartTimeout)
	params, err := resolveCreateAppTask(CreateAppTaskParams{AccountID: account, AppID: c.AppID, DeploymentID: deployment, Kind: AppTaskKindManual, Command: c.Command, CommandShell: true, StartDeadlineAt: &startDeadline})
	if err != nil {
		return nil, err
	}
	task, err := m.createAppTaskLocked(params)
	if err != nil {
		return nil, err
	}
	c.TaskID, c.RequestID, c.Generation, c.ReleaseID, c.ConfigVersion, c.ConfigHash = task.ID, req.RequestID, req.ExpectedGeneration, safe.ReleaseID, safe.Configuration.TargetVersion, safe.Configuration.TargetHash
	m.migrationContracts[key] = c
	return contractTaskResult(c, task, true, true), nil
}

func hydrateMigrationContract(ctx context.Context, store any, account string, r *api.ProjectSubmissionReceipt) error {
	if r.Apply.SafeRelease == nil || r.Apply.SafeRelease.ReleaseCommand == nil || r.Apply.SafeRelease.ReleaseCommand.ContractCommandHash == "" {
		return nil
	}
	reader, ok := store.(ProjectMigrationContractStore)
	if !ok {
		return ErrConflict
	}
	status, err := reader.ProjectMigrationContractStatus(ctx, account, r.AttemptID)
	if errors.Is(err, ErrNotFound) {
		r.Apply.SafeRelease.ReleaseCommand.Contract = &api.ProjectMigrationContract{Status: "blocked", Reason: "contract_not_staged"}
		return nil
	}
	if err == nil {
		r.Apply.SafeRelease.ReleaseCommand.Contract = status
	}
	return err
}
