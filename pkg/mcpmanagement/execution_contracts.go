package mcpmanagement

// Execution schemas describe stable selectors and bounded native projections.
// No command/environment/input snapshot or raw output manifest crosses MCP.
func executionOutputSchema(name string) map[string]any {
	properties := map[string]any{
		"schema_version": map[string]any{"type": "integer", "const": 1},
		"job":            map[string]any{"type": "string"}, "job_id": map[string]any{"type": "string"},
		"run_id": map[string]any{"type": "string"}, "task_index": map[string]any{"type": "integer"},
		"items": map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
		"limit": map[string]any{"type": "integer"}, "offset": map[string]any{"type": "integer"},
		"next_offset": map[string]any{"type": "integer"},
	}
	required := []string{"schema_version"}
	switch name {
	case "gregale_list_jobs", "gregale_list_job_runs", "gregale_list_job_tasks":
		required = append(required, "items", "limit", "offset", "next_offset")
		if name != "gregale_list_jobs" {
			required = append(required, "job")
		}
		if name == "gregale_list_job_tasks" {
			required = append(required, "run_id")
		}
	case "gregale_get_job":
		required = append(required, "id", "name", "status", "image_materialization_status")
		for _, field := range []string{"id", "name", "status", "image_materialization_status"} {
			properties[field] = map[string]any{"type": "string"}
		}
	case "gregale_start_job", "gregale_get_job_run", "gregale_cancel_job_run":
		required = append(required, "job", "job_id", "run_id", "status", "pending", "tasks", "observed_at")
		properties["status"] = map[string]any{"type": "string", "enum": []string{"queued", "running", "succeeded", "failed", "cancelled", "dead_letter"}}
		properties["pending"] = map[string]any{"type": "boolean"}
		properties["tasks"] = map[string]any{"type": "integer"}
		properties["observed_at"] = map[string]any{"type": "string", "format": "date-time"}
	case "gregale_get_job_logs":
		required = append(required, "job", "run_id", "task_index", "task_status", "log_content", "truncated", "max_bytes")
		properties["task_status"] = map[string]any{"type": "string"}
		properties["log_content"] = map[string]any{"type": "string"}
		properties["truncated"] = map[string]any{"type": "boolean"}
		properties["max_bytes"] = map[string]any{"type": "integer"}
	case "gregale_list_artifacts":
		required = append(required, "job", "run_id", "task_index", "items", "manifest_available", "availability_verified")
		properties["manifest_available"] = map[string]any{"type": "boolean"}
		properties["availability_verified"] = map[string]any{"type": "boolean", "const": false}
	case "gregale_read_artifact":
		required = append(required, "job", "run_id", "task_index", "name", "size_bytes", "sha256", "verified_at", "download")
		for _, field := range []string{"name", "sha256"} {
			properties[field] = map[string]any{"type": "string"}
		}
		properties["size_bytes"] = map[string]any{"type": "integer"}
		properties["verified_at"] = map[string]any{"type": "string", "format": "date-time"}
		properties["download"] = map[string]any{"type": "object", "required": []string{"url", "method", "expires_at"}, "properties": map[string]any{
			"url": map[string]any{"type": "string"}, "method": map[string]any{"type": "string", "const": "GET"},
			"expires_at": map[string]any{"type": "string", "format": "date-time"}}}
	}
	return map[string]any{"type": "object", "required": required, "properties": properties, "additionalProperties": true}
}
