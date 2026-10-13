package main

import (
	"errors"
	"fmt"
	"math"

	"github.com/onebox-faas/faas/pkg/chaos"
)

// A full-coverage HTTP rule counts routed requests, including application retries.
// TCP matches count sockets and DNS matches count lookups, so neither is suitable.
type testRetryAmplificationSpec struct {
	PerRequest            bool    `yaml:"per_request,omitempty" json:"per_request,omitempty"`
	Request               string  `yaml:"request"`
	MaxAttemptsPerRequest float64 `yaml:"max_attempts_per_request"`
	MinRequests           int     `yaml:"min_requests"`
}

type testRetryAmplificationEvidence struct {
	PerRequest            *testPerRequestAttemptEvidence `json:"per_request,omitempty"`
	Request               string                         `json:"request"`
	From                  string                         `json:"from"`
	To                    string                         `json:"to"`
	Port                  int                            `json:"port,omitempty"`
	Attempts              int64                          `json:"attempts"`
	Requests              int                            `json:"requests"`
	MinRequests           int                            `json:"min_requests"`
	AttemptsPerRequest    float64                        `json:"attempts_per_request"`
	MaxAttemptsPerRequest float64                        `json:"max_attempts_per_request"`
	Passed                bool                           `json:"passed"`
	Error                 string                         `json:"error,omitempty"`
}

func validateTestAttemptBudget(name, field string, spec *testRetryAmplificationSpec) error {
	if spec == nil {
		return nil
	}
	if spec.PerRequest && (spec.MaxAttemptsPerRequest != math.Trunc(spec.MaxAttemptsPerRequest) || spec.MinRequests > chaos.MaxRequestCorrelationSlots) {
		return fmt.Errorf("phase %q %s per_request requires an integer attempt limit and min_requests at most %d", name, field, chaos.MaxRequestCorrelationSlots)
	}
	if !testHTTPNamePattern.MatchString(spec.Request) || spec.MinRequests < 1 || spec.MinRequests > testLoadMaxRequests || math.IsNaN(spec.MaxAttemptsPerRequest) || math.IsInf(spec.MaxAttemptsPerRequest, 0) || spec.MaxAttemptsPerRequest <= 0 || spec.MaxAttemptsPerRequest > 1000 {
		return fmt.Errorf("phase %q %s requires a named request, min_requests between 1 and %d, and max_attempts_per_request above 0 and at most 1000", name, field, testLoadMaxRequests)
	}
	return nil
}

func validateTestRetryAmplification(phase testLoadChaosPhaseSpec) error {
	spec := phase.RetryAmplification
	if spec == nil {
		return nil
	}
	if err := validateTestAttemptBudget(phase.Name, "retry_amplification", spec); err != nil {
		return err
	}
	if len(phase.Rules) != 1 {
		return fmt.Errorf("phase %q retry_amplification requires exactly one HTTP fault rule", phase.Name)
	}
	rule := phase.Rules[0]
	if rule.From == "" || rule.Percent != 100 || (rule.Kind != "latency" && rule.Kind != "http_status") {
		return fmt.Errorf("phase %q retry_amplification requires a 100%% HTTP latency or http_status rule with an explicit from workload", phase.Name)
	}
	return nil
}

func applyTestRetryAmplification(report *testLoadEvidence, cfg *testLoadConfig, scheduleErr error) error {
	var failures []error
	for i, phase := range cfg.ChaosSchedule {
		spec := phase.RetryAmplification
		if spec == nil {
			continue
		}
		if i >= len(report.ChaosPhases) {
			failures = append(failures, fmt.Errorf("phase %q retry amplification evidence missing", phase.Name))
			continue
		}
		observed := &report.ChaosPhases[i]
		evidence := &testRetryAmplificationEvidence{Request: spec.Request, From: phase.Rules[0].From, To: phase.Rules[0].To, MinRequests: spec.MinRequests, MaxAttemptsPerRequest: spec.MaxAttemptsPerRequest}
		observed.RetryAmplification = evidence
		for _, request := range observed.Steps {
			if request.Name == spec.Request {
				evidence.Requests += request.Requests
			}
		}
		if observed.Chaos != nil && len(observed.Chaos.Matches) == 1 {
			evidence.Attempts = observed.Chaos.Matches[0].Matches
		}
		if evidence.Requests > 0 {
			evidence.AttemptsPerRequest = float64(evidence.Attempts) / float64(evidence.Requests)
		}
		switch {
		case scheduleErr != nil || observed.Chaos == nil || len(observed.Chaos.Matches) != 1:
			evidence.Error = "complete fault match evidence unavailable"
		case evidence.Requests < spec.MinRequests:
			evidence.Error = fmt.Sprintf("requires at least %d incoming request samples; observed %d", spec.MinRequests, evidence.Requests)
		case evidence.AttemptsPerRequest > spec.MaxAttemptsPerRequest+1e-12:
			evidence.Error = fmt.Sprintf("%.4g dependency attempts per request exceeds %.4g", evidence.AttemptsPerRequest, spec.MaxAttemptsPerRequest)
		default:
			evidence.Passed = true
		}
		if spec.PerRequest && observed.Chaos != nil && len(observed.Chaos.Matches) == 1 {
			evidence.PerRequest = evaluateTestPerRequestAttempts(*observed, observed.Chaos.Matches[0].Rule, spec.MaxAttemptsPerRequest, evidence.Requests)
			evidence.Passed = evidence.Passed && evidence.PerRequest.Passed
			if !evidence.Passed {
				evidence.Error = evidence.PerRequest.Error
			}
		}
		if !evidence.Passed {
			failures = append(failures, fmt.Errorf("phase %q retry amplification: %s", phase.Name, evidence.Error))
		}
	}
	err := errors.Join(failures...)
	if err != nil {
		report.Status = "failed"
	}
	return err
}
