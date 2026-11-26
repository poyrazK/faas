package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// MCP tools are billing routes "MCP /tools/<name>" (ADR-20261123192254), so
// --tool-weight and --tool-limit are --weight and --route-limit for one tool.

// parseToolWeights adds --tool-weight NAME=N entries to weights.
func parseToolWeights(weights map[string]int64, entries []string) (map[string]int64, error) {
	for _, entry := range entries {
		name, value, ok := strings.Cut(entry, "=")
		name = strings.TrimSpace(name)
		weight, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if !ok || err != nil || !api.ValidMCPBillingName(name) {
			return nil, fmt.Errorf("--tool-weight %q: use TOOL=N with an MCP tool name", entry)
		}
		if weights == nil {
			weights = map[string]int64{}
		}
		route := api.MCPToolBillingLabel(name)
		if _, dup := weights[route]; dup {
			return nil, fmt.Errorf("--tool-weight %q: tool given twice", entry)
		}
		weights[route] = weight
	}
	return weights, nil
}

// routeLimitEdit is one --route-limit or --tool-limit: a limit for route, or
// its removal ("none").
type routeLimitEdit struct {
	route  string
	limit  api.APIConsumerPlanRouteLimit
	remove bool
}

// parseRouteLimitEdits reads --route-limit "METHOD /template=SPEC" and
// --tool-limit TOOL=SPEC, where SPEC is N/minute, N/month, both separated by
// a comma, or none.
func parseRouteLimitEdits(routeEntries, toolEntries []string) ([]routeLimitEdit, error) {
	var edits []routeLimitEdit
	seen := map[string]bool{}
	add := func(flag, entry, route, spec string) error {
		if seen[route] {
			return fmt.Errorf("--%s %q: route given twice", flag, entry)
		}
		seen[route] = true
		edit := routeLimitEdit{route: route}
		if strings.TrimSpace(spec) == "none" {
			edit.remove = true
			edits = append(edits, edit)
			return nil
		}
		for _, part := range strings.Split(spec, ",") {
			count, window, ok := strings.Cut(strings.TrimSpace(part), "/")
			n, err := strconv.ParseInt(strings.TrimSpace(count), 10, 64)
			if !ok || err != nil || n < 1 {
				return fmt.Errorf("--%s %q: limits look like 100/minute, 5000/month or none", flag, entry)
			}
			switch strings.TrimSpace(window) {
			case "minute", "min", "m":
				edit.limit.MaxRequestsPerMinute = n
			case "month", "mo":
				edit.limit.MaxRequestsPerMonth = n
			default:
				return fmt.Errorf("--%s %q: the window is minute or month", flag, entry)
			}
		}
		edits = append(edits, edit)
		return nil
	}
	for _, entry := range routeEntries {
		at := strings.LastIndex(entry, "=")
		if at < 0 || strings.TrimSpace(entry[:at]) == "" {
			return nil, fmt.Errorf("--route-limit %q: use \"METHOD /template=100/minute\"", entry)
		}
		if err := add("route-limit", entry, strings.TrimSpace(entry[:at]), entry[at+1:]); err != nil {
			return nil, err
		}
	}
	for _, entry := range toolEntries {
		name, spec, ok := strings.Cut(entry, "=")
		name = strings.TrimSpace(name)
		if !ok || !api.ValidMCPBillingName(name) {
			return nil, fmt.Errorf("--tool-limit %q: use TOOL=1000/month with an MCP tool name", entry)
		}
		if err := add("tool-limit", entry, api.MCPToolBillingLabel(name), spec); err != nil {
			return nil, err
		}
	}
	return edits, nil
}

// applyRouteLimitEdits returns current with edits applied; nil when there
// are no edits, so an update keeps the plan's route limits.
func applyRouteLimitEdits(current map[string]api.APIConsumerPlanRouteLimit, edits []routeLimitEdit) map[string]api.APIConsumerPlanRouteLimit {
	if len(edits) == 0 {
		return nil
	}
	out := make(map[string]api.APIConsumerPlanRouteLimit, len(current)+len(edits))
	for route, limit := range current {
		out[route] = limit
	}
	for _, edit := range edits {
		if edit.remove {
			delete(out, edit.route)
			continue
		}
		out[edit.route] = edit.limit
	}
	return out
}
