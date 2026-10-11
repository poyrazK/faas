package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const OperationBusinessEffectKind = "gregale.business-effect.v1"

type OperationBusinessEffect struct {
	Workflow    string `json:"workflow"`
	InstanceID  string `json:"instance_id"`
	State       string `json:"state"`
	Operation   string `json:"operation"`
	Code        string `json:"code"`
	Version     string `json:"version"`
	Status      string `json:"status"`
	Reference   string `json:"reference,omitempty"`
	Description string `json:"description"`
	AmountMinor *int64 `json:"amount_minor,omitempty"`
	Currency    string `json:"currency,omitempty"`
}
type OperationBusinessEffectPayload struct {
	Kind   string                  `json:"kind"`
	Effect OperationBusinessEffect `json:"effect"`
}
type OperationWorkflowEffectRequirement struct {
	Milestone string `json:"milestone" yaml:"milestone" toml:"milestone"`
	Code      string `json:"code" yaml:"code" toml:"code"`
	Version   string `json:"version" yaml:"version" toml:"version"`
}
type OperationWorkflowPlannedEffect struct {
	Milestone string                  `json:"milestone"`
	Effect    OperationBusinessEffect `json:"effect"`
}
type OperationWorkflowUnmetEffect struct {
	Requirement OperationWorkflowEffectRequirement `json:"requirement"`
	Reason      string                             `json:"reason"`
}

func ValidateOperationBusinessEffect(input OperationBusinessEffect) error {
	if err := ValidateOperationBusinessDecision(OperationBusinessDecision{Workflow: input.Workflow, InstanceID: input.InstanceID, Code: input.Code, Description: input.Description, RuleID: input.State, RuleVersion: input.Version}); err != nil {
		return err
	}
	if len(input.Version) > 64 || len(input.Description) > 256 || input.Status != "pending" && input.Status != "failed" && input.Status != "confirmed" {
		return fmt.Errorf("invalid effect version, description, or status")
	}
	if err := ValidateOperationBusinessDecision(OperationBusinessDecision{Workflow: "effect", InstanceID: "effect", Code: input.Operation, Description: "effect", RuleID: "effect", RuleVersion: "effect"}); err != nil {
		return err
	}
	if input.Status == "confirmed" && strings.TrimSpace(input.Reference) == "" {
		return fmt.Errorf("confirmed effect requires a reference")
	}
	if len(input.Reference) > 256 || !utf8.ValidString(input.Reference) || strings.ContainsFunc(input.Reference, func(r rune) bool { return r < 32 || r == 127 }) {
		return fmt.Errorf("invalid effect reference")
	}
	if input.AmountMinor == nil && input.Currency != "" || input.AmountMinor != nil && (*input.AmountMinor < 0 || *input.AmountMinor > 9007199254740991 || len(input.Currency) != 3 || strings.ContainsFunc(input.Currency, func(r rune) bool { return r < 'A' || r > 'Z' })) {
		return fmt.Errorf("effect amount requires safe nonnegative minor units and three-letter uppercase currency")
	}
	return nil
}
func ParseOperationBusinessEffect(data []byte) (*OperationBusinessEffect, error) {
	if !operationMilestoneHasKind(data, OperationBusinessEffectKind) {
		return nil, nil
	}
	var payload OperationBusinessEffectPayload
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	if err := ValidateOperationBusinessEffect(payload.Effect); err != nil {
		return nil, err
	}
	return &payload.Effect, nil
}
func CanonicalOperationWorkflowEffects(input []OperationWorkflowEffectRequirement) ([]OperationWorkflowEffectRequirement, error) {
	if len(input) > 16 {
		return nil, fmt.Errorf("effect requirements exceed limit")
	}
	out := append([]OperationWorkflowEffectRequirement(nil), input...)
	sort.Slice(out, func(i, j int) bool { return out[i].Milestone < out[j].Milestone })
	for i, p := range out {
		if err := ValidateOperationBusinessEffect(OperationBusinessEffect{Workflow: "effect", InstanceID: "effect", State: p.Milestone, Operation: "effect", Code: p.Code, Version: p.Version, Status: "confirmed", Reference: "effect", Description: "effect"}); err != nil || i > 0 && out[i-1].Milestone == p.Milestone {
			return nil, fmt.Errorf("effect requirements need valid unique milestones, code, and version")
		}
	}
	return out, nil
}
func OperationWorkflowEffectEvidenceReason(p OperationWorkflowEffectRequirement, name, workflow, instance, state, operation string, effect OperationBusinessEffect) string {
	if p.Milestone != name {
		return "missing"
	}
	if p.Code != effect.Code || p.Version != effect.Version || effect.Workflow != workflow || effect.InstanceID != instance || effect.State != state || effect.Operation != operation {
		return "mismatched"
	}
	if effect.Status != "confirmed" {
		return effect.Status
	}
	return ""
}
