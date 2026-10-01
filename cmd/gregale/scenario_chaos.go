package main

import (
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/chaos"
)

type testChaosSpec struct {
	Duration string          `yaml:"duration"`
	Rules    []testChaosRule `yaml:"rules"`
}

type testChaosRule struct {
	From       string `yaml:"from,omitempty"`
	To         string `yaml:"to"`
	Kind       string `yaml:"kind"`
	Percent    int    `yaml:"percent"`
	Latency    string `yaml:"latency,omitempty"`
	StatusCode int    `yaml:"status_code,omitempty"`
	Seed       uint64 `yaml:"seed,omitempty"`
}

func (spec *testChaosSpec) plan() (chaos.Plan, error) {
	if spec == nil {
		return chaos.Plan{}, nil
	}
	duration, err := time.ParseDuration(spec.Duration)
	if err != nil || duration%time.Millisecond != 0 {
		return chaos.Plan{}, errors.New("chaos duration must be a whole-millisecond duration")
	}
	plan := chaos.Plan{DurationMS: duration.Milliseconds(), Rules: make([]chaos.Rule, 0, len(spec.Rules))}
	for i, rule := range spec.Rules {
		converted := chaos.Rule{
			From: rule.From, To: rule.To, Kind: rule.Kind, Percent: rule.Percent,
			StatusCode: rule.StatusCode, Seed: rule.Seed,
		}
		if rule.Kind == chaos.KindLatency {
			latency, parseErr := time.ParseDuration(rule.Latency)
			if parseErr != nil || latency%time.Millisecond != 0 {
				return chaos.Plan{}, fmt.Errorf("chaos rule %d latency must be a whole-millisecond duration", i+1)
			}
			converted.LatencyMS = latency.Milliseconds()
		}
		plan.Rules = append(plan.Rules, converted)
	}
	if err := plan.Validate(); err != nil {
		return chaos.Plan{}, err
	}
	return plan, nil
}

func validateScenarioChaos(scenario testScenario) error {
	if scenario.Chaos == nil {
		return nil
	}
	plan, err := scenario.Chaos.plan()
	if err != nil {
		return err
	}
	workloads := make(map[string]struct{}, len(scenario.Services)+1)
	workloads[scenario.Project] = struct{}{}
	for workload := range scenario.Services {
		workloads[workload] = struct{}{}
	}
	return plan.ValidateWorkloads(workloads)
}

func scenarioChaosAPIRules(rules []chaos.Rule) []api.ScenarioTestChaosRule {
	result := make([]api.ScenarioTestChaosRule, len(rules))
	for i, rule := range rules {
		result[i] = api.ScenarioTestChaosRule(rule)
	}
	return result
}

func cmdChaos(args []string) int {
	if len(args) == 0 || args[0] != "inject" {
		PrintUsage(osStderr, "usage: gregale chaos inject --scenario NAME --target SERVICE (--latency D|--error CODE) --percent N [--duration D] [--from SERVICE] [--profile warm|cold|restored]", "chaos")
		return 1
	}
	fs := newFlagSet("chaos inject", flag.ContinueOnError)
	scenario := fs.String("scenario", "", "scenario name from the test manifest")
	manifest := fs.String("manifest", "gregale-test.yaml", "scenario manifest path")
	target := fs.String("target", "", "scenario service workload to affect")
	from := fs.String("from", "", "only affect calls from this workload")
	latency := fs.String("latency", "", "add this delay to selected requests, such as 1500ms")
	errorCode := fs.Int("error", 0, "return this synthetic HTTP 5xx status, such as 503")
	percent := fs.Int("percent", 100, "fraction of matching requests affected (1..100)")
	duration := fs.String("duration", "5m", "maximum fault lease duration (1s..5m)")
	profile := fs.String("profile", "warm", "real-VM lifecycle profile: warm, cold, or restored")
	seed := fs.Uint64("seed", 0, "deterministic fault-selection seed")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 {
		PrintUsage(osStderr, "usage: gregale chaos inject --scenario NAME --target SERVICE (--latency D|--error CODE) --percent N [--duration D] [--from SERVICE] [--profile warm|cold|restored]", "chaos")
		return 1
	}
	if *scenario == "" || *target == "" {
		return printErr("Scenario and target required", errors.New("--scenario and --target are required"))
	}
	if (*latency == "") == (*errorCode == 0) {
		return printErr("Choose one chaos fault", errors.New("provide exactly one of --latency or --error"))
	}
	kind := chaos.KindLatency
	statusCode := 0
	if *errorCode != 0 {
		kind = chaos.KindHTTPStatus
		statusCode = *errorCode
	}
	spec := &testChaosSpec{Duration: *duration, Rules: []testChaosRule{{
		From: *from, To: *target, Kind: kind, Percent: *percent,
		Latency: *latency, StatusCode: statusCode, Seed: *seed,
	}}}
	if _, err := spec.plan(); err != nil {
		return printErr("Invalid chaos plan", err)
	}
	if !api.ValidAppSlug(*target) || (*from != "" && !api.ValidAppSlug(*from)) {
		return printErr("Invalid chaos workload", errors.New("--target and --from must be valid workload names"))
	}
	if *profile != "warm" && *profile != "cold" && *profile != "restored" {
		return printErr("Invalid profile", errors.New("--profile must be warm, cold, or restored"))
	}
	return cmdTestWithChaos([]string{"--scenario", *scenario, "--manifest", *manifest, "--profile", *profile}, spec)
}
