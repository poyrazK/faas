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
	Requirement OperationWorkflowRetainedMilestoneRequirement `json:"requirement"`
	Status      string                                        `json:"status"`
	Evidence    *OperationWorkflowInstanceMilestoneRef        `json:"evidence,omitempty"`
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
