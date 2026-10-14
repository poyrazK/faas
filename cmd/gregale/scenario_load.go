package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/chaos"
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
	PoolExhaustion   *testPoolExhaustionSpec  `yaml:"pool_exhaustion,omitempty" json:"pool_exhaustion,omitempty"`
	ConnectionLeaks  *testConnectionLeaksSpec `yaml:"connection_leaks,omitempty" json:"connection_leaks,omitempty"`
	FaultCampaign    *testFaultCampaignSpec   `yaml:"fault_campaign,omitempty" json:"fault_campaign,omitempty"`
	campaignExpanded bool
	OutageCycles     *testOutageCyclesSpec `yaml:"outage_cycles,omitempty" json:"outage_cycles,omitempty"`
	cyclesExpanded   bool
	Streams          []testLoadStreamSpec     `yaml:"streams,omitempty" json:"Streams,omitempty"`
	VUs              int                      `yaml:"vus"`
	Rate             int                      `yaml:"rate"`
	Iterations       int                      `yaml:"iterations"`
	Duration         string                   `yaml:"duration"`
	Pacing           string                   `yaml:"pacing"`
	Stages           []testLoadStageSpec      `yaml:"stages"`
	Thresholds       testLoadThresholds       `yaml:"thresholds"`
	Relative         *testLoadRelativeSpec    `yaml:"relative,omitempty"`
	Workload         string                   `yaml:"workload,omitempty"`
	Regression       *testRegressionSpec      `yaml:"regression,omitempty"`
	ChaosSchedule    []testLoadChaosPhaseSpec `yaml:"chaos_schedule,omitempty"`
}

type testLoadChaosPhaseSpec struct {
	RequestDeadlines   map[string]testRequestDeadlineSpec   `yaml:"request_deadlines,omitempty" json:"request_deadlines,omitempty"`
	Assertions         []string                             `yaml:"assertions,omitempty" json:"assertions,omitempty"`
	Cycle              int                                  `yaml:"-" json:"cycle,omitempty"`
	StepRecovery       map[string]*testLoadRecoverySpec     `yaml:"step_recovery,omitempty" json:"StepRecovery,omitempty"`
	StepThresholds     map[string]testLoadStepThresholdSpec `yaml:"step_thresholds,omitempty" json:"StepThresholds,omitempty"`
	CircuitBreaker     *testCircuitBreakerSpec              `yaml:"circuit_breaker,omitempty" json:"CircuitBreaker,omitempty"`
	ConnectionAttempts *testRetryAmplificationSpec          `yaml:"connection_attempts,omitempty" json:"ConnectionAttempts,omitempty"`
	RetryAmplification *testRetryAmplificationSpec          `yaml:"retry_amplification,omitempty" json:"RetryAmplification,omitempty"`
	Recovery           *testLoadRecoverySpec                `yaml:"recovery,omitempty"`
	Name               string                               `yaml:"name"`
	Duration           string                               `yaml:"duration"`
	Rules              []testChaosRule                      `yaml:"rules,omitempty"`
	Thresholds         testLoadStepThresholdSpec            `yaml:"thresholds,omitempty"`
}

type testLoadChaosPhase struct {
	RequestDeadlines   map[string]testRequestDeadlineSpec
	Assertions         []string
	Cycle              int
	StepRecovery       map[string]*testLoadRecoveryConfig
	StepThresholds     map[string]testLoadStepThresholdConfig
	CircuitBreaker     *testCircuitBreakerSpec
	ConnectionAttempts *testRetryAmplificationSpec
	RetryAmplification *testRetryAmplificationSpec
	Recovery           *testLoadRecoveryConfig
	Name               string
	Duration           time.Duration
	Rules              []testChaosRule
	Thresholds         testLoadStepThresholdConfig
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
	PoolExhaustion                      *testPoolExhaustionSpec
	ConnectionLeaks                     *testConnectionLeaksSpec
	CorrelationGate                     *sync.RWMutex
	FaultCampaign                       *testFaultCampaignSpec
	OutageCycles                        *testOutageCyclesSpec
	Streams                             []testLoadStreamSpec
	VUs, Iterations, Rate, RequestLimit int
	Duration, P95, P99, Pacing          time.Duration
	ErrorRate                           float64
	ErrorRateExplicit                   bool
	SuccessRate                         *float64
	MinSamples                          int
	Stages                              []testLoadStage
	ChaosSchedule                       []testLoadChaosPhase
	ChaosPhaseClock                     *testLoadPhaseClock
	OnStart                             func(time.Time)
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
	PoolExhaustion        *testPoolExhaustionEvidence  `json:"pool_exhaustion,omitempty"`
	ConnectionLeaks       *testConnectionLeaksEvidence `json:"connection_leaks,omitempty"`
	OutageCycles          *testOutageCyclesEvidence    `json:"outage_cycles,omitempty"`
	Streams               []testLoadStreamEvidence     `json:"streams,omitempty"`
	Mode                  string                       `json:"mode"`
	Status                string                       `json:"status"`
	VUs                   int                          `json:"vus"`
	MaxVUs                int                          `json:"max_vus"`
	PeakVUs               int                          `json:"peak_vus"`
	IterationLimit        int                          `json:"iteration_limit,omitempty"`
	RequestedDurationMS   int64                        `json:"requested_duration_ms,omitempty"`
	RequestLimit          int                          `json:"request_limit"`
	IterationsStarted     int                          `json:"iterations_started"`
	IterationsCompleted   int                          `json:"iterations_completed"`
	IterationsFailed      int                          `json:"iterations_failed"`
	IterationsInterrupted int                          `json:"iterations_interrupted"`
	DurationMS            float64                      `json:"duration_ms"`
	RequestsPerSecond     float64                      `json:"requests_per_second"`
	StopReason            string                       `json:"stop_reason,omitempty"`
	PacingMS              float64                      `json:"pacing_ms,omitempty"`
	testLoadMetrics
	Steps       []testLoadStepEvidence       `json:"steps,omitempty"`
	Thresholds  []testLoadThresholdEvidence  `json:"thresholds,omitempty"`
	Stages      []testLoadStageEvidence      `json:"stages,omitempty"`
	Arrival     *testLoadArrivalEvidence     `json:"arrival,omitempty"`
	Workload    *testLoadWorkload            `json:"workload,omitempty"`
	ChaosPhases []testLoadChaosPhaseEvidence `json:"chaos_phases,omitempty"`
}

type testLoadChaosPhaseEvidence struct {
	RequestDeadlines    map[string]*testRequestDeadlineEvidence `json:"request_deadlines,omitempty"`
	PoolExhaustion      *testPoolPhaseEvidence                  `json:"pool_exhaustion,omitempty"`
	ConnectionLeaks     *testConnectionLeakEvidence             `json:"connection_leaks,omitempty"`
	CorrelationRequests []testRequestAttemptSample              `json:"-"`
	CorrelationOverflow int                                     `json:"-"`
	Assertions          []string                                `json:"assertions,omitempty"`
	Cycle               int                                     `json:"cycle,omitempty"`
	StepRecovery        map[string]*testLoadRecoveryEvidence    `json:"step_recovery,omitempty"`
	CircuitRequests     []testCircuitIncomingSample             `json:"-"`
	CircuitBreaker      *testCircuitBreakerEvidence             `json:"circuit_breaker,omitempty"`
	ConnectionAttempts  *testRetryAmplificationEvidence         `json:"connection_attempts,omitempty"`
	RetryAmplification  *testRetryAmplificationEvidence         `json:"retry_amplification,omitempty"`
	Recovery            *testLoadRecoveryEvidence               `json:"recovery,omitempty"`
	Name                string                                  `json:"name"`
	PlannedStartMS      int64                                   `json:"planned_start_ms"`
	PlannedEndMS        int64                                   `json:"planned_end_ms"`
	ActualDurationMS    int64                                   `json:"actual_duration_ms,omitempty"`
	StartedAt           time.Time                               `json:"started_at,omitempty"`
	FinishedAt          time.Time                               `json:"finished_at,omitempty"`
	Chaos               *testChaosEvidence                      `json:"chaos,omitempty"`
	testLoadMetrics
	Steps      []testLoadStepEvidence      `json:"steps,omitempty"`
	Thresholds []testLoadThresholdEvidence `json:"thresholds,omitempty"`
}

func validateTestLoadSpec(spec *testLoadSpec) error {
	if spec != nil {
		for _, phase := range spec.ChaosSchedule {
			if err := validateTestRequestDeadlines(phase.RequestDeadlines); err != nil {
				return fmt.Errorf("phase %q: %w", phase.Name, err)
			}
		}
	}
	if spec != nil {
		if err := validateTestPoolExhaustion(spec); err != nil {
			return err
		}
	}
	if spec != nil {
		if err := validateTestConnectionLeaks(spec); err != nil {
			return err
		}
	}
	if spec != nil && (spec.OutageCycles != nil || spec.FaultCampaign != nil) {
		_, err := expandTestOutageCycleLoad(spec)
		if err != nil || (!spec.cyclesExpanded && !spec.campaignExpanded) {
			return err
		}
	}
	if spec == nil {
		return nil
	}
	if err := validateTestLoadStreams(spec); err != nil {
		return err
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
	if len(spec.ChaosSchedule) > 0 {
		if len(spec.ChaosSchedule) < 3 || len(spec.ChaosSchedule) > testLoadMaxStages {
			return fmt.Errorf("load.chaos_schedule needs between 3 and %d phases", testLoadMaxStages)
		}
		var loadDuration time.Duration
		if spec.Duration != "" {
			loadDuration, _ = time.ParseDuration(spec.Duration)
		} else {
			for _, stage := range spec.Stages {
				duration, _ := time.ParseDuration(stage.Duration)
				loadDuration += duration
			}
		}
		if loadDuration <= 0 {
			return errors.New("load.chaos_schedule requires a duration-based load or load stages")
		}
		seen := make(map[string]bool, len(spec.ChaosSchedule))
		var scheduleDuration time.Duration
		faulted := false
		for i, phase := range spec.ChaosSchedule {
			if err := validateTestEndpointRecovery(phase, i > 0 && (len(spec.ChaosSchedule[i-1].Rules) > 0 || (spec.campaignExpanded && spec.ChaosSchedule[i-1].Cycle > 0))); err != nil {
				return err
			}
			if err := validateTestPhaseStepBudgets(phase); err != nil {
				return err
			}
			if err := validateTestCircuitBreaker(spec.ChaosSchedule, i); err != nil {
				return err
			}
			if err := validateTestConnectionAttempts(phase); err != nil {
				return err
			}
			if err := validateTestRetryAmplification(phase); err != nil {
				return err
			}
			budget := phase.Thresholds
			if err := validateTestLoadThresholds(budget.P95, budget.P99, budget.ErrorRate, budget.SuccessRate, budget.MinSamples); err != nil {
				return fmt.Errorf("load.chaos_schedule phase %q thresholds: %w", phase.Name, err)
			}
			if !testHTTPNamePattern.MatchString(phase.Name) || seen[phase.Name] {
				return fmt.Errorf("load.chaos_schedule phase %d has an invalid or duplicate name %q", i+1, phase.Name)
			}
			seen[phase.Name] = true
			duration, err := time.ParseDuration(phase.Duration)
			if err != nil || duration < time.Second || duration > testLoadMaxDuration || duration%time.Millisecond != 0 {
				return fmt.Errorf("load.chaos_schedule phase %q duration must be between 1s and 5m and a whole millisecond", phase.Name)
			}
			if err := validateTestLoadRecovery(phase, i > 0 && (len(spec.ChaosSchedule[i-1].Rules) > 0 || (spec.campaignExpanded && spec.ChaosSchedule[i-1].Cycle > 0)), duration); err != nil {
				return err
			}
			if testPhaseNeedsRecoveryBaseline(testLoadChaosPhase{Recovery: resolveTestLoadRecovery(phase.Recovery), StepRecovery: resolveTestEndpointRecovery(phase.StepRecovery)}) && (i < 2 || len(spec.ChaosSchedule[i-2].Rules) > 0) {
				return fmt.Errorf("load.chaos_schedule phase %q relative recovery requires a fault-free phase immediately before the fault", phase.Name)
			}
			scheduleDuration += duration
			faulted = faulted || len(phase.Rules) > 0
			if (i == 0 || i == len(spec.ChaosSchedule)-1) && len(phase.Rules) > 0 {
				return errors.New("load.chaos_schedule must start and end with a phase that has no rules")
			}
			if i > 0 && len(phase.Rules) > 0 && len(spec.ChaosSchedule[i-1].Rules) > 0 {
				return errors.New("load.chaos_schedule fault phases must be separated by a phase with no rules")
			}
		}
		if scheduleDuration > testLoadMaxDuration {
			return errors.New("load.chaos_schedule phases must total at most 5m")
		}
		if !faulted {
			return errors.New("load.chaos_schedule must include at least one fault phase")
		}
		if scheduleDuration != loadDuration {
			return fmt.Errorf("load.chaos_schedule phases total %s; they must equal the load scheduling duration %s", scheduleDuration, loadDuration)
		}
		if spec.Relative != nil && spec.Relative.Retry != nil {
			return errors.New("load.chaos_schedule cannot be combined with relative retry_until_passes")
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
	for _, phase := range scenario.Load.ChaosSchedule {
		for name := range phase.StepRecovery {
			if !names[name] {
				return fmt.Errorf("load.chaos_schedule phase %q step_recovery references undeclared HTTP step %q", phase.Name, name)
			}
		}
		for name := range phase.StepThresholds {
			if !names[name] {
				return fmt.Errorf("load.chaos_schedule phase %q step_thresholds references undeclared HTTP step %q", phase.Name, name)
			}
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
	if len(spec.Streams) > 0 && (overrides.VUsSet || overrides.RateSet || overrides.IterationsSet || overrides.PacingSet) {
		return nil, errors.New("load streams cannot use VU, rate, iteration, or pacing overrides")
	}
	expanded, err := expandTestOutageCycleLoad(&spec)
	if err != nil {
		return nil, err
	}
	spec = *expanded
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
	cfg := &testLoadConfig{PoolExhaustion: spec.PoolExhaustion, ConnectionLeaks: spec.ConnectionLeaks, FaultCampaign: spec.FaultCampaign, OutageCycles: spec.OutageCycles, VUs: spec.VUs, Iterations: spec.Iterations, Rate: spec.Rate, RequestLimit: testLoadMaxRequests}
	cfg.Streams = append([]testLoadStreamSpec(nil), spec.Streams...)
	if err := validateTestLoadStreamRequests(cfg, append(append([]testHTTPRequest(nil), scenario.Requests...), scenario.Checks...)); err != nil {
		return nil, err
	}
	cfg.Regression = resolveTestRegressionConfig(spec.Regression)
	if len(cfg.Streams) > 0 {
		cfg.VUs = cfg.maxVUs()
	} else if cfg.VUs == 0 {
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
	if len(cfg.Streams) > 0 {
		planned := 0
		for _, stream := range cfg.Streams {
			sc := testLoadConfig{Rate: stream.Rate, Duration: cfg.Duration}
			planned += sc.plannedArrivals() * len(stream.Requests)
		}
		if planned > testLoadMaxRequests {
			return nil, fmt.Errorf("stream schedules would exceed %d HTTP steps", testLoadMaxRequests)
		}
	}
	for _, phase := range spec.ChaosSchedule {
		duration, _ := time.ParseDuration(phase.Duration)
		p95, _ := time.ParseDuration(phase.Thresholds.P95)
		p99, _ := time.ParseDuration(phase.Thresholds.P99)
		cfg.ChaosSchedule = append(cfg.ChaosSchedule, testLoadChaosPhase{Cycle: phase.Cycle,
			RequestDeadlines: phase.RequestDeadlines, Assertions: append([]string(nil), phase.Assertions...), StepRecovery: resolveTestEndpointRecovery(phase.StepRecovery), StepThresholds: resolveTestPhaseStepBudgets(phase.StepThresholds), CircuitBreaker: phase.CircuitBreaker, ConnectionAttempts: phase.ConnectionAttempts, RetryAmplification: phase.RetryAmplification, Name: phase.Name, Duration: duration, Rules: append([]testChaosRule(nil), phase.Rules...), Recovery: resolveTestLoadRecovery(phase.Recovery),
			Thresholds: testLoadStepThresholdConfig{P95: p95, P99: p99, ErrorRate: phase.Thresholds.ErrorRate, SuccessRate: phase.Thresholds.SuccessRate, MinSamples: phase.Thresholds.MinSamples},
		})
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
	for phaseIndex, phase := range cfg.ChaosSchedule {
		phase.RequestDeadlines = maps.Clone(phase.RequestDeadlines)
		cfg.ChaosSchedule[phaseIndex].RequestDeadlines = phase.RequestDeadlines
		for name, budget := range phase.RequestDeadlines {
			count := 0
			for _, request := range append(append([]testHTTPRequest(nil), scenario.Requests...), scenario.Checks...) {
				if request.Name == name {
					count++
					if budget.Expect == nil {
						expected := request.Expect
						budget.Expect = &expected
						phase.RequestDeadlines[name] = budget
					}
				}
			}
			if count != 1 {
				return nil, fmt.Errorf("phase %q request_deadlines.%s must name exactly one load request", phase.Name, name)
			}
		}
		if phase.CircuitBreaker != nil {
			count := 0
			for _, request := range append(append([]testHTTPRequest(nil), scenario.Requests...), scenario.Checks...) {
				if request.Name == phase.CircuitBreaker.Request {
					count++
				}
			}
			if count != 1 {
				return nil, fmt.Errorf("phase %q circuit_breaker request must name exactly one load request", phase.Name)
			}
		}
		for _, budget := range []*testRetryAmplificationSpec{phase.RetryAmplification, phase.ConnectionAttempts} {
			if budget == nil {
				continue
			}
			count := 0
			for _, request := range append(append([]testHTTPRequest(nil), scenario.Requests...), scenario.Checks...) {
				if request.Name == budget.Request {
					count++
					if budget.PerRequest {
						for name := range request.Headers {
							if strings.EqualFold(name, chaos.RequestCorrelationHeader) {
								return nil, fmt.Errorf("phase %q per_request reserves header %s", phase.Name, chaos.RequestCorrelationHeader)
							}
						}
					}
				}
			}
			if count != 1 {
				return nil, fmt.Errorf("phase %q attempt budget request %q must identify exactly one load request", phase.Name, budget.Request)
			}
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
	if len(cfg.Streams) > 0 {
		mode = "streams"
	}
	report := &testLoadEvidence{Mode: mode, Status: "not_started", VUs: cfg.VUs, MaxVUs: cfg.maxVUs(), IterationLimit: cfg.Iterations, RequestedDurationMS: cfg.Duration.Milliseconds(), RequestLimit: cfg.RequestLimit, PacingMS: loadMilliseconds(cfg.Pacing)}
	if cfg.Rate > 0 {
		report.Arrival = &testLoadArrivalEvidence{TargetRate: cfg.Rate, Planned: cfg.plannedArrivals()}
	}
	for _, stream := range cfg.Streams {
		sc := testLoadConfig{Rate: stream.Rate, Duration: cfg.Duration}
		report.Streams = append(report.Streams, testLoadStreamEvidence{Name: stream.Name, Requests: append([]string(nil), stream.Requests...), VUs: stream.VUs, Arrival: testLoadArrivalEvidence{TargetRate: stream.Rate, Planned: sc.plannedArrivals()}})
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
	chaosPhases        []testLoadPhaseSamples
	phaseClock         *testLoadPhaseClock
	loadStartedAt      time.Time
	stageStarts        []int
	arrivalsScheduled  int
	droppedCapacity    int
	droppedLate        int
	schedulingDuration time.Duration
}

type testLoadPhaseSamples struct {
	correlationSlots, correlationOverflow int
	correlationRequests                   []testRequestAttemptSample
	collectCircuit                        bool
	circuitSamples                        []testCircuitIncomingSample
	collectRecovery                       bool
	recoverySamples                       []testLoadRecoverySample
	steps                                 []testLoadStepSamples
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
	c.recordAt(index, result, duration, time.Now())
}

func (c *testLoadCollector) recordAt(index int, result testHTTPRequestEvidence, duration time.Duration, startedAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	addTestLoadSample(&c.steps[index], result, duration)
	if phase := c.phaseIndex(startedAt); phase >= 0 && phase < len(c.chaosPhases) {
		addTestLoadSample(&c.chaosPhases[phase].steps[index], result, duration)
		if result.CorrelationID != "" {
			c.chaosPhases[phase].correlationRequests = append(c.chaosPhases[phase].correlationRequests, testRequestAttemptSample{ID: result.CorrelationID, Expected: result.ExpectedDependencyAttempts})
		}
		if c.chaosPhases[phase].collectCircuit {
			c.chaosPhases[phase].circuitSamples = append(c.chaosPhases[phase].circuitSamples, testCircuitIncomingSample{At: startedAt, Name: c.steps[index].evidence.Name})
		}
		if c.chaosPhases[phase].collectRecovery {
			c.chaosPhases[phase].recoverySamples = append(c.chaosPhases[phase].recoverySamples, testLoadRecoverySample{Name: c.steps[index].evidence.Name, StartedAt: startedAt, Duration: duration, Passed: result.Passed})
		}
	}
}

func (c *testLoadCollector) phaseIndex(startedAt time.Time) int {
	if c.phaseClock == nil {
		return -1
	}
	return c.phaseClock.phaseAt(startedAt)
}

func addTestLoadSample(step *testLoadStepSamples, result testHTTPRequestEvidence, duration time.Duration) {
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
	collector := &testLoadCollector{
		steps: make([]testLoadStepSamples, len(steps)), stageStarts: make([]int, len(cfg.Stages)),
		phaseClock: cfg.ChaosPhaseClock, loadStartedAt: started,
	}
	for i, step := range steps {
		collector.steps[i].evidence = testLoadStepEvidence{Name: step.Name, Method: step.Method, Path: strings.SplitN(step.Path, "?", 2)[0], testLoadMetrics: testLoadMetrics{Statuses: map[string]int{}}}
	}
	collectPending := testLoadNeedsPendingRecovery(cfg.ChaosSchedule)
	for phaseIndex, scheduledPhase := range cfg.ChaosSchedule {
		collectBaseline := phaseIndex+2 < len(cfg.ChaosSchedule) && testPhaseNeedsRecoveryBaseline(cfg.ChaosSchedule[phaseIndex+2])
		collectCircuit := scheduledPhase.CircuitBreaker != nil || (phaseIndex > 0 && cfg.ChaosSchedule[phaseIndex-1].CircuitBreaker != nil) || (phaseIndex+1 < len(cfg.ChaosSchedule) && cfg.ChaosSchedule[phaseIndex+1].CircuitBreaker != nil)
		phase := testLoadPhaseSamples{collectCircuit: collectCircuit, collectRecovery: scheduledPhase.Recovery != nil || len(scheduledPhase.StepRecovery) > 0 || collectBaseline || collectPending, steps: make([]testLoadStepSamples, len(steps))}
		for i, step := range steps {
			phase.steps[i].evidence = testLoadStepEvidence{Name: step.Name, Method: step.Method, Path: strings.SplitN(step.Path, "?", 2)[0], testLoadMetrics: testLoadMetrics{Statuses: map[string]int{}}}
		}
		collector.chaosPhases = append(collector.chaosPhases, phase)
	}
	journey := testLoadJourney{
		Client: client, BaseURL: baseURL, RunID: runID, ConsumerKeys: consumerKeys, Steps: steps, Data: data, InitialCaptures: captures,
	}
	if cfg.OnStart != nil {
		cfg.OnStart(started)
	}
	runJourney := func(iteration int) { journey.run(ctx, collector, cfg, iteration) }
	stopProgress := monitorTestLoadProgress(ctx, collector, cfg, started)
	var streamEvidence []testLoadStreamEvidence
	if len(cfg.Streams) > 0 {
		streamEvidence = runTestLoadStreams(ctx, collector, cfg, started, journey)
	} else if cfg.Rate > 0 {
		arrivalLoadRunner(ctx, collector, cfg, started, runJourney)
	} else {
		runTestConcurrencyLoad(ctx, collector, cfg, started, runJourney)
	}
	stopProgress()
	duration := time.Since(started)
	report := collector.report(cfg, duration)
	report.Streams = streamEvidence
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
		for _, stream := range streamEvidence {
			if stream.Arrival.Dropped > 0 {
				report.StopReason = "dropped_arrivals"
				executionError = fmt.Errorf("load stream %q dropped %d arrivals", stream.Name, stream.Arrival.Dropped)
				break
			}
		}
	}
	if !testLoadThresholdsPassed(report) {
		executionError = errors.Join(executionError, testLoadThresholdFailure(report))
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
	report.ChaosPhases = c.reportChaosPhases(cfg)
	return report
}

func (c *testLoadCollector) reportChaosPhases(cfg *testLoadConfig) []testLoadChaosPhaseEvidence {
	if len(cfg.ChaosSchedule) == 0 {
		return nil
	}
	phases := make([]testLoadChaosPhaseEvidence, 0, len(cfg.ChaosSchedule))
	var plannedStart time.Duration
	for i, phase := range cfg.ChaosSchedule {
		entry := testLoadChaosPhaseEvidence{CorrelationRequests: slices.Clone(c.chaosPhases[i].correlationRequests), CorrelationOverflow: c.chaosPhases[i].correlationOverflow, Assertions: append([]string(nil), phase.Assertions...), Cycle: phase.Cycle,
			CircuitRequests: append([]testCircuitIncomingSample(nil), c.chaosPhases[i].circuitSamples...), Name: phase.Name, PlannedStartMS: plannedStart.Milliseconds(),
			PlannedEndMS:    (plannedStart + phase.Duration).Milliseconds(),
			testLoadMetrics: testLoadMetrics{Statuses: map[string]int{}},
		}
		window := c.phaseClock.window(i)
		entry.StartedAt, entry.FinishedAt = window.StartedAt, window.FinishedAt
		if !window.StartedAt.IsZero() && !window.FinishedAt.IsZero() {
			entry.ActualDurationMS = window.FinishedAt.Sub(window.StartedAt).Milliseconds()
		}
		var allSamples []time.Duration
		for _, sample := range c.chaosPhases[i].steps {
			step := sample.evidence
			step.Latency = testLoadLatencySummary(sample.samples)
			if step.Requests > 0 {
				step.ErrorRate = float64(step.Failures) / float64(step.Requests)
				step.SuccessRate = 1 - step.ErrorRate
			}
			if budget, ok := phase.StepThresholds[step.Name]; ok {
				step.Thresholds = testLoadThresholdEvidenceFor(step.testLoadMetrics, budget.P95, budget.P99, budget.ErrorRate, budget.SuccessRate, budget.MinSamples)
			}
			entry.Steps = append(entry.Steps, step)
			entry.Requests += step.Requests
			entry.Failures += step.Failures
			for status, count := range step.Statuses {
				entry.Statuses[status] += count
			}
			allSamples = append(allSamples, sample.samples...)
		}
		entry.Latency = testLoadLatencySummary(allSamples)
		if entry.Requests > 0 {
			entry.ErrorRate = float64(entry.Failures) / float64(entry.Requests)
			entry.SuccessRate = 1 - entry.ErrorRate
		}
		budget := phase.Thresholds
		entry.Thresholds = testLoadThresholdEvidenceFor(entry.testLoadMetrics,
			budget.P95, budget.P99, budget.ErrorRate, budget.SuccessRate, budget.MinSamples)
		recoveryWindow := window
		if recoveryWindow.FinishedAt.IsZero() {
			recoveryWindow.FinishedAt = time.Now()
		}
		var baseline *testLoadRecoveryBaseline
		if phase.Recovery != nil && phase.Recovery.Relative != nil && i >= 2 {
			baseline = testLoadRecoveryBaselineFor(cfg.ChaosSchedule[i-2].Name, c.phaseClock.window(i-2), c.chaosPhases[i-2].recoverySamples, phase.Recovery.Relative.BaselineMinSamples)
		}
		entry.Recovery = evaluateTestLoadRecovery(phase.Recovery, recoveryWindow, c.recoverySamplesFor(i, phase.Recovery), baseline)
		if len(phase.StepRecovery) > 0 {
			entry.StepRecovery = make(map[string]*testLoadRecoveryEvidence, len(phase.StepRecovery))
		}
		for name, budget := range phase.StepRecovery {
			var endpointBaseline *testLoadRecoveryBaseline
			if budget.Relative != nil && i >= 2 {
				endpointBaseline = testLoadRecoveryBaselineFor(cfg.ChaosSchedule[i-2].Name, c.phaseClock.window(i-2), testEndpointRecoverySamples(c.chaosPhases[i-2].recoverySamples, name), budget.Relative.BaselineMinSamples)
			}
			entry.StepRecovery[name] = evaluateTestLoadRecovery(budget, recoveryWindow, testEndpointRecoverySamples(c.recoverySamplesFor(i, budget), name), endpointBaseline)
		}
		phases = append(phases, entry)
		plannedStart += phase.Duration
	}
	return phases
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
	for _, phase := range report.ChaosPhases {
		for _, r := range phase.StepRecovery {
			if r == nil || !r.Passed {
				return false
			}
		}
		if phase.Recovery != nil && !phase.Recovery.Passed {
			return false
		}
		for _, step := range phase.Steps {
			for _, threshold := range step.Thresholds {
				if !threshold.Passed {
					return false
				}
			}
		}
		for _, threshold := range phase.Thresholds {
			if !threshold.Passed {
				return false
			}
		}
	}
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

func testLoadThresholdFailure(report *testLoadEvidence) error {
	var failures []string
	for _, phase := range report.ChaosPhases {
		for _, name := range testEndpointRecoveryNames(phase.StepRecovery) {
			r := phase.StepRecovery[name]
			if r == nil {
				failures = append(failures, fmt.Sprintf("chaos phase %q HTTP step %q recovery evidence missing", phase.Name, name))
			} else if !r.Passed {
				failures = append(failures, fmt.Sprintf("chaos phase %q HTTP step %q recovery failed: %s", phase.Name, name, r.Error))
			}
		}
		if phase.Recovery != nil && !phase.Recovery.Passed {
			failures = append(failures, fmt.Sprintf("chaos phase %q recovery failed: %s", phase.Name, phase.Recovery.Error))
		}
		for _, step := range phase.Steps {
			for _, threshold := range step.Thresholds {
				if !threshold.Passed {
					failures = append(failures, fmt.Sprintf("chaos phase %q HTTP step %q: %s %s %.4g, observed %.4g", phase.Name, step.Name, threshold.Metric, threshold.Operator, threshold.Limit, threshold.Actual))
				}
			}
		}
		for _, threshold := range phase.Thresholds {
			if !threshold.Passed {
				failures = append(failures, fmt.Sprintf("chaos phase %q: %s %s %.4g, observed %.4g", phase.Name, threshold.Metric, threshold.Operator, threshold.Limit, threshold.Actual))
			}
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("load thresholds failed: %s", strings.Join(failures, "; "))
	}
	return errors.New("load thresholds failed")
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
	printTestRequestDeadlines(out, report)
	printTestPoolExhaustion(out, report)
	if report != nil && report.ConnectionLeaks != nil {
		e := report.ConnectionLeaks
		_, _ = fmt.Fprintf(out, "  application connection leaks: passed %t; baseline %v, sampled peak %d; first failed connection cycle %d; %s\n", e.Passed, testConnectionBaselineText(e.Baseline), e.Peak, e.FirstFailedCycle, e.Error)
		if e.PostDrain != nil {
			_, _ = fmt.Fprintf(out, "    post-drain: remaining %d, limit %d, passed %t; %s\n", e.PostDrain.Remaining, e.PostDrain.Limit, e.PostDrain.Passed, e.PostDrain.Error)
		}
		for _, phase := range report.ChaosPhases {
			if e := phase.ConnectionLeaks; e != nil {
				_, _ = fmt.Fprintf(out, "    %s connections: baseline %d, sampled peak %d, remaining %d, limit %d, passed %t; %s\n", phase.Name, e.Baseline, e.Peak, e.Remaining, e.Limit, e.Passed, e.Error)
			}
		}
	}

	for _, stream := range report.Streams {
		_, _ = fmt.Fprintf(out, "  stream %s: target %d journeys/s, %d VUs; %d/%d arrivals scheduled, %d dropped\n", stream.Name, stream.Arrival.TargetRate, stream.VUs, stream.Arrival.Scheduled, stream.Arrival.Planned, stream.Arrival.Dropped)
	}
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
	if report.OutageCycles != nil {
		e := report.OutageCycles
		if e.Campaign != nil {
			_, _ = fmt.Fprintf(out, "  fault campaign: seed %d (%s)\n", e.Campaign.Seed, e.Campaign.Algorithm)
		}
		_, _ = fmt.Fprintf(out, "  outage cycles: %s; %d/%d completed\n", e.Status, e.Completed, e.Count)
		if e.FirstRecoveryFailedCycle > 0 {
			_, _ = fmt.Fprintf(out, "    first failed recovery comparison: cycle %d\n", e.FirstRecoveryFailedCycle)
		}
		for _, cycle := range e.Cycles {
			_, _ = fmt.Fprintf(out, "    cycle %d (%s → %s): %s %s\n", cycle.Number, cycle.FaultPhase, cycle.RecoveryPhase, cycle.Status, cycle.Error)
			if cycle.FaultKind != "" {
				_, _ = fmt.Fprintf(out, "      fault: %s\n", cycle.FaultKind)
			}
			for _, check := range cycle.RecoveryChecks {
				target := check.Request
				if target == "" {
					target = "aggregate"
				}
				if check.BaselineMS != nil && check.CurrentMS != nil && check.LimitMS != nil {
					status := "failed"
					if check.Passed {
						status = "passed"
					}
					_, _ = fmt.Fprintf(out, "      %s recovery: %.3fms; cycle 1 %.3fms, limit %.3fms (%s)\n", target, *check.CurrentMS, *check.BaselineMS, *check.LimitMS, status)
				} else {
					_, _ = fmt.Fprintf(out, "      %s recovery: %s\n", target, check.Error)
				}
			}
		}
	}
	for _, phase := range report.ChaosPhases {
		_, _ = fmt.Fprintf(out, "  chaos phase %q: %d HTTP steps, %.2f%% success, p95 %.3fms, p99 %.3fms (%dms actual)\n",
			phase.Name, phase.Requests, phase.SuccessRate*100, phase.Latency.P95MS, phase.Latency.P99MS, phase.ActualDurationMS)
		for _, name := range phase.Assertions {
			_, _ = fmt.Fprintf(out, "    assertion profile %q\n", name)
		}
		for _, threshold := range phase.Thresholds {
			status := "failed"
			if threshold.Passed {
				status = "passed"
			}
			_, _ = fmt.Fprintf(out, "    %s %s %.4g: %.4g (%s)\n", threshold.Metric, threshold.Operator, threshold.Limit, threshold.Actual, status)
		}
		for _, step := range phase.Steps {
			if len(step.Thresholds) > 0 {
				_, _ = fmt.Fprintf(out, "    HTTP step %q: %d samples, p95 %.3fms, p99 %.3fms, %.2f%% errors\n", step.Name, step.Requests, step.Latency.P95MS, step.Latency.P99MS, step.ErrorRate*100)
				for _, threshold := range step.Thresholds {
					status := "failed"
					if threshold.Passed {
						status = "passed"
					}
					_, _ = fmt.Fprintf(out, "      %s %s %.4g: %.4g (%s)\n", threshold.Metric, threshold.Operator, threshold.Limit, threshold.Actual, status)
				}
			}
		}
		if breaker := phase.CircuitBreaker; breaker != nil {
			stopped, resumed := "unconfirmed", "unconfirmed"
			if breaker.StoppedMS != nil {
				stopped = fmt.Sprintf("%dms", *breaker.StoppedMS)
			}
			if breaker.ResumedMS != nil {
				resumed = fmt.Sprintf("%dms", *breaker.ResumedMS)
			}
			_, _ = fmt.Fprintf(out, "    circuit breaker %s -> %s: stopped %s (within %dms), resumed %s (within %dms); probes <= %d / %dms; recovery >= %d / window; passed %t %s\n", breaker.From, breaker.To, stopped, breaker.StopWithinMS, resumed, breaker.ResumeWithinMS, breaker.MaxProbes, breaker.WindowMS, breaker.ResumeMinAttempts, breaker.Passed, breaker.Error)
		}
		if attempts := phase.ConnectionAttempts; attempts != nil {
			_, _ = fmt.Fprintf(out, "    TCP connection attempts %s -> %s:%d: %d attempts / %d %s requests = %.4g (limit %.4g, passed %t) %s\n", attempts.From, attempts.To, attempts.Port, attempts.Attempts, attempts.Requests, attempts.Request, attempts.AttemptsPerRequest, attempts.MaxAttemptsPerRequest, attempts.Passed, attempts.Error)
		}
		if phase.RetryAmplification != nil && phase.RetryAmplification.PerRequest != nil {
			strict := phase.RetryAmplification.PerRequest
			_, _ = fmt.Fprintf(out, "    per-request attempts: %d/%d correlated; maximum %d, limit %d; %d violations; passed=%t; %s\n", strict.Correlated, strict.Requests, strict.MaxAttempts, strict.Limit, strict.ViolationCount, strict.Passed, strict.Error)
			for _, violation := range strict.Violations {
				_, _ = fmt.Fprintf(out, "      request %s: %d attempts\n", violation.RequestID, violation.Attempts)
			}
		}
		if retry := phase.RetryAmplification; retry != nil {
			_, _ = fmt.Fprintf(out, "    retry amplification %s -> %s: %d attempts / %d %s requests = %.4g (limit %.4g, passed %t) %s\n", retry.From, retry.To, retry.Attempts, retry.Requests, retry.Request, retry.AttemptsPerRequest, retry.MaxAttemptsPerRequest, retry.Passed, retry.Error)
		}
		for _, name := range testEndpointRecoveryNames(phase.StepRecovery) {
			r := phase.StepRecovery[name]
			if r == nil {
				continue
			}
			observed := "unconfirmed"
			if r.RecoveryMS != nil {
				observed = fmt.Sprintf("%.3fms", *r.RecoveryMS)
			}
			_, _ = fmt.Fprintf(out, "    HTTP step %q recovery: passed=%t, observed=%s, within=%dms, sustain=%dms; %s\n", name, r.Passed, observed, r.WithinMS, r.SustainMS, r.Error)
			printTestRecoveryStability(out, r)
		}
		if phase.Recovery != nil {
			observed := "unconfirmed"
			if phase.Recovery.RecoveryMS != nil {
				observed = fmt.Sprintf("%.3fms", *phase.Recovery.RecoveryMS)
			}
			_, _ = fmt.Fprintf(out, "    recovery: passed=%t, observed=%s, within=%dms, sustain=%dms; %s\n", phase.Recovery.Passed, observed, phase.Recovery.WithinMS, phase.Recovery.SustainMS, phase.Recovery.Error)
			printTestRecoveryStability(out, phase.Recovery)
			if phase.Recovery.Baseline != nil {
				_, _ = fmt.Fprintf(out, "      baseline %q: %d completed samples\n", phase.Recovery.Baseline.Phase, phase.Recovery.Baseline.Requests)
			}
			for _, check := range phase.Recovery.Relative {
				_, _ = fmt.Fprintf(out, "      relative %s: baseline %.4g, limit %s %.4g, observed %.4g (passed=%t)\n", check.Metric, check.Baseline, check.Operator, check.Limit, check.Actual, check.Passed)
			}
		}
		if phase.Chaos != nil {
			for _, match := range phase.Chaos.Matches {
				_, _ = fmt.Fprintf(out, "    %s to %s: %d match(es)\n", match.Rule.Kind, match.Rule.To, match.Matches)
			}
		}
	}
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

func printTestRecoveryStability(out io.Writer, r *testLoadRecoveryEvidence) {
	if r.MaxPendingAgeMS > 0 {
		_, _ = fmt.Fprintf(out, "      pending requests: %d overdue (maximum age %dms)\n", r.OverdueRequests, r.MaxPendingAgeMS)
	}
	if r == nil || r.Stability == nil {
		return
	}
	s := r.Stability
	_, _ = fmt.Fprintf(out, "      stability: observed=%d/%dms, windows=%d, passed=%t\n", s.ObservedMS, s.DurationMS, s.WindowsChecked, s.Passed)
	if s.RelapseMS != nil {
		_, _ = fmt.Fprintf(out, "      relapse at %.3fms after fault removal\n", *s.RelapseMS)
	}
}

// Preserve requests that were already in flight when the fault was cleared.
func (c *testLoadCollector) recoverySamplesFor(index int, budget *testLoadRecoveryConfig) []testLoadRecoverySample {
	if budget == nil || budget.MaxPendingAge == 0 {
		return c.chaosPhases[index].recoverySamples
	}
	var samples []testLoadRecoverySample
	for i := 0; i <= index; i++ {
		samples = append(samples, c.chaosPhases[i].recoverySamples...)
	}
	return samples
}
func testLoadNeedsPendingRecovery(phases []testLoadChaosPhase) bool {
	for _, phase := range phases {
		if phase.Recovery != nil && phase.Recovery.MaxPendingAge > 0 {
			return true
		}
		for _, budget := range phase.StepRecovery {
			if budget != nil && budget.MaxPendingAge > 0 {
				return true
			}
		}
	}
	return false
}
