package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"time"
)

type testRestartInFlightSpec struct {
	Request     testHTTPRequest             `yaml:"request"`
	Checkpoints []testRestartCheckpointSpec `yaml:"checkpoints"`
}
type testRestartCheckpointSpec struct {
	Workload string          `yaml:"workload"`
	Request  testHTTPRequest `yaml:"request"`
}
type testRestartInFlightEvidence struct {
	Name               string                    `json:"name"`
	CheckpointAttempts int                       `json:"checkpoint_attempts"`
	Checkpoints        []testHTTPRequestEvidence `json:"checkpoints,omitempty"`
	PendingAtRestart   bool                      `json:"pending_at_restart"`
	RestartCompleted   bool                      `json:"restart_completed"`
	Joined             bool                      `json:"joined"`
	Outcome            string                    `json:"outcome,omitempty"`
	ResponseStatus     int                       `json:"response_status,omitempty"`
}
type testRestartFlight struct {
	cancel   context.CancelFunc
	done     chan testHTTPRequestEvidence
	result   *testHTTPRequestEvidence
	evidence *testRestartInFlightEvidence
}

func validateTestRestartInFlight(scenario testScenario, index int, spec *testRestartInFlightSpec, dataFields []string) error {
	if spec == nil {
		return nil
	}
	r := spec.Request
	if (r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodPatch) || r.JSON == nil || len(r.Capture) > 0 {
		return errors.New("restart.in_flight.request requires POST, PUT or PATCH with JSON and no captures")
	}
	if len(spec.Checkpoints) < 1 || len(spec.Checkpoints) > 5 {
		return errors.New("restart.in_flight needs between 1 and 5 read-only checkpoints")
	}
	requests := []testHTTPRequest{r}
	for _, c := range spec.Checkpoints {
		if c.Workload != "app" {
			if _, ok := scenario.Services[c.Workload]; !ok || c.Workload == "" {
				return errors.New("in-flight checkpoint workload must be app or a declared service")
			}
		}
		probe := c.Request
		if probe.Method != http.MethodGet || probe.JSON != nil || len(probe.Capture) > 0 || len(probe.Expect.JSON) == 0 || probe.Expect.Status < 200 || probe.Expect.Status >= 300 || (c.Workload != "app" && probe.As != "") {
			return errors.New("in-flight checkpoints require read-only 2xx GET JSON assertions without captures; service probes cannot use primary credentials")
		}
		requests = append(requests, probe)
	}
	prior := []testHTTPRequest{}
	for _, step := range scenario.Steps[:index] {
		prior = append(prior, step.Requests...)
		prior = append(prior, step.Checks...)
	}
	return validateTestHTTPRequestsWithData(testScenario{Consumers: scenario.Consumers, Requests: append(prior, requests...)}, dataFields)
}

func startTestRestartFlight(ctx context.Context, baseURL string, env, consumerEnv []string, runID string, spec testRestartInFlightSpec, captures map[string]string, evidence *testRestartInFlightEvidence) (*testRestartFlight, error) {
	requestCtx, cancel := context.WithCancel(ctx)
	flight := &testRestartFlight{cancel: cancel, done: make(chan testHTTPRequestEvidence, 1), evidence: evidence}
	copied := maps.Clone(captures)
	// Only the owning goroutine updates evidence and captures. The HTTP client
	// has no shorter timeout than the restart's overall bounded context.
	go func() {
		r := spec.Request
		result := testHTTPRequestEvidence{Name: r.Name, Method: r.Method, Path: strings.SplitN(r.Path, "?", 2)[0]}
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		err := runOneTestHTTPRequest(requestCtx, client, baseURL, r, testHTTPValues(runID, copied, nil), testHTTPConsumerKeys(consumerEnv), copied, nil, &result)
		result.Passed = err == nil
		flight.done <- result
	}()
	for {
		if flight.completed() {
			return flight, errors.New("request ended before the checkpoint")
		}
		evidence.CheckpointAttempts++
		evidence.Checkpoints = nil
		healthy := true
		for _, checkpoint := range spec.Checkpoints {
			url := baseURL
			if checkpoint.Workload != "app" {
				key := "GREGALE_TEST_SERVICE_" + strings.ToUpper(strings.ReplaceAll(checkpoint.Workload, "-", "_")) + "_URL="
				url = ""
				for _, entry := range env {
					if value, ok := strings.CutPrefix(entry, key); ok {
						url = value
						break
					}
				}
			}
			if url == "" {
				return flight, fmt.Errorf("checkpoint workload %q is unavailable", checkpoint.Workload)
			}
			results, err := runTestHTTPRequests(ctx, url, runID, consumerEnv, []testHTTPRequest{checkpoint.Request}, captures)
			evidence.Checkpoints = append(evidence.Checkpoints, results...)
			healthy = healthy && err == nil
		}
		if flight.completed() {
			return flight, errors.New("request ended before the checkpoint")
		}
		if healthy {
			return flight, nil
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return flight, ctx.Err()
		case result := <-flight.done:
			flight.result = &result
			timer.Stop()
			return flight, errors.New("request ended before the checkpoint")
		case <-timer.C:
		}
	}
}
func (f *testRestartFlight) completed() bool {
	if f.result != nil {
		return true
	}
	select {
	case result := <-f.done:
		f.result = &result
		return true
	default:
		return false
	}
}
func (f *testRestartFlight) close() {
	if f.evidence.Joined {
		return
	}
	completed := f.completed()
	f.cancel()
	if f.result == nil {
		result := <-f.done
		f.result = &result
	}
	f.evidence.Joined = true
	f.evidence.ResponseStatus = f.result.Status
	if completed {
		f.evidence.Outcome = "request_ended"
	} else if f.evidence.RestartCompleted {
		f.evidence.Outcome = "canceled_after_restart"
	} else {
		f.evidence.Outcome = "canceled_on_failure"
	}
}
