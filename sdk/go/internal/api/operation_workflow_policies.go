package api

import (
	"fmt"
	"regexp"
	"sort"
)

type OperationWorkflowPolicyRequirement struct {
	Milestone   string `json:"milestone" yaml:"milestone" toml:"milestone"`
	RuleID      string `json:"rule_id" yaml:"rule_id" toml:"rule_id"`
	RuleVersion string `json:"rule_version" yaml:"rule_version" toml:"rule_version"`
	Code        string `json:"code" yaml:"code" toml:"code"`
}
type OperationWorkflowPlannedDecision struct {
	Milestone string                    `json:"milestone"`
	Decision  OperationBusinessDecision `json:"decision"`
}

func CanonicalOperationWorkflowPolicies(input []OperationWorkflowPolicyRequirement) ([]OperationWorkflowPolicyRequirement, error) {
	if len(input) > 16 {
		return nil, fmt.Errorf("workflow policy requirements exceed limit")
	}
	out := append([]OperationWorkflowPolicyRequirement(nil), input...)
	sort.Slice(out, func(i, j int) bool { return out[i].Milestone < out[j].Milestone })
	for i, p := range out {
		if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`).MatchString(p.Milestone) || ValidateOperationBusinessDecision(OperationBusinessDecision{Workflow: "policy", InstanceID: "policy", Code: p.Code, Description: "policy", RuleID: p.RuleID, RuleVersion: p.RuleVersion}) != nil || i > 0 && out[i-1].Milestone == p.Milestone {
			return nil, fmt.Errorf("policy requirements need unique milestone names and valid rule, version, and decision code")
		}
	}
	return out, nil
}
func OperationWorkflowPolicyMatches(p OperationWorkflowPolicyRequirement, name, workflow, instance string, d OperationBusinessDecision) bool {
	return p.Milestone == name && p.RuleID == d.RuleID && p.RuleVersion == d.RuleVersion && p.Code == d.Code && d.Workflow == workflow && d.InstanceID == instance
}
