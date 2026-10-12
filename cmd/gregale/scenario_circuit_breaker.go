package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type testCircuitBreakerSpec struct {
	CounterPath       string `yaml:"counter_path"`
	As                string `yaml:"as,omitempty"`
	Request           string `yaml:"request"`
	MinRequests       int    `yaml:"min_requests"`
	StopWithin        string `yaml:"stop_within"`
	Window            string `yaml:"window"`
	MaxProbes         int64  `yaml:"max_probes"`
	ResumeWithin      string `yaml:"resume_within"`
	ResumeMinAttempts int64  `yaml:"resume_min_attempts"`
}

type testCircuitBreakerWindow struct {
	IncomingRequests int    `json:"incoming_requests"`
	Phase            string `json:"phase"`
	StartMS          int64  `json:"start_ms"`
	EndMS            int64  `json:"end_ms"`
	Attempts         int64  `json:"attempts"`
	Passed           bool   `json:"passed"`
}

type testCircuitBreakerEvidence struct {
	From              string                     `json:"from"`
	To                string                     `json:"to"`
	Port              int                        `json:"port,omitempty"`
	MinRequests       int                        `json:"min_requests"`
	Request           string                     `json:"request"`
	CounterPath       string                     `json:"counter_path"`
	StopWithinMS      int64                      `json:"stop_within_ms"`
	ResumeWithinMS    int64                      `json:"resume_within_ms"`
	WindowMS          int64                      `json:"window_ms"`
	MaxProbes         int64                      `json:"max_probes"`
	ResumeMinAttempts int64                      `json:"resume_min_attempts"`
	StoppedMS         *int64                     `json:"stopped_ms,omitempty"`
	ResumedMS         *int64                     `json:"resumed_ms,omitempty"`
	Windows           []testCircuitBreakerWindow `json:"windows,omitempty"`
	Passed            bool                       `json:"passed"`
	Error             string                     `json:"error,omitempty"`
}

type testCircuitIncomingSample struct {
	At   time.Time
	Name string
}

type testCircuitSample struct {
	At    time.Time
	Phase int
	Count int64
}
type testCircuitObservation struct {
	Samples []testCircuitSample
	Error   string
}

func circuitDuration(value string) time.Duration {
	duration, _ := time.ParseDuration(value)
	return duration
}
func circuitPoll(spec *testCircuitBreakerSpec) time.Duration {
	interval := circuitDuration(spec.Window) / 10
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	if interval > 250*time.Millisecond {
		interval = 250 * time.Millisecond
	}
	return interval
}

func validateTestCircuitBreaker(phases []testLoadChaosPhaseSpec, index int) error {
	phase := phases[index]
	spec := phase.CircuitBreaker
	if spec == nil {
		return nil
	}
	fail := func(message string) error { return fmt.Errorf("phase %q circuit_breaker: %s", phase.Name, message) }
	if index < 1 || index+1 >= len(phases) || len(phase.Rules) != 1 || len(phases[index-1].Rules) != 0 || len(phases[index+1].Rules) != 0 {
		return fail("requires one fault rule between a baseline and recovery phase with no rules")
	}
	if phase.Rules[0].From == "" || phase.Rules[0].Percent != 100 {
		return fail("requires a 100% fault rule with an explicit from workload")
	}
	if !testHTTPNamePattern.MatchString(spec.Request) || spec.MinRequests < 1 || spec.MinRequests > testLoadMaxRequests || spec.MaxProbes < 0 || spec.MaxProbes > 10000 || spec.ResumeMinAttempts <= spec.MaxProbes || spec.ResumeMinAttempts > 1000000 {
		return fail("requires a named request, positive min_requests, max_probes between 0 and 10000, and resume_min_attempts above max_probes and at most 1000000")
	}
	for _, value := range []string{spec.Window, spec.StopWithin, spec.ResumeWithin} {
		duration, err := time.ParseDuration(value)
		if err != nil || duration < time.Second || duration > testLoadMaxDuration || duration%time.Millisecond != 0 {
			return fail("window, stop_within and resume_within must be whole-millisecond durations between 1s and 5m")
		}
	}
	window := circuitDuration(spec.Window)
	if circuitDuration(spec.StopWithin) < window+circuitPoll(spec) || circuitDuration(spec.ResumeWithin) < window+circuitPoll(spec) {
		return fail("deadlines must allow a full window plus one polling interval")
	}
	if circuitDuration(phases[index-1].Duration) < window+circuitPoll(spec) || circuitDuration(phase.Duration) < circuitDuration(spec.StopWithin)+window || circuitDuration(phases[index+1].Duration) < circuitDuration(spec.ResumeWithin)+window {
		return fail("baseline must fit a window and polling interval; fault and recovery must each fit their deadline plus a sustained window")
	}
	if !strings.HasPrefix(spec.CounterPath, "/") || strings.HasPrefix(spec.CounterPath, "//") || strings.ContainsAny(spec.CounterPath, "${#\\\r\n") {
		return fail("counter_path must be a local absolute path without templates or fragments")
	}
	return nil
}

func readTestCircuitCounter(ctx context.Context, client *http.Client, baseURL string, spec *testCircuitBreakerSpec, keys map[string]string) (int64, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+spec.CounterPath, nil)
	if err != nil {
		return 0, errors.New("invalid counter URL")
	}
	if spec.As != "" {
		token := keys[spec.As]
		if token == "" {
			return 0, errors.New("counter consumer credential unavailable")
		}
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, errors.New("counter request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("counter returned HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil || len(raw) > 4096 {
		return 0, errors.New("counter response unreadable or exceeds 4 KiB")
	}
	var payload struct {
		Attempts json.RawMessage `json:"attempts"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || decoder.Decode(new(any)) != io.EOF {
		return 0, errors.New("counter must return a single JSON object with integer attempts")
	}
	count, err := strconv.ParseInt(string(payload.Attempts), 10, 64)
	if err != nil || count < 0 {
		return 0, errors.New("counter attempts must be a nonnegative int64")
	}
	return count, nil
}

func monitorTestCircuitCounter(ctx context.Context, baseURL string, keys map[string]string, cfg *testLoadConfig, clock *testLoadPhaseClock, index int) testCircuitObservation {
	spec := cfg.ChaosSchedule[index].CircuitBreaker
	result := testCircuitObservation{}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ticker := time.NewTicker(circuitPoll(spec))
	defer ticker.Stop()
	var previous *int64
	for {
		if ctx.Err() != nil {
			return result
		}
		at := time.Now()
		phase := clock.phaseAt(at)
		if phase > index+1 {
			return result
		}
		if phase >= index-1 && phase <= index+1 {
			requestCtx, cancel := context.WithTimeout(ctx, circuitDuration(spec.Window)/4)
			count, err := readTestCircuitCounter(requestCtx, client, baseURL, spec, keys)
			cancel()
			if ctx.Err() != nil {
				return result
			}
			if err != nil {
				result.Error = err.Error()
				return result
			}
			if previous != nil && count < *previous {
				result.Error = "dependency attempt counter reset or decreased"
				return result
			}
			previous = &count
			finished := time.Now()
			if clock.phaseAt(finished) == phase {
				result.Samples = append(result.Samples, testCircuitSample{At: finished, Phase: phase, Count: count})
			}
		}
		select {
		case <-ctx.Done():
			return result
		case <-ticker.C:
		}
	}
}

func monitorTestCircuitBreakers(ctx context.Context, baseURL string, consumerEnv []string, cfg *testLoadConfig, clock *testLoadPhaseClock) []testCircuitObservation {
	results := make([]testCircuitObservation, len(cfg.ChaosSchedule))
	type completed struct {
		Index       int
		Observation testCircuitObservation
	}
	done := make(chan completed, len(results))
	count := 0
	for i, phase := range cfg.ChaosSchedule {
		if phase.CircuitBreaker != nil {
			count++
			go func(index int) {
				done <- completed{index, monitorTestCircuitCounter(ctx, baseURL, testHTTPConsumerKeys(consumerEnv), cfg, clock, index)}
			}(i)
		}
	}
	for j := 0; j < count; j++ {
		entry := <-done
		results[entry.Index] = entry.Observation
	}
	return results
}

func evaluateTestCircuitBreaker(spec *testCircuitBreakerSpec, phases []testLoadChaosPhaseEvidence, index int, clock *testLoadPhaseClock, observation testCircuitObservation) *testCircuitBreakerEvidence {
	window := circuitDuration(spec.Window)
	evidence := &testCircuitBreakerEvidence{Request: spec.Request, MinRequests: spec.MinRequests, CounterPath: spec.CounterPath, StopWithinMS: circuitDuration(spec.StopWithin).Milliseconds(), ResumeWithinMS: circuitDuration(spec.ResumeWithin).Milliseconds(), WindowMS: window.Milliseconds(), MaxProbes: spec.MaxProbes, ResumeMinAttempts: spec.ResumeMinAttempts}
	fail := func(message string) *testCircuitBreakerEvidence { evidence.Error = message; return evidence }
	if observation.Error != "" {
		return fail(observation.Error)
	}
	for j, sample := range observation.Samples {
		if sample.Count < 0 || (j > 0 && (sample.Count < observation.Samples[j-1].Count || !sample.At.After(observation.Samples[j-1].At))) {
			return fail("counter reset or nonmonotonic observation")
		}
	}
	for phaseIndex := index - 1; phaseIndex <= index+1; phaseIndex++ {
		if phaseIndex >= len(phases) {
			return fail("phase evidence missing")
		}
		requests := 0
		for _, request := range phases[phaseIndex].Steps {
			if request.Name == spec.Request {
				requests += request.Requests
			}
		}
		if requests < spec.MinRequests {
			return fail(fmt.Sprintf("phase %q has %d incoming samples; requires %d", phases[phaseIndex].Name, requests, spec.MinRequests))
		}
		boundary := clock.window(phaseIndex)
		if boundary.StartedAt.IsZero() || boundary.FinishedAt.IsZero() || boundary.FinishedAt.Sub(boundary.StartedAt) < window {
			return fail("phase ended early or has incomplete timing evidence")
		}
		var samples []testCircuitSample
		for _, sample := range observation.Samples {
			if sample.Phase == phaseIndex {
				samples = append(samples, sample)
			}
		}
		if len(samples) < 2 || samples[0].At.Before(boundary.StartedAt) || samples[len(samples)-1].At.After(boundary.FinishedAt) || samples[0].At.Sub(boundary.StartedAt) > window/2 || boundary.FinishedAt.Sub(samples[len(samples)-1].At) > window/2 {
			return fail(fmt.Sprintf("phase %q has incomplete counter coverage", phases[phaseIndex].Name))
		}
		var incoming []time.Time
		for _, request := range phases[phaseIndex].CircuitRequests {
			if request.Name == spec.Request {
				incoming = append(incoming, request.At)
			}
		}
		sort.Slice(incoming, func(i, j int) bool { return incoming[i].Before(incoming[j]) })
		left := 0
		fullWindows := 0
		sustained := false
		for j := 1; j < len(samples); j++ {
			sample := samples[j]
			if sample.At.Sub(samples[j-1].At) > window/2 {
				return fail("counter sampling gap")
			}
			target := sample.At.Add(-window)
			if samples[0].At.After(target) {
				continue
			}
			for left+1 < j && !samples[left+1].At.After(target) {
				left++
			}
			start := samples[left]
			begin := sort.Search(len(incoming), func(k int) bool { return !incoming[k].Before(start.At) })
			end := sort.Search(len(incoming), func(k int) bool { return !incoming[k].Before(sample.At) })
			requestCount := end - begin
			if requestCount < spec.MinRequests {
				return fail(fmt.Sprintf("phase %q observation window has %d incoming requests; requires %d", phases[phaseIndex].Name, requestCount, spec.MinRequests))
			}
			attempts := sample.Count - start.Count
			elapsed := sample.At.Sub(boundary.StartedAt).Milliseconds()
			healthy := attempts >= spec.ResumeMinAttempts
			if phaseIndex == index {
				healthy = attempts <= spec.MaxProbes
			}
			fullWindows++
			evidence.Windows = append(evidence.Windows, testCircuitBreakerWindow{IncomingRequests: requestCount, Phase: phases[phaseIndex].Name, StartMS: start.At.Sub(boundary.StartedAt).Milliseconds(), EndMS: elapsed, Attempts: attempts, Passed: healthy})
			switch phaseIndex {
			case index - 1:
				if !healthy {
					return fail("baseline dependency activity is below resume_min_attempts per window")
				}
			case index:
				if evidence.StoppedMS == nil {
					if healthy && elapsed <= evidence.StopWithinMS {
						value := elapsed
						evidence.StoppedMS = &value
					}
				} else {
					if !healthy {
						return fail("dependency probe budget exceeded after suppression")
					}
					if elapsed-*evidence.StoppedMS >= evidence.WindowMS {
						sustained = true
					}
				}
			case index + 1:
				if evidence.ResumedMS == nil {
					if healthy && elapsed <= evidence.ResumeWithinMS {
						value := elapsed
						evidence.ResumedMS = &value
					}
				} else {
					if !healthy {
						return fail("dependency traffic dropped below recovery activity budget")
					}
					if elapsed-*evidence.ResumedMS >= evidence.WindowMS {
						sustained = true
					}
				}
			}
		}
		if fullWindows == 0 {
			return fail("no complete observation window")
		}
		if phaseIndex == index && (evidence.StoppedMS == nil || !sustained) {
			return fail("dependency traffic did not suppress within stop_within and remain suppressed for another window")
		}
		if phaseIndex == index+1 && (evidence.ResumedMS == nil || !sustained) {
			return fail("dependency traffic did not resume within resume_within and sustain for another window")
		}
	}
	evidence.Passed = true
	return evidence
}

func applyTestCircuitBreakers(report *testLoadEvidence, cfg *testLoadConfig, clock *testLoadPhaseClock, observations []testCircuitObservation, scheduleErr error) error {
	var failures []error
	for i, phase := range cfg.ChaosSchedule {
		if phase.CircuitBreaker == nil {
			continue
		}
		observation := testCircuitObservation{Error: "counter observations missing"}
		if i < len(observations) {
			observation = observations[i]
		}
		if scheduleErr != nil {
			observation.Error = "chaos schedule did not complete"
		}
		evidence := evaluateTestCircuitBreaker(phase.CircuitBreaker, report.ChaosPhases, i, clock, observation)
		evidence.From, evidence.To, evidence.Port = phase.Rules[0].From, phase.Rules[0].To, phase.Rules[0].Port
		if i < len(report.ChaosPhases) {
			report.ChaosPhases[i].CircuitBreaker = evidence
		}
		if !evidence.Passed {
			failures = append(failures, fmt.Errorf("phase %q circuit breaker: %s", phase.Name, evidence.Error))
		}
	}
	err := errors.Join(failures...)
	if err != nil {
		report.Status = "failed"
	}
	return err
}
