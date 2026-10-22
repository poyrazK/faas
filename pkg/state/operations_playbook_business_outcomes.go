package state

import (
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

func projectWorkflowPlaybookBusinessOutcomes(out *api.OperationWorkflowCase) {
	for i := range out.PlaybookProgress {
		p := &out.PlaybookProgress[i]
		projectPlaybookStepReadiness(p)
		p.BusinessOutcome = nil
		if p.Playbook.ExpectedOutcomeCode == "" {
			continue
		}
		evidence, reason := observeWorkflowBusinessOutcome(out.Instance, p.Playbook.ExpectedOutcomeCode, p.ContractVersion, p.StartStateRevision, out.EvidenceCheckedAt)
		if evidence.Status == "achieved" {
			evidence.Explanation = "The application explicitly reports the expected terminal outcome. This does not establish that playbook work caused it."
		}
		status := evidence.Status
		if status == "unknown" {
			status = "unavailable"
		}
		if _, err := api.CanonicalOperationWorkflowPlaybooks([]api.OperationWorkflowPlaybook{p.Playbook}); err != nil {
			status, reason = "unavailable", "pinned_guidance_invalid"
			evidence.Status = "unknown"
			evidence.Explanation = "The pinned playbook is invalid; its business expectation cannot be evaluated."
		}
		p.BusinessOutcome = &api.OperationWorkflowPlaybookBusinessOutcome{Status: status, Reason: reason, Evidence: evidence, DeadlineAt: p.OutcomeDeadlineAt, DeadlineReached: p.OutcomeDeadlineAt != nil && !out.EvidenceCheckedAt.Before(*p.OutcomeDeadlineAt)}
		p.BusinessOutcome.OutcomeGuidance = slices.Clone(p.Playbook.OutcomeGuidance)
		projectPlaybookOutcomeGuidance(p.BusinessOutcome)
		classifyPlaybookOutcomeAcceptance(p.BusinessOutcome, p.Playbook.OutcomeAcceptance)
	}
	out.Limitations = append(out.Limitations, "Playbook business results follow the frozen expected terminal outcome in the exact workflow contract. They neither close human checklists nor establish that playbook work caused an application outcome.")
}

func playbookBusinessOutcomeKey(out *api.OperationWorkflowPlaybookBusinessOutcome) string {
	if out == nil {
		return ""
	}
	code := ""
	if out.Status == "achieved" || out.Status == "different_outcome" {
		code = out.Evidence.ReportedOutcomeCode
	}
	return readinessDigest([]string{out.Status, out.Reason, code, out.Acceptance})
}

// Retention loss must not turn an older report into a newly observed business result.
func guardPlaybookWatchOutcome(w *api.OperationWorkflowPlaybookEvidenceWatch, current *api.OperationWorkflowPlaybookBusinessOutcome, startRevision int64) {
	w.OutcomeStateRevision = max(w.OutcomeStateRevision, startRevision)
	w.BusinessOutcome = nil
	if current == nil {
		return
	}
	observed := *current
	if observed.Status != "unavailable" && observed.Evidence.StateRevision < w.OutcomeStateRevision {
		observed.Status, observed.Reason = "unavailable", "baseline_ahead"
		observed.Evidence.Status = "unknown"
		observed.Evidence.Explanation = "Retained state precedes the watch's latest comparable report; no new business result is inferred."
	}
	if observed.Status != "unavailable" {
		w.OutcomeStateRevision = max(w.OutcomeStateRevision, observed.Evidence.StateRevision)
	}
	projectPlaybookOutcomeGuidance(&observed)
	classifyPlaybookOutcomeAcceptance(&observed, observed.AcceptancePolicy)
	w.BusinessOutcome = &observed
}

func classifyPlaybookOutcomeAcceptance(out *api.OperationWorkflowPlaybookBusinessOutcome, policy *api.OperationWorkflowPlaybookOutcomeAcceptancePolicy) {
	out.AcceptancePolicy = policy
	out.Acceptance = ""
	if policy == nil {
		return
	}
	switch out.Status {
	case "achieved":
		out.Acceptance = "expected"
	case "different_outcome":
		out.Acceptance = "unexpected"
		if slices.Contains(policy.AlternativeOutcomeCodes, out.Evidence.ReportedOutcomeCode) {
			out.Acceptance = "acceptable_alternative"
		}
	case "pending":
		out.Acceptance = "pending"
	default:
		out.Acceptance = "unavailable"
	}
}

func projectPlaybookOutcomeGuidance(out *api.OperationWorkflowPlaybookBusinessOutcome) {
	out.Guidance = nil
	if out.Status != "achieved" && out.Status != "different_outcome" {
		return
	}
	for _, guidance := range out.OutcomeGuidance {
		if guidance.OutcomeCode == out.Evidence.ReportedOutcomeCode {
			copy := guidance
			out.Guidance = &copy
			return
		}
	}
}
