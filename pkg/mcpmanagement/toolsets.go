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
	// crons, including their scoped inventory (ADR-20261103155008).
	ToolsetProvision = "provision"
	// ToolsetOperate adds configuration, restart, scaling, plan recovery and costs.
	ToolsetOperate = "operate"
	// ToolsetAll explicitly opts into every tool. Core is the default.
	ToolsetAll = "all"
)

// toolsetOf assigns every tool a primary toolset. Shared plan helpers are also
// included in the workflow groups below. TestEveryToolHasAToolset keeps this complete.
var toolsetOf = map[string]string{
	"gregale_get_saved_plan":           ToolsetOperate,
	"gregale_check_plan":               ToolsetOperate,
	"gregale_list_actions":             ToolsetOperate,
	"gregale_get_action":               ToolsetOperate,
	"gregale_get_action_outcome":       ToolsetOperate,
	"gregale_get_environment_plan":     ToolsetRelease,
	"gregale_check_environment_plan":   ToolsetRelease,
	"gregale_list_environment_actions": ToolsetRelease,
	"gregale_get_environment_action":   ToolsetRelease,
	"gregale_get_provision":            ToolsetProvision,
	"gregale_cancel_provision":         ToolsetProvision,
	"gregale_get_config":               ToolsetOperate,
	"gregale_list_config_history":      ToolsetOperate,
	"gregale_plan_config_rollback":     ToolsetOperate,
	"gregale_plan_restart":             ToolsetOperate,
	"gregale_get_restart":              ToolsetOperate,
	"gregale_plan_scale":               ToolsetOperate,
	"gregale_apply_operation":          ToolsetOperate,
	"gregale_get_plan":                 ToolsetOperate,
	"gregale_verify_plan":              ToolsetOperate,
	"gregale_list_plans":               ToolsetOperate,
	"gregale_get_costs":                ToolsetOperate,
	"gregale_list_domains":             ToolsetProvision,
	"gregale_list_crons":               ToolsetProvision,
	"gregale_list_buckets":             ToolsetProvision,
	"gregale_list_databases":           ToolsetProvision,
	"gregale_get_storage_bindings":     ToolsetProvision,
	"gregale_plan_cron_change":         ToolsetProvision,
	"gregale_plan_domain_removal":      ToolsetProvision,
	"gregale_context":                  ToolsetCore,
	"gregale_list_apps":                ToolsetCore,
	"gregale_get_app":                  ToolsetCore,
	"gregale_list_app_deployments":     ToolsetCore,
	"gregale_get_deployment":           ToolsetCore,
	"gregale_wait_deployment":          ToolsetCore,
	"gregale_get_logs":                 ToolsetCore,
	"gregale_get_metrics":              ToolsetCore,
	"gregale_get_health":               ToolsetCore,
	"gregale_list_error_groups":        ToolsetCore,
	"gregale_plan_local_deploy":        ToolsetCore,
	"gregale_apply_local_deploy":       ToolsetCore,
	"gregale_plan_env_change":          ToolsetCore,
	"gregale_apply_env_change":         ToolsetCore,
	"gregale_promote_deployment":       ToolsetCore,
	"gregale_plan_rollback":            ToolsetCore,
	"gregale_apply_rollback":           ToolsetCore,
	"gregale_get_rollback":             ToolsetCore,
	"gregale_request_secret":           ToolsetCore,
	"gregale_get_secret_request":       ToolsetCore,
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

func toolsetIncludes(name, selected string) bool {
	if toolsetOf[name] == selected {
		return true
	}
	switch name {
	case "gregale_get_saved_plan", "gregale_check_plan", "gregale_list_actions", "gregale_get_action", "gregale_get_action_outcome":
		return selected == ToolsetRelease || selected == ToolsetDiagnose
	case "gregale_get_plan", "gregale_list_plans", "gregale_verify_plan":
		return selected == ToolsetCore || selected == ToolsetRelease || selected == ToolsetProvision
	case "gregale_get_restart":
		return selected == ToolsetCore
	case "gregale_apply_operation":
		return selected == ToolsetProvision
	}
	return false
}

// ParseToolsets reads a comma-separated toolset list such as "core,diagnose".
// An omitted selection uses core. All remains an explicit, durable selection.
func ParseToolsets(spec string) ([]string, error) {
	if spec = strings.TrimSpace(spec); spec == "" {
		return []string{ToolsetCore}, nil
	}
	seen := map[string]bool{}
	all := false
	for _, name := range strings.Split(spec, ",") {
		switch name = strings.TrimSpace(name); name {
		case ToolsetAll:
			all = true
		case ToolsetCore, ToolsetRelease, ToolsetDiagnose, ToolsetProvision, ToolsetOperate:
			seen[name] = true
		default:
			return nil, fmt.Errorf("unknown toolset %q; use core, release, diagnose, provision, operate or all", name)
		}
	}
	if all {
		return []string{ToolsetAll}, nil
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// A browser connection may narrow its catalog, never expand the operator's
// configured maximum. Nil selects a focused default; an empty effective set is
// deliberately distinct from nil when an operator later removes a toolset.
func connectionToolsets(spec string, maximum []string) ([]string, error) {
	if strings.TrimSpace(spec) == "" {
		return defaultConnectionToolsets(maximum), nil
	}
	selected, err := ParseToolsets(spec)
	if err != nil {
		return nil, err
	}
	if hasAllToolsets(selected) {
		if maximum != nil && !hasAllToolsets(maximum) {
			return append([]string{}, maximum...), nil
		}
		return selected, nil
	}
	if maximum != nil && !hasAllToolsets(maximum) {
		for _, set := range selected {
			found := false
			for _, allowed := range maximum {
				if set == allowed {
					found = true
				}
			}
			if !found {
				return nil, fmt.Errorf("toolset %s is unavailable on this server", set)
			}
		}
	}
	return selected, nil
}

func effectiveToolsets(selected, maximum []string) []string {
	if selected == nil {
		return defaultConnectionToolsets(maximum)
	}
	if maximum == nil || hasAllToolsets(maximum) {
		return selected
	}
	if hasAllToolsets(selected) {
		return append([]string{}, maximum...)
	}
	out := []string{}
	for _, set := range selected {
		for _, allowed := range maximum {
			if set == allowed {
				out = append(out, set)
				break
			}
		}
	}
	return out
}

func hasAllToolsets(sets []string) bool {
	for _, set := range sets {
		if set == ToolsetAll {
			return true
		}
	}
	return false
}

func defaultConnectionToolsets(maximum []string) []string {
	if maximum == nil || hasAllToolsets(maximum) {
		return []string{ToolsetCore}
	}
	for _, set := range maximum {
		if set == ToolsetCore {
			return []string{ToolsetCore}
		}
	}
	return append([]string{}, maximum...)
}

// toolListed reports whether the connection's toolsets include name.
func (m *management) toolListed(name string) bool {
	if m.opts.agentKey && m.opts.AllowedApps != nil {
		// App-bound native credentials deliberately cannot enumerate global
		// inventory or operate on project/environment registries.
		switch name {
		case "gregale_list_deployments", "gregale_list_environments", "gregale_get_environment", "gregale_plan_environment_promotion", "gregale_apply_environment_promotion", "gregale_get_environment_promotion", "gregale_plan_environment_rollback", "gregale_apply_environment_rollback", "gregale_plan_domain", "gregale_check_domain", "gregale_plan_cron", "gregale_plan_cron_change", "gregale_plan_domain_removal":
			return false
		}
	}
	if hasAllToolsets(m.opts.Toolsets) {
		return true
	}
	if _, ok := toolsetOf[name]; !ok {
		return false
	}
	for _, selected := range m.opts.Toolsets {
		if toolsetIncludes(name, selected) {
			return true
		}
	}
	return false
}
