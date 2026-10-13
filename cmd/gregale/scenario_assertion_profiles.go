package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
)

type testAssertionProfile struct {
	RequestDeadlines   map[string]testRequestDeadlineSpec   `yaml:"request_deadlines,omitempty"`
	DependencyAttempts *testRetryAmplificationSpec          `yaml:"dependency_attempts,omitempty"`
	ConnectionAttempts *testRetryAmplificationSpec          `yaml:"connection_attempts,omitempty"`
	HealthyRoutes      map[string]testLoadStepThresholdSpec `yaml:"healthy_routes,omitempty"`
	RecoveryDeadlines  map[string]*testLoadRecoverySpec     `yaml:"recovery_deadlines,omitempty"`
}

func validateTestAssertionProfiles(profiles map[string]testAssertionProfile) error {
	if len(profiles) > 100 {
		return fmt.Errorf("assertion_profiles may declare at most 100 profiles")
	}
	for _, name := range testEndpointRecoveryNames(profiles) {
		profile := profiles[name]
		if !testHTTPNamePattern.MatchString(name) || len(profile.HealthyRoutes)+len(profile.RecoveryDeadlines)+len(profile.RequestDeadlines) == 0 && profile.DependencyAttempts == nil && profile.ConnectionAttempts == nil {
			return fmt.Errorf("assertion profile %q requires a valid name and at least one route assertion", name)
		}
		if err := validateTestRequestDeadlines(profile.RequestDeadlines); err != nil {
			return fmt.Errorf("assertion profile %q: %w", name, err)
		}
		if err := validateTestAssertionHealthyRoutes(profile.HealthyRoutes); err != nil {
			return fmt.Errorf("assertion profile %q: %w", name, err)
		}
		if profile.ConnectionAttempts != nil && profile.ConnectionAttempts.PerRequest {
			return fmt.Errorf("assertion profile %q per_request requires HTTP dependency_attempts", name)
		}
		if profile.DependencyAttempts != nil && profile.ConnectionAttempts != nil {
			return fmt.Errorf("assertion profile %q cannot combine dependency_attempts and connection_attempts: they require different fault rules", name)
		}
		for _, budget := range []struct {
			field string
			spec  *testRetryAmplificationSpec
		}{{"dependency_attempts", profile.DependencyAttempts}, {"connection_attempts", profile.ConnectionAttempts}} {
			if err := validateTestAttemptBudget(name, budget.field, budget.spec); err != nil {
				return fmt.Errorf("assertion profile %q: %w", name, err)
			}
		}
		phase := testLoadChaosPhaseSpec{Name: name, Duration: "5m", StepRecovery: profile.RecoveryDeadlines}
		if err := validateTestEndpointRecovery(phase, true); err != nil {
			return fmt.Errorf("assertion profile %q: %w", name, err)
		}
	}
	return nil
}

func validateTestAssertionHealthyRoutes(routes map[string]testLoadStepThresholdSpec) error {
	if len(routes) > 100 {
		return fmt.Errorf("healthy_routes may declare at most 100 routes")
	}
	for _, name := range testEndpointRecoveryNames(routes) {
		budget := routes[name]
		if !testHTTPNamePattern.MatchString(name) || budget.MinSamples < 1 || (budget.P95 == "" && budget.P99 == "") || (budget.ErrorRate == nil && budget.SuccessRate == nil) {
			return fmt.Errorf("healthy_routes.%s requires latency, success/error rate and positive min_samples budgets", name)
		}
		if err := validateTestLoadThresholds(budget.P95, budget.P99, budget.ErrorRate, budget.SuccessRate, budget.MinSamples); err != nil {
			return fmt.Errorf("healthy_routes.%s: %w", name, err)
		}
	}
	return nil
}

// Compile profiles into the existing endpoint evaluators before schedule expansion.
// Copies isolate routes, phases and scenarios from later matrix/sweep overrides.
func expandTestAssertionProfiles(scenario testScenario, profiles map[string]testAssertionProfile) (testScenario, error) {
	var err error
	scenario.Load, err = expandTestLoadAssertionProfiles(scenario.Load, profiles)
	if err != nil {
		return scenario, err
	}
	scenario.Steps = slices.Clone(scenario.Steps)
	for i := range scenario.Steps {
		scenario.Steps[i].Load, err = expandTestLoadAssertionProfiles(scenario.Steps[i].Load, profiles)
		if err != nil {
			return scenario, fmt.Errorf("step %q: %w", scenario.Steps[i].Name, err)
		}
	}
	return scenario, nil
}

func expandTestLoadAssertionProfiles(load *testLoadSpec, profiles map[string]testAssertionProfile) (*testLoadSpec, error) {
	if load == nil {
		return nil, nil
	}
	result := *load
	result.ChaosSchedule = slices.Clone(load.ChaosSchedule)
	for i := range result.ChaosSchedule {
		phase := &result.ChaosSchedule[i]
		if len(phase.Assertions) > 100 {
			return nil, fmt.Errorf("phase %q may reference at most 100 assertion profiles", phase.Name)
		}
		phase.StepThresholds = maps.Clone(phase.StepThresholds)
		phase.StepRecovery = maps.Clone(phase.StepRecovery)
		phase.RequestDeadlines = maps.Clone(phase.RequestDeadlines)
		seen := map[string]bool{}
		for _, name := range phase.Assertions {
			profile, ok := profiles[name]
			if !ok || seen[name] {
				return nil, fmt.Errorf("phase %q has unknown or duplicate assertion profile %q", phase.Name, name)
			}
			seen[name] = true
			if err := applyTestAssertionProfile(phase, name, profile); err != nil {
				return nil, err
			}
		}
	}
	return &result, nil
}

func applyTestAssertionProfile(phase *testLoadChaosPhaseSpec, name string, profile testAssertionProfile) error {
	data, err := json.Marshal(profile)
	if err != nil {
		return fmt.Errorf("assertion profile %q: %w", name, err)
	}
	var copyProfile testAssertionProfile
	if err := json.Unmarshal(data, &copyProfile); err != nil {
		return fmt.Errorf("assertion profile %q: %w", name, err)
	}
	if len(copyProfile.RequestDeadlines) > 0 && phase.RequestDeadlines == nil {
		phase.RequestDeadlines = map[string]testRequestDeadlineSpec{}
	}
	for _, route := range testEndpointRecoveryNames(copyProfile.RequestDeadlines) {
		if _, exists := phase.RequestDeadlines[route]; exists {
			return fmt.Errorf("phase %q assertion profile %q conflicts with request_deadlines.%s", phase.Name, name, route)
		}
		phase.RequestDeadlines[route] = copyProfile.RequestDeadlines[route]
	}
	if copyProfile.DependencyAttempts != nil {
		if phase.RetryAmplification != nil {
			return fmt.Errorf("phase %q assertion profile %q conflicts with retry_amplification", phase.Name, name)
		}
		phase.RetryAmplification = copyProfile.DependencyAttempts
	}
	if copyProfile.ConnectionAttempts != nil {
		if phase.ConnectionAttempts != nil {
			return fmt.Errorf("phase %q assertion profile %q conflicts with connection_attempts", phase.Name, name)
		}
		phase.ConnectionAttempts = copyProfile.ConnectionAttempts
	}
	if phase.StepThresholds == nil {
		phase.StepThresholds = map[string]testLoadStepThresholdSpec{}
	}
	if phase.StepRecovery == nil {
		phase.StepRecovery = map[string]*testLoadRecoverySpec{}
	}
	for _, route := range testEndpointRecoveryNames(copyProfile.HealthyRoutes) {
		if _, exists := phase.StepThresholds[route]; exists {
			return fmt.Errorf("phase %q assertion profile %q conflicts with step_thresholds.%s", phase.Name, name, route)
		}
		phase.StepThresholds[route] = copyProfile.HealthyRoutes[route]
	}
	for _, route := range testEndpointRecoveryNames(copyProfile.RecoveryDeadlines) {
		if _, exists := phase.StepRecovery[route]; exists {
			return fmt.Errorf("phase %q assertion profile %q conflicts with step_recovery.%s", phase.Name, name, route)
		}
		phase.StepRecovery[route] = copyProfile.RecoveryDeadlines[route]
	}
	return nil
}
