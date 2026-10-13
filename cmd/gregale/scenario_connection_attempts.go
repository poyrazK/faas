package main

import (
	"errors"
	"fmt"
	"github.com/onebox-faas/faas/pkg/chaos"
)

func validateTestConnectionAttempts(phase testLoadChaosPhaseSpec) error {
	spec := phase.ConnectionAttempts
	if spec == nil {
		return nil
	}
	if spec.PerRequest {
		return fmt.Errorf("phase %q per_request requires HTTP dependency attempts; TCP streams do not carry request IDs", phase.Name)
	}
	if err := validateTestAttemptBudget(phase.Name, "connection_attempts", spec); err != nil {
		return err
	}
	if len(phase.Rules) != 1 {
		return fmt.Errorf("phase %q connection_attempts requires exactly one TCP fault rule", phase.Name)
	}
	rule := phase.Rules[0]
	if rule.From == "" || rule.Percent != 100 || !(chaos.Rule{Kind: rule.Kind}).IsTCP() || rule.Port < 1 || rule.Port > 65535 {
		return fmt.Errorf("phase %q connection_attempts requires a 100%% TCP fault rule with a valid port and an explicit from workload", phase.Name)
	}
	return nil
}

func applyTestConnectionAttempts(report *testLoadEvidence, cfg *testLoadConfig, scheduleErr error) error {
	var failures []error
	for i, phase := range cfg.ChaosSchedule {
		spec := phase.ConnectionAttempts
		if spec == nil {
			continue
		}
		if i >= len(report.ChaosPhases) {
			failures = append(failures, fmt.Errorf("phase %q connection attempts evidence missing", phase.Name))
			continue
		}
		observed := &report.ChaosPhases[i]
		evidence := &testRetryAmplificationEvidence{Request: spec.Request, From: phase.Rules[0].From, To: phase.Rules[0].To, Port: phase.Rules[0].Port, MinRequests: spec.MinRequests, MaxAttemptsPerRequest: spec.MaxAttemptsPerRequest}
		observed.ConnectionAttempts = evidence
		for _, request := range observed.Steps {
			if request.Name == spec.Request {
				evidence.Requests += request.Requests
			}
		}
		if observed.Chaos != nil && len(observed.Chaos.Matches) == 1 {
			if observed.Chaos.Matches[0].TCPAttempts != nil {
				evidence.Attempts = *observed.Chaos.Matches[0].TCPAttempts
			}
		}
		if evidence.Requests > 0 {
			evidence.AttemptsPerRequest = float64(evidence.Attempts) / float64(evidence.Requests)
		}
		switch {
		case scheduleErr != nil || observed.Chaos == nil || len(observed.Chaos.Matches) != 1 || observed.Chaos.Matches[0].TCPAttempts == nil:
			evidence.Error = "complete TCP attempt evidence unavailable; use a gateway with connection-attempt support"
		case evidence.Requests < spec.MinRequests:
			evidence.Error = fmt.Sprintf("requires at least %d incoming request samples; observed %d", spec.MinRequests, evidence.Requests)
		case evidence.AttemptsPerRequest > spec.MaxAttemptsPerRequest+1e-12:
			evidence.Error = fmt.Sprintf("%.4g TCP connection attempts per request exceeds %.4g", evidence.AttemptsPerRequest, spec.MaxAttemptsPerRequest)
		default:
			evidence.Passed = true
		}
		if !evidence.Passed {
			failures = append(failures, fmt.Errorf("phase %q connection attempts: %s", phase.Name, evidence.Error))
		}
	}
	err := errors.Join(failures...)
	if err != nil {
		report.Status = "failed"
	}
	return err
}
