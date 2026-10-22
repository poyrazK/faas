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
		assignmentCount := 0
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

			dependencies, err := canonicalPlaybookStepDependencies(guidance.FollowUpDependencies, guidance.FollowUpSteps)
			if err != nil {
				return nil, err
			}
			r.OutcomeGuidance[j].FollowUpDependencies = dependencies
			assignmentCount += len(guidance.FollowUpAssignments)
			if assignmentCount > OperationWorkflowPlaybookEvidenceMax {
				return nil, fmt.Errorf("workflow playbook step assignments exceed their limit")
			}
			r.OutcomeGuidance[j].FollowUpAssignments = slices.Clone(guidance.FollowUpAssignments)
			for k := range r.OutcomeGuidance[j].FollowUpAssignments {
				policy := &r.OutcomeGuidance[j].FollowUpAssignments[k]
				if policy.ReviewImpact == nil {
					continue
				}
				impact := *policy.ReviewImpact
				if !slices.Contains([]string{"milestone", "outcome"}, impact.Kind) || ValidateOperationWorkflowStateName(impact.Code) != nil || !slices.Contains([]string{"normal", "high", "critical"}, impact.Priority) || !text(impact.Description, OperationWorkflowReadinessFollowUpNoteMaxBytes, true) {
					return nil, fmt.Errorf("workflow playbook review business impact is invalid")
				}
				if impact.Kind == "outcome" && impact.Code != r.ExpectedOutcomeCode && (r.OutcomeAcceptance == nil || !slices.Contains(r.OutcomeAcceptance.AlternativeOutcomeCodes, impact.Code)) {
					return nil, fmt.Errorf("workflow playbook review impact references an undeclared outcome")
				}
				policy.ReviewImpact = &impact
			}
			slices.SortFunc(r.OutcomeGuidance[j].FollowUpAssignments, func(a, b OperationWorkflowPlaybookStepAssignmentPolicy) int { return strings.Compare(a.Label, b.Label) })
			assigned := map[string]bool{}
			for _, policy := range guidance.FollowUpAssignments {
				if policy.ReviewAfterSeconds < 0 || policy.ReviewAfterSeconds > OperationWorkflowReadinessOutcomeEscalationDelayMaxSeconds || (policy.ReviewFallbackOwner != "" && (policy.ReviewAfterSeconds == 0 || policy.ReviewFallbackOwner == policy.Owner || !text(policy.ReviewFallbackOwner, OperationWorkflowReadinessFollowUpOwnerMaxBytes, true))) {
					return nil, fmt.Errorf("workflow playbook step review deadline or fallback owner is invalid")
				}
				if policy.AcknowledgeAfterSeconds < 0 || policy.AcknowledgeAfterSeconds > OperationWorkflowReadinessOutcomeEscalationDelayMaxSeconds || (policy.FallbackOwner != "" && (policy.AcknowledgeAfterSeconds == 0 || policy.FallbackOwner == policy.Owner || !text(policy.FallbackOwner, OperationWorkflowReadinessFollowUpOwnerMaxBytes, true))) || !slices.Contains(guidance.FollowUpSteps, policy.Label) || assigned[policy.Label] || !text(policy.Owner, OperationWorkflowReadinessFollowUpOwnerMaxBytes, true) || policy.CompleteAfterSeconds < 1 || policy.CompleteAfterSeconds > OperationWorkflowReadinessOutcomeEscalationDelayMaxSeconds {
					return nil, fmt.Errorf("workflow playbook step assignment is invalid or duplicated")
				}
				assigned[policy.Label] = true
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

func canonicalPlaybookStepDependencies(input []OperationWorkflowPlaybookStepDependency, labels []string) ([]OperationWorkflowPlaybookStepDependency, error) {
	if len(input) > OperationWorkflowPlaybookEvidenceMax {
		return nil, fmt.Errorf("workflow playbook step dependencies exceed their limit")
	}
	out := slices.Clone(input)
	edges := map[string][]string{}
	for i := range out {
		d := &out[i]
		if !slices.Contains(labels, d.Label) || len(d.Prerequisites) == 0 || len(d.Prerequisites) >= OperationWorkflowPlaybookEvidenceMax {
			return nil, fmt.Errorf("workflow playbook step dependency is invalid")
		}
		if _, exists := edges[d.Label]; exists {
			return nil, fmt.Errorf("workflow playbook step dependency is duplicated")
		}
		d.Prerequisites = slices.Clone(d.Prerequisites)
		slices.Sort(d.Prerequisites)
		for j, label := range d.Prerequisites {
			if label == d.Label || !slices.Contains(labels, label) || (j > 0 && d.Prerequisites[j-1] == label) {
				return nil, fmt.Errorf("workflow playbook step prerequisite is invalid or duplicated")
			}
		}
		edges[d.Label] = d.Prerequisites
	}
	colors := map[string]int{}
	var visit func(string) bool
	visit = func(label string) bool {
		if colors[label] == 1 {
			return false
		}
		if colors[label] == 2 {
			return true
		}
		colors[label] = 1
		for _, before := range edges[label] {
			if !visit(before) {
				return false
			}
		}
		colors[label] = 2
		return true
	}
	for label := range edges {
		if !visit(label) {
			return nil, fmt.Errorf("workflow playbook step dependencies contain a cycle")
		}
	}
	slices.SortFunc(out, func(a, b OperationWorkflowPlaybookStepDependency) int { return strings.Compare(a.Label, b.Label) })
	return out, nil
}
