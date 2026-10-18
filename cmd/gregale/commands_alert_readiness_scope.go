package main

import "github.com/onebox-faas/faas/pkg/api"

func alertRuleDisplayName(r api.AlertRuleResponse) string {
	if r.Metric != api.MetricMCPReadinessUnhealthy {
		return r.Name
	}
	scope := r.MCPReadinessScope
	if scope == "" {
		scope = "all environments"
	}
	return r.Name + " [" + scope + "]"
}
