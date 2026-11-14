package mcpmanagement

func invocationOutputSchema() map[string]any {
	props := map[string]any{"schema_version": map[string]any{"type": "integer", "const": 1}, "recovery": map[string]any{"$ref": "#/$defs/recovery"}}
	for _, name := range []string{"invocation_id", "version", "name", "account_id", "app", "deployment_id", "result_base64"} {
		props[name] = map[string]any{"type": "string"}
	}
	props["status"] = map[string]any{"type": "string", "enum": []string{"queued", "running", "completed", "tool_error", "not_started", "cancelled", "outcome_unknown"}}
	for _, name := range []string{"cancellation_requested", "effects_may_have_occurred", "result_available"} {
		props[name] = map[string]any{"type": "boolean"}
	}
	for _, name := range []string{"result_offset", "next_result_offset"} {
		props[name] = map[string]any{"type": "integer"}
	}
	return map[string]any{"type": "object", "required": []string{"schema_version", "invocation_id", "version", "name", "status", "cancellation_requested", "effects_may_have_occurred", "result_available", "recovery"}, "properties": props, "additionalProperties": true}
}
