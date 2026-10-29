package mcpmanagement

import (
	"fmt"
	"sort"
	"strings"
)

// Toolsets let a connection list only the tools its agent needs. A client
// loads every listed tool's schema into context on every turn, so a lean
// catalog is cheaper and easier to choose from (ADR-20261026152009).
// Resources and prompts are always listed.
const (
	// ToolsetCore inspects apps, deploys a directory, sets variables, and
	// promotes or rolls back: an agent's everyday loop.
	ToolsetCore = "core"
	// ToolsetRelease adds image and Git source releases, service and worker
	// rollouts, named environments and route budgets.
	ToolsetRelease = "release"
	// ToolsetDiagnose adds request, performance, wake, trace and regression
	// diagnostics.
	ToolsetDiagnose = "diagnose"
	// ToolsetProvision adds custom domains, buckets, managed PostgreSQL and
	// crons on local connections (ADR-20261026174727).
	ToolsetProvision = "provision"
	// ToolsetAll lists every tool. It is the default.
	ToolsetAll = "all"
)

// toolsetOf assigns every tool to exactly one toolset. A tool missing here is
// never listed; TestEveryToolHasAToolset keeps the map complete.
var toolsetOf = map[string]string{
	"gregale_context":              ToolsetCore,
	"gregale_list_apps":            ToolsetCore,
	"gregale_get_app":              ToolsetCore,
	"gregale_list_app_deployments": ToolsetCore,
	"gregale_get_deployment":       ToolsetCore,
	"gregale_wait_deployment":      ToolsetCore,
	"gregale_get_logs":             ToolsetCore,
	"gregale_get_metrics":          ToolsetCore,
	"gregale_get_health":           ToolsetCore,
	"gregale_list_error_groups":    ToolsetCore,
	"gregale_plan_local_deploy":    ToolsetCore,
	"gregale_apply_local_deploy":   ToolsetCore,
	"gregale_plan_env_change":      ToolsetCore,
	"gregale_apply_env_change":     ToolsetCore,
	"gregale_promote_deployment":   ToolsetCore,
	"gregale_plan_rollback":        ToolsetCore,
	"gregale_apply_rollback":       ToolsetCore,
	"gregale_get_rollback":         ToolsetCore,
	"gregale_request_secret":       ToolsetCore,
	"gregale_get_secret_request":   ToolsetCore,
	// Hosted grants deploy code they wrote by upload (ADR-20261026195952).
	"gregale_create_source_upload": ToolsetCore,
	"gregale_plan_upload_deploy":   ToolsetCore,
	"gregale_apply_upload_deploy":  ToolsetCore,

	"gregale_list_deployments":            ToolsetRelease,
	"gregale_preflight":                   ToolsetRelease,
	"gregale_plan_deploy":                 ToolsetRelease,
	"gregale_apply_deploy":                ToolsetRelease,
	"gregale_plan_source_deploy":          ToolsetRelease,
	"gregale_plan_workload_rollout":       ToolsetRelease,
	"gregale_apply_workload_rollout":      ToolsetRelease,
	"gregale_plan_service_abort":          ToolsetRelease,
	"gregale_apply_service_abort":         ToolsetRelease,
	"gregale_get_rollout":                 ToolsetRelease,
	"gregale_watch_rollout":               ToolsetRelease,
	"gregale_list_environments":           ToolsetRelease,
	"gregale_get_environment":             ToolsetRelease,
	"gregale_plan_environment_promotion":  ToolsetRelease,
	"gregale_apply_environment_promotion": ToolsetRelease,
	"gregale_get_environment_promotion":   ToolsetRelease,
	"gregale_plan_environment_rollback":   ToolsetRelease,
	"gregale_apply_environment_rollback":  ToolsetRelease,
	"gregale_get_route_budgets":           ToolsetRelease,
	"gregale_plan_route_budgets":          ToolsetRelease,
	"gregale_apply_route_budgets":         ToolsetRelease,

	"gregale_get_request":                      ToolsetDiagnose,
	"gregale_list_requests":                    ToolsetDiagnose,
	"gregale_get_request_evidence":             ToolsetDiagnose,
	"gregale_get_request_analytics":            ToolsetDiagnose,
	"gregale_get_request_analytics_timeseries": ToolsetDiagnose,
	"gregale_list_error_requests":              ToolsetDiagnose,
	"gregale_get_profiles":                     ToolsetDiagnose,
	"gregale_get_wake_history":                 ToolsetDiagnose,
	"gregale_get_wake_timeline":                ToolsetDiagnose,
	"gregale_get_debug_coverage":               ToolsetDiagnose,
	"gregale_get_dependency_latency":           ToolsetDiagnose,
	"gregale_get_critical_paths":               ToolsetDiagnose,
	"gregale_compare_deployments":              ToolsetDiagnose,
	"gregale_list_regressions":                 ToolsetDiagnose,
	"gregale_list_fleet_regressions":           ToolsetDiagnose,
	"gregale_plan_regression_action":           ToolsetDiagnose,
	"gregale_apply_regression_action":          ToolsetDiagnose,
	"gregale_get_trace":                        ToolsetDiagnose,
	"gregale_get_incident_bundle":              ToolsetDiagnose,
	"gregale_get_running_causes":               ToolsetDiagnose,
	"gregale_list_instances":                   ToolsetDiagnose,
	"gregale_get_routes":                       ToolsetDiagnose,
	"gregale_get_operational_summary":          ToolsetDiagnose,

	"gregale_plan_domain":     ToolsetProvision,
	"gregale_plan_bucket":     ToolsetProvision,
	"gregale_plan_postgres":   ToolsetProvision,
	"gregale_plan_cron":       ToolsetProvision,
	"gregale_check_domain":    ToolsetProvision,
	"gregale_apply_provision": ToolsetProvision,
}

// ParseToolsets reads a comma-separated toolset list such as "core,diagnose".
// "all" (or an empty list) selects every tool and returns nil.
func ParseToolsets(spec string) ([]string, error) {
	if spec = strings.TrimSpace(spec); spec == "" {
		return nil, nil
	}
	seen := map[string]bool{}
	for _, name := range strings.Split(spec, ",") {
		switch name = strings.TrimSpace(name); name {
		case ToolsetAll:
			return nil, nil
		case ToolsetCore, ToolsetRelease, ToolsetDiagnose, ToolsetProvision:
			seen[name] = true
		default:
			return nil, fmt.Errorf("unknown toolset %q; use core, release, diagnose, provision or all", name)
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// toolListed reports whether the connection's toolsets include name.
func (m *management) toolListed(name string) bool {
	if m.opts.Toolsets == nil {
		return true
	}
	set, ok := toolsetOf[name]
	if !ok {
		return false
	}
	for _, selected := range m.opts.Toolsets {
		if selected == set {
			return true
		}
	}
	return false
}
