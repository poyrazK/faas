package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	testLoadMaxVUs        = 50
	testLoadMaxIterations = 10000
	testLoadMaxRequests   = 100000
	testLoadMaxDuration   = 5 * time.Minute
	testLoadDrainTimeout  = 30 * time.Second
)

type testLoadSpec struct {
	VUs        int                `yaml:"vus"`
	Iterations int                `yaml:"iterations"`
	Duration   string             `yaml:"duration"`
	Thresholds testLoadThresholds `yaml:"thresholds"`
}

type testLoadThresholds struct {
	P95       string   `yaml:"p95"`
	ErrorRate *float64 `yaml:"error_rate"`
}

type testLoadOverrides struct {
	VUs, Iterations                    int
	Duration                           string
	VUsSet, IterationsSet, DurationSet bool
}

type testLoadConfig struct {
	VUs, Iterations, RequestLimit int
	Duration, P95                 time.Duration
	ErrorRate                     float64
}

type testLoadLatency struct {
	Samples int     `json:"samples"`
	MinMS   float64 `json:"min_ms"`
	MeanMS  float64 `json:"mean_ms"`
	P50MS   float64 `json:"p50_ms"`
	P95MS   float64 `json:"p95_ms"`
	P99MS   float64 `json:"p99_ms"`
	MaxMS   float64 `json:"max_ms"`
}

type testLoadMetrics struct {
	Requests  int             `json:"requests"`
	Failures  int             `json:"failures"`
	ErrorRate float64         `json:"error_rate"`
	Statuses  map[string]int  `json:"statuses"`
	Latency   testLoadLatency `json:"latency"`
}

type testLoadStepEvidence struct {
	Name   string `json:"name"`
	Method string `json:"method"`
	Path   string `json:"path"`
	testLoadMetrics
	Errors []string `json:"errors,omitempty"`
}

type testLoadThresholdEvidence struct {
	Metric string  `json:"metric"`
	Limit  float64 `json:"limit"`
	Actual float64 `json:"actual"`
	Passed bool    `json:"passed"`
}

type testLoadEvidence struct {
	Mode                  string  `json:"mode"`
	Status                string  `json:"status"`
	VUs                   int     `json:"vus"`
	PeakVUs               int     `json:"peak_vus"`
	IterationLimit        int     `json:"iteration_limit,omitempty"`
	RequestedDurationMS   int64   `json:"requested_duration_ms,omitempty"`
	RequestLimit          int     `json:"request_limit"`
	IterationsStarted     int     `json:"iterations_started"`
	IterationsCompleted   int     `json:"iterations_completed"`
	IterationsFailed      int     `json:"iterations_failed"`
	IterationsInterrupted int     `json:"iterations_interrupted"`
	DurationMS            float64 `json:"duration_ms"`
	RequestsPerSecond     float64 `json:"requests_per_second"`
	StopReason            string  `json:"stop_reason,omitempty"`
	testLoadMetrics
	Steps      []testLoadStepEvidence      `json:"steps,omitempty"`
	Thresholds []testLoadThresholdEvidence `json:"thresholds,omitempty"`
}

func validateTestLoadSpec(spec *testLoadSpec) error {
	if spec == nil {
		return nil
	}
	if spec.VUs < 0 || spec.VUs > testLoadMaxVUs {
		return fmt.Errorf("load.vus must be between 1 and %d when supplied", testLoadMaxVUs)
	}
	if spec.Iterations < 0 || spec.Iterations > testLoadMaxIterations {
		return fmt.Errorf("load.iterations must be between 1 and %d when supplied", testLoadMaxIterations)
	}
	if spec.Duration != "" {
		duration, err := time.ParseDuration(spec.Duration)
		if err != nil || duration < time.Second || duration > testLoadMaxDuration {
			return errors.New("load.duration must be between 1s and 5m")
		}
		if spec.Iterations > 0 {
			return errors.New("load must choose iterations or duration, not both")
		}
	}
	if spec.Thresholds.P95 != "" {
		limit, err := time.ParseDuration(spec.Thresholds.P95)
		if err != nil || limit <= 0 || limit > time.Minute {
			return errors.New("load.thresholds.p95 must be a positive duration up to 1m")
		}
	}
	if spec.Thresholds.ErrorRate != nil {
		rate := *spec.Thresholds.ErrorRate
		if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 || rate > 1 {
			return errors.New("load.thresholds.error_rate must be a finite fraction between 0 and 1")
		}
	}
	return nil
}

func resolveTestLoadConfig(scenario testScenario, overrides testLoadOverrides) (*testLoadConfig, error) {
	if len(scenario.Requests)+len(scenario.Checks) == 0 {
		return nil, errors.New("load tests need native requests or checks")
	}
	if err := validateLocalTestScenario(scenario); err != nil {
		return nil, err
	}
	spec := testLoadSpec{}
	if scenario.Load != nil {
		spec = *scenario.Load
	}
	if overrides.IterationsSet && overrides.DurationSet {
		return nil, errors.New("choose --iterations or --duration, not both")
	}
	if overrides.VUsSet {
		if overrides.VUs < 1 {
			return nil, errors.New("--vus must be at least 1")
		}
		spec.VUs = overrides.VUs
	}
	if overrides.IterationsSet {
		if overrides.Iterations < 1 {
			return nil, errors.New("--iterations must be at least 1")
		}
		spec.Iterations, spec.Duration = overrides.Iterations, ""
	}
	if overrides.DurationSet {
		if overrides.Duration == "" {
			return nil, errors.New("--duration must be between 1s and 5m")
		}
		spec.Duration, spec.Iterations = overrides.Duration, 0
	}
	if err := validateTestLoadSpec(&spec); err != nil {
		return nil, err
	}
	cfg := &testLoadConfig{VUs: spec.VUs, Iterations: spec.Iterations, RequestLimit: testLoadMaxRequests}
	if cfg.VUs == 0 {
		cfg.VUs = 1
	}
	if spec.Duration != "" {
		cfg.Duration, _ = time.ParseDuration(spec.Duration)
	} else if cfg.Iterations == 0 {
		cfg.Iterations = 100
	}
	if cfg.Iterations > testLoadMaxRequests/(len(scenario.Requests)+len(scenario.Checks)) {
		return nil, fmt.Errorf("load journey would exceed %d HTTP steps; select fewer iterations", testLoadMaxRequests)
	}
	if spec.Thresholds.ErrorRate != nil {
		cfg.ErrorRate = *spec.Thresholds.ErrorRate
	}
	if spec.Thresholds.P95 != "" {
		cfg.P95, _ = time.ParseDuration(spec.Thresholds.P95)
	}
	if scenario.Timeout != "" && cfg.Duration > 0 {
		timeout, _ := time.ParseDuration(scenario.Timeout)
		if cfg.Duration > timeout {
			return nil, errors.New("load.duration exceeds the scenario timeout")
		}
	}
	return cfg, nil
}

func newTestLoadEvidence(cfg *testLoadConfig) *testLoadEvidence {
	mode := "iterations"
	if cfg.Duration > 0 {
		mode = "duration"
	}
	return &testLoadEvidence{Mode: mode, Status: "not_started", VUs: cfg.VUs, IterationLimit: cfg.Iterations, RequestedDurationMS: cfg.Duration.Milliseconds(), RequestLimit: cfg.RequestLimit}
}

type testLoadStepSamples struct {
	evidence testLoadStepEvidence
	samples  []time.Duration
}

type testLoadCollector struct {
	mu                 sync.Mutex
	started, completed int
	failedIterations   int
	active, peak       int
	reservedRequests   int
	budgetExceeded     bool
	steps              []testLoadStepSamples
}

func (c *testLoadCollector) next(ctx context.Context, cfg *testLoadConfig, end time.Time) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil || c.budgetExceeded || (cfg.Iterations > 0 && c.started >= cfg.Iterations) || (!end.IsZero() && !time.Now().Before(end)) {
		return 0, false
	}
	c.started++
	c.active++
	if c.active > c.peak {
		c.peak = c.active
	}
	return c.started, true
}

func (c *testLoadCollector) reserve(ctx context.Context, limit int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil || c.budgetExceeded {
		return false
	}
	if c.reservedRequests >= limit {
		c.budgetExceeded = true
		return false
	}
	c.reservedRequests++
	return true
}

func (c *testLoadCollector) record(index int, result testHTTPRequestEvidence, duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	step := &c.steps[index]
	step.evidence.Requests++
	step.evidence.Statuses[strconv.Itoa(result.Status)]++
	step.samples = append(step.samples, duration)
	if !result.Passed {
		step.evidence.Failures++
		if len(step.evidence.Errors) < 5 {
			duplicate := false
			for _, previous := range step.evidence.Errors {
				duplicate = duplicate || previous == result.Error
			}
			if !duplicate {
				step.evidence.Errors = append(step.evidence.Errors, result.Error)
			}
		}
	}
}

func (c *testLoadCollector) finishIteration(failed, interrupted bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active--
	if !interrupted {
		c.completed++
		if failed {
			c.failedIterations++
		}
	}
}

func runTestLoad(parent context.Context, baseURL, runID string, consumerEnv []string, steps []testHTTPRequest, data map[string]any, cfg *testLoadConfig) (*testLoadEvidence, error) {
	started := time.Now()
	end := time.Time{}
	maxDuration := testLoadMaxDuration
	if cfg.Duration > 0 {
		end = started.Add(cfg.Duration)
		maxDuration = cfg.Duration + testLoadDrainTimeout
	}
	ctx, cancel := context.WithTimeout(parent, maxDuration)
	defer cancel()
	transport := &http.Transport{
		DialContext:  (&net.Dialer{Timeout: testLoadDrainTimeout, KeepAlive: testLoadDrainTimeout}).DialContext,
		MaxIdleConns: cfg.VUs, MaxIdleConnsPerHost: cfg.VUs, MaxConnsPerHost: cfg.VUs,
		IdleConnTimeout: testLoadDrainTimeout, ResponseHeaderTimeout: testLoadDrainTimeout,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: testLoadDrainTimeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	consumerKeys := testHTTPConsumerKeys(consumerEnv)
	collector := &testLoadCollector{steps: make([]testLoadStepSamples, len(steps))}
	for i, step := range steps {
		collector.steps[i].evidence = testLoadStepEvidence{Name: step.Name, Method: step.Method, Path: strings.SplitN(step.Path, "?", 2)[0], testLoadMetrics: testLoadMetrics{Statuses: map[string]int{}}}
	}
	var workers sync.WaitGroup
	for vu := 0; vu < cfg.VUs; vu++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				iteration, ok := collector.next(ctx, cfg, end)
				if !ok {
					return
				}
				values := testHTTPValues(fmt.Sprintf("%s-%d", runID, iteration), nil, data)
				captures := map[string]string{}
				failed, interrupted := false, false
				for i, step := range steps {
					if !collector.reserve(ctx, cfg.RequestLimit) {
						interrupted = true
						break
					}
					result := testHTTPRequestEvidence{Name: step.Name, Method: step.Method}
					requestStarted := time.Now()
					err := runOneTestHTTPRequestWithBody(ctx, client, baseURL, step, values, consumerKeys, captures, data, &result, true)
					result.Passed = err == nil
					if err != nil {
						result.Error = err.Error()
					}
					collector.record(i, result, time.Since(requestStarted))
					if err != nil {
						failed, interrupted = true, ctx.Err() != nil
						break
					}
				}
				collector.finishIteration(failed, interrupted)
			}
		}()
	}
	workers.Wait()
	duration := time.Since(started)
	report := collector.report(cfg, duration)
	var executionError error
	switch {
	case errors.Is(parent.Err(), context.Canceled):
		report.StopReason, executionError = "canceled", errors.New("load canceled")
	case errors.Is(parent.Err(), context.DeadlineExceeded):
		report.StopReason, executionError = "scenario_timeout", errors.New("scenario timed out during load")
	case ctx.Err() != nil:
		report.StopReason, executionError = "time_limit", errors.New("load exceeded its execution deadline")
	case collector.budgetExceeded:
		report.StopReason, executionError = "request_limit", fmt.Errorf("load reached the %d HTTP-step limit before completion", cfg.RequestLimit)
	case cfg.Duration > 0:
		report.StopReason = "duration"
	default:
		report.StopReason = "iterations"
	}
	if report.Requests == 0 && executionError == nil {
		executionError = errors.New("load generated no HTTP steps")
	}
	if executionError == nil {
		for _, threshold := range report.Thresholds {
			if !threshold.Passed {
				executionError = errors.New("load thresholds failed")
				break
			}
		}
	}
	report.Status = "passed"
	if executionError != nil {
		report.Status = "failed"
	}
	return report, executionError
}

func (c *testLoadCollector) report(cfg *testLoadConfig, duration time.Duration) *testLoadEvidence {
	report := newTestLoadEvidence(cfg)
	report.IterationsStarted, report.IterationsCompleted = c.started, c.completed
	report.IterationsFailed, report.IterationsInterrupted = c.failedIterations, c.started-c.completed
	report.PeakVUs = c.peak
	report.DurationMS = loadMilliseconds(duration)
	report.Statuses = map[string]int{}
	var samples []time.Duration
	for _, step := range c.steps {
		entry := step.evidence
		entry.Latency = testLoadLatencySummary(step.samples)
		if entry.Requests > 0 {
			entry.ErrorRate = float64(entry.Failures) / float64(entry.Requests)
		}
		report.Steps = append(report.Steps, entry)
		report.Requests += entry.Requests
		report.Failures += entry.Failures
		for status, count := range entry.Statuses {
			report.Statuses[status] += count
		}
		samples = append(samples, step.samples...)
	}
	report.Latency = testLoadLatencySummary(samples)
	if report.Requests > 0 {
		report.ErrorRate = float64(report.Failures) / float64(report.Requests)
		if duration > 0 {
			report.RequestsPerSecond = float64(report.Requests) / duration.Seconds()
		}
	}
	report.Thresholds = append(report.Thresholds, testLoadThresholdEvidence{Metric: "error_rate", Limit: cfg.ErrorRate, Actual: report.ErrorRate, Passed: report.Requests > 0 && report.ErrorRate <= cfg.ErrorRate})
	if cfg.P95 > 0 {
		report.Thresholds = append(report.Thresholds, testLoadThresholdEvidence{Metric: "p95_ms", Limit: loadMilliseconds(cfg.P95), Actual: report.Latency.P95MS, Passed: report.Requests > 0 && report.Latency.P95MS <= loadMilliseconds(cfg.P95)})
	}
	return report
}

func testLoadLatencySummary(samples []time.Duration) testLoadLatency {
	if len(samples) == 0 {
		return testLoadLatency{}
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	var total float64
	for _, sample := range samples {
		total += loadMilliseconds(sample)
	}
	percentile := func(percent int) float64 {
		index := (len(samples)*percent+99)/100 - 1
		return loadMilliseconds(samples[index])
	}
	return testLoadLatency{Samples: len(samples), MinMS: loadMilliseconds(samples[0]), MeanMS: total / float64(len(samples)), P50MS: percentile(50), P95MS: percentile(95), P99MS: percentile(99), MaxMS: loadMilliseconds(samples[len(samples)-1])}
}

func loadMilliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}

func printTestLoadSummary(out io.Writer, report *testLoadEvidence) {
	if report.Status == "not_started" {
		_, _ = fmt.Fprintln(out, "  load: not started")
		return
	}
	_, _ = fmt.Fprintf(out, "  load: %d/%d journeys completed, %d HTTP steps, %d failures; %.1f steps/s, p95 %.3fms (%d VUs, peak %d)\n", report.IterationsCompleted, report.IterationsStarted, report.Requests, report.Failures, report.RequestsPerSecond, report.Latency.P95MS, report.VUs, report.PeakVUs)
	for _, threshold := range report.Thresholds {
		status := "failed"
		if threshold.Passed {
			status = "passed"
		}
		_, _ = fmt.Fprintf(out, "  %s <= %.4g: %.4g (%s)\n", threshold.Metric, threshold.Limit, threshold.Actual, status)
	}
	shown, remaining := 0, 0
	for _, step := range report.Steps {
		if step.Failures == 0 {
			continue
		}
		if shown == 10 {
			remaining++
			continue
		}
		shown++
		_, _ = fmt.Fprintf(out, "  %s: %d/%d failed HTTP steps", step.Name, step.Failures, step.Requests)
		if len(step.Errors) > 0 {
			_, _ = fmt.Fprintf(out, ": %s", step.Errors[0])
		}
		_, _ = fmt.Fprintln(out)
	}
	if remaining > 0 {
		_, _ = fmt.Fprintf(out, "  %d more steps failed; use --report to save all step metrics\n", remaining)
	}
}
