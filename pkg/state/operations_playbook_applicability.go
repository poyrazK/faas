package state

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func projectWorkflowPlaybookApplicability(out *api.OperationWorkflowCase, source workflowCasePlaybookContext) {
	checkedAt := time.Now().UTC().Truncate(time.Microsecond)
	for i := range out.PlaybookProgress {
		p := &out.PlaybookProgress[i]
		result := workflowPlaybookApplicability(out, source, *p, checkedAt)
		p.Applicability = &result
	}
	out.Limitations = append(out.Limitations, "Playbook applicability is a live advisory observation of current retained evidence, not a durable review or proof that attached evidence remains available. Absent triggers and terminal reports do not establish business success or close checklists.")
}
func workflowPlaybookApplicability(out *api.OperationWorkflowCase, source workflowCasePlaybookContext, p api.OperationWorkflowPlaybookProgress, at time.Time) api.OperationWorkflowPlaybookApplicability {
	a := api.OperationWorkflowPlaybookApplicability{CheckedAt: at, Status: "evidence_unavailable", Summary: "Fresh comparable application evidence is unavailable.", SuggestedAction: "Refresh application evidence before deciding whether to continue this checklist."}
	for _, item := range p.Items {
		if item.Status != "completed" {
			a.RemainingItemCount++
		}
	}
	instance := out.Instance
	if instance == nil || instance.State == nil {
		return a
	}
	current := instance.State
	a.CurrentContractVersion = instance.ContractVersion
	a.StateRevision = current.Revision
	a.ReportingOperationID = current.OperationID
	if source.DefinitionID != "" {
		a.DefinitionID = source.DefinitionID
		a.DefinitionChanged = !sameOperationHistoryIdentity(source.DefinitionID, p.DefinitionID)
	}
	if current.Stale || operationWorkflowStateIsStale(at, *current) || instance.ContractVersion != effectiveWorkflowContractVersion(current.ContractVersion) {
		return a
	}
	if instance.ContractVersion != p.ContractVersion {
		a.Status = "contract_changed"
		a.Summary = "The current workflow contract version differs from this pinned checklist."
		a.SuggestedAction = "Review the changed workflow contract and start matched guidance separately if needed; keep this checklist's history."
		return a
	}
	if current.Terminal {
		a.Status = "terminal_reported"
		a.Summary = "The application reports a terminal workflow state; this does not establish a successful outcome."
		a.SuggestedAction = "Review the application-reported outcome and decide what coordination remains necessary."
		return a
	}
	if out.GuidanceStatus == "evidence_unavailable" {
		return a
	}
	if out.GuidanceStatus == "definition_unavailable" || source.DefinitionID == "" {
		a.Status = "definition_unavailable"
		a.Summary = "The exact current reporting definition is unavailable or invalid."
		a.SuggestedAction = "Restore access to the reporting definition before comparing pinned guidance."
		return a
	}
	a.DefinitionID = source.DefinitionID
	a.DefinitionChanged = !sameOperationHistoryIdentity(source.DefinitionID, p.DefinitionID)
	pinned, err := api.CanonicalOperationWorkflowPlaybooks([]api.OperationWorkflowPlaybook{p.Playbook})
	if err != nil {
		a.Status = "definition_unavailable"
		a.Summary = "The pinned playbook cannot be validated."
		a.SuggestedAction = "Review the retained checklist's original guidance before continuing."
		return a
	}
	sameRule := false
	for _, rule := range source.Rules {
		if rule.ID == p.Playbook.ID && readinessDigest(rule) == readinessDigest(pinned[0]) {
			sameRule = true
			break
		}
	}
	if !sameRule {
		a.Status = "guidance_changed"
		a.Summary = "The pinned playbook was changed or removed in the current reporting definition."
		a.SuggestedAction = "Compare the current application guidance with this frozen checklist before continuing; its evidence and reviews remain pinned."
		return a
	}
	a.TriggerMatched = workflowCasePlaybookMatches(instance, pinned[0])
	a.EvidenceComplete = workflowPlaybookTriggerEvidenceComplete(instance, pinned[0])
	if a.TriggerMatched {
		a.Status = "applicable"
		a.Summary = "The pinned playbook still matches observed current application evidence."
		a.SuggestedAction = "Review remaining checklist items and obtain any required application verification; checklist completion alone is not business success."
		return a
	}
	if !a.EvidenceComplete {
		a.Status = "evidence_incomplete"
		a.Summary = "The evaluated evidence is incomplete, so trigger absence cannot be established."
		a.SuggestedAction = "Refresh or narrow the application evidence before deciding whether this work is still relevant."
		return a
	}
	a.Status = "trigger_absent"
	a.Summary = "The pinned trigger is absent from the complete current retained evaluation; this is not proof of business resolution."
	a.SuggestedAction = "Review whether this checklist remains necessary. No item, review or business outcome has been changed."
	return a
}
func workflowPlaybookTriggerEvidenceComplete(instance *api.OperationWorkflowInstanceSnapshot, rule api.OperationWorkflowPlaybook) bool {
	switch rule.MatchKind {
	case "blocker":
		return true
	case "pending_verification":
		return int64(len(instance.ReadinessVerifications)) == instance.ResolutionVerificationCount
	default:
		if instance.Readiness == nil || instance.Readiness.HasMore || len(instance.Readiness.Items) != instance.Readiness.TransitionCount {
			return false
		}
		for _, edge := range instance.Readiness.Items {
			if edge.ContractVersion != instance.ContractVersion || !edge.Declared {
				return false
			}
		}
		return true
	}
}
