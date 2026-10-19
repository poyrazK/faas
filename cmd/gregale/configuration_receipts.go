package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// The update response confirms persisted intent, not runtime convergence.
// Print names only: public-auth credentials and environment values stay private.
func printAppSettingsReceipt(slug, environment string, request api.UpdateAppRequest) {
	fields := appSettingsFieldLabels(request)
	if environment == "" {
		PrintOK(osStdout, "Updated app-level settings for %s", compactInspectFailureText(slug))
	} else {
		PrintOK(osStdout, "Updated settings for %s (environment=%s)", compactInspectFailureText(slug), compactInspectFailureText(environment))
	}
	if len(fields) > 0 {
		_, _ = fmt.Fprintf(osStdout, "  Saved settings: %s\n", strings.Join(fields, ", "))
	}
	_, _ = fmt.Fprintln(osStdout, "Saved settings do not confirm that running workloads have applied them. Routing, scheduling, and boot settings take effect through their respective controllers.")
	if environment == "" && (request.RAMMB != nil || request.ResourceProfile != nil) {
		// A VM's memory is fixed at boot (CPU changes apply live). Running
		// instances keep the old size, and are billed at it, until they park
		// or restart: an always-on --min instance never does on its own
		// (production-us hunt #9).
		PrintWarn(osStdout, "Memory changes apply to instances started from now on; running instances keep their current memory and billing until they park or restart. Apply now: %s app %s restart", recoveryCLI(), recoveryArgument(slug))
	}
	command := recoveryCLI() + " app " + recoveryArgument(slug)
	if environment != "" {
		command += " --environment " + recoveryArgument(environment)
		_, _ = fmt.Fprintln(osStdout, "This update targets the named environment's settings; it does not confirm a deployment or release there.")
	}
	_, _ = fmt.Fprintf(osStdout, "\nReview saved settings (POSIX shells):\n  %s\n", command)
	if environment == "" {
		_, _ = fmt.Fprintf(osStdout, "Review current operations and health:\n  %s inspect %s\n", recoveryCLI(), recoveryArgument(slug))
	} else {
		_, _ = fmt.Fprintln(osStdout, "Review that environment's release and workload state before deciding whether another release is needed.")
	}
}

// Share the value-free field projection between the preview and saved receipt.
func appSettingsFieldNames(request api.UpdateAppRequest) []string {
	fields := []string{}
	if raw, err := json.Marshal(request); err == nil {
		var values map[string]json.RawMessage
		if json.Unmarshal(raw, &values) == nil {
			// This legacy field serializes even when nil; nil means unchanged.
			if request.StartCommand == nil {
				delete(values, "StartCommand")
			}
			for key := range values {
				fields = append(fields, key)
			}
		}
	}
	sort.Strings(fields)
	return fields
}

func printAppSettingsTarget(slug, environment, source string, request api.UpdateAppRequest) {
	if jsonOutput {
		return
	}
	profileSource := "selected connection profile"
	if profileOverride != "" {
		profileSource = "explicit --profile"
	}
	target := "app-level settings (no named environment selected)"
	if environment != "" {
		target = "environment=" + compactInspectFailureText(environment) + " (" + source + ")"
	}
	_, _ = fmt.Fprintf(osStderr, "Target: connection profile=%s (%s); app=%s (explicit argument); %s\n",
		compactInspectFailureText(currentProfile()), profileSource, compactInspectFailureText(slug), target)
	fields := appSettingsFieldLabels(request)
	if len(fields) > 0 {
		_, _ = fmt.Fprintf(osStderr, "  Settings to change: %s\n", strings.Join(fields, ", "))
	}
}

// Keep raw field names in the request projection; translate only human output.
func appSettingsFieldLabels(request api.UpdateAppRequest) []string {
	fields := appSettingsFieldNames(request)
	for i, field := range fields {
		if label, ok := appScalePlanFieldLabels[field]; ok {
			fields[i] = label
		} else if label, ok := appSettingsAdditionalLabels[field]; ok {
			fields[i] = label
		}
	}
	sort.Strings(fields)
	return fields
}

var appSettingsAdditionalLabels = map[string]string{
	"visibility":                      "Public edge exposure",
	"allowed_service_callers":         "Allowed service callers",
	"allowed_service_call_scopes":     "Allowed service call scopes",
	"service_binding_targets":         "Service binding targets",
	"service_reliability":             "Service dependency reliability",
	"service_binding_policy":          "Service binding authorization",
	"service_binding_transport":       "Service binding transport",
	"execution_mode":                  "Execution mode",
	"restart_policy":                  "Restart policy",
	"after_restore":                   "After-restore hook",
	"before_checkpoint":               "Before-checkpoint hook",
	"pause":                           "Pause tier",
	"profiling":                       "Profiling",
	"startup_deadline_s":              "Startup deadline (seconds)",
	"max_retries":                     "Maximum retries",
	"stop_grace_period_s":             "Stop grace period (seconds)",
	"stop_signal":                     "Stop signal",
	"request_rate_limit_rps":          "Request rate limit (requests per second)",
	"request_rate_limit_burst":        "Request rate limit burst",
	"retry_policy":                    "Invocation retry policy",
	"service_replicas":                "Service replicas",
	"worker_replicas":                 "Worker replicas",
	"ports":                           "Workload ports",
	"favicon":                         "Favicon",
	"robots_txt":                      "Robots policy",
	"pre_auth_rate_limit":             "Rate limiting before authentication",
	"session_affinity":                "Session affinity",
	"version_affinity_cookie":         "Version affinity cookie",
	"version_affinity_managed_cookie": "Managed version affinity cookie",
	"revision_pin_ttl_seconds":        "Revision pin lifetime (seconds)",
	"egress_allowlist":                "Outbound IP allowlist",
	"egress_ports":                    "Outbound TCP ports",
	"streaming_enabled":               "Streaming responses",
	"websocket_enabled":               "WebSocket support",
	"route_metrics_enabled":           "Route metrics",
	"only_allow_declared_routes":      "Declared routes enforcement",
	"declared_routes":                 "Declared routes",
	"maintenance_mode":                "Maintenance mode",
	"require_signed":                  "Signed image requirement",
	"consumer_auth_mode":              "Customer API key authentication",
	"platform_tenant_required":        "Verified tenant identity requirement",
	"eviction_priority":               "Eviction priority",
	"overflow_node":                   "Overflow node",
	"cors_default_enabled":            "Default CORS policy",
	"cors_default_origins":            "Default allowed CORS origins",
	"StartCommand":                    "Start command",
}
