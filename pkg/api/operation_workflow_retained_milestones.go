package api

import (
	"fmt"
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

// OperationWorkflowRetainedMilestoneGroup requires any one retained fact in a named group.
type OperationWorkflowRetainedMilestoneGroup struct {
	ID    string                                          `json:"id" yaml:"id" toml:"id"`
	AnyOf []OperationWorkflowRetainedMilestoneRequirement `json:"any_of" yaml:"any_of" toml:"any_of"`
}

// OperationWorkflowRetainedMilestoneGroupResult preserves every alternative's diagnostics.
type OperationWorkflowRetainedMilestoneGroupResult struct {
	ID           string                               `json:"id"`
	Satisfied    bool                                 `json:"satisfied"`
	Alternatives []OperationWorkflowRetainedMilestone `json:"alternatives"`
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
		if ValidateOperationWorkflowStateName(out[i].ID) != nil || i > 0 && out[i-1].ID == out[i].ID || len(out[i].AnyOf) < 2 {
			return nil, fmt.Errorf("retained milestone groups require unique valid IDs and at least two alternatives")
		}
		choices, err := CanonicalOperationWorkflowRetainedMilestones(out[i].AnyOf)
		if err != nil {
			return nil, err
		}
		out[i].AnyOf = choices
	}
	return out, nil
}

// OperationWorkflowRetainedSelectors includes required facts and every alternative for lookup and policy validation.
func OperationWorkflowRetainedSelectors(required []OperationWorkflowRetainedMilestoneRequirement, groups []OperationWorkflowRetainedMilestoneGroup) []OperationWorkflowRetainedMilestoneRequirement {
	out := append([]OperationWorkflowRetainedMilestoneRequirement(nil), required...)
	for _, group := range groups {
		out = append(out, group.AnyOf...)
	}
	return out
}

func CloneOperationWorkflowRetainedMilestoneGroups(input []OperationWorkflowRetainedMilestoneGroup) []OperationWorkflowRetainedMilestoneGroup {
	out := append([]OperationWorkflowRetainedMilestoneGroup(nil), input...)
	for i := range out {
		out[i].AnyOf = append([]OperationWorkflowRetainedMilestoneRequirement(nil), out[i].AnyOf...)
	}
	return out
}
