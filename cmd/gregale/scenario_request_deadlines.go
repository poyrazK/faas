package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// Expectations are phase-local; requests in other phases keep their original
// success contract. Within covers dispatch through bounded body consumption.
type testRequestDeadlineSpec struct {
	Within     string          `yaml:"within" json:"within"`
	MinSamples int             `yaml:"min_samples" json:"min_samples"`
	Expect     *testHTTPExpect `yaml:"expect,omitempty" json:"expect,omitempty"`
}
type testRequestDeadlineEvidence struct {
	Passed         bool    `json:"passed"`
	Request        string  `json:"request"`
	WithinMS       float64 `json:"within_ms"`
	Requests       int     `json:"requests"`
	MinSamples     int     `json:"min_samples"`
	MaxMS          float64 `json:"max_ms"`
	Failures       int     `json:"failures"`
	ExpectedStatus int     `json:"expected_status,omitempty"`
	Error          string  `json:"error,omitempty"`
}

func validateTestRequestDeadlines(budgets map[string]testRequestDeadlineSpec) error {
	if len(budgets) > 100 {
		return errors.New("request_deadlines may declare at most 100 named requests")
	}
	for _, name := range testEndpointRecoveryNames(budgets) {
		spec := budgets[name]
		duration, err := time.ParseDuration(spec.Within)
		if !testHTTPNamePattern.MatchString(name) || err != nil || duration < time.Millisecond || duration > 30*time.Second || duration%time.Millisecond != 0 || spec.MinSamples < 1 || spec.MinSamples > testLoadMaxRequests {
			return fmt.Errorf("request_deadlines.%s requires within in whole milliseconds between 1ms and 30s and positive min_samples", name)
		}
		if spec.Expect != nil {
			scenario := testScenario{Requests: []testHTTPRequest{{Name: name, Method: "GET", Path: "/", Expect: *spec.Expect}}}
			if err := validateTestHTTPRequests(scenario); err != nil {
				return fmt.Errorf("request_deadlines.%s expect: %w", name, err)
			}
		}
	}
	return nil
}

func prepareTestRequestDeadline(ctx context.Context, cfg *testLoadConfig, phase int, step testHTTPRequest, started time.Time) (context.Context, context.CancelFunc, testHTTPRequest) {
	if phase < 0 || phase >= len(cfg.ChaosSchedule) {
		return ctx, func() {}, step
	}
	budget, ok := cfg.ChaosSchedule[phase].RequestDeadlines[step.Name]
	if !ok {
		return ctx, func() {}, step
	}
	if budget.Expect != nil {
		step.Expect = *budget.Expect
	}
	requestCtx, cancel := context.WithDeadline(ctx, started.Add(circuitDuration(budget.Within)))
	return requestCtx, cancel, step
}

func applyTestRequestDeadlines(report *testLoadEvidence, cfg *testLoadConfig) error {
	var failures []error
	for i, phase := range cfg.ChaosSchedule {
		if len(phase.RequestDeadlines) == 0 {
			continue
		}
		if i >= len(report.ChaosPhases) {
			failures = append(failures, fmt.Errorf("phase %q request deadline evidence missing", phase.Name))
			continue
		}
		observed := &report.ChaosPhases[i]
		observed.RequestDeadlines = map[string]*testRequestDeadlineEvidence{}
		for _, name := range testEndpointRecoveryNames(phase.RequestDeadlines) {
			spec := phase.RequestDeadlines[name]
			e := &testRequestDeadlineEvidence{Request: name, WithinMS: loadMilliseconds(circuitDuration(spec.Within)), MinSamples: spec.MinSamples}
			observed.RequestDeadlines[name] = e
			if spec.Expect != nil {
				e.ExpectedStatus = spec.Expect.Status
			}
			incomplete := false
			for _, step := range observed.Steps {
				if step.Name == name {
					if step.Latency.Samples != step.Requests || !finiteNonnegative(step.Latency.MaxMS) {
						incomplete = true
					}
					e.Requests += step.Requests
					e.Failures += step.Failures
					if step.Latency.MaxMS > e.MaxMS {
						e.MaxMS = step.Latency.MaxMS
					}
				}
			}
			switch {
			case incomplete:
				e.Error = "complete request latency evidence unavailable"
			case e.Requests < e.MinSamples:
				e.Error = "request deadline samples unavailable or below min_samples"
			case e.Failures > 0:
				e.Error = "requests failed to complete the expected response contract; client cancellation does not prove an application timeout response"
			case !finiteNonnegative(e.MaxMS) || e.MaxMS > e.WithinMS+1e-9:
				e.Error = "request exceeded its overall deadline"
			default:
				e.Passed = true
			}
			if !e.Passed {
				failures = append(failures, fmt.Errorf("phase %q request_deadlines.%s: %s", phase.Name, name, e.Error))
			}
		}
	}
	err := errors.Join(failures...)
	if err != nil {
		report.Status = "failed"
	}
	return err
}

func printTestRequestDeadlines(out io.Writer, report *testLoadEvidence) {
	if report == nil {
		return
	}
	for _, phase := range report.ChaosPhases {
		for _, name := range testEndpointRecoveryNames(phase.RequestDeadlines) {
			e := phase.RequestDeadlines[name]
			_, _ = fmt.Fprintf(out, "  %s request deadline %s: maximum %.3fms / %.3fms; %d requests, %d failures; expected status %d, passed %t; %s\n", phase.Name, name, e.MaxMS, e.WithinMS, e.Requests, e.Failures, e.ExpectedStatus, e.Passed, e.Error)
		}
	}
}
