package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type ProjectMigrationRecoveryEvidence struct {
	StartedAt           time.Time
	RuntimeFingerprints map[string]string
	Probes              []api.ProjectReleaseProbeEvidence
}
type ProjectMigrationRecoveryStore interface {
	ResolveProjectMigrationContract(context.Context, string, string, api.ProjectMigrationRecoveryRequest, ProjectMigrationRecoveryEvidence) (*api.ProjectMigrationRecovery, error)
}
type migrationRecoveryRecord struct {
	RequestHash string
	Receipt     api.ProjectMigrationRecovery
}

func recoveryTaskEligible(c projectMigrationContract, t AppTask, found bool, req api.ProjectMigrationRecoveryRequest) bool {
	if !found || c.TaskID != req.ExpectedTaskID || c.Generation != req.ExpectedGeneration || !t.Status.Terminal() {
		return false
	}
	status := contractTaskResult(c, t, true, true).Status
	return status == "uncertain" || status == "failed" || status == "cancelled" || status == "timed_out"
}
func recoveryReplacementEligible(r api.ProjectSubmissionReceipt, project, oldRelease string) bool {
	safe := r.Apply.SafeRelease
	if r.Status != "complete" || r.Apply.ProjectID != project || safe == nil || safe.Status != "succeeded" || safe.ReleaseID == "" || safe.ReleaseID == oldRelease || safe.Configuration == nil || safe.Verification == nil || len(safe.Verification.Probes) == 0 || safe.Verification.HealthyRounds < api.ProjectReleaseVerificationRounds || safe.Verification.CheckedAt == nil || api.ProjectReleaseMonitoring(safe) {
		return false
	}
	if v := safe.Verification.Stabilization; v != nil && v.Enabled && (v.Status != "completed" || v.Health != "healthy") {
		return false
	}
	return true
}
func recoveryEvidenceEligible(r api.ProjectSubmissionReceipt, e ProjectMigrationRecoveryEvidence) bool {
	return !e.StartedAt.IsZero() && !e.StartedAt.After(time.Now()) && time.Since(e.StartedAt) <= api.ProjectMigrationBaselineEvidenceMaxAge && migrationBaselineVerdict(r.Apply.SafeRelease.Verification.Probes, e.Probes) == "healthy"
}
func recoveryReceipt(account string, req api.ProjectMigrationRecoveryRequest, release string) api.ProjectMigrationRecovery {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return api.ProjectMigrationRecovery{Status: "resolved", RequestID: req.RequestID, TaskID: req.ExpectedTaskID, ExpectedGeneration: req.ExpectedGeneration, ReplacementAttemptID: req.ReplacementAttemptID, ReplacementReleaseID: release, EvidenceHash: req.EvidenceHash, OperatorAccountID: account, ResolvedAt: &now}
}
func annotateMigrationRecovery(c projectMigrationContract, result *api.ProjectMigrationContract) *api.ProjectMigrationContract {
	if c.Recovery != nil {
		copy := c.Recovery.Receipt
		if copy.ResolvedAt != nil {
			at := *copy.ResolvedAt
			copy.ResolvedAt = &at
		}
		result.Recovery = &copy
		return result
	}
	recovery := &api.ProjectMigrationRecovery{Status: "blocked", Reason: "cleanup_not_terminal", TaskID: c.TaskID, ExpectedGeneration: c.Generation}
	switch result.Status {
	case "failed", "uncertain", "cancelled", "timed_out":
		recovery.Status = "needs_operator"
		recovery.Reason = "inspect_database_and_execution_then_verify_replacement"
	case "succeeded":
		recovery.Status = "not_required"
		recovery.Reason = ""
	case "unknown":
		recovery.Reason = "task_receipt_missing"
	}
	result.Recovery = recovery
	return result
}
func (m *MemStore) ResolveProjectMigrationContract(_ context.Context, account, attempt string, req api.ProjectMigrationRecoveryRequest, e ProjectMigrationRecoveryEvidence) (*api.ProjectMigrationRecovery, error) {
	if !req.Valid() || req.ReplacementAttemptID == attempt {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := account + "\x00" + attempt
	c, ok := m.migrationContracts[key]
	if !ok {
		return nil, ErrNotFound
	}
	if c.Recovery != nil {
		if c.Recovery.RequestHash != req.Fingerprint() {
			return nil, ErrConflict
		}
		return copyMigrationRecovery(c.Recovery.Receipt), nil
	}
	task, found := m.appTasks[c.TaskID]
	if !recoveryTaskEligible(c, task, found, req) {
		return nil, ErrConflict
	}
	entry, ok := m.projectSubmissions[account+"\x00"+req.ReplacementAttemptID]
	if !ok {
		return nil, ErrNotFound
	}
	var r api.ProjectSubmissionReceipt
	if json.Unmarshal(entry.Payload, &r) != nil || !recoveryReplacementEligible(r, c.ProjectID, c.ReleaseID) || !recoveryEvidenceEligible(r, e) {
		return nil, ErrConflict
	}
	safe := r.Apply.SafeRelease
	if reason := m.contractBlockerExceptTaskLocked(c.ProjectID, req.ReplacementAttemptID, safe, c.TaskID); reason != "" {
		return nil, &MigrationRecoveryBlockedError{Reason: reason}
	}
	graph := m.projectReleaseSets[safe.ReleaseID]
	if len(graph.Members) != len(r.Apply.Builds) || len(e.RuntimeFingerprints) != len(graph.Members) {
		return nil, ErrConflict
	}
	for _, member := range graph.Members {
		matches := 0
		for _, build := range r.Apply.Builds {
			if build.AppID == member.AppID && build.DeploymentID == member.DeploymentID {
				matches++
			}
		}
		snapshot, err := m.runtimeAppValuesLocked(account, member.AppID, member.DeploymentID)
		if err != nil || matches != 1 {
			return nil, ErrConflict
		}
		fence, err := NewRuntimeAppConfigFence(snapshot)
		if err != nil || fence.Fingerprint != e.RuntimeFingerprints[member.AppID] {
			return nil, ErrConflict
		}
	}
	current := map[string]bool{}
	for _, member := range graph.Members {
		current[member.DeploymentID] = true
	}
	seen := map[string]bool{}
	for _, id := range c.RetiredIDs {
		seen[id] = true
	}
	for id, dep := range m.deployments {
		if m.apps[dep.AppID].ProjectID == c.ProjectID && normalizedDeploymentScope(dep.Scope) == "production" && !current[id] && !seen[id] {
			c.RetiredIDs = append(c.RetiredIDs, id)
		}
	}
	receipt := recoveryReceipt(account, req, safe.ReleaseID)
	c.Recovery = &migrationRecoveryRecord{RequestHash: req.Fingerprint(), Receipt: receipt}
	m.migrationContracts[key] = c
	return copyMigrationRecovery(receipt), nil
}

func copyMigrationRecovery(r api.ProjectMigrationRecovery) *api.ProjectMigrationRecovery {
	if r.ResolvedAt != nil {
		at := *r.ResolvedAt
		r.ResolvedAt = &at
	}
	return &r
}
func (m *MemStore) migrationTaskExecutionResolvedLocked(task string) bool {
	if m.promotionMigrationTaskResolvedLocked(task) {
		return true
	}
	for _, contract := range m.migrationContracts {
		if contract.TaskID == task && contract.Recovery != nil {
			return true
		}
	}
	return false
}

type MigrationRecoveryBlockedError struct{ Reason string }

func (e *MigrationRecoveryBlockedError) Error() string {
	return "migration recovery blocked: " + e.Reason
}
func (e *MigrationRecoveryBlockedError) Unwrap() error { return ErrConflict }
