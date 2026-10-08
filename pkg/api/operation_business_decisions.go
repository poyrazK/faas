package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const OperationBusinessDecisionKind = "gregale.business-decision.v1"

// OperationBusinessDecision is application-reported reasoning, not execution authority.
type OperationBusinessDecision struct {
	Workflow    string `json:"workflow"`
	InstanceID  string `json:"instance_id"`
	Code        string `json:"code"`
	Description string `json:"description"`
	RuleID      string `json:"rule_id"`
	RuleVersion string `json:"rule_version"`
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
	return nil
}

// ParseOperationBusinessDecision recognizes only the versioned envelope. Ordinary
// milestone payloads remain unchanged. Recognized malformed evidence is rejected.
func ParseOperationBusinessDecision(data []byte) (*OperationBusinessDecision, error) {
	var header struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(data, &header) != nil || header.Kind != OperationBusinessDecisionKind {
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
	return &payload.Decision, nil
}
