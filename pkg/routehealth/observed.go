package routehealth

import (
	"fmt"
	"time"
)

// RouteLabel is the request telemetry route label a selector matches.
func RouteLabel(method, path string) string { return method + " " + path }

// UnobservedWarnings names the selectors whose route label no request matched
// within lookback (ADR-20261028110044). An enforce guard on such a route holds
// a canary on unknown evidence until the hold deadline, and a monitor on one
// never reports; usually the method or path does not match the route label.
func UnobservedWarnings(labels, observed []string, lookback time.Duration) []string {
	seen := make(map[string]bool, len(observed))
	for _, label := range observed {
		seen[label] = true
	}
	days := max(int(lookback/(24*time.Hour)), 1)
	unit := "days"
	if days == 1 {
		unit = "day"
	}
	var warnings []string
	for _, label := range labels {
		if seen[label] {
			continue
		}
		seen[label] = true
		warnings = append(warnings, fmt.Sprintf("%s: no requests matched this selector in the last %d %s; selectors match the route label exactly", label, days, unit))
	}
	return warnings
}
