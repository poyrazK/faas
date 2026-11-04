package mcpmanagement

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Schemas describe stable envelope fields while allowing additive native
// evidence. Nested diagnostic projections retain their existing versioned
// contracts; this does not pretend unknown evidence fields are fixed.
func toolOutputSchema(name string) map[string]any {
	defs := map[string]any{"recovery": recoverySchema()}
	if isApplyTool(name) || name == "gregale_get_plan" || name == "gregale_verify_plan" || name == "gregale_list_plans" {
		defs["verification"] = verificationSchema()
	}
	return map[string]any{"type": "object", "anyOf": []any{toolSuccessSchema(name), toolErrorSchema()}, "$defs": defs}
}

func toolSuccessSchema(name string) map[string]any {
	props := map[string]any{}
	var required []string
	add := func(kind string, names ...string) {
		for _, n := range names {
			props[n] = map[string]any{"type": kind}
		}
	}
	switch {
	case strings.HasPrefix(name, "gregale_plan_"):
		required = []string{"plan_id", "action", "expires_at"}
		add("string", "plan_id", "action", "account_id", "app", "app_id", "scope", "reason", "traffic_effect", "expires_at")
		add("object", "operation", "source", "local_source", "env_change", "provision")
	case strings.HasPrefix(name, "gregale_apply_") || name == "gregale_promote_deployment":
		required = []string{"plan_id"}
		add("string", "plan_id")
		props["app"] = map[string]any{"type": []string{"string", "object"}}
		add("object", "deployment", "result")
	case strings.HasPrefix(name, "gregale_list_"):
		props["items"] = map[string]any{"type": "array", "items": map[string]any{"type": "object"}}
		add("integer", "total", "next_offset")
		add("string", "next_before", "next_cursor")
	case name == "gregale_context":
		required = []string{"profile", "api_base", "read_only", "account", "capabilities", "toolsets"}
		add("object", "account", "request_access")
		add("boolean", "read_only")
		add("string", "profile", "api_base")
	case name == "gregale_create_source_upload":
		add("string", "upload_id", "upload_url", "method", "expires_at", "command", "next")
		add("integer", "max_bytes")
	case name == "gregale_get_plan" || name == "gregale_verify_plan":
		required = []string{"schema_version", "plan_id", "action", "status", "expires_at", "recovery"}
		add("string", "plan_id", "app", "action", "scope", "status", "expires_at")
		props["status"] = map[string]any{"type": "string", "enum": planStatuses}
		props["schema_version"] = map[string]any{"type": "integer", "const": 1}
		props["recovery"] = map[string]any{"$ref": "#/$defs/recovery"}
		props["problem"] = toolErrorSchema()
		props["expires_at"] = map[string]any{"type": "string", "format": "date-time"}
	case name == "gregale_get_provision" || name == "gregale_cancel_provision":
		required = []string{"schema_version", "plan_id", "status", "recovery"}
		add("string", "plan_id", "operation_id", "status", "phase", "resource_id", "binding_id")
		add("object", "next", "result")
		props["schema_version"] = map[string]any{"type": "integer", "const": 1}
		props["problem"] = map[string]any{"anyOf": []any{toolErrorSchema(), map[string]any{"type": "null"}}}
		props["status"] = map[string]any{"type": "string", "enum": provisionStatuses}
		props["recovery"] = map[string]any{"$ref": "#/$defs/recovery"}
	case name == "gregale_preflight":
		required = []string{"app_id", "scope", "operation", "checked_at", "checks", "authorization_preview"}
		add("string", "app_id", "scope", "project_id", "environment_id", "operation", "basis", "limitations")
		props["operation"] = map[string]any{"type": "string", "enum": []string{"deploy", "restart", "scale", "env_change"}}
		props["checked_at"] = map[string]any{"type": "string", "format": "date-time"}
		add("object", "checks")
		props["authorization_preview"] = map[string]any{"type": []string{"object", "null"}}
	case name == "gregale_get_costs":
		required = []string{"app_id", "app_slug", "scope", "currency", "period_start", "period_end", "as_of", "known_usage_millicents", "meters", "scope_description", "bill_estimate_available", "missing_bill_components"}
		add("string", "app_id", "app_slug", "scope", "currency", "scope_description")
		for _, field := range []string{"period_start", "period_end", "as_of"} {
			props[field] = map[string]any{"type": "string", "format": "date-time"}
		}
		add("integer", "known_usage_millicents")
		props["bill_estimate_available"] = map[string]any{"type": "boolean", "const": false}
		props["meters"] = map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []string{"meter", "coverage", "quantity", "net_millicents", "allocations"}}}
		props["missing_bill_components"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	case name == "gregale_get_app":
		required = []string{"id", "slug", "status"}
		add("string", "id", "slug", "type", "status", "url", "canonical_url")
		add("integer", "ram_mb", "cpu_millicores", "max_concurrency", "min_instances")
	case name == "gregale_get_deployment":
		required = []string{"id", "app_id", "status"}
		add("string", "id", "app_id", "status", "scope", "kind")
		add("integer", "traffic_percent")
	}
	schema := map[string]any{"type": "object", "properties": props, "additionalProperties": true}
	if len(required) != 0 {
		schema["required"] = required
	}
	strengthenResultSchema(name, schema)
	return schema
}

func toolIdempotent(name string, readOnly bool) bool {
	if strings.HasPrefix(name, "gregale_plan_") || name == "gregale_request_secret" || name == "gregale_create_source_upload" {
		return false
	}
	return readOnly || strings.HasPrefix(name, "gregale_apply_") || name == "gregale_promote_deployment" || name == "gregale_check_domain"
}

// Native result DTOs keep their existing fields while every apply result also
// identifies the MCP plan, including receipts written by older bridges.
func bindToolPlanID(name string, args any, data json.RawMessage) (json.RawMessage, error) {
	if !strings.HasPrefix(name, "gregale_apply_") && name != "gregale_promote_deployment" {
		return data, nil
	}
	var lookup planLookupArgs
	raw, _ := json.Marshal(args)
	if json.Unmarshal(raw, &lookup) != nil || lookup.PlanID == "" {
		return nil, invalid("invalid_response", "apply result has no plan identity")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return nil, invalid("invalid_response", "apply result must be an object")
	}
	if raw, ok := fields["plan_id"]; ok {
		var id string
		if json.Unmarshal(raw, &id) != nil || id != lookup.PlanID {
			return nil, invalid("invalid_response", "apply result plan identity mismatch")
		}
		return data, nil
	}
	fields["plan_id"], _ = json.Marshal(lookup.PlanID)
	result, err := json.Marshal(fields)
	if err != nil || len(result) > maxResultBytes {
		return nil, invalid("response_too_large", "apply result exceeds the MCP output bound")
	}
	return result, nil
}

func validateToolEnvelope(name string, data json.RawMessage) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return invalid("invalid_response", "tool output must be an object")
	}
	var value any
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if d.Decode(&value) != nil {
		return invalid("invalid_response", "invalid tool output")
	}
	root := toolOutputSchema(name)
	if err := validateSchemaValue(toolSuccessSchema(name), value, root); err != nil {
		return invalid("invalid_response", "tool output differs from its schema: "+err.Error())
	}
	return nil
}
