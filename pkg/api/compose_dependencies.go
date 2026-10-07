package api

import (
	"fmt"
	"sort"
	"strings"
)

const (
	ComposeDependencyStarted = "service_started"
	ComposeDependencyHealthy = "service_healthy"
)

// ValidateComposeDependencyConditions checks the source-owned conditions on
// separate project apps. Completion gates need a durable job-run contract.
func ValidateComposeDependencyConditions(conditions map[string]string, dependencies []string) error {
	if len(conditions) > ProjectDependencyGateCapMax {
		return fmt.Errorf("too many dependency conditions; maximum is %d", ProjectDependencyGateCapMax)
	}
	declared := make(map[string]bool, len(dependencies))
	for _, dependency := range dependencies {
		declared[strings.ToLower(dependency)] = true
	}
	names := make([]string, 0, len(conditions))
	for name := range conditions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if name != strings.ToLower(strings.TrimSpace(name)) || name == "" || !declared[name] {
			return fmt.Errorf("dependency condition must name a declared dependency")
		}
		switch conditions[name] {
		case ComposeDependencyStarted, ComposeDependencyHealthy:
		default:
			return fmt.Errorf("dependency condition must be service_started or service_healthy; completion gates are unsupported")
		}
	}
	return nil
}
