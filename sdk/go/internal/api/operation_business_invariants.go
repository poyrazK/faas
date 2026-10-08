package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

const OperationBusinessInvariantKind = "gregale.business-invariant.v1"
const OperationBusinessInvariantBlockerPrefix = "invariant-"

type OperationBusinessInvariant struct {
	Workflow    string   `json:"workflow"`
	InstanceID  string   `json:"instance_id"`
	State       string   `json:"state"`
	Code        string   `json:"code"`
	Version     string   `json:"version"`
	Status      string   `json:"status"`
	Description string   `json:"description"`
	Operations  []string `json:"operations"`
}
type OperationBusinessInvariantPayload struct {
	Kind      string                     `json:"kind"`
	Invariant OperationBusinessInvariant `json:"invariant"`
}

func CanonicalOperationBusinessInvariant(input OperationBusinessInvariant) (OperationBusinessInvariant, error) {
	if len(input.Code) > 54 || len(input.Version) > 64 || len(input.Description) > 256 || len(input.Operations) < 1 || len(input.Operations) > 16 || input.Status != "passed" && input.Status != "failed" && input.Status != "unknown" {
		return input, fmt.Errorf("invalid invariant code, version, description, status, or action count")
	}
	if err := ValidateOperationBusinessDecision(OperationBusinessDecision{Workflow: input.Workflow, InstanceID: input.InstanceID, Code: input.Code, Description: input.Description, RuleID: input.State, RuleVersion: input.Version}); err != nil {
		return input, err
	}
	input.Operations = append([]string(nil), input.Operations...)
	sort.Strings(input.Operations)
	for i, operation := range input.Operations {
		if err := ValidateOperationBusinessDecision(OperationBusinessDecision{Workflow: "invariant", InstanceID: "invariant", Code: operation, Description: "invariant", RuleID: "invariant", RuleVersion: "invariant"}); err != nil || i > 0 && input.Operations[i-1] == operation {
			return input, fmt.Errorf("invariant actions must be valid unique Operation names")
		}
	}
	return input, nil
}
func ParseOperationBusinessInvariant(data []byte) (*OperationBusinessInvariant, error) {
	var header struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(data, &header) != nil || header.Kind != OperationBusinessInvariantKind {
		return nil, nil
	}
	var payload OperationBusinessInvariantPayload
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	invariant, err := CanonicalOperationBusinessInvariant(payload.Invariant)
	if err != nil {
		return nil, err
	}
	return &invariant, nil
}

// ApplyOperationBusinessInvariant preserves other codes and replaces this check's
// entire action set. Callers must supply the complete locked blocker snapshot.
func ApplyOperationBusinessInvariant(input OperationBusinessInvariant, current []OperationWorkflowBlocker) ([]OperationWorkflowBlocker, error) {
	input, err := CanonicalOperationBusinessInvariant(input)
	if err != nil {
		return nil, err
	}
	code := OperationBusinessInvariantBlockerPrefix + input.Code
	out := make([]OperationWorkflowBlocker, 0, len(current)+len(input.Operations))
	for _, blocker := range current {
		if blocker.Code != code {
			out = append(out, blocker)
		}
	}
	if input.Status != "passed" {
		for _, operation := range input.Operations {
			blocker := OperationWorkflowBlocker{Code: code, Operation: operation, Description: fmt.Sprintf("Invariant %s (%s) %s: %s", input.Code, input.Version, input.Status, input.Description)}
			for _, prior := range current {
				if prior.Code == code && prior.Operation == operation {
					blocker.FirstObservedAt = prior.FirstObservedAt
				}
			}
			out = append(out, blocker)
		}
	}
	if len(out) > 16 {
		return nil, fmt.Errorf("invariant update exceeds workflow blocker limit")
	}
	return out, nil
}
