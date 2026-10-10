package api

import (
	"fmt"
	"regexp"
	"sort"
	"time"
)

// OperationWorkflowRetainedMilestoneRequirement selects a transaction-backed
// fact from a declared step of the same workflow contract.
type OperationWorkflowRetainedMilestoneRequirement struct {
	Operation string `json:"operation" yaml:"operation" toml:"operation"`
	Milestone string `json:"milestone" yaml:"milestone" toml:"milestone"`
}

// Verified means a matching app-reported fact remains retained in the selected
// ownership, subject, workflow instance and version. It is not external proof.
type OperationWorkflowRetainedMilestone struct {
	Recovery     *OperationWorkflowPrerequisiteRecovery          `json:"recovery,omitempty"`
	Freshness    *OperationWorkflowRetainedMilestoneFreshness    `json:"freshness,omitempty"`
	Invalidation *OperationWorkflowRetainedMilestoneInvalidation `json:"invalidation,omitempty"`
	Requirement  OperationWorkflowRetainedMilestoneRequirement   `json:"requirement"`
	Status       string                                          `json:"status"`
	Evidence     *OperationWorkflowInstanceMilestoneRef          `json:"evidence,omitempty"`
}

// OperationWorkflowRetainedMilestoneInvalidationRule links a prerequisite to a
// later or simultaneous app-reported fact in the same workflow instance/version.
type OperationWorkflowRetainedMilestoneInvalidationRule struct {
	Prerequisite  OperationWorkflowRetainedMilestoneRequirement `json:"prerequisite" yaml:"prerequisite" toml:"prerequisite"`
	InvalidatedBy OperationWorkflowRetainedMilestoneRequirement `json:"invalidated_by" yaml:"invalidated_by" toml:"invalidated_by"`
}

// OperationWorkflowRetainedMilestoneInvalidation identifies the retained fact
// that made a prerequisite unusable at the evaluation time.
type OperationWorkflowRetainedMilestoneInvalidation struct {
	Source   OperationWorkflowRetainedMilestoneRequirement `json:"source"`
	Evidence OperationWorkflowInstanceMilestoneRef         `json:"evidence"`
}

// OperationWorkflowRetainedMilestoneFreshnessRule pins the maximum age of a prerequisite.
type OperationWorkflowRetainedMilestoneFreshnessRule struct {
	Prerequisite  OperationWorkflowRetainedMilestoneRequirement `json:"prerequisite" yaml:"prerequisite" toml:"prerequisite"`
	MaxAgeSeconds int64                                         `json:"max_age_seconds" yaml:"max_age_seconds" toml:"max_age_seconds"`
}

// OperationWorkflowRetainedMilestoneFreshness describes eligibility at the server evaluation time.
type OperationWorkflowRetainedMilestoneFreshness struct {
	MaxAgeSeconds    int64      `json:"max_age_seconds"`
	EvaluatedAt      time.Time  `json:"evaluated_at"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	AgeSeconds       *int64     `json:"age_seconds,omitempty"`
	RemainingSeconds *int64     `json:"remaining_seconds,omitempty"`
}

// OperationWorkflowPrerequisiteRecovery describes application-owned recovery, not executable commands.
type OperationWorkflowPrerequisiteRecovery struct {
	Reason          string     `json:"reason"`
	Operation       string     `json:"operation"`
	Milestone       string     `json:"milestone"`
	AfterOccurredAt *time.Time `json:"after_occurred_at,omitempty"`
	Steps           []string   `json:"steps"`
}

// OperationWorkflowRetainedMilestoneGroup accepts any one fact or any one complete named path.
type OperationWorkflowRetainedMilestoneGroup struct {
	Paths []OperationWorkflowRetainedMilestonePath        `json:"paths,omitempty" yaml:"paths,omitempty" toml:"paths"`
	ID    string                                          `json:"id" yaml:"id" toml:"id"`
	AnyOf []OperationWorkflowRetainedMilestoneRequirement `json:"any_of,omitempty" yaml:"any_of,omitempty" toml:"any_of"`
}

// OperationWorkflowRetainedMilestoneGroupResult preserves every alternative's diagnostics.
type OperationWorkflowRetainedMilestoneGroupResult struct {
	SelectedPathID string                                         `json:"selected_path_id,omitempty"`
	Paths          []OperationWorkflowRetainedMilestonePathResult `json:"paths,omitempty"`
	ID             string                                         `json:"id"`
	Satisfied      bool                                           `json:"satisfied"`
	Alternatives   []OperationWorkflowRetainedMilestone           `json:"alternatives,omitempty"`
}

// OperationWorkflowRetainedMilestonePath requires all facts in one alternative business path.
type OperationWorkflowRetainedMilestonePath struct {
	Decision *OperationWorkflowPolicyRequirement             `json:"decision,omitempty" yaml:"decision,omitempty" toml:"decision"`
	ID       string                                          `json:"id" yaml:"id" toml:"id"`
	AllOf    []OperationWorkflowRetainedMilestoneRequirement `json:"all_of" yaml:"all_of" toml:"all_of"`
}

// OperationWorkflowRetainedMilestonePathResult includes every fact needed by a named path.
type OperationWorkflowRetainedMilestonePathResult struct {
	Decision     *OperationWorkflowPathDecisionResult `json:"decision,omitempty"`
	ID           string                               `json:"id"`
	Satisfied    bool                                 `json:"satisfied"`
	Requirements []OperationWorkflowRetainedMilestone `json:"requirements"`
}

// OperationWorkflowPathDecisionResult compares planned application reasoning with the pinned path rule.
type OperationWorkflowPathDecisionResult struct {
	Requirement OperationWorkflowPolicyRequirement `json:"requirement"`
	Reported    *OperationBusinessDecision         `json:"reported,omitempty"`
	Satisfied   bool                               `json:"satisfied"`
}

// OperationWorkflowPathSelection is the application's chosen path within a compound group.
type OperationWorkflowPathSelection struct {
	GroupID             string `json:"group_id"`
	PathID              string `json:"path_id"`
	DecisionMilestoneID string `json:"decision_milestone_id,omitempty"`
	DecisionCode        string `json:"decision_code,omitempty"`
	DecisionDescription string `json:"decision_description,omitempty"`
	DecisionRuleID      string `json:"decision_rule_id,omitempty"`
	DecisionRuleVersion string `json:"decision_rule_version,omitempty"`
}

func CanonicalOperationWorkflowRetainedMilestones(input []OperationWorkflowRetainedMilestoneRequirement) ([]OperationWorkflowRetainedMilestoneRequirement, error) {
	if len(input) > OperationWorkflowTransitionEvidenceMax {
		return nil, fmt.Errorf("retained milestone prerequisites exceed limit")
	}
	out := append([]OperationWorkflowRetainedMilestoneRequirement(nil), input...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Operation != out[j].Operation {
			return out[i].Operation < out[j].Operation
		}
		return out[i].Milestone < out[j].Milestone
	})
	for i, p := range out {
		if ValidateOperationWorkflowName(p.Operation) != nil || ValidateOperationWorkflowStateName(p.Milestone) != nil || i > 0 && out[i-1] == p {
			return nil, fmt.Errorf("retained milestone prerequisites require valid, unique operation and milestone pairs")
		}
	}
	return out, nil
}

func CanonicalOperationWorkflowRetainedMilestoneInvalidations(input []OperationWorkflowRetainedMilestoneInvalidationRule, prerequisites []OperationWorkflowRetainedMilestoneRequirement) ([]OperationWorkflowRetainedMilestoneInvalidationRule, error) {
	if len(input) > OperationWorkflowInvalidationRulesMax {
		return nil, fmt.Errorf("retained milestone invalidation rules exceed limit")
	}
	out := append([]OperationWorkflowRetainedMilestoneInvalidationRule(nil), input...)
	key := func(p OperationWorkflowRetainedMilestoneInvalidationRule) string {
		return p.Prerequisite.Operation + "/" + p.Prerequisite.Milestone + "/" + p.InvalidatedBy.Operation + "/" + p.InvalidatedBy.Milestone
	}
	sort.Slice(out, func(i, j int) bool { return key(out[i]) < key(out[j]) })
	for i, p := range out {
		declared := false
		for _, prerequisite := range prerequisites {
			if p.Prerequisite == prerequisite {
				declared = true
				break
			}
		}
		if !declared || ValidateOperationWorkflowName(p.Prerequisite.Operation) != nil || ValidateOperationWorkflowStateName(p.Prerequisite.Milestone) != nil || p.Prerequisite == p.InvalidatedBy || ValidateOperationWorkflowName(p.InvalidatedBy.Operation) != nil || ValidateOperationWorkflowStateName(p.InvalidatedBy.Milestone) != nil || i > 0 && out[i-1] == p {
			return nil, fmt.Errorf("invalidation rules require a declared prerequisite, valid distinct source, and unique pair")
		}
	}
	return out, nil
}

func CanonicalOperationWorkflowRetainedMilestoneFreshness(input []OperationWorkflowRetainedMilestoneFreshnessRule, prerequisites []OperationWorkflowRetainedMilestoneRequirement) ([]OperationWorkflowRetainedMilestoneFreshnessRule, error) {
	if len(input) > OperationWorkflowFreshnessRulesMax {
		return nil, fmt.Errorf("retained milestone freshness rules exceed limit")
	}
	out := append([]OperationWorkflowRetainedMilestoneFreshnessRule(nil), input...)
	key := func(p OperationWorkflowRetainedMilestoneFreshnessRule) string {
		return p.Prerequisite.Operation + "/" + p.Prerequisite.Milestone
	}
	sort.Slice(out, func(i, j int) bool { return key(out[i]) < key(out[j]) })
	for i, p := range out {
		declared := false
		for _, prerequisite := range prerequisites {
			if p.Prerequisite == prerequisite {
				declared = true
				break
			}
		}
		if !declared || ValidateOperationWorkflowName(p.Prerequisite.Operation) != nil || ValidateOperationWorkflowStateName(p.Prerequisite.Milestone) != nil || p.MaxAgeSeconds < 1 || p.MaxAgeSeconds > OperationWorkflowFreshnessMaxSeconds || i > 0 && out[i-1].Prerequisite == p.Prerequisite {
			return nil, fmt.Errorf("freshness rules require a declared prerequisite, unique selector, and maximum age between 1 second and 10 years")
		}
	}
	return out, nil
}

func CanonicalOperationWorkflowRetainedMilestoneGroups(input []OperationWorkflowRetainedMilestoneGroup) ([]OperationWorkflowRetainedMilestoneGroup, error) {
	if len(input) > OperationWorkflowRetainedGroupsMax {
		return nil, fmt.Errorf("retained milestone groups exceed limit")
	}
	out := append([]OperationWorkflowRetainedMilestoneGroup(nil), input...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	for i := range out {
		if ValidateOperationWorkflowStateName(out[i].ID) != nil || i > 0 && out[i-1].ID == out[i].ID {
			return nil, fmt.Errorf("retained milestone groups require unique valid IDs")
		}
		if len(out[i].Paths) > 0 {
			if len(out[i].AnyOf) > 0 {
				return nil, fmt.Errorf("retained milestone groups must choose any_of or paths")
			}
			paths, err := canonicalRetainedMilestonePaths(out[i].Paths)
			if err != nil {
				return nil, err
			}
			out[i].Paths, out[i].AnyOf = paths, nil
		} else {
			if len(out[i].AnyOf) < 2 {
				return nil, fmt.Errorf("retained milestone groups require at least two alternatives")
			}
			choices, err := CanonicalOperationWorkflowRetainedMilestones(out[i].AnyOf)
			if err != nil {
				return nil, err
			}
			out[i].AnyOf, out[i].Paths = choices, nil
		}
	}
	return out, nil
}

func canonicalRetainedMilestonePaths(input []OperationWorkflowRetainedMilestonePath) ([]OperationWorkflowRetainedMilestonePath, error) {
	if len(input) < 2 || len(input) > OperationWorkflowRetainedPathsMax {
		return nil, fmt.Errorf("retained prerequisite paths require between two and eight paths")
	}
	out := append([]OperationWorkflowRetainedMilestonePath(nil), input...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	total := 0
	for i := range out {
		if ValidateOperationWorkflowStateName(out[i].ID) != nil || i > 0 && out[i-1].ID == out[i].ID || len(out[i].AllOf) == 0 {
			return nil, fmt.Errorf("retained prerequisite paths require unique valid IDs and nonempty all_of requirements")
		}
		if out[i].Decision != nil {
			policies, err := CanonicalOperationWorkflowPolicies([]OperationWorkflowPolicyRequirement{*out[i].Decision})
			if err != nil {
				return nil, err
			}
			out[i].Decision = &policies[0]
		}
		facts, err := CanonicalOperationWorkflowRetainedMilestones(out[i].AllOf)
		if err != nil {
			return nil, err
		}
		total += len(facts)
		if total > OperationWorkflowTransitionEvidenceMax {
			return nil, fmt.Errorf("retained prerequisite paths exceed the per-group fact budget")
		}
		out[i].AllOf = facts
	}
	return out, nil
}

// OperationWorkflowRetainedSelectors includes required facts and every alternative for lookup and policy validation.
func OperationWorkflowRetainedSelectors(required []OperationWorkflowRetainedMilestoneRequirement, groups []OperationWorkflowRetainedMilestoneGroup) []OperationWorkflowRetainedMilestoneRequirement {
	out := append([]OperationWorkflowRetainedMilestoneRequirement(nil), required...)
	for _, group := range groups {
		out = append(out, group.AnyOf...)
		for _, path := range group.Paths {
			out = append(out, path.AllOf...)
		}
	}
	return out
}

func CloneOperationWorkflowRetainedMilestoneGroups(input []OperationWorkflowRetainedMilestoneGroup) []OperationWorkflowRetainedMilestoneGroup {
	out := append([]OperationWorkflowRetainedMilestoneGroup(nil), input...)
	for i := range out {
		out[i].AnyOf = append([]OperationWorkflowRetainedMilestoneRequirement(nil), out[i].AnyOf...)
		out[i].Paths = append([]OperationWorkflowRetainedMilestonePath(nil), out[i].Paths...)
		for j := range out[i].Paths {
			if out[i].Paths[j].Decision != nil {
				copy := *out[i].Paths[j].Decision
				out[i].Paths[j].Decision = &copy
			}
			out[i].Paths[j].AllOf = append([]OperationWorkflowRetainedMilestoneRequirement(nil), out[i].Paths[j].AllOf...)
		}
	}
	return out
}

func CanonicalOperationWorkflowPathSelections(input []OperationWorkflowPathSelection) ([]OperationWorkflowPathSelection, error) {
	if len(input) > OperationWorkflowRetainedGroupsMax {
		return nil, fmt.Errorf("workflow path selections exceed limit")
	}
	out := append([]OperationWorkflowPathSelection(nil), input...)
	sort.Slice(out, func(i, j int) bool { return out[i].GroupID < out[j].GroupID })
	for i, p := range out {
		if err := ValidateOperationWorkflowPathDecision(p); err != nil {
			return nil, err
		}
		if ValidateOperationWorkflowStateName(p.GroupID) != nil || ValidateOperationWorkflowStateName(p.PathID) != nil || i > 0 && out[i-1].GroupID == p.GroupID {
			return nil, fmt.Errorf("workflow path selections require valid IDs and unique groups")
		}
	}
	return out, nil
}

func ValidateOperationWorkflowPathSelections(selections []OperationWorkflowPathSelection, groups []OperationWorkflowRetainedMilestoneGroup) error {
	for _, selection := range selections {
		found := false
		for _, group := range groups {
			if group.ID == selection.GroupID {
				for _, path := range group.Paths {
					found = found || path.ID == selection.PathID
				}
			}
		}
		if !found {
			return fmt.Errorf("selected workflow path is not declared for this edge")
		}
	}
	return nil
}

// ValidateOperationWorkflowPathDecision validates the bounded explanation snapshot.
// The state store verifies its identity against transaction-backed milestone evidence.
func ValidateOperationWorkflowPathDecision(p OperationWorkflowPathSelection) error {
	if p.DecisionMilestoneID == "" {
		if p.DecisionCode != "" || p.DecisionDescription != "" || p.DecisionRuleID != "" || p.DecisionRuleVersion != "" {
			return fmt.Errorf("path decision explanation requires a milestone reference")
		}
		return nil
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(p.DecisionMilestoneID) || p.DecisionMilestoneID == "00000000-0000-0000-0000-000000000000" {
		return fmt.Errorf("path decision milestone requires a canonical UUID")
	}
	return ValidateOperationBusinessDecision(OperationBusinessDecision{Workflow: "path", InstanceID: "path", Code: p.DecisionCode, Description: p.DecisionDescription, RuleID: p.DecisionRuleID, RuleVersion: p.DecisionRuleVersion})
}
