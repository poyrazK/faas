package mcpmanagement

func toolBuildingOutputSchema(name string) map[string]any {
	props := map[string]any{"schema_version": map[string]any{"type": "integer", "const": 1}}
	required := []string{"schema_version"}
	add := func(kind string, names ...string) {
		for _, name := range names {
			props[name] = map[string]any{"type": kind}
		}
	}
	switch name {
	case "gregale_generate_tools_from_openapi":
		required = append(required, "runtime", "document_sha256", "project_sha256", "server_url", "operation_ids", "fixtures_require_review")
		add("string", "runtime", "document_sha256", "project_sha256", "server_url", "file", "content", "encoding")
		add("integer", "offset", "next_offset")
		add("boolean", "fixtures_require_review")
		props["files"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
		props["operation_ids"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	case "gregale_get_tool_template":
		required = append(required, "runtime", "template_sha256")
		add("string", "runtime", "template_sha256", "file", "content", "encoding")
		add("integer", "offset", "next_offset")
		props["files"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	case "gregale_test_tool":
		required = append(required, "app", "deployment_id", "passed", "report")
		add("string", "app", "deployment_id")
		add("boolean", "passed")
		add("object", "report")
	default:
		required = append(required, "plan_id", "action", "account_id", "app", "status", "recovery")
		add("string", "plan_id", "action", "account_id", "app", "app_id", "expected_serving_deployment_id")
		props["status"] = map[string]any{"type": "string", "enum": []string{"planned", "running", "completed", "failed", "outcome_unknown"}}
		props["expires_at"] = map[string]any{"type": "string", "format": "date-time"}
		props["recovery"] = map[string]any{"$ref": "#/$defs/recovery"}
	}
	return map[string]any{"type": "object", "required": required, "properties": props, "additionalProperties": true}
}
