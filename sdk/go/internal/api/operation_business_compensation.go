package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const OperationBusinessCompensationKind = "gregale.business-compensation.v1"

type OperationBusinessEffectReference struct {
	OperationID string `json:"operation_id"`
	MilestoneID string `json:"milestone_id"`
}
type OperationBusinessCompensation struct {
	Workflow     string                           `json:"workflow"`
	InstanceID   string                           `json:"instance_id"`
	State        string                           `json:"state"`
	Operation    string                           `json:"operation"`
	Code         string                           `json:"code"`
	Version      string                           `json:"version"`
	Status       string                           `json:"status"`
	SourceEffect OperationBusinessEffectReference `json:"source_effect"`
	Reference    string                           `json:"reference,omitempty"`
	Description  string                           `json:"description"`
}
type OperationBusinessCompensationPayload struct {
	Kind         string                        `json:"kind"`
	Compensation OperationBusinessCompensation `json:"compensation"`
}

func ValidateOperationBusinessCompensation(input OperationBusinessCompensation) error {
	if err := ValidateOperationBusinessDecision(OperationBusinessDecision{Workflow: input.Workflow, InstanceID: input.InstanceID, Code: input.Code, Description: input.Description, RuleID: input.State, RuleVersion: input.Version}); err != nil {
		return err
	}
	if len(input.Version) > 64 || len(input.Description) > 256 || input.Status != "required" && input.Status != "pending" && input.Status != "failed" && input.Status != "confirmed" {
		return fmt.Errorf("invalid compensation version, description, or status")
	}
	if err := ValidateOperationBusinessDecision(OperationBusinessDecision{Workflow: "compensation", InstanceID: "compensation", Code: input.Operation, Description: "compensation", RuleID: "compensation", RuleVersion: "compensation"}); err != nil {
		return err
	}
	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	for _, id := range []string{input.SourceEffect.OperationID, input.SourceEffect.MilestoneID} {
		if !pattern.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
			return fmt.Errorf("compensation source requires canonical nonzero UUIDs")
		}
	}
	if input.Status == "confirmed" && strings.TrimSpace(input.Reference) == "" {
		return fmt.Errorf("confirmed compensation requires a reference")
	}
	if len(input.Reference) > 256 || !utf8.ValidString(input.Reference) || strings.ContainsFunc(input.Reference, func(r rune) bool { return r < 32 || r == 127 }) {
		return fmt.Errorf("invalid compensation reference")
	}
	return nil
}
func ParseOperationBusinessCompensation(data []byte) (*OperationBusinessCompensation, error) {
	if !operationMilestoneHasKind(data, OperationBusinessCompensationKind) {
		return nil, nil
	}
	var payload OperationBusinessCompensationPayload
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	if err := ValidateOperationBusinessCompensation(payload.Compensation); err != nil {
		return nil, err
	}
	return &payload.Compensation, nil
}
