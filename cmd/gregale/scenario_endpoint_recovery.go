package main

import (
	"fmt"
	"sort"
	"time"
)

func testEndpointRecoveryNames[T any](steps map[string]T) []string {
	names := make([]string, 0, len(steps))
	for name := range steps {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func validateTestEndpointRecovery(phase testLoadChaosPhaseSpec, followsFault bool) error {
	if len(phase.StepRecovery) > 100 {
		return fmt.Errorf("phase %q step_recovery may declare at most 100 endpoints", phase.Name)
	}
	for _, name := range testEndpointRecoveryNames(phase.StepRecovery) {
		if !testHTTPNamePattern.MatchString(name) {
			return fmt.Errorf("phase %q step_recovery has invalid HTTP step name %q", phase.Name, name)
		}
		endpoint := phase
		endpoint.Recovery = phase.StepRecovery[name]
		if endpoint.Recovery == nil {
			return fmt.Errorf("phase %q step_recovery.%s requires a recovery assertion", phase.Name, name)
		}
		duration, _ := time.ParseDuration(phase.Duration)
		if err := validateTestLoadRecovery(endpoint, followsFault, duration); err != nil {
			return fmt.Errorf("HTTP step %q: %w", name, err)
		}
	}
	return nil
}

func resolveTestEndpointRecovery(spec map[string]*testLoadRecoverySpec) map[string]*testLoadRecoveryConfig {
	if len(spec) == 0 {
		return nil
	}
	result := make(map[string]*testLoadRecoveryConfig, len(spec))
	for name, budget := range spec {
		result[name] = resolveTestLoadRecovery(budget)
	}
	return result
}

func testPhaseNeedsRecoveryBaseline(phase testLoadChaosPhase) bool {
	if phase.Recovery != nil && phase.Recovery.Relative != nil {
		return true
	}
	for _, r := range phase.StepRecovery {
		if r != nil && r.Relative != nil {
			return true
		}
	}
	return false
}

func testEndpointRecoverySamples(samples []testLoadRecoverySample, name string) []testLoadRecoverySample {
	selected := make([]testLoadRecoverySample, 0)
	for _, sample := range samples {
		if sample.Name == name {
			selected = append(selected, sample)
		}
	}
	return selected
}

// Include endpoint names in summaries and CI keys; an aggregate has an empty name.
func testRecoverySummaryKey(step, phase, request string) string {
	return step + "/" + phase + "/" + request
}

func testPhaseRecoveryEvidence(phase testLoadChaosPhaseEvidence) map[string]*testLoadRecoveryEvidence {
	result := make(map[string]*testLoadRecoveryEvidence, len(phase.StepRecovery)+1)
	if phase.Recovery != nil {
		result[""] = phase.Recovery
	}
	for name, r := range phase.StepRecovery {
		result[name] = r
	}
	return result
}
func testRecoveryDisplayPhase(phase, request string) string {
	if request != "" {
		return phase + "/" + request
	}
	return phase
}
