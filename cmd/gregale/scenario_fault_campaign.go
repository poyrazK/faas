package main

import (
	"fmt"
	"time"
)

// The algorithm and its draw order are part of the campaign format. Never
// change them in place: replay fingerprints also pin the expanded schedule.
const testFaultCampaignAlgorithm = "splitmix64-v1"
const testDeclaredFaultCampaignAlgorithm = "declared-splitmix64-v1"

type testFaultCampaignSpec struct {
	Order                string                    `yaml:"order,omitempty" json:"order,omitempty"`
	CoverageRequirements map[int]int               `yaml:"-" json:"coverage_requirements,omitempty"`
	Seed                 *uint64                   `yaml:"seed" json:"seed"`
	Count                int                       `yaml:"count" json:"count"`
	MaxDuration          string                    `yaml:"max_duration" json:"max_duration"`
	Choices              []testFaultCampaignChoice `yaml:"choices" json:"choices"`
}

type testFaultCampaignChoice struct {
	Duration string        `yaml:"duration" json:"duration"`
	Rule     testChaosRule `yaml:"rule" json:"rule"`
}

type testFaultCampaignEvidence struct {
	CoverageRequirements map[int]int              `json:"coverage_requirements,omitempty"`
	Seed                 uint64                   `json:"seed"`
	Algorithm            string                   `json:"algorithm"`
	Schedule             []testFaultCampaignPhase `json:"schedule"`
}

type testFaultCampaignPhase struct {
	Name       string          `json:"name"`
	DurationMS int64           `json:"duration_ms"`
	Rules      []testChaosRule `json:"rules,omitempty"`
}

func testFaultCampaignDraw(state *uint64) uint64 {
	*state += 0x9e3779b97f4a7c15
	z := *state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// Consume the same two draws per cycle in both modes. The seed still controls
// fault sampling, while declaration order guarantees every selected fault runs.
func testFaultCampaignChoiceIndex(spec *testFaultCampaignSpec, state *uint64, cycle int) int {
	draw := testFaultCampaignDraw(state)
	if spec.Order == "declared" {
		return cycle % len(spec.Choices)
	}
	return int(draw % uint64(len(spec.Choices)))
}

func expandTestFaultCampaignLoad(original *testLoadSpec) (*testLoadSpec, error) {
	spec := original.FaultCampaign
	fail := func(message string) (*testLoadSpec, error) {
		return original, fmt.Errorf("load.fault_campaign: %s", message)
	}
	if spec.Order != "" && spec.Order != "declared" {
		return fail("order must be omitted for seeded selection or set to declared")
	}
	if spec.Order == "declared" && !original.campaignExpanded && spec.Count != len(spec.Choices) {
		return fail("declared order requires count equal to the number of choices")
	}
	if spec.Seed == nil || spec.Count < 2 || spec.Count > 9 || len(spec.Choices) < 2 || len(spec.Choices) > 16 {
		return fail("requires an explicit seed, count 2..9, and 2..16 choices")
	}
	cap, err := time.ParseDuration(spec.MaxDuration)
	if err != nil || cap < time.Second || cap > testLoadMaxDuration || cap%time.Millisecond != 0 {
		return fail("max_duration must be a whole-millisecond duration between 1s and 5m")
	}
	for i, choice := range spec.Choices {
		d, err := time.ParseDuration(choice.Duration)
		if err != nil || d < time.Second || d > testLoadMaxDuration || d%time.Millisecond != 0 {
			return fail(fmt.Sprintf("choice %d duration must be whole milliseconds between 1s and 5m", i+1))
		}
		switch choice.Rule.Kind {
		case "tcp_latency", "tcp_bandwidth", "tcp_timeout", "tcp_reset", "tcp_connect_timeout", "tcp_connect_refused":
		default:
			return fail("choices must use TCP faults")
		}
		if _, err := (&testChaosSpec{Duration: choice.Duration, Rules: []testChaosRule{choice.Rule}}).plan(); err != nil {
			return fail(fmt.Sprintf("choice %d: %s", i+1, err))
		}
		first := spec.Choices[0].Rule
		if choice.Rule.From != first.From || choice.Rule.To != first.To || choice.Rule.Port != first.Port {
			return fail("all choices must target the same source, dependency, and TCP port")
		}
	}
	if original.campaignExpanded {
		if original.OutageCycles == nil || original.OutageCycles.Count != spec.Count || !original.cyclesExpanded {
			return fail("expanded cycle metadata does not match campaign")
		}
		copy := *original
		copy.FaultCampaign = nil
		if _, err := expandTestOutageCycleLoad(&copy); err != nil {
			return fail(err.Error())
		}
		d, err := time.ParseDuration(original.Duration)
		if err != nil || d > cap {
			return fail("expanded schedule exceeds max_duration")
		}
		return original, nil // Preserve replay/minimization edits; never redraw.
	}
	if original.OutageCycles != nil {
		return fail("cannot combine fault_campaign with outage_cycles")
	}
	if len(original.ChaosSchedule) != 3 || len(original.ChaosSchedule[1].Rules) != 1 {
		return fail("requires a healthy/fault/recovery template with one fault rule")
	}
	base := original.ChaosSchedule[1].Rules[0]
	first := spec.Choices[0].Rule
	if base.From != first.From || base.To != first.To || base.Port != first.Port {
		return fail("choices must target the template's TCP dependency")
	}
	copy := *original
	copy.FaultCampaign = nil
	copy.OutageCycles = &testOutageCyclesSpec{Count: spec.Count}
	load, err := expandTestOutageCycleLoad(&copy)
	if err != nil {
		return fail(err.Error())
	}
	state := *spec.Seed
	total, _ := time.ParseDuration(load.ChaosSchedule[0].Duration)
	for cycle := 1; cycle <= spec.Count; cycle++ {
		choice := spec.Choices[testFaultCampaignChoiceIndex(spec, &state, cycle-1)]
		rule := choice.Rule
		rule.Seed = testFaultCampaignDraw(&state)
		phase := &load.ChaosSchedule[2*cycle-1]
		phase.Duration, phase.Rules = choice.Duration, []testChaosRule{rule}
		fault, _ := time.ParseDuration(choice.Duration)
		recovery, _ := time.ParseDuration(load.ChaosSchedule[2*cycle].Duration)
		total += fault + recovery
	}
	if total > cap {
		return fail("generated schedule exceeds max_duration; reduce count or choice durations")
	}
	load.Duration = total.String()
	if err := validateTestLoadSpec(load); err != nil {
		return fail(err.Error())
	}
	load.FaultCampaign, load.campaignExpanded = spec, true
	return load, nil
}

func testFaultCampaignReport(cfg *testLoadConfig) *testFaultCampaignEvidence {
	e := &testFaultCampaignEvidence{Seed: *cfg.FaultCampaign.Seed, Algorithm: testFaultCampaignAlgorithm, CoverageRequirements: cloneTestCampaignRequirements(cfg.FaultCampaign.CoverageRequirements)}
	if cfg.FaultCampaign.Order == "declared" {
		e.Algorithm = testDeclaredFaultCampaignAlgorithm
	}
	for _, phase := range cfg.ChaosSchedule {
		e.Schedule = append(e.Schedule, testFaultCampaignPhase{Name: phase.Name, DurationMS: phase.Duration.Milliseconds(), Rules: append([]testChaosRule(nil), phase.Rules...)})
	}
	return e
}
