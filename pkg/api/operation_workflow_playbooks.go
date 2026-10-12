package api

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

var workflowPlaybookMatchCode = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func CanonicalOperationWorkflowPlaybooks(input []OperationWorkflowPlaybook) ([]OperationWorkflowPlaybook, error) {
	if len(input) > OperationWorkflowPlaybooksMax {
		return nil, fmt.Errorf("workflow playbooks exceed their limit")
	}
	if len(input) == 0 {
		return nil, nil
	}
	out := append([]OperationWorkflowPlaybook(nil), input...)
	text := func(v string, max int, required bool) bool {
		return len(v) <= max && utf8.ValidString(v) && strings.TrimSpace(v) == v && (!required || v != "") && strings.IndexFunc(v, unicode.IsControl) < 0
	}
	link := func(v string) bool {
		if v == "" {
			return true
		}
		if !text(v, OperationWorkflowPlaybookURLMaxBytes, true) {
			return false
		}
		u, err := url.Parse(v)
		return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Opaque == ""
	}
	seen := map[string]bool{}
	for i := range out {
		r := &out[i]
		if ValidateOperationWorkflowName(r.ID) != nil || seen[r.ID] || r.Version < 1 || r.Version > OperationWorkflowContractVersionMax || !slices.Contains([]string{"blocker", "missing_milestone", "missing_dependency", "pending_verification", "requirement"}, r.MatchKind) || !workflowPlaybookMatchCode.MatchString(r.MatchCode) || !text(r.Summary, OperationWorkflowPlaybookSummaryMaxBytes, true) || !text(r.Owner, OperationWorkflowReadinessFollowUpOwnerMaxBytes, false) || len(r.RequiredEvidence) > OperationWorkflowPlaybookEvidenceMax || !link(r.RunbookURL) || !link(r.ActionURL) {
			return nil, fmt.Errorf("workflow playbook is invalid or duplicated")
		}
		if r.ExpectedOutcomeCode != "" && ValidateOperationWorkflowStateName(r.ExpectedOutcomeCode) != nil {
			return nil, fmt.Errorf("workflow playbook expected outcome code is invalid")
		}
		if r.OutcomeAcceptance != nil {
			if r.ExpectedOutcomeCode == "" || len(r.OutcomeAcceptance.AlternativeOutcomeCodes) > OperationWorkflowPlaybookEvidenceMax {
				return nil, fmt.Errorf("workflow playbook outcome acceptance policy is invalid")
			}
			policy := *r.OutcomeAcceptance
			policy.AlternativeOutcomeCodes = append([]string{}, policy.AlternativeOutcomeCodes...)
			slices.Sort(policy.AlternativeOutcomeCodes)
			for j, code := range policy.AlternativeOutcomeCodes {
				if ValidateOperationWorkflowStateName(code) != nil || code == r.ExpectedOutcomeCode || (j > 0 && policy.AlternativeOutcomeCodes[j-1] == code) {
					return nil, fmt.Errorf("workflow playbook alternative outcome is invalid or duplicated")
				}
			}
			r.OutcomeAcceptance = &policy
		}
		if len(r.OutcomeGuidance) > OperationWorkflowPlaybookEvidenceMax+1 || (len(r.OutcomeGuidance) > 0 && r.ExpectedOutcomeCode == "") {
			return nil, fmt.Errorf("workflow playbook outcome guidance is invalid")
		}
		r.OutcomeGuidance = slices.Clone(r.OutcomeGuidance)
		slices.SortFunc(r.OutcomeGuidance, func(a, b OperationWorkflowPlaybookOutcomeGuidance) int {
			return strings.Compare(a.OutcomeCode, b.OutcomeCode)
		})
		for j, guidance := range r.OutcomeGuidance {
			if len(guidance.FollowUpSteps) > OperationWorkflowPlaybookEvidenceMax {
				return nil, fmt.Errorf("workflow playbook outcome follow-up steps exceed their limit")
			}
			r.OutcomeGuidance[j].FollowUpSteps = slices.Clone(guidance.FollowUpSteps)
			labels := map[string]bool{}
			for _, label := range guidance.FollowUpSteps {
				if !text(label, OperationWorkflowPlaybookEvidenceMaxBytes, true) || labels[label] {
					return nil, fmt.Errorf("workflow playbook outcome follow-up step is invalid or duplicated")
				}
				labels[label] = true
			}

			allowed := guidance.OutcomeCode == r.ExpectedOutcomeCode || (r.OutcomeAcceptance != nil && slices.Contains(r.OutcomeAcceptance.AlternativeOutcomeCodes, guidance.OutcomeCode))
			if !allowed || (j > 0 && r.OutcomeGuidance[j-1].OutcomeCode == guidance.OutcomeCode) || !text(guidance.Description, OperationWorkflowReadinessFollowUpNoteMaxBytes, true) || !text(guidance.SuggestedFollowUp, OperationWorkflowReadinessFollowUpNoteMaxBytes, false) {
				return nil, fmt.Errorf("workflow playbook outcome guidance code or text is invalid or duplicated")
			}
		}
		if r.OutcomeDeadline != nil {
			policy := *r.OutcomeDeadline
			if policy.AcknowledgeAfterSeconds < 0 || policy.AcknowledgeAfterSeconds > OperationWorkflowReadinessOutcomeEscalationDelayMaxSeconds || (policy.FallbackOwner != "" && (policy.AcknowledgeAfterSeconds == 0 || policy.FallbackOwner == policy.Owner || !text(policy.FallbackOwner, OperationWorkflowReadinessFollowUpOwnerMaxBytes, true))) || r.ExpectedOutcomeCode == "" || policy.AfterSeconds < 1 || policy.AfterSeconds > OperationWorkflowPlaybookOutcomeDeadlineMaxSeconds || policy.FollowUpAfterSeconds < 1 || policy.FollowUpAfterSeconds > OperationWorkflowReadinessOutcomeEscalationDelayMaxSeconds || !text(policy.Owner, OperationWorkflowReadinessFollowUpOwnerMaxBytes, true) || !text(policy.Note, OperationWorkflowReadinessFollowUpNoteMaxBytes, false) {
				return nil, fmt.Errorf("workflow playbook outcome deadline policy is invalid")
			}
			r.OutcomeDeadline = &policy
		}
		seen[r.ID] = true
		r.RequiredEvidence = append([]string(nil), r.RequiredEvidence...)
		slices.Sort(r.RequiredEvidence)
		for j, e := range r.RequiredEvidence {
			if !text(e, OperationWorkflowPlaybookEvidenceMaxBytes, true) || j > 0 && r.RequiredEvidence[j-1] == e {
				return nil, fmt.Errorf("workflow playbook evidence is invalid or duplicated")
			}
		}
		if len(r.EvidenceRequirements) > OperationWorkflowPlaybookEvidenceMax {
			return nil, fmt.Errorf("workflow playbook evidence requirements exceed their limit")
		}
		r.EvidenceRequirements = append([]OperationWorkflowPlaybookEvidenceRequirement(nil), r.EvidenceRequirements...)
		slices.SortFunc(r.EvidenceRequirements, func(a, b OperationWorkflowPlaybookEvidenceRequirement) int { return strings.Compare(a.Label, b.Label) })
		for j, e := range r.EvidenceRequirements {
			if !slices.Contains(r.RequiredEvidence, e.Label) || j > 0 && r.EvidenceRequirements[j-1].Label == e.Label || ValidateOperationWorkflowName(e.Operation) != nil {
				return nil, fmt.Errorf("workflow playbook evidence requirement needs a unique declared label and valid operation")
			}
			switch e.Kind {
			case "milestone":
				if ValidateOperationWorkflowStateName(e.Milestone) != nil || e.PrerequisiteOperation != "" || e.FromState != "" || e.ToState != "" || e.GroupID != "" || e.PathID != "" || e.BlockerCode != "" {
					return nil, fmt.Errorf("invalid playbook milestone requirement")
				}
			case "prerequisite":
				if ValidateOperationWorkflowStateName(e.Milestone) != nil || ValidateOperationWorkflowName(e.PrerequisiteOperation) != nil || ValidateOperationWorkflowStateName(e.FromState) != nil || ValidateOperationWorkflowStateName(e.ToState) != nil || e.BlockerCode != "" || e.GroupID != "" && ValidateOperationWorkflowStateName(e.GroupID) != nil || e.PathID != "" && (e.GroupID == "" || ValidateOperationWorkflowStateName(e.PathID) != nil) {
					return nil, fmt.Errorf("invalid playbook prerequisite requirement")
				}
			case "verification":
				if !workflowPlaybookMatchCode.MatchString(e.BlockerCode) || e.Milestone != "" || e.PrerequisiteOperation != "" || e.FromState != "" || e.ToState != "" || e.GroupID != "" || e.PathID != "" {
					return nil, fmt.Errorf("invalid playbook verification requirement")
				}
			default:
				return nil, fmt.Errorf("invalid playbook evidence requirement kind")
			}
		}
	}
	slices.SortFunc(out, func(a, b OperationWorkflowPlaybook) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}
