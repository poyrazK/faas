package api

import (
	"fmt"
	"sort"
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
