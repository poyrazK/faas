// adr: 20261103193000
package mcpmanagement

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

var planStatuses = []string{"planned", "running", "accepted", "completed", "promoted", "awaiting_approval", "refused", "partial", "interrupted", "cancelled", "cancellation_requested", "outcome_unknown"}
var provisionStatuses = []string{"planned", "running", "completed", "failed", "interrupted", "cancelled", "cancellation_requested"}
var recoveryActions = []string{"none", "retry_same_plan", "retry_read", "replan", "inspect", "request_access", "await_approval", "retry_original", "stop"}

func recoverySchema() map[string]any {
	return map[string]any{"type": "object", "required": []string{"action"}, "additionalProperties": false,
		"properties": map[string]any{
			"action":                map[string]any{"type": "string", "enum": recoveryActions},
			"tool":                  map[string]any{"type": "string"},
			"arguments":             map[string]any{"type": "object", "additionalProperties": true},
			"after_seconds":         map[string]any{"type": "integer"},
			"reuse_original_inputs": map[string]any{"type": "boolean"},
			"toolsets":              map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		}}
}

func toolErrorSchema() map[string]any {
	return map[string]any{"type": "object", "required": []string{"schema_version", "code", "message", "recovery"}, "additionalProperties": true,
		"properties": map[string]any{
			"schema_version": map[string]any{"type": "integer", "const": 1},
			"code":           map[string]any{"type": "string"}, "message": map[string]any{"type": "string"},
			"status": map[string]any{"type": "integer"}, "hint": map[string]any{"type": "string"},
			"retry_after_seconds": map[string]any{"type": "integer"},
			"outcome":             map[string]any{"type": "string", "enum": []string{"refused", "partial", "outcome_unknown"}},
			"recovery":            map[string]any{"$ref": "#/$defs/recovery"},
		}}
}

// Validate the exact JSON numbers emitted by the bridge. The schema vocabulary
// is deliberately limited to the constructs we emit; unsupported references
// fail closed rather than silently relaxing the published contract.
func validateSchemaValue(schema map[string]any, value any, root map[string]any) error {
	if ref, ok := schema["$ref"].(string); ok {
		name, ok := strings.CutPrefix(ref, "#/$defs/")
		defs, _ := root["$defs"].(map[string]any)
		target, found := defs[name].(map[string]any)
		if !ok || !found {
			return fmt.Errorf("unknown schema reference")
		}
		return validateSchemaValue(target, value, root)
	}
	if alternatives, ok := schema["anyOf"].([]any); ok {
		matched := false
		for _, alternative := range alternatives {
			if candidate, ok := alternative.(map[string]any); ok && validateSchemaValue(candidate, value, root) == nil {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("no response variant matched")
		}
	}
	if kind, ok := schema["type"]; ok {
		kinds, ok := kind.([]string)
		if !ok {
			kinds = []string{kind.(string)}
		}
		valid := false
		for _, wanted := range kinds {
			switch wanted {
			case "null":
				valid = value == nil
			case "object":
				_, valid = value.(map[string]any)
			case "array":
				_, valid = value.([]any)
			case "string":
				_, valid = value.(string)
			case "boolean":
				_, valid = value.(bool)
			case "integer":
				if number, ok := value.(json.Number); ok {
					_, err := number.Int64()
					valid = err == nil
				}
			}
			if valid {
				break
			}
		}
		if !valid {
			return fmt.Errorf("wrong field type")
		}
	}
	if constant, ok := schema["const"]; ok {
		a, _ := json.Marshal(constant)
		b, _ := json.Marshal(value)
		if !bytes.Equal(a, b) {
			return fmt.Errorf("wrong contract version")
		}
	}
	if choices, ok := schema["enum"].([]string); ok {
		matched := false
		for _, choice := range choices {
			if value == choice {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("unknown field value")
		}
	}
	if format, _ := schema["format"].(string); format == "date-time" {
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("invalid time")
		}
		if _, err := time.Parse(time.RFC3339Nano, s); err != nil {
			return fmt.Errorf("invalid time")
		}
	}
	if fields, ok := value.(map[string]any); ok {
		for _, key := range stringSchemaList(schema["required"]) {
			if _, ok := fields[key]; !ok {
				return fmt.Errorf("missing required field %s", key)
			}
		}
		properties, _ := schema["properties"].(map[string]any)
		for key, field := range fields {
			if spec, ok := properties[key].(map[string]any); ok {
				if err := validateSchemaValue(spec, field, root); err != nil {
					return fmt.Errorf("field %s: %w", key, err)
				}
			} else if additional, ok := schema["additionalProperties"].(bool); ok && !additional {
				return fmt.Errorf("unknown field %s", key)
			}
		}
	}
	if values, ok := value.([]any); ok {
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for _, item := range values {
				if err := validateSchemaValue(itemSchema, item, root); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func stringSchemaList(value any) []string {
	values, _ := value.([]string)
	return values
}
