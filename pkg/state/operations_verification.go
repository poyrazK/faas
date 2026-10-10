package state

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func resolutionVerificationKey(r api.OperationWorkflowBlockerResolution, operationID string) string {
	proofOperation := r.VerificationOperationID
	if proofOperation == "" {
		proofOperation = operationID
	}
	return strings.Join([]string{r.BlockerOperationID, r.BlockerReportID, fmt.Sprint(r.BlockerRevision), r.Operation, r.Code, proofOperation, r.VerificationMilestoneID, r.VerificationMilestoneName}, "\x1f")
}
func verificationMatches(v api.OperationWorkflowResolutionVerification, opts api.OperationWorkflowAttentionOptions) bool {
	r := v.Resolution
	return v.Status == "awaiting_verification" && opts.Priority == "" && (opts.Reason == "" || opts.Reason == "awaiting_verification") && (opts.Owner == "" || r.VerificationOwner == opts.Owner) && (!opts.Unassigned || r.VerificationOwner == "") && (opts.BlockerCode == "" || r.Code == opts.BlockerCode) && (opts.TargetOperation == "" || r.Operation == opts.TargetOperation)
}
func selectedVerifications(all []api.OperationWorkflowResolutionVerification, opts api.OperationWorkflowAttentionOptions) []api.OperationWorkflowResolutionVerification {
	var selected []api.OperationWorkflowResolutionVerification
	for _, v := range all {
		if verificationMatches(v, opts) {
			selected = append(selected, v)
		}
	}
	return selected
}
func verificationPreview(all []api.OperationWorkflowResolutionVerification, opts api.OperationWorkflowAttentionOptions) ([]api.OperationWorkflowResolutionVerification, int64) {
	result := append([]api.OperationWorkflowResolutionVerification(nil), all...)
	var pending int64
	for _, v := range all {
		if v.Status == "awaiting_verification" {
			pending++
		}
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := verificationMatches(result[i], opts), verificationMatches(result[j], opts)
		if left != right {
			return left
		}
		if result[i].Status != result[j].Status {
			return result[i].Status == "awaiting_verification"
		}
		return resolutionVerificationKey(result[i].Resolution, result[i].ResolutionOperationID) < resolutionVerificationKey(result[j].Resolution, result[j].ResolutionOperationID)
	})
	if len(result) > api.OperationWorkflowResolutionVerificationPreviewMax {
		result = result[:api.OperationWorkflowResolutionVerificationPreviewMax]
	}
	return result, pending
}
func addResolutionVerificationStats(stats *api.OperationWorkflowAttentionStats, all []api.OperationWorkflowResolutionVerification) {
	if len(all) > 0 {
		stats.AwaitingVerificationWorkflowCount++
		stats.AwaitingVerificationResolutionCount += int64(len(all))
	}
}
func (m *MemStore) workflowVerificationsLocked(account, tenant, app, scope string, subject api.OperationSubject, workflow, instance string, at, now time.Time) []api.OperationWorkflowResolutionVerification {
	data := m.operationMemoryLocked()
	selected := map[string]api.OperationWorkflowResolutionVerification{}
	for _, receipt := range data.workflowStateReports {
		h := receipt.History
		op, ok := data.operations[h.OperationID]
		if !ok || !operationRetained(op, now) || !sameOperationHistoryIdentity(op.AccountID, account) || !sameOperationHistoryIdentity(op.AppID, app) || !sameOperationHistoryIdentity(op.PlatformTenantID, tenant) || op.Scope != scope || op.Subject == nil || *op.Subject != subject || h.Workflow != workflow || h.InstanceID != instance || h.PublishedAt.After(at) {
			continue
		}
		for _, r := range h.BlockerResolutions {
			if r.VerificationMilestoneID == "" {
				continue
			}
			key := resolutionVerificationKey(r, h.OperationID)
			v := api.OperationWorkflowResolutionVerification{Resolution: r, ResolutionOperationID: h.OperationID, ResolutionReportID: h.ID, ResolutionRevision: h.Revision, Status: "awaiting_verification"}
			if prior, ok := selected[key]; ok && (prior.ResolutionRevision < h.Revision || prior.ResolutionRevision == h.Revision && (prior.ResolutionReportID < h.ID || prior.ResolutionReportID == h.ID && prior.ResolutionOperationID <= h.OperationID)) {
				continue
			}
			proofID := r.VerificationOperationID
			if proofID == "" {
				proofID = h.OperationID
			}
			proofOp, exists := data.operations[proofID]
			proof, found := data.milestones[proofID][r.VerificationMilestoneID]
			if exists && found && operationRetained(proofOp, now) && sameOperationHistoryIdentity(proofOp.AccountID, account) && sameOperationHistoryIdentity(proofOp.AppID, app) && sameOperationHistoryIdentity(proofOp.PlatformTenantID, tenant) && proofOp.Scope == scope && proofOp.Subject != nil && *proofOp.Subject == subject && proof.Milestone.Name == r.VerificationMilestoneName && !proof.Milestone.CreatedAt.After(at) {
				for _, step := range proof.Milestone.WorkflowSteps {
					if step.Workflow == workflow && step.InstanceID == instance && effectiveWorkflowContractVersion(step.Version) == h.ContractVersion {
						v.Status = "verified"
						created := proof.Milestone.CreatedAt
						v.VerifiedAt = &created
						break
					}
				}
			}
			selected[key] = v
		}
	}
	result := make([]api.OperationWorkflowResolutionVerification, 0, len(selected))
	for _, v := range selected {
		result = append(result, v)
	}
	return result
}
func (m *MemStore) projectResolutionVerificationsLocked(page *api.OperationMilestonesResponse, account, tenant string, opts api.OperationMilestoneListOptions, now time.Time) {
	if page.WorkflowInstance == nil || page.WorkflowInstance.State == nil || opts.SubjectType == "" {
		return
	}
	if tenant == "" {
		tenant = page.WorkflowInstance.State.PlatformTenantID
	}
	all := m.workflowVerificationsLocked(account, tenant, opts.AppID, opts.Scope, api.OperationSubject{Type: opts.SubjectType, ID: opts.SubjectID}, opts.Workflow, opts.WorkflowInstanceID, now, now)
	page.WorkflowInstance.ResolutionVerifications, page.WorkflowInstance.AwaitingVerificationCount = verificationPreview(all, api.OperationWorkflowAttentionOptions{})
	page.WorkflowInstance.ResolutionVerificationCount = int64(len(all))
	attachResolutionHistoryVerifications(page.WorkflowStateHistory, all)
	attachResolutionHistoryVerifications(page.WorkflowInstance.Transitions, all)
	noteResolutionVerificationAttention(page.WorkflowInstance)
	if !opts.ReadinessOnly {
		observations := m.workflowBottleneckHistoryLocked(account, tenant, opts.AppID, opts.Scope, api.OperationSubject{Type: opts.SubjectType, ID: opts.SubjectID}, opts.Workflow, opts.WorkflowInstanceID, now)
		if op, ok := m.operationMemoryLocked().operations[page.WorkflowInstance.State.OperationID]; ok {
			page.WorkflowInstance.State.SLA = workflowStateSLA(*page.WorkflowInstance.State, observations, workflowStateSLABudget(m.operationMemoryLocked().definitions[op.DefinitionID].Spec, *page.WorkflowInstance.State), workflowStateSLAWarning(m.operationMemoryLocked().definitions[op.DefinitionID].Spec, *page.WorkflowInstance.State), now)
			noteWorkflowSLAAttention(page.WorkflowInstance)
		}
		page.WorkflowInstance.Bottlenecks = workflowBottlenecks(observations, page.WorkflowInstance.State, all, now)
	}
}

func attachResolutionHistoryVerifications(entries []api.OperationWorkflowStateHistoryEntry, all []api.OperationWorkflowResolutionVerification) {
	byKey := map[string]api.OperationWorkflowResolutionVerification{}
	for _, v := range all {
		byKey[resolutionVerificationKey(v.Resolution, v.ResolutionOperationID)] = v
	}
	for i := range entries {
		entries[i].ResolutionVerifications = nil
		for _, r := range entries[i].BlockerResolutions {
			if v, ok := byKey[resolutionVerificationKey(r, entries[i].OperationID)]; ok {
				entries[i].ResolutionVerifications = append(entries[i].ResolutionVerifications, v)
			}
		}
	}
}

func noteResolutionVerificationAttention(instance *api.OperationWorkflowInstanceSnapshot) {
	if instance.Decision != nil && instance.AwaitingVerificationCount > 0 {
		instance.Decision.NeedsAttention = true
		instance.Decision.Explanation += " Retained milestone evidence is still pending for reported blocker resolutions."
	}
}
