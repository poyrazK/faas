package main

import (
	"fmt"
	"sort"
	"time"
)

// Phase budgets are independent of whole-load step and aggregate phase budgets.
// Explicit sample floors prevent an endpoint that is starved of traffic passing.
func validateTestPhaseStepBudgets(phase testLoadChaosPhaseSpec) error {
	names := make([]string, 0, len(phase.StepThresholds))
	for name := range phase.StepThresholds {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		budget := phase.StepThresholds[name]
		if !testHTTPNamePattern.MatchString(name) || budget.MinSamples < 1 || (budget.P95 == "" && budget.P99 == "" && budget.ErrorRate == nil && budget.SuccessRate == nil) {
			return fmt.Errorf("load.chaos_schedule phase %q step_thresholds.%s requires a valid HTTP step name, positive min_samples and at least one latency or rate budget", phase.Name, name)
		}
		if err := validateTestLoadThresholds(budget.P95, budget.P99, budget.ErrorRate, budget.SuccessRate, budget.MinSamples); err != nil {
			return fmt.Errorf("load.chaos_schedule phase %q step_thresholds.%s: %w", phase.Name, name, err)
		}
	}
	return nil
}

func resolveTestPhaseStepBudgets(spec map[string]testLoadStepThresholdSpec) map[string]testLoadStepThresholdConfig {
	if len(spec) == 0 {
		return nil
	}
	result := make(map[string]testLoadStepThresholdConfig, len(spec))
	for name, budget := range spec {
		p95, _ := time.ParseDuration(budget.P95)
		p99, _ := time.ParseDuration(budget.P99)
		result[name] = testLoadStepThresholdConfig{P95: p95, P99: p99, ErrorRate: budget.ErrorRate, SuccessRate: budget.SuccessRate, MinSamples: budget.MinSamples}
	}
	return result
}
