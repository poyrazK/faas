package main

import (
	"fmt"
	"sort"
	"time"
)

type testLoadRecoverySpec struct {
	MaxPendingAge string                        `yaml:"max_pending_age,omitempty"`
	Relative      *testLoadRecoveryRelativeSpec `yaml:"relative,omitempty"`
	Within        string                        `yaml:"within"`
	Stability     string                        `yaml:"stability,omitempty" json:"stability,omitempty"`
	Sustain       string                        `yaml:"sustain"`
	Window        string                        `yaml:"window"`
	Thresholds    testLoadStepThresholdSpec     `yaml:"thresholds"`
}
type testLoadRecoveryConfig struct {
	MaxPendingAge                      time.Duration
	Relative                           *testLoadRecoveryRelativeSpec
	Within, Sustain, Window, Stability time.Duration
	Thresholds                         testLoadStepThresholdConfig
}
type testLoadRecoverySample struct {
	Name      string
	StartedAt time.Time
	Duration  time.Duration
	Passed    bool
}
type testLoadRecoveryEvidence struct {
	MaxPendingAgeMS int64                              `json:"max_pending_age_ms,omitempty"`
	OverdueRequests int                                `json:"overdue_requests"`
	Thresholds      []testLoadThresholdEvidence        `json:"thresholds,omitempty"`
	Stability       *testLoadRecoveryStabilityEvidence `json:"stability,omitempty"`
	DiagnosticsRef  string                             `json:"diagnostics_ref,omitempty"`
	Baseline        *testLoadRecoveryBaseline          `json:"baseline,omitempty"`
	Relative        []testLoadRecoveryRelativeEvidence `json:"relative,omitempty"`
	Passed          bool                               `json:"passed"`
	WithinMS        int64                              `json:"within_ms"`
	SustainMS       int64                              `json:"sustain_ms"`
	WindowMS        int64                              `json:"window_ms"`
	CheckIntervalMS int64                              `json:"check_interval_ms"`
	RecoveryMS      *float64                           `json:"recovery_ms,omitempty"`
	WindowsChecked  int                                `json:"windows_checked"`
	Error           string                             `json:"error,omitempty"`
}

// Stability is observed after the initial sustained recovery is confirmed.
type testLoadRecoveryStabilityEvidence struct {
	DurationMS     int64    `json:"duration_ms"`
	StartedMS      *float64 `json:"started_ms,omitempty"`
	ObservedMS     int64    `json:"observed_ms"`
	WindowsChecked int      `json:"windows_checked"`
	Passed         bool     `json:"passed"`
	RelapseMS      *float64 `json:"relapse_ms,omitempty"`
}

func validateTestLoadRecovery(phase testLoadChaosPhaseSpec, followsFault bool, duration time.Duration) error {
	if phase.Recovery == nil {
		return nil
	}
	fail := func(message string) error {
		return fmt.Errorf("load.chaos_schedule phase %q recovery: %s", phase.Name, message)
	}
	if len(phase.Rules) > 0 || !followsFault {
		return fail("requires a phase without rules immediately after a fault phase")
	}
	r := phase.Recovery
	for _, value := range []string{r.Within, r.Sustain, r.Window} {
		d, err := time.ParseDuration(value)
		if err != nil || d < 100*time.Millisecond || d > testLoadMaxDuration || d%time.Millisecond != 0 {
			return fail("within, sustain and window must be whole milliseconds between 100ms and 5m")
		}
	}
	if r.Stability != "" {
		d, err := time.ParseDuration(r.Stability)
		if err != nil || d < 100*time.Millisecond || d > testLoadMaxDuration || d%time.Millisecond != 0 {
			return fail("stability must be whole milliseconds between 100ms and 5m")
		}
	}
	if r.MaxPendingAge != "" {
		d, err := time.ParseDuration(r.MaxPendingAge)
		if err != nil || d < time.Millisecond || d > testLoadMaxDuration || d%time.Millisecond != 0 {
			return fail("max_pending_age must be whole milliseconds between 1ms and 5m")
		}
	}
	cfg := resolveTestLoadRecovery(r)
	if cfg.Window > cfg.Within || cfg.Within+cfg.Sustain+cfg.Stability > duration {
		return fail("window must not exceed within, and within plus sustain plus stability must fit the phase duration")
	}
	b := r.Thresholds
	if err := validateTestLoadThresholds(b.P95, b.P99, b.ErrorRate, b.SuccessRate, b.MinSamples); err != nil {
		return fail(err.Error())
	}
	if b.MinSamples < 1 {
		return fail("thresholds.min_samples must be at least 1 per window")
	}
	if err := validateTestLoadRecoveryRelative(r.Relative); err != nil {
		return fail(err.Error())
	}
	if r.Relative == nil && b.P95 == "" && b.P99 == "" && b.ErrorRate == nil && b.SuccessRate == nil {
		return fail("thresholds must include a latency or rate budget")
	}
	return nil
}
func resolveTestLoadRecovery(spec *testLoadRecoverySpec) *testLoadRecoveryConfig {
	if spec == nil {
		return nil
	}
	pendingAge, _ := time.ParseDuration(spec.MaxPendingAge)
	within, _ := time.ParseDuration(spec.Within)
	sustain, _ := time.ParseDuration(spec.Sustain)
	window, _ := time.ParseDuration(spec.Window)
	stability, _ := time.ParseDuration(spec.Stability)
	p95, _ := time.ParseDuration(spec.Thresholds.P95)
	p99, _ := time.ParseDuration(spec.Thresholds.P99)
	return &testLoadRecoveryConfig{MaxPendingAge: pendingAge, Relative: spec.Relative, Within: within, Sustain: sustain, Window: window, Stability: stability, Thresholds: testLoadStepThresholdConfig{P95: p95, P99: p99, ErrorRate: spec.Thresholds.ErrorRate, SuccessRate: spec.Thresholds.SuccessRate, MinSamples: spec.Thresholds.MinSamples}}
}

// Recovery windows use completion times of requests started in this phase.
// Pending requests contribute no healthy samples; late completions appear only
// in later windows, with their full latency. Carryover requests from earlier
// phases affect only the optional pending-age budget.
func evaluateTestLoadRecovery(cfg *testLoadRecoveryConfig, phase testLoadPhaseWindow, samples []testLoadRecoverySample, baselines ...*testLoadRecoveryBaseline) *testLoadRecoveryEvidence {
	if cfg == nil {
		return nil
	}
	interval := cfg.Window / 10
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	evidence := &testLoadRecoveryEvidence{MaxPendingAgeMS: cfg.MaxPendingAge.Milliseconds(), WithinMS: cfg.Within.Milliseconds(), SustainMS: cfg.Sustain.Milliseconds(), WindowMS: cfg.Window.Milliseconds(), CheckIntervalMS: interval.Milliseconds(), Error: "no sustained healthy interval within the recovery deadline"}
	defer func() {
		if !evidence.Passed && evidence.OverdueRequests > 0 {
			evidence.Error += fmt.Sprintf("; %d requests exceed max_pending_age of %dms", evidence.OverdueRequests, evidence.MaxPendingAgeMS)
		}
	}()
	if cfg.Stability > 0 {
		evidence.Stability = &testLoadRecoveryStabilityEvidence{DurationMS: cfg.Stability.Milliseconds()}
	}
	if cfg.Relative != nil {
		if len(baselines) == 0 || baselines[0] == nil {
			evidence.Error = "relative recovery baseline is unavailable"
			return evidence
		}
		evidence.Baseline = baselines[0]
		if evidence.Baseline.Requests < cfg.Relative.BaselineMinSamples {
			evidence.Error = fmt.Sprintf("baseline phase %q has %d completed samples; relative recovery requires %d", evidence.Baseline.Phase, evidence.Baseline.Requests, cfg.Relative.BaselineMinSamples)
			return evidence
		}
		if (cfg.Relative.P95IncreasePercent != nil && evidence.Baseline.Latency.P95MS <= 0) || (cfg.Relative.P99IncreasePercent != nil && evidence.Baseline.Latency.P99MS <= 0) {
			evidence.Error = "relative latency recovery requires positive baseline latency"
			return evidence
		}
	}
	if phase.StartedAt.IsZero() || phase.FinishedAt.IsZero() {
		evidence.Error = "recovery phase did not complete"
		return evidence
	}
	ordered := make([]testLoadRecoverySample, 0, len(samples))
	for _, sample := range samples {
		if !sample.StartedAt.Before(phase.StartedAt) {
			ordered = append(ordered, sample)
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].StartedAt.Add(ordered[i].Duration).Before(ordered[j].StartedAt.Add(ordered[j].Duration))
	})
	limit := phase.FinishedAt.Sub(phase.StartedAt)
	if limit > cfg.Within+cfg.Sustain+cfg.Stability {
		limit = cfg.Within + cfg.Sustain + cfg.Stability
	}
	// Overdue intervals allow exact historical pending counts without scanning
	// every request for every rolling window.
	var overdueStarts, overdueEnds []time.Time
	if cfg.MaxPendingAge > 0 {
		for _, sample := range samples {
			if sample.Duration > cfg.MaxPendingAge {
				overdueStarts = append(overdueStarts, sample.StartedAt.Add(cfg.MaxPendingAge))
				overdueEnds = append(overdueEnds, sample.StartedAt.Add(sample.Duration))
			}
		}
		sort.Slice(overdueStarts, func(i, j int) bool { return overdueStarts[i].Before(overdueStarts[j]) })
		sort.Slice(overdueEnds, func(i, j int) bool { return overdueEnds[i].Before(overdueEnds[j]) })
	}
	var healthySince, stabilityStart time.Duration
	left, right := 0, 0
	for elapsed := cfg.Window; elapsed <= limit; {
		end := phase.StartedAt.Add(elapsed)
		start := end.Add(-cfg.Window)
		for left < len(ordered) && ordered[left].StartedAt.Add(ordered[left].Duration).Before(start) {
			left++
		}
		if right < left {
			right = left
		}
		for right < len(ordered) && !ordered[right].StartedAt.Add(ordered[right].Duration).After(end) {
			right++
		}
		metrics := testLoadMetrics{Requests: right - left}
		latencies := make([]time.Duration, 0, right-left)
		for _, sample := range ordered[left:right] {
			if !sample.Passed {
				metrics.Failures++
			}
			latencies = append(latencies, sample.Duration)
		}
		metrics.Latency = testLoadLatencySummary(latencies)
		if metrics.Requests > 0 {
			metrics.ErrorRate = float64(metrics.Failures) / float64(metrics.Requests)
			metrics.SuccessRate = 1 - metrics.ErrorRate
		}
		b := cfg.Thresholds
		healthy := metrics.Requests > 0
		evidence.Thresholds = testLoadThresholdEvidenceFor(metrics, b.P95, b.P99, b.ErrorRate, b.SuccessRate, b.MinSamples)
		evidence.OverdueRequests = sort.Search(len(overdueStarts), func(i int) bool { return !overdueStarts[i].Before(end) }) - sort.Search(len(overdueEnds), func(i int) bool { return overdueEnds[i].After(end) })
		healthy = healthy && evidence.OverdueRequests == 0
		for _, threshold := range evidence.Thresholds {
			healthy = healthy && threshold.Passed
		}
		if cfg.Relative != nil {
			evidence.Relative = testLoadRecoveryRelativeChecks(cfg.Relative, evidence.Baseline.testLoadMetrics, metrics)
			for _, check := range evidence.Relative {
				healthy = healthy && check.Passed
			}
		}
		evidence.WindowsChecked++
		if stabilityStart > 0 {
			evidence.Stability.WindowsChecked++
			evidence.Stability.ObservedMS = (elapsed - stabilityStart).Milliseconds()
			if !healthy {
				relapse := loadMilliseconds(elapsed)
				evidence.Stability.RelapseMS = &relapse
				evidence.Error = "recovery stability relapsed into an unhealthy window"
				return evidence
			}
			if elapsed-stabilityStart >= cfg.Stability {
				evidence.Stability.Passed = true
				evidence.Passed = true
				evidence.Error = ""
				return evidence
			}
		} else {
			if !healthy {
				healthySince = 0
			} else if healthySince == 0 {
				healthySince = elapsed
			}
			if healthySince > 0 && healthySince <= cfg.Within && elapsed-healthySince >= cfg.Sustain {
				observed := loadMilliseconds(healthySince)
				evidence.RecoveryMS = &observed
				if cfg.Stability == 0 {
					evidence.Passed = true
					evidence.Error = ""
					return evidence
				}
				stabilityStart = elapsed
				started := loadMilliseconds(elapsed)
				evidence.Stability.StartedMS = &started
				evidence.Error = "recovery stability observation did not complete"
			}
		}
		if elapsed == limit {
			break
		}
		next := elapsed + interval
		// Always check the deadline and the end of the required observation period.
		if elapsed < cfg.Within && next > cfg.Within {
			next = cfg.Within
		}
		if healthySince > 0 && elapsed < healthySince+cfg.Sustain && next > healthySince+cfg.Sustain {
			next = healthySince + cfg.Sustain
		}
		if stabilityStart > 0 && next > stabilityStart+cfg.Stability {
			next = stabilityStart + cfg.Stability
		}
		if next > limit {
			next = limit
		}
		elapsed = next
	}
	for _, check := range evidence.Relative {
		if !check.Passed {
			evidence.Error += fmt.Sprintf("; relative %s %s %.4g, observed %.4g (baseline %.4g)", check.Metric, check.Operator, check.Limit, check.Actual, check.Baseline)
		}
	}
	return evidence
}
