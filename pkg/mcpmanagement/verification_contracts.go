// adr: 20261103202626
package mcpmanagement

import (
	"encoding/json"
	"strings"
)

func isApplyTool(name string) bool {
	return strings.HasPrefix(name, "gregale_apply_") || name == "gregale_promote_deployment"
}

func verificationSchema() map[string]any {
	statuses := []string{"required", "pending", "verified", "failed", "blocked", "unknown", "not_required"}
	return map[string]any{"type": "object", "required": []string{"status", "goal", "checks"}, "additionalProperties": false, "properties": map[string]any{
		"status": map[string]any{"type": "string", "enum": statuses},
		"goal":   map[string]any{"type": "string", "enum": []string{"manual_review", "configuration_commit", "scaling_configuration", "restart_processing_and_health", "release_readiness", "worker_generation_residency"}},
		"checks": map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []string{"criterion", "status", "reason"}, "additionalProperties": false, "properties": map[string]any{
			"criterion": map[string]any{"type": "string"}, "status": map[string]any{"type": "string", "enum": statuses}, "reason": map[string]any{"type": "string"}, "resource_id": map[string]any{"type": "string"},
		}}},
		"next": map[string]any{"$ref": "#/$defs/recovery"}, "checked_at": map[string]any{"type": "string", "format": "date-time"},
	}}
}

func deploymentSchema() map[string]any {
	return map[string]any{"type": "object", "required": []string{"id", "app_id", "status", "scope", "traffic_percent"}, "properties": map[string]any{
		"id": map[string]any{"type": "string"}, "app_id": map[string]any{"type": "string"}, "status": map[string]any{"type": "string"},
		"scope": map[string]any{"type": "string"}, "traffic_percent": map[string]any{"type": "integer"},
	}, "additionalProperties": true}
}

func strengthenResultSchema(name string, schema map[string]any) {
	props := schema["properties"].(map[string]any)
	require := func(keys ...string) { schema["required"] = append(stringSchemaList(schema["required"]), keys...) }
	if isApplyTool(name) || name == "gregale_get_plan" || name == "gregale_verify_plan" {
		props["verification"] = map[string]any{"$ref": "#/$defs/verification"}
		require("verification")
	}
	switch name {
	case "gregale_apply_deploy", "gregale_apply_upload_deploy", "gregale_apply_rollback", "gregale_promote_deployment", "gregale_apply_workload_rollout", "gregale_apply_service_abort":
		require("app", "deployment")
		props["deployment"] = deploymentSchema()
	case "gregale_apply_local_deploy":
		require("app")
		props["deployment"] = deploymentSchema()
		schema["anyOf"] = []any{map[string]any{"required": []string{"deployment"}}, map[string]any{"properties": map[string]any{"app": map[string]any{"type": "object", "required": []string{"id", "slug", "status"}}}}}
	case "gregale_apply_env_change", "gregale_apply_operation":
		require("app", "scope", "kind", "result")
		props["scope"], props["kind"] = map[string]any{"type": "string"}, map[string]any{"type": "string"}
		if name == "gregale_apply_env_change" {
			props["kind"] = map[string]any{"const": "env", "type": "string"}
			props["result"] = map[string]any{"type": "object", "required": []string{"updated_keys", "revision", "config_change"}, "properties": map[string]any{
				"updated_keys": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "revision": map[string]any{"type": "integer"}, "config_change": map[string]any{"type": "object", "required": []string{"revision"}},
			}}
		} else {
			schema["anyOf"] = []any{
				map[string]any{"properties": map[string]any{"kind": map[string]any{"const": "restart"}, "result": map[string]any{"type": "object", "required": []string{"wake_id"}, "properties": map[string]any{"wake_id": map[string]any{"type": "string"}}}}},
				map[string]any{"properties": map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"env", "scale", "config_rollback", "config_rollback_guarded", "cron_update", "cron_delete", "domain_delete"}}}},
			}
		}
	case "gregale_apply_route_budgets":
		require("app_id", "scope", "revision", "app_revision", "config")
		props["app_id"], props["scope"] = map[string]any{"type": "string"}, map[string]any{"type": "string"}
		props["revision"], props["app_revision"], props["config"] = map[string]any{"type": "integer"}, map[string]any{"type": "integer"}, map[string]any{"type": "object"}
	case "gregale_apply_regression_action":
		require("app_id", "deployment_id", "route", "version", "state")
		for _, key := range []string{"app_id", "deployment_id", "route", "version", "state"} {
			props[key] = map[string]any{"type": "string"}
		}
	case "gregale_apply_environment_promotion", "gregale_apply_environment_rollback":
		confirmed := map[string]any{"required": []string{"promotion_id", "project_slug", "from_environment", "to_environment"}}
		for _, key := range []string{"promotion_id", "project_slug", "from_environment", "to_environment"} {
			props[key] = map[string]any{"type": "string"}
		}
		schema["anyOf"] = []any{confirmed}
		if name == "gregale_apply_environment_promotion" {
			schema["anyOf"] = append(schema["anyOf"].([]any), map[string]any{"required": []string{"status", "review_url", "expires_at"}, "properties": map[string]any{"status": map[string]any{"const": "approval_pending"}, "review_url": map[string]any{"type": "string"}, "expires_at": map[string]any{"type": "string", "format": "date-time"}}})
		}
	case "gregale_apply_provision":
		schema["anyOf"] = []any{
			map[string]any{"required": []string{"schema_version", "status", "recovery"}, "properties": map[string]any{"schema_version": map[string]any{"const": 1}, "status": map[string]any{"type": "string", "enum": provisionStatuses}, "recovery": map[string]any{"$ref": "#/$defs/recovery"}}},
			map[string]any{"required": []string{"app", "kind", "result"}, "properties": map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"domain", "bucket", "postgres", "cron"}}}},
		}
	case "gregale_get_restart":
		require("wake_id", "status", "attempts", "requested_at")
		props["wake_id"] = map[string]any{"type": "string"}
		props["status"] = map[string]any{"type": "string", "enum": []string{"queued", "retrying", "running", "completed", "failed"}}
		props["attempts"] = map[string]any{"type": "integer"}
		props["requested_at"] = map[string]any{"type": "string", "format": "date-time"}
	}
	if mode := paginationMode(name); mode != "" {
		require("items", "pagination")
		props["pagination"] = paginationSchema(mode)
		props["items"] = map[string]any{"type": "array", "items": inventoryItemSchema(name)}
	}
}

func inventoryItemSchema(name string) map[string]any {
	if name == "gregale_list_app_deployments" || name == "gregale_list_deployments" {
		return deploymentSchema()
	}
	if name == "gregale_list_plans" {
		return toolSuccessSchema("gregale_get_plan")
	}
	keys := []string{"id"}
	switch name {
	case "gregale_list_apps":
		keys = []string{"id", "slug", "status"}
	case "gregale_list_buckets", "gregale_list_databases":
		keys = []string{"id", "name"}
	case "gregale_list_crons":
		keys = []string{"id", "app_id"}
	case "gregale_list_domains":
		keys = []string{"domain"}
	}
	props := map[string]any{}
	for _, key := range keys {
		props[key] = map[string]any{"type": "string"}
	}
	return map[string]any{"type": "object", "required": keys, "properties": props, "additionalProperties": true}
}

// MCP-owned inventory envelopes have explicit end-of-list evidence. Native
// diagnostic projections keep their separate pagination and evidence contracts.
func paginationMode(name string) string {
	switch name {
	case "gregale_list_apps", "gregale_list_domains", "gregale_list_crons", "gregale_list_buckets", "gregale_list_databases", "gregale_get_storage_bindings", "gregale_list_instances", "gregale_list_plans":
		return "offset"
	case "gregale_list_app_deployments", "gregale_list_deployments":
		return "before"
	}
	return ""
}

func paginationSchema(mode string) map[string]any {
	next, kind := "next_offset", "integer"
	if mode == "before" {
		next, kind = "next_before", "string"
	}
	props := map[string]any{"mode": map[string]any{"type": "string", "const": mode}, "has_more": map[string]any{"type": "boolean"}}
	out := map[string]any{"type": "object", "required": []string{"mode", "has_more"}, "properties": props, "additionalProperties": false}
	if mode == "none" {
		props["has_more"] = map[string]any{"type": "boolean", "const": false}
	} else {
		props[next] = map[string]any{"type": kind}
		out["anyOf"] = []any{map[string]any{"properties": map[string]any{"has_more": map[string]any{"const": false}}}, map[string]any{"required": []string{next}, "properties": map[string]any{"has_more": map[string]any{"const": true}}}}
	}
	return out
}

func addToolPagination(name string, data json.RawMessage) (json.RawMessage, error) {
	mode := paginationMode(name)
	if mode == "" {
		return data, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return nil, invalid("invalid_response", "inventory must be an object")
	}
	page := map[string]any{"mode": mode, "has_more": false}
	switch mode {
	case "before":
		var next string
		if raw, ok := fields["next_before"]; ok {
			if json.Unmarshal(raw, &next) != nil {
				return nil, invalid("invalid_response", "inventory cursor must be a string")
			}
			if next != "" {
				page["has_more"], page["next_before"] = true, next
			}
		}
	case "offset":
		if raw, ok := fields["next_offset"]; ok {
			var next int64
			if json.Unmarshal(raw, &next) != nil || next <= 0 {
				return nil, invalid("invalid_response", "inventory offset must be a positive integer")
			}
			page["has_more"], page["next_offset"] = true, next
		}
	}
	fields["pagination"], _ = json.Marshal(page)
	out, err := json.Marshal(fields)
	if err != nil || len(out) > maxResultBytes {
		return nil, invalid("response_too_large", "pagination exceeds the MCP output bound")
	}
	return out, nil
}
