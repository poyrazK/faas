package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type testRestartSpec struct {
	InFlight      *testRestartInFlightSpec `yaml:"in_flight,omitempty"`
	Workload      string                   `yaml:"workload"`
	Timeout       string                   `yaml:"timeout"`
	Readiness     testHTTPRequest          `yaml:"readiness"`
	StorageChecks []testHTTPRequest        `yaml:"storage_checks"`
}

type testRestartEvidence struct {
	InFlight          *testRestartInFlightEvidence `json:"in_flight,omitempty"`
	Workload          string                       `json:"workload"`
	WakeID            string                       `json:"wake_id,omitempty"`
	Status            string                       `json:"status"`
	ProcessingStatus  string                       `json:"processing_status,omitempty"`
	BootMethod        string                       `json:"boot_method,omitempty"`
	ReadinessAttempts int                          `json:"readiness_attempts"`
	Before            []testHTTPRequestEvidence    `json:"storage_before,omitempty"`
	After             []testHTTPRequestEvidence    `json:"storage_after,omitempty"`
	Readiness         []testHTTPRequestEvidence    `json:"readiness,omitempty"`
	DurationMS        int64                        `json:"duration_ms"`
}

type testRestartClient interface {
	restartStatusClient
	testWakeBootClient
	RestartAppFresh(context.Context, string) (api.AppRestartResponse, error)
}

func validateTestRestart(scenario testScenario, index int, step testScenarioStep, dataFields []string) error {
	r := step.Restart
	if r == nil {
		return nil
	}
	if index == 0 || step.Chaos != nil || step.ClearChaos || len(step.Command)+len(step.Requests)+len(step.Checks) != 0 || step.Load != nil {
		return errors.New("restart must be a standalone step after baseline")
	}
	if r.Workload != "app" {
		if _, ok := scenario.Services[r.Workload]; !ok || r.Workload == "" {
			return errors.New("restart.workload must be app or a declared service")
		}
	}
	d, err := time.ParseDuration(r.Timeout)
	if err != nil || d < time.Second || d > 5*time.Minute || d%time.Millisecond != 0 {
		return errors.New("restart.timeout must be whole milliseconds between 1s and 5m")
	}
	if scenario.Timeout != "" {
		limit, _ := time.ParseDuration(scenario.Timeout)
		if d > limit {
			return errors.New("restart.timeout exceeds scenario timeout")
		}
	}
	if len(r.StorageChecks) == 0 || len(r.StorageChecks) > 20 {
		return errors.New("restart.storage_checks needs between 1 and 20 persisted JSON assertions")
	}
	if err := validateTestRestartInFlight(scenario, index, r.InFlight, dataFields); err != nil {
		return err
	}
	requests := append([]testHTTPRequest{r.Readiness}, r.StorageChecks...)
	for i, request := range requests {
		if request.Method != "GET" || request.JSON != nil || len(request.Capture) != 0 {
			return errors.New("restart probes must be read-only GET requests without captures")
		}
		if request.Expect.Status < 200 || request.Expect.Status >= 300 {
			return errors.New("restart probes must expect a successful 2xx response")
		}
		if r.Workload != "app" && request.As != "" {
			return errors.New("service restart probes cannot use the primary app's consumer credentials")
		}
		if i > 0 && len(request.Expect.JSON) == 0 {
			return errors.New("restart.storage_checks must assert persisted JSON values")
		}
	}
	prior := []testHTTPRequest{}
	for _, previous := range scenario.Steps[:index] {
		prior = append(prior, previous.Requests...)
		prior = append(prior, previous.Checks...)
	}
	return validateTestHTTPRequestsWithData(testScenario{Consumers: scenario.Consumers, Requests: append(prior, requests...)}, dataFields)
}

func runTestRestart(ctx context.Context, client testRestartClient, baseURL string, env, consumerEnv []string, runID string, spec testRestartSpec, captures map[string]string) (evidence *testRestartEvidence, err error) {
	started := time.Now()
	evidence = &testRestartEvidence{Workload: spec.Workload, Status: "failed"}
	defer func() { evidence.DurationMS = time.Since(started).Milliseconds() }()
	lookup := func(key string) string {
		for _, value := range env {
			if result, ok := strings.CutPrefix(value, key+"="); ok {
				return result
			}
		}
		return ""
	}
	primaryURL := baseURL
	slug := lookup("GREGALE_TEST_APP_SLUG")
	if spec.Workload != "app" {
		key := "GREGALE_TEST_SERVICE_" + strings.ToUpper(strings.ReplaceAll(spec.Workload, "-", "_"))
		slug, baseURL = lookup(key+"_APP_SLUG"), lookup(key+"_URL")
	}
	if slug == "" || baseURL == "" || client == nil {
		return evidence, errors.New("restart target is unavailable in this scenario run")
	}
	duration, _ := time.ParseDuration(spec.Timeout)
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	var flight *testRestartFlight
	if spec.InFlight != nil {
		evidence.InFlight = &testRestartInFlightEvidence{Name: spec.InFlight.Request.Name}
		flight, err = startTestRestartFlight(ctx, primaryURL, env, consumerEnv, runID, *spec.InFlight, captures, evidence.InFlight)
		if flight != nil {
			defer flight.close()
		}
		if err != nil {
			return evidence, fmt.Errorf("in-flight checkpoint: %w", err)
		}
	}
	evidence.Before, err = runTestHTTPRequests(ctx, baseURL, runID, consumerEnv, spec.StorageChecks, captures)
	if err != nil {
		return evidence, fmt.Errorf("pre-restart storage checks: %w", err)
	}
	if flight != nil {
		if flight.completed() {
			return evidence, errors.New("in-flight request ended before restart submission")
		}
		evidence.InFlight.PendingAtRestart = true
	}
	// Submit once. An uncertain receipt or status must never trigger a second
	// destructive request. The wake ID remains in reports for investigation.
	receipt, err := client.RestartAppFresh(ctx, slug)
	evidence.WakeID = receipt.WakeID
	if err != nil {
		return evidence, fmt.Errorf("request fresh restart: %w", err)
	}
	if id, parseErr := uuid.Parse(receipt.WakeID); parseErr != nil || id == uuid.Nil {
		return evidence, errors.New("restart accepted without a valid receipt; do not resubmit")
	}
	row, err := pollAppRestartStatus(ctx, client, slug, receipt.WakeID, true, 200*time.Millisecond, nil)
	evidence.ProcessingStatus = row.Status
	if err != nil {
		return evidence, fmt.Errorf("restart processing: %w", err)
	}
	evidence.BootMethod, err = verifyWakeBootMethod(ctx, client, slug, receipt.WakeID, "cold_boot")
	if err != nil {
		return evidence, fmt.Errorf("restart boot evidence: %w", err)
	}
	for {
		evidence.ReadinessAttempts++
		evidence.Readiness, err = runTestHTTPRequests(ctx, baseURL, runID, consumerEnv, []testHTTPRequest{spec.Readiness}, captures)
		if err == nil {
			break
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return evidence, fmt.Errorf("restart readiness: %w", ctx.Err())
		case <-timer.C:
		}
	}
	evidence.After, err = runTestHTTPRequests(ctx, baseURL, runID, consumerEnv, spec.StorageChecks, captures)
	if err != nil {
		return evidence, fmt.Errorf("post-restart storage checks: %w", err)
	}
	if flight != nil {
		evidence.InFlight.RestartCompleted = true
		flight.close()
	}
	evidence.Status = "passed"
	return evidence, nil
}

// Exclude only completed, cold-boot-verified planned restarts from the
// original service-profile check. Unexpected wakes remain failures.
func testServiceRestartBaseline(original map[string]bool, steps []testScenarioStepEvidence, service string) (map[string]bool, []string) {
	baseline := make(map[string]bool, len(original))
	for id, value := range original {
		baseline[id] = value
	}
	var planned []string
	for _, step := range steps {
		if r := step.Restart; r != nil && r.Workload == service && r.Status == "passed" && r.ProcessingStatus == "completed" && r.BootMethod == "cold_boot" && r.WakeID != "" {
			baseline[r.WakeID] = true
			planned = append(planned, r.WakeID)
		}
	}
	return baseline, planned
}
