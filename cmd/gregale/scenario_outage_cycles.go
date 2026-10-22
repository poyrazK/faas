package main

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// Repeat an existing healthy/fault/recovery template within one isolated run.
type testOutageCyclesSpec struct {
	MaxRecoveryIncreasePercent *float64 `yaml:"max_recovery_increase_percent,omitempty" json:"max_recovery_increase_percent,omitempty"`
	Count                      int      `yaml:"count" json:"count"`
	MaxP95IncreasePercent      *float64 `yaml:"max_p95_increase_percent,omitempty" json:"max_p95_increase_percent,omitempty"`
	MaxErrorRateIncrease       *float64 `yaml:"max_error_rate_increase,omitempty" json:"max_error_rate_increase,omitempty"`
}
type testOutageCycleEvidence struct {
	FaultKind      string                      `json:"fault_kind,omitempty"`
	RecoveryChecks []testOutageRecoveryCheck   `json:"recovery_checks,omitempty"`
	Number         int                         `json:"number"`
	FaultPhase     string                      `json:"fault_phase"`
	RecoveryPhase  string                      `json:"recovery_phase"`
	Status         string                      `json:"status"`
	Checks         []testLoadThresholdEvidence `json:"checks,omitempty"`
	Error          string                      `json:"error,omitempty"`
}
type testOutageCyclesEvidence struct {
	Campaign                 *testFaultCampaignEvidence `json:"campaign,omitempty"`
	FirstRecoveryFailedCycle int                        `json:"first_recovery_failed_cycle,omitempty"`
	Count                    int                        `json:"count"`
	Completed                int                        `json:"completed"`
	FirstFailedCycle         int                        `json:"first_failed_cycle,omitempty"`
	Status                   string                     `json:"status"`
	Cycles                   []testOutageCycleEvidence  `json:"cycles"`
}

func expandTestOutageCycleLoad(original *testLoadSpec) (*testLoadSpec, error) {
	if original != nil && original.FaultCampaign != nil {
		return expandTestFaultCampaignLoad(original)
	}
	if original == nil || original.OutageCycles == nil {
		return original, nil
	}
	fail := func(message string) (*testLoadSpec, error) {
		return original, fmt.Errorf("load.outage_cycles: %s", message)
	}
	spec := original.OutageCycles
	if spec.Count < 2 || spec.Count > 9 {
		return fail("count must be between 2 and 9 (at most 19 phases)")
	}
	for _, limit := range []struct {
		value *float64
		max   float64
	}{{spec.MaxRecoveryIncreasePercent, 1000}, {spec.MaxP95IncreasePercent, 1000}, {spec.MaxErrorRateIncrease, 1}} {
		if limit.value != nil && (math.IsNaN(*limit.value) || math.IsInf(*limit.value, 0) || *limit.value < 0 || *limit.value > limit.max) {
			return fail("trend limits must be finite; p95 and recovery increase percentages are 0..1000 and error rate increase is 0..1")
		}
	}
	if original.cyclesExpanded {
		if len(original.ChaosSchedule) != 1+2*spec.Count {
			return fail("expanded schedule does not match count")
		}
		for i, phase := range original.ChaosSchedule {
			expected := 0
			if i > 0 {
				expected = (i + 1) / 2
			}
			if phase.Cycle != expected {
				return fail("expanded cycle identities do not match schedule")
			}
		}
		return original, nil
	}
	if original.Duration == "" || len(original.Stages) > 0 || len(original.ChaosSchedule) != 3 {
		return fail("requires an explicit duration and exactly three healthy, fault, recovery phases without stages")
	}
	phases := original.ChaosSchedule
	if len(phases[0].Rules) != 0 || len(phases[1].Rules) == 0 || len(phases[2].Rules) != 0 || (phases[2].Recovery == nil && len(phases[2].StepRecovery) == 0) {
		return fail("template must be healthy, fault, recovery with aggregate or endpoint recovery assertions")
	}
	// Validate the declared single-cycle duration before generating its repeats.
	template := *original
	template.OutageCycles = nil
	if err := validateTestLoadSpec(&template); err != nil {
		return fail(err.Error())
	}
	baseline, _ := time.ParseDuration(phases[0].Duration)
	fault, _ := time.ParseDuration(phases[1].Duration)
	recovery, _ := time.ParseDuration(phases[2].Duration)
	total := baseline + time.Duration(spec.Count)*(fault+recovery)
	if total > testLoadMaxDuration {
		return fail("expanded cycles must total at most 5m")
	}
	load := *original
	load.cyclesExpanded = true
	load.Duration = total.String()
	load.ChaosSchedule = make([]testLoadChaosPhaseSpec, 0, 1+2*spec.Count)
	load.ChaosSchedule = append(load.ChaosSchedule, phases[0])
	for cycle := 1; cycle <= spec.Count; cycle++ {
		for _, phase := range phases[1:] {
			copy := phase
			copy.Cycle = cycle
			copy.Rules = append([]testChaosRule(nil), phase.Rules...)
			if cycle > 1 {
				copy.Name = fmt.Sprintf("%s-cycle-%d", phase.Name, cycle)
			}
			if !testHTTPNamePattern.MatchString(copy.Name) {
				return fail("generated cycle phase name is invalid or too long")
			}
			load.ChaosSchedule = append(load.ChaosSchedule, copy)
		}
	}
	if err := validateTestLoadSpec(&load); err != nil {
		return fail(err.Error())
	}
	return &load, nil
}

func expandTestOutageCycles(scenario testScenario) (testScenario, error) {
	if scenario.Load != nil && (scenario.Load.OutageCycles != nil || scenario.Load.FaultCampaign != nil) {
		return scenario, errors.New("outage_cycles and fault_campaign require a staged load step")
	}
	needed := false
	for _, step := range scenario.Steps {
		needed = needed || (step.Load != nil && (step.Load.OutageCycles != nil || step.Load.FaultCampaign != nil))
	}
	if !needed {
		return scenario, nil
	}
	result := scenario
	result.Steps = append([]testScenarioStep(nil), scenario.Steps...)
	for i, step := range scenario.Steps {
		load, err := expandTestOutageCycleLoad(step.Load)
		if err != nil {
			return scenario, fmt.Errorf("step %q: %w", step.Name, err)
		}
		result.Steps[i].Load = load
	}
	return result, nil
}

func testOutageCyclePhasePassed(observed testLoadChaosPhaseEvidence, planned testLoadChaosPhase) bool {
	for name := range planned.RequestDeadlines {
		if observed.RequestDeadlines[name] == nil || !observed.RequestDeadlines[name].Passed {
			return false
		}
	}
	if observed.PoolExhaustion != nil && !observed.PoolExhaustion.Passed {
		return false
	}
	if observed.ConnectionLeaks != nil && !observed.ConnectionLeaks.Passed {
		return false
	}
	if observed.Name != planned.Name || observed.StartedAt.IsZero() || !observed.FinishedAt.After(observed.StartedAt) {
		return false
	}
	if planned.Recovery != nil && (observed.Recovery == nil || !observed.Recovery.Passed) {
		return false
	}
	for name := range planned.StepRecovery {
		if observed.StepRecovery[name] == nil || !observed.StepRecovery[name].Passed {
			return false
		}
	}
	if len(planned.Rules) > 0 {
		if observed.Chaos == nil || observed.Chaos.RulesInstalled != len(planned.Rules) || len(observed.Chaos.Matches) != len(planned.Rules) {
			return false
		}
		for i, r := range planned.Rules {
			if observed.Chaos.Matches[i].Matches < int64(r.MinMatches) {
				return false
			}
		}
	}
	if planned.RetryAmplification != nil && (observed.RetryAmplification == nil || !observed.RetryAmplification.Passed) {
		return false
	}
	if planned.ConnectionAttempts != nil && (observed.ConnectionAttempts == nil || !observed.ConnectionAttempts.Passed) {
		return false
	}
	if planned.CircuitBreaker != nil && (observed.CircuitBreaker == nil || !observed.CircuitBreaker.Passed) {
		return false
	}
	return testLoadThresholdsPassed(&testLoadEvidence{ChaosPhases: []testLoadChaosPhaseEvidence{observed}})
}

func applyTestOutageCycles(report *testLoadEvidence, cfg *testLoadConfig, scheduleErr error) error {
	spec := cfg.OutageCycles
	if spec == nil {
		return nil
	}
	evidence := &testOutageCyclesEvidence{Count: spec.Count, Status: "passed"}
	report.OutageCycles = evidence
	if cfg.FaultCampaign != nil {
		evidence.Campaign = testFaultCampaignReport(cfg)
	}
	var failures []error
	abortedCycle := 0
	if scheduleErr != nil {
		lastStarted := 0
		for i, phase := range report.ChaosPhases {
			if !phase.StartedAt.IsZero() {
				lastStarted = i
			}
		}
		abortedCycle = (lastStarted + 1) / 2
		if abortedCycle < 1 {
			abortedCycle = 1
		}
	}
	for cycle := 1; cycle <= spec.Count; cycle++ {
		row := testOutageCycleEvidence{Number: cycle, Status: "passed"}
		fi, ri := 2*cycle-1, 2*cycle
		if ri >= len(cfg.ChaosSchedule) {
			row.Status = "failed"
			row.Error = "cycle configuration missing"
		} else {
			if len(cfg.ChaosSchedule[fi].Rules) == 1 {
				row.FaultKind = cfg.ChaosSchedule[fi].Rules[0].Kind
			}
			row.FaultPhase = cfg.ChaosSchedule[fi].Name
			row.RecoveryPhase = cfg.ChaosSchedule[ri].Name
			complete := ri < len(report.ChaosPhases) && (abortedCycle == 0 || cycle < abortedCycle)
			if complete {
				for _, i := range []int{fi, ri} {
					p := report.ChaosPhases[i]
					complete = complete && !p.StartedAt.IsZero() && p.FinishedAt.After(p.StartedAt)
				}
			}
			if !complete {
				row.Status = "incomplete"
				row.Error = "complete fault/recovery phase evidence unavailable"
			} else {
				evidence.Completed++
				for _, i := range []int{fi, ri} {
					p := report.ChaosPhases[i]
					if !testOutageCyclePhasePassed(p, cfg.ChaosSchedule[i]) || (cfg.ConnectionLeaks != nil && i == ri && p.ConnectionLeaks == nil) || (cfg.PoolExhaustion != nil && p.PoolExhaustion == nil) {
						row.Status = "failed"
						row.Error = fmt.Sprintf("phase %q: fault, recovery, or phase budget failed", p.Name)
						for _, name := range testEndpointRecoveryNames(cfg.ChaosSchedule[i].RequestDeadlines) {
							if d := p.RequestDeadlines[name]; d == nil || !d.Passed {
								row.Error = fmt.Sprintf("phase %q request deadline %q failed", p.Name, name)
							}
						}
						if p.PoolExhaustion != nil && !p.PoolExhaustion.Passed {
							row.Error = fmt.Sprintf("phase %q pool exhaustion: %s", p.Name, p.PoolExhaustion.Error)
						}
						if p.ConnectionLeaks != nil && !p.ConnectionLeaks.Passed {
							row.Error = fmt.Sprintf("phase %q connection leaks: %s", p.Name, p.ConnectionLeaks.Error)
						}
						if p.ConnectionAttempts != nil && !p.ConnectionAttempts.Passed {
							row.Error = fmt.Sprintf("phase %q connection attempts: %s", p.Name, p.ConnectionAttempts.Error)
						}
					}
					if cycle > 1 && (spec.MaxP95IncreasePercent != nil || spec.MaxErrorRateIncrease != nil) {
						base := report.ChaosPhases[1+(i-fi)]
						if base.Requests < 1 || p.Requests < 1 {
							row.Status = "failed"
							row.Error = "trend comparison requires samples in both phases"
							continue
						}
						if spec.MaxP95IncreasePercent != nil {
							limit := base.Latency.P95MS * (1 + *spec.MaxP95IncreasePercent/100)
							row.Checks = append(row.Checks, testLoadThresholdEvidence{Metric: p.Name + ".p95_ms", Operator: "<=", Limit: limit, Actual: p.Latency.P95MS, Passed: base.Latency.Samples > 0 && p.Latency.Samples > 0 && finiteNonnegative(base.Latency.P95MS) && finiteNonnegative(p.Latency.P95MS) && p.Latency.P95MS <= limit+1e-12})
						}
						if spec.MaxErrorRateIncrease != nil {
							limit := base.ErrorRate + *spec.MaxErrorRateIncrease
							row.Checks = append(row.Checks, testLoadThresholdEvidence{Metric: p.Name + ".error_rate", Operator: "<=", Limit: limit, Actual: p.ErrorRate, Passed: finiteNonnegative(base.ErrorRate) && finiteNonnegative(p.ErrorRate) && base.ErrorRate <= 1 && p.ErrorRate <= 1 && p.ErrorRate <= limit+1e-12})
						}
					}
				}
				for _, check := range row.Checks {
					if !check.Passed {
						row.Status = "failed"
						row.Error = "cycle metrics exceeded the first-cycle trend budget"
					}
				}
			}
		}
		if spec.MaxRecoveryIncreasePercent != nil {
			row.RecoveryChecks = testOutageCycleRecoveryChecks(report, cfg, cycle, abortedCycle)
			for _, check := range row.RecoveryChecks {
				if check.Passed {
					continue
				}
				if evidence.FirstRecoveryFailedCycle == 0 {
					evidence.FirstRecoveryFailedCycle = cycle
				}
				if row.Status == "passed" {
					row.Status = "failed"
				}
				if row.Error != "" {
					row.Error += "; "
				}
				target := check.Request
				if target == "" {
					target = "aggregate"
				}
				row.Error += fmt.Sprintf("%s recovery comparison failed: %s", target, check.Error)
			}
		}
		if row.Status != "passed" {
			evidence.Status = "failed"
			if evidence.FirstFailedCycle == 0 {
				evidence.FirstFailedCycle = cycle
			}
			failures = append(failures, fmt.Errorf("outage cycle %d: %s", cycle, row.Error))
		}
		evidence.Cycles = append(evidence.Cycles, row)
	}
	if scheduleErr != nil {
		evidence.Status = "failed"
		failures = append(failures, fmt.Errorf("outage cycles schedule: %w", scheduleErr))
	}
	err := errors.Join(failures...)
	if err != nil {
		report.Status = "failed"
	}
	return err
}
