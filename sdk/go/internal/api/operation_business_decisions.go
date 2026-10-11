package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const OperationBusinessConditionsMax = 16
const OperationBusinessConditionDescriptionMaxBytes = 1024

const OperationBusinessDecisionKind = "gregale.business-decision.v1"

// OperationBusinessDecision is application-reported reasoning, not execution authority.
type OperationBusinessDecision struct {
	Conditions  []OperationBusinessConditionResult `json:"conditions,omitempty"`
	Workflow    string                             `json:"workflow"`
	InstanceID  string                             `json:"instance_id"`
	Code        string                             `json:"code"`
	Description string                             `json:"description"`
	RuleID      string                             `json:"rule_id"`
	RuleVersion string                             `json:"rule_version"`
}

type OperationBusinessDecisionPayload struct {
	Kind     string                    `json:"kind"`
	Decision OperationBusinessDecision `json:"decision"`
}

func ValidateOperationBusinessDecision(d OperationBusinessDecision) error {
	slug := regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	if !slug.MatchString(d.Workflow) || len(d.Workflow) > 63 || !slug.MatchString(d.Code) || !slug.MatchString(d.RuleID) {
		return fmt.Errorf("business decision workflow, code, and rule ID must be bounded lowercase slugs")
	}
	for _, field := range []struct {
		value string
		limit int
	}{{d.InstanceID, 256}, {d.Description, 1024}, {d.RuleVersion, 128}} {
		if strings.TrimSpace(field.value) == "" || len(field.value) > field.limit || !utf8.ValidString(field.value) || strings.ContainsFunc(field.value, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return fmt.Errorf("business decision requires bounded nonempty instance ID, description, and rule version without control characters")
		}
	}
	_, err := CanonicalOperationBusinessConditionResults(d.Conditions)
	return err
}

// ParseOperationBusinessDecision recognizes only the versioned envelope. Ordinary
// milestone payloads remain unchanged. Recognized malformed evidence is rejected.
func ParseOperationBusinessDecision(data []byte) (*OperationBusinessDecision, error) {
	if !operationMilestoneHasKind(data, OperationBusinessDecisionKind) {
		return nil, nil
	}
	var payload OperationBusinessDecisionPayload
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("invalid business decision envelope: %w", err)
	}
	if err := ValidateOperationBusinessDecision(payload.Decision); err != nil {
		return nil, err
	}
	payload.Decision.Conditions, _ = CanonicalOperationBusinessConditionResults(payload.Decision.Conditions)
	return &payload.Decision, nil
}

// operationMilestoneHasKind recognizes typed envelopes while allowing ordinary
// milestone payloads, including scalar and array values, to use their own schema.
func operationMilestoneHasKind(data []byte, kind string) bool {
	var header struct {
		Kind string `json:"kind"`
	}
	return json.Unmarshal(data, &header) == nil && header.Kind == kind
}

// OperationWorkflowChangeReason links an application explanation to a transaction-backed decision.
type OperationWorkflowChangeReason struct {
	MilestoneID string                    `json:"milestone_id"`
	Decision    OperationBusinessDecision `json:"decision"`
}

func CloneOperationWorkflowChangeReason(input *OperationWorkflowChangeReason) *OperationWorkflowChangeReason {
	if input == nil {
		return nil
	}
	copy := *input
	copy.Decision = CloneOperationBusinessDecision(input.Decision)
	return &copy
}
func CanonicalOperationWorkflowChangeReason(input *OperationWorkflowChangeReason) (*OperationWorkflowChangeReason, error) {
	if input == nil {
		return nil, nil
	}
	if err := ValidateOperationBusinessDecision(input.Decision); err != nil {
		return nil, err
	}
	p := OperationWorkflowPathSelection{DecisionMilestoneID: input.MilestoneID, DecisionCode: input.Decision.Code, DecisionDescription: input.Decision.Description, DecisionRuleID: input.Decision.RuleID, DecisionRuleVersion: input.Decision.RuleVersion}
	if err := ValidateOperationWorkflowPathDecision(p); err != nil {
		return nil, err
	}
	if input.MilestoneID == "" {
		return nil, fmt.Errorf("workflow change reason requires a milestone ID")
	}
	out := CloneOperationWorkflowChangeReason(input)
	out.Decision.Conditions, _ = CanonicalOperationBusinessConditionResults(out.Decision.Conditions)
	return out, nil
}

// OperationBusinessConditionDefinition declares public reasoning vocabulary on a pinned milestone step.
type OperationBusinessConditionDefinition struct {
	ID          string `json:"id" yaml:"id" toml:"id"`
	Description string `json:"description" yaml:"description" toml:"description"`
}

// OperationBusinessConditionResult is reported by the application; Gregale never evaluates it.
type OperationBusinessConditionResult struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Result      string `json:"result"`
}

func CanonicalOperationBusinessConditions(input []OperationBusinessConditionDefinition) ([]OperationBusinessConditionDefinition, error) {
	if len(input) > OperationBusinessConditionsMax {
		return nil, fmt.Errorf("business conditions exceed limit")
	}
	out := append([]OperationBusinessConditionDefinition(nil), input...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	for i, c := range out {
		if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`).MatchString(c.ID) || strings.TrimSpace(c.Description) == "" || len(c.Description) > OperationBusinessConditionDescriptionMaxBytes || !utf8.ValidString(c.Description) || strings.ContainsFunc(c.Description, func(r rune) bool { return r < 0x20 || r == 0x7f }) || i > 0 && out[i-1].ID == c.ID {
			return nil, fmt.Errorf("business conditions require unique bounded IDs and public descriptions without control characters")
		}
	}
	return out, nil
}
func CanonicalOperationBusinessConditionResults(input []OperationBusinessConditionResult) ([]OperationBusinessConditionResult, error) {
	if len(input) > OperationBusinessConditionsMax {
		return nil, fmt.Errorf("business conditions exceed limit")
	}
	definitions := make([]OperationBusinessConditionDefinition, 0, len(input))
	for _, c := range input {
		if c.Result != "pass" && c.Result != "fail" && c.Result != "unknown" {
			return nil, fmt.Errorf("business condition result must be pass, fail, or unknown")
		}
		definitions = append(definitions, OperationBusinessConditionDefinition{ID: c.ID, Description: c.Description})
	}
	if _, err := CanonicalOperationBusinessConditions(definitions); err != nil {
		return nil, err
	}
	out := append([]OperationBusinessConditionResult(nil), input...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func ValidateOperationBusinessConditionContract(declarations []OperationBusinessConditionDefinition, d OperationBusinessDecision) error {
	for _, c := range d.Conditions {
		matched := false
		for _, definition := range declarations {
			if c.ID == definition.ID && c.Description == definition.Description {
				matched = true
			}
		}
		if !matched {
			return fmt.Errorf("business condition %s does not match the pinned milestone declaration", c.ID)
		}
	}
	return nil
}
func CloneOperationBusinessDecision(d OperationBusinessDecision) OperationBusinessDecision {
	d.Conditions = append([]OperationBusinessConditionResult(nil), d.Conditions...)
	return d
}
func SameOperationBusinessDecision(a, b OperationBusinessDecision) bool {
	var err error
	a.Conditions, err = CanonicalOperationBusinessConditionResults(a.Conditions)
	if err != nil {
		return false
	}
	b.Conditions, err = CanonicalOperationBusinessConditionResults(b.Conditions)
	if err != nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}
