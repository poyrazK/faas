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
	testLoadMaxStages     = 20
)

type testLoadSpec struct {
	VUs        int                 `yaml:"vus"`
	Iterations int                 `yaml:"iterations"`
	Duration   string              `yaml:"duration"`
	Pacing     string              `yaml:"pacing"`
	Stages     []testLoadStageSpec `yaml:"stages"`
	Thresholds testLoadThresholds  `yaml:"thresholds"`
}

type testLoadThresholds struct {
	P95       string                               `yaml:"p95"`
	ErrorRate *float64                             `yaml:"error_rate"`
	Steps     map[string]testLoadStepThresholdSpec `yaml:"steps"`
}

type testLoadStepThresholdSpec struct {
	P95       string   `yaml:"p95"`
	ErrorRate *float64 `yaml:"error_rate"`
}

type testLoadStepThresholdConfig struct {
	P95       time.Duration
	ErrorRate *float64
}

type testLoadStageSpec struct {
	Duration string `yaml:"duration"`
	Target   *int   `yaml:"target"`
}

type testLoadStage struct {
	Duration time.Duration
	Target   int
}

type testLoadOverrides struct {
	VUs, Iterations                               int
	Duration, Pacing                              string
	VUsSet, IterationsSet, DurationSet, PacingSet bool
}

type testLoadConfig struct {
	VUs, Iterations, RequestLimit int
	Duration, P95, Pacing         time.Duration
	ErrorRate                     float64
	Stages                        []testLoadStage
	StepThresholds                map[string]testLoadStepThresholdConfig
	Progress                      func(testLoadProgress)
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
	Errors     []string                    `json:"errors,omitempty"`
	Thresholds []testLoadThresholdEvidence `json:"thresholds,omitempty"`
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
	MaxVUs                int     `json:"max_vus"`
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
	PacingMS              float64 `json:"pacing_ms,omitempty"`
	testLoadMetrics
	Steps      []testLoadStepEvidence      `json:"steps,omitempty"`
	Thresholds []testLoadThresholdEvidence `json:"thresholds,omitempty"`
	Stages     []testLoadStageEvidence     `json:"stages,omitempty"`
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
	if spec.Pacing != "" {
		pacing, err := time.ParseDuration(spec.Pacing)
		if err != nil || pacing < 0 || pacing > time.Minute {
			return errors.New("load.pacing must be between 0s and 1m")
		}
	}
	if len(spec.Stages) > 0 {
		if spec.Iterations != 0 || spec.Duration != "" {
			return errors.New("load.stages cannot be combined with iterations or duration")
		}
		if len(spec.Stages) > testLoadMaxStages {
			return fmt.Errorf("load may declare at most %d stages", testLoadMaxStages)
		}
		var total time.Duration
		for i, stage := range spec.Stages {
			duration, err := time.ParseDuration(stage.Duration)
			if err != nil || duration < time.Second || duration > testLoadMaxDuration {
				return fmt.Errorf("load stage %d duration must be between 1s and 5m", i+1)
			}
			if stage.Target == nil || *stage.Target < 0 || *stage.Target > testLoadMaxVUs {
				return fmt.Errorf("load stage %d needs a target between 0 and %d", i+1, testLoadMaxVUs)
			}
			total += duration
		}
		if total > testLoadMaxDuration {
			return errors.New("load stages must total at most 5m")
		}
	}
	if err := validateTestLoadThresholds(spec.Thresholds.P95, spec.Thresholds.ErrorRate); err != nil {
		return fmt.Errorf("load.thresholds: %w", err)
	}
	if len(spec.Thresholds.Steps) > 200 {
		return errors.New("load.thresholds.steps may declare at most 200 step budgets")
	}
	for name, threshold := range spec.Thresholds.Steps {
		if !testHTTPNamePattern.MatchString(name) {
			return fmt.Errorf("load.thresholds.steps has an invalid step name %q", name)
		}
		if threshold.P95 == "" && threshold.ErrorRate == nil {
			return fmt.Errorf("load.thresholds.steps.%s needs p95 or error_rate", name)
		}
		if err := validateTestLoadThresholds(threshold.P95, threshold.ErrorRate); err != nil {
			return fmt.Errorf("load.thresholds.steps.%s: %w", name, err)
		}
	}
	return nil
}

func validateTestLoadThresholds(p95 string, errorRate *float64) error {
	if p95 != "" {
		limit, err := time.ParseDuration(p95)
		if err != nil || limit <= 0 || limit > time.Minute {
			return errors.New("p95 must be a positive duration up to 1m")
		}
	}
	if errorRate != nil {
		rate := *errorRate
		if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 || rate > 1 {
			return errors.New("error_rate must be a finite fraction between 0 and 1")
		}
	}
	return nil
}

func validateTestLoadStepThresholds(scenario testScenario) error {
	if scenario.Load == nil {
		return nil
	}
	names := make(map[string]bool)
	for _, step := range append(append([]testHTTPRequest{}, scenario.Requests...), scenario.Checks...) {
		names[step.Name] = true
	}
	for name := range scenario.Load.Thresholds.Steps {
		if !names[name] {
			return fmt.Errorf("load.thresholds.steps references undeclared HTTP step %q", name)
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
		spec.Iterations, spec.Duration, spec.Stages = overrides.Iterations, "", nil
	}
	if overrides.DurationSet {
		if overrides.Duration == "" {
			return nil, errors.New("--duration must be between 1s and 5m")
		}
		spec.Duration, spec.Iterations, spec.Stages = overrides.Duration, 0, nil
	}
	if overrides.PacingSet {
		if overrides.Pacing == "" {
			return nil, errors.New("--pacing must be between 0s and 1m")
		}
		spec.Pacing = overrides.Pacing
	}
	if err := validateTestLoadSpec(&spec); err != nil {
		return nil, err
	}
	if err := validateTestLoadStepThresholds(scenario); err != nil {
		return nil, err
	}
	cfg := &testLoadConfig{VUs: spec.VUs, Iterations: spec.Iterations, RequestLimit: testLoadMaxRequests}
	if cfg.VUs == 0 {
		cfg.VUs = 1
	}
	if len(spec.Stages) > 0 {
		for _, stage := range spec.Stages {
			duration, _ := time.ParseDuration(stage.Duration)
			cfg.Stages = append(cfg.Stages, testLoadStage{Duration: duration, Target: *stage.Target})
			cfg.Duration += duration
		}
	} else if spec.Duration != "" {
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
	if spec.Pacing != "" {
		cfg.Pacing, _ = time.ParseDuration(spec.Pacing)
	}
	cfg.StepThresholds = make(map[string]testLoadStepThresholdConfig, len(spec.Thresholds.Steps))
	for name, threshold := range spec.Thresholds.Steps {
		p95, _ := time.ParseDuration(threshold.P95)
		cfg.StepThresholds[name] = testLoadStepThresholdConfig{P95: p95, ErrorRate: threshold.ErrorRate}
	}
	if scenario.Timeout != "" && cfg.Duration > 0 {
		timeout, _ := time.ParseDuration(scenario.Timeout)
		if cfg.Duration > timeout {
			return nil, errors.New("load scheduling duration exceeds the scenario timeout")
		}
	}
	return cfg, nil
}

func newTestLoadEvidence(cfg *testLoadConfig) *testLoadEvidence {
	mode := "iterations"
	if cfg.Duration > 0 {
		mode = "duration"
	}
	if len(cfg.Stages) > 0 {
		mode = "stages"
	}
	report := &testLoadEvidence{Mode: mode, Status: "not_started", VUs: cfg.VUs, MaxVUs: cfg.maxVUs(), IterationLimit: cfg.Iterations, RequestedDurationMS: cfg.Duration.Milliseconds(), RequestLimit: cfg.RequestLimit, PacingMS: loadMilliseconds(cfg.Pacing)}
	startVUs := cfg.VUs
	for i, stage := range cfg.Stages {
		report.Stages = append(report.Stages, testLoadStageEvidence{Index: i + 1, StartVUs: startVUs, TargetVUs: stage.Target, DurationMS: stage.Duration.Milliseconds()})
		startVUs = stage.Target
	}
	return report
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
	stageStarts        []int
}

func (c *testLoadCollector) next(ctx context.Context, cfg *testLoadConfig, started time.Time, vu int, readyAt time.Time) (int, time.Duration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(started)
	if ctx.Err() != nil || c.budgetExceeded || (cfg.Iterations > 0 && c.started >= cfg.Iterations) || (cfg.Duration > 0 && elapsed >= cfg.Duration) {
		return 0, 0, false
	}
	target, stage := cfg.targetAt(elapsed)
	delay := time.Duration(0)
	if vu >= target {
		delay = 20 * time.Millisecond
	} else if now.Before(readyAt) {
		delay = readyAt.Sub(now)
	}
	if delay > 0 {
		if delay > 20*time.Millisecond {
			delay = 20 * time.Millisecond
		}
		if cfg.Duration > 0 && delay > cfg.Duration-elapsed {
			delay = cfg.Duration - elapsed
		}
		return 0, delay, true
	}
	c.started++
	c.active++
	if c.active > c.peak {
		c.peak = c.active
	}
	if stage >= 0 && stage < len(c.stageStarts) {
		c.stageStarts[stage]++
	}
	return c.started, 0, true
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
	maxDuration := testLoadMaxDuration
	if cfg.Duration > 0 {
		maxDuration = cfg.Duration + testLoadDrainTimeout
	}
	ctx, cancel := context.WithTimeout(parent, maxDuration)
	defer cancel()
	transport := &http.Transport{
		DialContext:  (&net.Dialer{Timeout: testLoadDrainTimeout, KeepAlive: testLoadDrainTimeout}).DialContext,
		MaxIdleConns: cfg.maxVUs(), MaxIdleConnsPerHost: cfg.maxVUs(), MaxConnsPerHost: cfg.maxVUs(),
		IdleConnTimeout: testLoadDrainTimeout, ResponseHeaderTimeout: testLoadDrainTimeout,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: testLoadDrainTimeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	consumerKeys := testHTTPConsumerKeys(consumerEnv)
	collector := &testLoadCollector{steps: make([]testLoadStepSamples, len(steps)), stageStarts: make([]int, len(cfg.Stages))}
	for i, step := range steps {
		collector.steps[i].evidence = testLoadStepEvidence{Name: step.Name, Method: step.Method, Path: strings.SplitN(step.Path, "?", 2)[0], testLoadMetrics: testLoadMetrics{Statuses: map[string]int{}}}
	}
	var workers sync.WaitGroup
	stopProgress := monitorTestLoadProgress(ctx, collector, cfg, started)
	for vu := 0; vu < cfg.maxVUs(); vu++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			readyAt := time.Time{}
			for {
				iteration, delay, ok := collector.next(ctx, cfg, started, vu, readyAt)
				if !ok {
					return
				}
				if delay > 0 {
					if !waitTestLoad(ctx, delay) {
						return
					}
					continue
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
				readyAt = time.Now().Add(cfg.Pacing)
			}
		}()
	}
	workers.Wait()
	stopProgress()
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
	case len(cfg.Stages) > 0:
		report.StopReason = "stages"
	case cfg.Duration > 0:
		report.StopReason = "duration"
	default:
		report.StopReason = "iterations"
	}
	if report.Requests == 0 && executionError == nil {
		executionError = errors.New("load generated no HTTP steps")
	}
	if executionError == nil {
		if !testLoadThresholdsPassed(report) {
			executionError = errors.New("load thresholds failed")
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
	for i, count := range c.stageStarts {
		report.Stages[i].IterationsStarted = count
	}
	var samples []time.Duration
	for _, step := range c.steps {
		entry := step.evidence
		entry.Latency = testLoadLatencySummary(step.samples)
		if entry.Requests > 0 {
			entry.ErrorRate = float64(entry.Failures) / float64(entry.Requests)
		}
		if budget, ok := cfg.StepThresholds[entry.Name]; ok {
			entry.Thresholds = testLoadThresholdEvidenceFor(entry.testLoadMetrics, budget.P95, budget.ErrorRate)
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
	report.Thresholds = testLoadThresholdEvidenceFor(report.testLoadMetrics, cfg.P95, &cfg.ErrorRate)
	return report
}

func testLoadThresholdEvidenceFor(metrics testLoadMetrics, p95 time.Duration, errorRate *float64) []testLoadThresholdEvidence {
	var evidence []testLoadThresholdEvidence
	if errorRate != nil {
		evidence = append(evidence, testLoadThresholdEvidence{Metric: "error_rate", Limit: *errorRate, Actual: metrics.ErrorRate, Passed: metrics.Requests > 0 && metrics.ErrorRate <= *errorRate})
	}
	if p95 > 0 {
		evidence = append(evidence, testLoadThresholdEvidence{Metric: "p95_ms", Limit: loadMilliseconds(p95), Actual: metrics.Latency.P95MS, Passed: metrics.Requests > 0 && metrics.Latency.P95MS <= loadMilliseconds(p95)})
	}
	return evidence
}

func testLoadThresholdsPassed(report *testLoadEvidence) bool {
	for _, threshold := range report.Thresholds {
		if !threshold.Passed {
			return false
		}
	}
	for _, step := range report.Steps {
		for _, threshold := range step.Thresholds {
			if !threshold.Passed {
				return false
			}
		}
	}
	return true
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
	_, _ = fmt.Fprintf(out, "  load: %d/%d journeys completed, %d HTTP steps, %d failures; %.1f steps/s, p95 %.3fms (start %d VUs, max %d, peak %d)\n", report.IterationsCompleted, report.IterationsStarted, report.Requests, report.Failures, report.RequestsPerSecond, report.Latency.P95MS, report.VUs, report.MaxVUs, report.PeakVUs)
	for _, threshold := range report.Thresholds {
		status := "failed"
		if threshold.Passed {
			status = "passed"
		}
		_, _ = fmt.Fprintf(out, "  %s <= %.4g: %.4g (%s)\n", threshold.Metric, threshold.Limit, threshold.Actual, status)
	}
	shown, remaining := 0, 0
	for _, step := range report.Steps {
		for _, threshold := range step.Thresholds {
			status := "failed"
			if threshold.Passed {
				status = "passed"
			}
			_, _ = fmt.Fprintf(out, "  %s.%s <= %.4g: %.4g (%s)\n", step.Name, threshold.Metric, threshold.Limit, threshold.Actual, status)
		}
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
