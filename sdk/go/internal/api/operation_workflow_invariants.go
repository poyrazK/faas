package api

import (
	"fmt"
	"sort"
)

type OperationWorkflowInvariantRequirement struct {
	Milestone string `json:"milestone" yaml:"milestone" toml:"milestone"`
	Code      string `json:"code" yaml:"code" toml:"code"`
	Version   string `json:"version" yaml:"version" toml:"version"`
}
type OperationWorkflowPlannedInvariant struct {
	Milestone string                     `json:"milestone"`
	Invariant OperationBusinessInvariant `json:"invariant"`
}
type OperationWorkflowUnmetInvariant struct {
	Requirement OperationWorkflowInvariantRequirement `json:"requirement"`
	Reason      string                                `json:"reason"`
}

func CanonicalOperationWorkflowInvariants(input []OperationWorkflowInvariantRequirement) ([]OperationWorkflowInvariantRequirement, error) {
	if len(input) > 16 {
		return nil, fmt.Errorf("invariant requirements exceed limit")
	}
	out := append([]OperationWorkflowInvariantRequirement(nil), input...)
	sort.Slice(out, func(i, j int) bool { return out[i].Milestone < out[j].Milestone })
	for i, p := range out {
		if _, err := CanonicalOperationBusinessInvariant(OperationBusinessInvariant{Workflow: "invariant", InstanceID: "invariant", State: p.Milestone, Code: p.Code, Version: p.Version, Status: "passed", Description: "invariant", Operations: []string{"invariant"}}); err != nil || i > 0 && out[i-1].Milestone == p.Milestone {
			return nil, fmt.Errorf("invariant requirements need valid unique milestone names, code, and version")
		}
	}
	return out, nil
}
func OperationWorkflowInvariantEvidenceReason(p OperationWorkflowInvariantRequirement, name, workflow, instance, state, operation string, invariant OperationBusinessInvariant) string {
	if p.Milestone != name {
		return "missing"
	}
	if p.Code != invariant.Code || p.Version != invariant.Version || invariant.Workflow != workflow || invariant.InstanceID != instance || invariant.State != state {
		return "mismatched"
	}
	targeted := false
	for _, target := range invariant.Operations {
		if target == operation {
			targeted = true
		}
	}
	if !targeted {
		return "mismatched"
	}
	if invariant.Status != "passed" {
		return invariant.Status
	}
	return ""
}
