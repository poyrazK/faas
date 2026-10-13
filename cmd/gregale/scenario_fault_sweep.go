package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/chaos"
)

// Values are explicit and executed in declaration order, without inferring a
// monotonic failure boundary or generating an unbounded range.
type testFaultSweepSpec struct {
	Step               string   `yaml:"step" json:"step"`
	Phase              string   `yaml:"phase" json:"phase"`
	RuleIndex          *int     `yaml:"rule_index,omitempty" json:"rule_index,omitempty"`
	Mode               string   `yaml:"mode" json:"mode"`
	Values             []string `yaml:"values" json:"values"`
	MaxRuns            int      `yaml:"max_runs" json:"max_runs"`
	MaxWorkloadMinutes int      `yaml:"max_workload_minutes" json:"max_workload_minutes"`
}
type testFaultSweepSetting struct {
	Rule  testFaultRuleRef `json:"rule"`
	Mode  string           `json:"mode"`
	Value string           `json:"value"`
}

func expandTestFaultSweep(scenario testScenario) (testScenario, error) {
	var err error
	scenario, err = expandTestOutageCycles(scenario)
	if err != nil {
		return scenario, err
	}
	scenario, err = expandTestCampaignCoverage(scenario)
	if err != nil {
		return scenario, err
	}
	if scenario.Resilience == nil || scenario.Resilience.FaultSweep == nil || scenario.Resilience.sweepExpanded {
		return scenario, nil
	}
	spec := scenario.Resilience.FaultSweep
	fail := func(message string) (testScenario, error) {
		return scenario, fmt.Errorf("resilience.fault_sweep: %s", message)
	}
	if len(scenario.Resilience.FaultMatrix) > 0 {
		return fail("cannot combine fault_sweep with fault_matrix")
	}
	if len(spec.Values) < 2 || len(spec.Values) > testFaultMatrixMaxCases {
		return fail("values must contain 2..10 settings")
	}
	if spec.Mode == "duration" && spec.RuleIndex != nil {
		return fail("duration mode targets the whole phase and must omit rule_index")
	}
	if spec.Mode != "duration" && (spec.RuleIndex == nil || *spec.RuleIndex < 0) {
		return fail("rule_index must declare a nonnegative original rule index")
	}
	if spec.MaxRuns < 2 || spec.MaxRuns > 20 || scenario.Resilience.Runs < 2 || scenario.Resilience.Runs > 20 || len(spec.Values)*scenario.Resilience.Runs > spec.MaxRuns {
		return fail("max_runs must be 2..20 and cover every setting's repeated runs")
	}
	if scenario.Timeout != "" {
		d, err := time.ParseDuration(scenario.Timeout)
		if err != nil || d <= 0 {
			return fail("timeout must be a positive duration")
		}
	}
	if spec.MaxWorkloadMinutes < 1 {
		return fail("max_workload_minutes must be positive")
	}
	var rules []testChaosRule
	for _, step := range scenario.Steps {
		if step.Name == spec.Step && step.Load != nil {
			for _, phase := range step.Load.ChaosSchedule {
				if phase.Name == spec.Phase {
					rules = phase.Rules
				}
			}
		}
	}
	index := -1
	if spec.RuleIndex != nil {
		index = *spec.RuleIndex
	}
	if len(rules) == 0 || (spec.Mode != "duration" && index >= len(rules)) {
		return fail("selected rule is absent from the fault phase")
	}
	cases := make([]testFaultMatrixCaseSpec, 0, len(spec.Values))
	seen := map[string]bool{}
	for _, value := range spec.Values {
		var rule testChaosRule
		if index >= 0 {
			rule = rules[index]
		}
		canonical := ""
		switch spec.Mode {
		case "duration":
			d, err := time.ParseDuration(value)
			if err != nil || d < time.Second || d > testLoadMaxDuration || d%time.Millisecond != 0 {
				return fail("duration values must be whole milliseconds from 1s to 5m")
			}
			canonical = d.String()
		case "latency":
			d, err := time.ParseDuration(value)
			if err != nil || d < time.Millisecond || d > chaos.MaxLatency || d%time.Millisecond != 0 || (rule.Kind != chaos.KindLatency && rule.Kind != chaos.KindTCPLatency) {
				return fail("latency values require an HTTP/TCP latency rule and whole milliseconds from 1ms to 30s")
			}
			canonical = d.String()
			rule.Latency = canonical
		case "bandwidth":
			rate, err := strconv.ParseInt(value, 10, 64)
			if err != nil || rate < 1 || rate > api.ScenarioTCPChaosMaxRateKiBPerSecond || rule.Kind != chaos.KindTCPBandwidth {
				return fail("bandwidth values require a TCP bandwidth rule and integers from 1 to 1000000 KiB/s")
			}
			canonical = strconv.FormatInt(rate, 10)
			rule.RateKiBPerSecond = rate
		case "percent":
			percent, err := strconv.ParseInt(value, 10, 64)
			if err != nil || percent < 1 || percent > 100 {
				return fail("percent values must be integer selection percentages from 1 to 100")
			}
			canonical = strconv.FormatInt(percent, 10)
			rule.Percent = int(percent)
		default:
			return fail("mode must be latency, bandwidth, percent, or duration")
		}
		if seen[canonical] {
			return fail("values contain duplicate normalized settings")
		}
		seen[canonical] = true
		changed := append([]testChaosRule(nil), rules...)
		if index >= 0 {
			changed[index] = rule
		}
		name := fmt.Sprintf("sweep-%s-%d", spec.Mode, len(cases)+1)
		setting := &testFaultSweepSetting{Rule: testFaultRuleRef{Step: spec.Step, Phase: spec.Phase, Index: index}, Mode: spec.Mode, Value: canonical}
		c := testFaultMatrixCaseSpec{Name: name, Step: spec.Step, Phase: spec.Phase, Rules: changed, Sweep: setting}
		if spec.Mode == "duration" {
			c.PhaseDuration = canonical
		}
		cases = append(cases, c)
	}
	result := scenario
	resilience := *scenario.Resilience
	resilience.FaultMatrix = cases
	resilience.sweepExpanded = true
	result.Resilience = &resilience
	if spec.Mode == "duration" {
		if err := validateTestScenarioSteps(scenario, nil); err != nil {
			return fail(err.Error())
		}
		for _, c := range cases {
			variant, err := applyTestFaultMatrixCase(result, c)
			if err != nil {
				return fail(err.Error())
			}
			if err := validateTestScenarioSteps(variant, nil); err != nil {
				return fail(err.Error())
			}
		}
	}
	if estimateTestWorkloadMinutes(result, 1) > spec.MaxWorkloadMinutes {
		return fail("estimated workload-minutes exceed max_workload_minutes")
	}
	return result, nil
}
