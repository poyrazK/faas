package main

import (
	"errors"
	"fmt"
)

// Validate execution requirements before starting any member of a suite, and
// use the same checks for standalone validation and execution.
func validateTestScenarioExecution(scenario testScenario, engine string, profiles ...string) error {
	if engine != "real-vm" {
		if scenario.Actions != nil {
			return errors.New("webhook action capture requires the real-vm engine")
		}
		if scenario.Chaos != nil {
			return errors.New("scenario chaos rules require the real-vm engine")
		}
		if len(scenario.Steps) > 0 {
			return errors.New("staged scenarios require the real-vm engine")
		}
	}
	if len(profiles) == 0 {
		profiles = []string{engine}
		if engine == "real-vm" {
			profiles[0] = "warm"
		}
	}
	for _, profile := range profiles {
		if err := validateTestCommandExecution(scenario, engine, profile); err != nil {
			return fmt.Errorf("profile %q: %w", profile, err)
		}
	}
	return nil
}
