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
	testLoadMaxVUs              = 50
	testLoadMaxIterations       = 10000
	testLoadMaxRequests         = 100000
	testLoadMaxDuration         = 5 * time.Minute
	testLoadDrainTimeout        = 30 * time.Second
	testLoadMaxStages           = 20
	testLoadMaxRate             = 1000
	testLoadRelativeMaxAttempts = 100
)

type testLoadSpec struct {
	VUs        int                   `yaml:"vus"`
	Rate       int                   `yaml:"rate"`
	Iterations int                   `yaml:"iterations"`
	Duration   string                `yaml:"duration"`
	Pacing     string                `yaml:"pacing"`
	Stages     []testLoadStageSpec   `yaml:"stages"`
	Thresholds testLoadThresholds    `yaml:"thresholds"`
	Relative   *testLoadRelativeSpec `yaml:"relative,omitempty"`
	Workload   string                `yaml:"workload,omitempty"`
	Regression *testRegressionSpec   `yaml:"regression,omitempty"`
}

type testLoadRelativeSpec struct {
	CompareTo          string                              `yaml:"compare_to"`
	P95IncreasePercent *float64                            `yaml:"p95_increase_percent,omitempty"`
	P99IncreasePercent *float64                            `yaml:"p99_increase_percent,omitempty"`
	ErrorRateIncrease  *float64                            `yaml:"error_rate_increase,omitempty"`
	SuccessRateDrop    *float64                            `yaml:"success_rate_drop,omitempty"`
	MinSamples         int                                 `yaml:"min_samples"`
	Steps              map[string]testLoadRelativeStepSpec `yaml:"steps,omitempty"`
	Retry              *testLoadRelativeRetrySpec          `yaml:"retry_until_passes,omitempty"`
}

type testLoadRelativeStepSpec struct {
	BaselineStep       string   `yaml:"baseline_step"`
	CurrentStep        string   `yaml:"current_step"`
	P95IncreasePercent *float64 `yaml:"p95_increase_percent,omitempty"`
	P99IncreasePercent *float64 `yaml:"p99_increase_percent,omitempty"`
	ErrorRateIncrease  *float64 `yaml:"error_rate_increase,omitempty"`
	SuccessRateDrop    *float64 `yaml:"success_rate_drop,omitempty"`
	MinSamples         int      `yaml:"min_samples,omitempty"`
}

type testLoadRelativeRetrySpec struct {
	Timeout           string `yaml:"timeout"`
	Interval          string `yaml:"interval,omitempty"`
	ConsecutivePasses *int   `yaml:"consecutive_passes,omitempty"`
}

type testLoadThresholds struct {
	P95         string                               `yaml:"p95"`
	P99         string                               `yaml:"p99"`
	ErrorRate   *float64                             `yaml:"error_rate"`
	SuccessRate *float64                             `yaml:"success_rate"`
	MinSamples  int                                  `yaml:"min_samples"`
	Steps       map[string]testLoadStepThresholdSpec `yaml:"steps"`
}

type testLoadStepThresholdSpec struct {
	P95         string   `yaml:"p95"`
	P99         string   `yaml:"p99"`
	ErrorRate   *float64 `yaml:"error_rate"`
	SuccessRate *float64 `yaml:"success_rate"`
	MinSamples  int      `yaml:"min_samples"`
}

type testLoadStepThresholdConfig struct {
	P95, P99    time.Duration
	ErrorRate   *float64
	SuccessRate *float64
	MinSamples  int
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
	VUs, Iterations, Rate                                  int
	Duration, Pacing                                       string
	VUsSet, IterationsSet, RateSet, DurationSet, PacingSet bool
}

type testLoadConfig struct {
	VUs, Iterations, Rate, RequestLimit int
	Duration, P95, P99, Pacing          time.Duration
	ErrorRate                           float64
	ErrorRateExplicit                   bool
	SuccessRate                         *float64
	MinSamples                          int
	Stages                              []testLoadStage
	StepThresholds                      map[string]testLoadStepThresholdConfig
	Progress                            func(testLoadProgress)
	Regression                          testRegressionConfig
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
	Requests    int             `json:"requests"`
	Failures    int             `json:"failures"`
	ErrorRate   float64         `json:"error_rate"`
	SuccessRate float64         `json:"success_rate"`
	Statuses    map[string]int  `json:"statuses"`
	Latency     testLoadLatency `json:"latency"`
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
	Metric   string  `json:"metric"`
	Operator string  `json:"operator"`
	Limit    float64 `json:"limit"`
	Actual   float64 `json:"actual"`
	Passed   bool    `json:"passed"`
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
	Arrival    *testLoadArrivalEvidence    `json:"arrival,omitempty"`
	Workload   *testLoadWorkload           `json:"workload,omitempty"`
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
	if spec.Rate < 0 || spec.Rate > testLoadMaxRate {
		return fmt.Errorf("load.rate must be between 1 and %d when supplied", testLoadMaxRate)
	}
	if spec.Rate > 0 {
		if spec.Duration == "" || spec.Iterations != 0 || len(spec.Stages) > 0 {
			return errors.New("arrival-rate load requires duration and cannot use iterations or stages")
		}
		if spec.Pacing != "" {
			pacing, err := time.ParseDuration(spec.Pacing)
			if err != nil || pacing != 0 {
				return errors.New("arrival-rate load cannot use pacing")
			}
		}
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
	if err := validateTestLoadRelativeSpec(spec.Relative); err != nil {
		return fmt.Errorf("load.relative: %w", err)
	}
	if err := validateTestLoadThresholds(spec.Thresholds.P95, spec.Thresholds.P99,
		spec.Thresholds.ErrorRate, spec.Thresholds.SuccessRate, spec.Thresholds.MinSamples); err != nil {
		return fmt.Errorf("load.thresholds: %w", err)
	}
	if len(spec.Thresholds.Steps) > 200 {
		return errors.New("load.thresholds.steps may declare at most 200 step budgets")
	}
	for name, threshold := range spec.Thresholds.Steps {
		if !testHTTPNamePattern.MatchString(name) {
			return fmt.Errorf("load.thresholds.steps has an invalid step name %q", name)
		}
		if threshold.P95 == "" && threshold.P99 == "" && threshold.ErrorRate == nil && threshold.SuccessRate == nil && threshold.MinSamples == 0 {
			return fmt.Errorf("load.thresholds.steps.%s needs p95, p99, error_rate, success_rate, or min_samples", name)
		}
		if err := validateTestLoadThresholds(threshold.P95, threshold.P99,
			threshold.ErrorRate, threshold.SuccessRate, threshold.MinSamples); err != nil {
			return fmt.Errorf("load.thresholds.steps.%s: %w", name, err)
		}
	}
	if spec.Workload != "" && !testSuiteNamePattern.MatchString(spec.Workload) {
		return errors.New("load.workload needs 1..80 lowercase letters, digits, or hyphens starting with a letter")
	}
	return validateTestRegressionSpec(spec.Regression)
}

func validateTestLoadRelativeSpec(spec *testLoadRelativeSpec) error {
	if spec == nil {
		return nil
	}
	if !testHTTPNamePattern.MatchString(spec.CompareTo) {
		return errors.New("compare_to must name an earlier staged step")
	}
	if spec.P95IncreasePercent == nil && spec.P99IncreasePercent == nil &&
		spec.ErrorRateIncrease == nil && spec.SuccessRateDrop == nil && len(spec.Steps) == 0 {
		return errors.New("relative comparison needs at least one latency or rate budget")
	}
	if err := validateTestLoadRelativeBudgets(spec.P95IncreasePercent, spec.P99IncreasePercent,
		spec.ErrorRateIncrease, spec.SuccessRateDrop); err != nil {
		return err
	}
	if spec.MinSamples < 1 || spec.MinSamples > testLoadMaxRequests {
		return fmt.Errorf("min_samples must be between 1 and %d", testLoadMaxRequests)
	}
	if len(spec.Steps) > 200 {
		return errors.New("steps may declare at most 200 per-request budgets")
	}
	for name, step := range spec.Steps {
		if !testHTTPNamePattern.MatchString(name) {
			return fmt.Errorf("steps has an invalid comparison name %q", name)
		}
		if !testHTTPNamePattern.MatchString(step.BaselineStep) {
			return fmt.Errorf("steps.%s.baseline_step must name an HTTP step in compare_to", name)
		}
		if !testHTTPNamePattern.MatchString(step.CurrentStep) {
			return fmt.Errorf("steps.%s.current_step must name an HTTP step in the current staged step", name)
		}
		if step.P95IncreasePercent == nil && step.P99IncreasePercent == nil &&
			step.ErrorRateIncrease == nil && step.SuccessRateDrop == nil {
			return fmt.Errorf("steps.%s needs at least one latency or rate budget", name)
		}
		if err := validateTestLoadRelativeBudgets(step.P95IncreasePercent, step.P99IncreasePercent,
			step.ErrorRateIncrease, step.SuccessRateDrop); err != nil {
			return fmt.Errorf("steps.%s: %w", name, err)
		}
		if step.MinSamples < 0 || step.MinSamples > testLoadMaxRequests {
			return fmt.Errorf("steps.%s.min_samples must be between 1 and %d when supplied", name, testLoadMaxRequests)
		}
	}
	if spec.Retry != nil {
		if spec.Retry.ConsecutivePasses != nil && (*spec.Retry.ConsecutivePasses < 1 || *spec.Retry.ConsecutivePasses > testLoadRelativeMaxAttempts) {
			return fmt.Errorf("retry_until_passes.consecutive_passes must be between 1 and %d", testLoadRelativeMaxAttempts)
		}
		timeout, err := time.ParseDuration(spec.Retry.Timeout)
		if err != nil || timeout < time.Second || timeout > testLoadMaxDuration {
			return errors.New("retry_until_passes.timeout must be between 1s and 5m")
		}
		if spec.Retry.Interval != "" {
			interval, err := time.ParseDuration(spec.Retry.Interval)
			if err != nil || interval < 100*time.Millisecond || interval > 30*time.Second {
				return errors.New("retry_until_passes.interval must be between 100ms and 30s")
			}
			if interval > timeout {
				return errors.New("retry_until_passes.interval cannot exceed its timeout")
			}
		}
	}
	return nil
}

func validateTestLoadRelativeBudgets(p95, p99, errorRate, successRate *float64) error {
	for _, budget := range []struct {
		name  string
		value *float64
	}{{"p95_increase_percent", p95}, {"p99_increase_percent", p99}} {
		if budget.value != nil && (!finiteNonnegative(*budget.value) || *budget.value > 1000) {
			return fmt.Errorf("%s must be a finite percent between 0 and 1000", budget.name)
		}
	}
	for _, budget := range []struct {
		name  string
		value *float64
	}{{"error_rate_increase", errorRate}, {"success_rate_drop", successRate}} {
		if budget.value != nil && (!finiteNonnegative(*budget.value) || *budget.value > 1) {
			return fmt.Errorf("%s must be a finite fraction between 0 and 1", budget.name)
		}
	}
	return nil
}

func validateTestLoadThresholds(p95, p99 string, errorRate, successRate *float64, minSamples int) error {
	for _, threshold := range []struct{ name, value string }{{"p95", p95}, {"p99", p99}} {
		name, value := threshold.name, threshold.value
		if value == "" {
			continue
		}
		limit, err := time.ParseDuration(value)
		if err != nil || limit <= 0 || limit > time.Minute {
			return fmt.Errorf("%s must be a positive duration up to 1m", name)
		}
	}
	for _, threshold := range []struct {
		name  string
		value *float64
	}{{"error_rate", errorRate}, {"success_rate", successRate}} {
		name, value := threshold.name, threshold.value
		if value == nil {
			continue
		}
		rate := *value
		if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 || rate > 1 {
			return fmt.Errorf("%s must be a finite fraction between 0 and 1", name)
		}
	}
	if minSamples < 0 || minSamples > testLoadMaxRequests {
		return fmt.Errorf("min_samples must be between 1 and %d when supplied", testLoadMaxRequests)
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
	if scenario.Load.Regression != nil {
		for name := range scenario.Load.Regression.Steps {
			if !names[name] {
				return fmt.Errorf("load.regression.steps references undeclared HTTP step %q", name)
			}
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
	if overrides.RateSet && overrides.IterationsSet {
		return nil, errors.New("choose --rate with --duration or --iterations, not both")
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
		spec.Iterations, spec.Duration, spec.Stages, spec.Rate = overrides.Iterations, "", nil, 0
	}
	if overrides.RateSet {
		if overrides.Rate < 1 {
			return nil, errors.New("--rate must be at least 1 journey per second")
		}
		spec.Rate, spec.Iterations, spec.Stages, spec.Pacing = overrides.Rate, 0, nil, ""
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
	cfg := &testLoadConfig{VUs: spec.VUs, Iterations: spec.Iterations, Rate: spec.Rate, RequestLimit: testLoadMaxRequests}
	cfg.Regression = resolveTestRegressionConfig(spec.Regression)
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
	if cfg.Rate > 0 && cfg.plannedArrivals() > testLoadMaxRequests/(len(scenario.Requests)+len(scenario.Checks)) {
		return nil, fmt.Errorf("arrival schedule would exceed %d HTTP steps; select a lower rate or shorter duration", testLoadMaxRequests)
	}
	if spec.Thresholds.ErrorRate != nil {
		cfg.ErrorRate = *spec.Thresholds.ErrorRate
		cfg.ErrorRateExplicit = true
	}
	if spec.Thresholds.P95 != "" {
		cfg.P95, _ = time.ParseDuration(spec.Thresholds.P95)
	}
	if spec.Thresholds.P99 != "" {
		cfg.P99, _ = time.ParseDuration(spec.Thresholds.P99)
	}
	cfg.SuccessRate = spec.Thresholds.SuccessRate
	cfg.MinSamples = spec.Thresholds.MinSamples
	if spec.Pacing != "" {
		cfg.Pacing, _ = time.ParseDuration(spec.Pacing)
	}
	cfg.StepThresholds = make(map[string]testLoadStepThresholdConfig, len(spec.Thresholds.Steps))
	for name, threshold := range spec.Thresholds.Steps {
		p95, _ := time.ParseDuration(threshold.P95)
		p99, _ := time.ParseDuration(threshold.P99)
		cfg.StepThresholds[name] = testLoadStepThresholdConfig{
			P95: p95, P99: p99, ErrorRate: threshold.ErrorRate,
			SuccessRate: threshold.SuccessRate, MinSamples: threshold.MinSamples,
		}
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
	if cfg.Rate > 0 {
		mode = "arrival-rate"
	}
	report := &testLoadEvidence{Mode: mode, Status: "not_started", VUs: cfg.VUs, MaxVUs: cfg.maxVUs(), IterationLimit: cfg.Iterations, RequestedDurationMS: cfg.Duration.Milliseconds(), RequestLimit: cfg.RequestLimit, PacingMS: loadMilliseconds(cfg.Pacing)}
	if cfg.Rate > 0 {
		report.Arrival = &testLoadArrivalEvidence{TargetRate: cfg.Rate, Planned: cfg.plannedArrivals()}
	}
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
	arrivalsScheduled  int
	droppedCapacity    int
	droppedLate        int
	schedulingDuration time.Duration
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
	return runTestLoadWithCaptures(parent, baseURL, runID, consumerEnv, steps, data, nil, cfg)
}

func runTestLoadWithCaptures(parent context.Context, baseURL, runID string, consumerEnv []string, steps []testHTTPRequest, data map[string]any, captures map[string]string, cfg *testLoadConfig) (*testLoadEvidence, error) {
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
	journey := testLoadJourney{
		Client: client, BaseURL: baseURL, RunID: runID, ConsumerKeys: consumerKeys, Steps: steps, Data: data, InitialCaptures: captures,
	}
	runJourney := func(iteration int) { journey.run(ctx, collector, cfg, iteration) }
	stopProgress := monitorTestLoadProgress(ctx, collector, cfg, started)
	if cfg.Rate > 0 {
		runTestArrivalLoad(ctx, collector, cfg, started, runJourney)
	} else {
		runTestConcurrencyLoad(ctx, collector, cfg, started, runJourney)
	}
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
	case report.Arrival != nil && report.Arrival.Dropped > 0:
		report.StopReason, executionError = "dropped_arrivals", fmt.Errorf("load dropped %d of %d scheduled arrivals (%d at the VU limit, %d late); the requested arrival rate was not delivered", report.Arrival.Dropped, report.Arrival.Scheduled, report.Arrival.DroppedCapacity, report.Arrival.DroppedLate)
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
	if report.Arrival != nil {
		report.Arrival.Scheduled = c.arrivalsScheduled
		report.Arrival.DroppedCapacity, report.Arrival.DroppedLate = c.droppedCapacity, c.droppedLate
		report.Arrival.Dropped = c.droppedCapacity + c.droppedLate
		report.Arrival.SchedulingDurationMS = loadMilliseconds(c.schedulingDuration)
		if c.schedulingDuration > 0 {
			report.Arrival.StartedPerSecond = float64(c.started) / c.schedulingDuration.Seconds()
		}
	}
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
			entry.SuccessRate = 1 - entry.ErrorRate
		}
		if budget, ok := cfg.StepThresholds[entry.Name]; ok {
			entry.Thresholds = testLoadThresholdEvidenceFor(entry.testLoadMetrics,
				budget.P95, budget.P99, budget.ErrorRate, budget.SuccessRate, budget.MinSamples)
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
		report.SuccessRate = 1 - report.ErrorRate
		if duration > 0 {
			report.RequestsPerSecond = float64(report.Requests) / duration.Seconds()
		}
	}
	errorRateThreshold := &cfg.ErrorRate
	if cfg.SuccessRate != nil && !cfg.ErrorRateExplicit {
		errorRateThreshold = nil
	}
	report.Thresholds = testLoadThresholdEvidenceFor(report.testLoadMetrics,
		cfg.P95, cfg.P99, errorRateThreshold, cfg.SuccessRate, cfg.MinSamples)
	return report
}

func testLoadThresholdEvidenceFor(metrics testLoadMetrics, p95, p99 time.Duration, errorRate, successRate *float64, minSamples int) []testLoadThresholdEvidence {
	var evidence []testLoadThresholdEvidence
	if errorRate != nil {
		evidence = append(evidence, testLoadThresholdEvidence{Metric: "error_rate", Operator: "<=", Limit: *errorRate, Actual: metrics.ErrorRate, Passed: metrics.Requests > 0 && metrics.ErrorRate <= *errorRate})
	}
	if successRate != nil {
		evidence = append(evidence, testLoadThresholdEvidence{Metric: "success_rate", Operator: ">=", Limit: *successRate, Actual: metrics.SuccessRate, Passed: metrics.Requests > 0 && metrics.SuccessRate >= *successRate})
	}
	if p95 > 0 {
		evidence = append(evidence, testLoadThresholdEvidence{Metric: "p95_ms", Operator: "<=", Limit: loadMilliseconds(p95), Actual: metrics.Latency.P95MS, Passed: metrics.Requests > 0 && metrics.Latency.P95MS <= loadMilliseconds(p95)})
	}
	if p99 > 0 {
		evidence = append(evidence, testLoadThresholdEvidence{Metric: "p99_ms", Operator: "<=", Limit: loadMilliseconds(p99), Actual: metrics.Latency.P99MS, Passed: metrics.Requests > 0 && metrics.Latency.P99MS <= loadMilliseconds(p99)})
	}
	if minSamples > 0 {
		evidence = append(evidence, testLoadThresholdEvidence{Metric: "min_samples", Operator: ">=", Limit: float64(minSamples), Actual: float64(metrics.Requests), Passed: metrics.Requests >= minSamples})
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
	if arrival := report.Arrival; arrival != nil {
		_, _ = fmt.Fprintf(out, "  arrivals: target %d journeys/s, achieved %.2f starts/s; %d/%d scheduled, %d started, %d dropped (%d at VU limit, %d late)\n", arrival.TargetRate, arrival.StartedPerSecond, arrival.Scheduled, arrival.Planned, report.IterationsStarted, arrival.Dropped, arrival.DroppedCapacity, arrival.DroppedLate)
	}
	_, _ = fmt.Fprintf(out, "  load: %d/%d journeys completed, %d HTTP steps, %.2f%% success, %.2f%% errors; %.1f steps/s, p95 %.3fms, p99 %.3fms (start %d VUs, max %d, peak %d)\n",
		report.IterationsCompleted, report.IterationsStarted, report.Requests,
		report.SuccessRate*100, report.ErrorRate*100, report.RequestsPerSecond,
		report.Latency.P95MS, report.Latency.P99MS, report.VUs, report.MaxVUs, report.PeakVUs)
	for _, threshold := range report.Thresholds {
		status := "failed"
		if threshold.Passed {
			status = "passed"
		}
		_, _ = fmt.Fprintf(out, "  %s %s %.4g: %.4g (%s)\n", threshold.Metric, threshold.Operator, threshold.Limit, threshold.Actual, status)
	}
	shown, remaining := 0, 0
	for _, step := range report.Steps {
		for _, threshold := range step.Thresholds {
			status := "failed"
			if threshold.Passed {
				status = "passed"
			}
			_, _ = fmt.Fprintf(out, "  %s.%s %s %.4g: %.4g (%s)\n", step.Name, threshold.Metric, threshold.Operator, threshold.Limit, threshold.Actual, status)
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
