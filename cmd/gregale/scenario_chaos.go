package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/chaos"
)

type testChaosSpec struct {
	Duration string          `yaml:"duration"`
	Rules    []testChaosRule `yaml:"rules"`
}

type testChaosRule struct {
	CorrelationScope string `yaml:"-" json:"-"`
	From             string `yaml:"from,omitempty"`
	To               string `yaml:"to"`
	Kind             string `yaml:"kind"`
	Percent          int    `yaml:"percent"`
	Latency          string `yaml:"latency,omitempty"`
	StatusCode       int    `yaml:"status_code,omitempty"`
	RetryAfter       string `yaml:"retry_after,omitempty"`
	Seed             uint64 `yaml:"seed,omitempty"`
	Port             int    `yaml:"port,omitempty"`
	Direction        string `yaml:"direction,omitempty"`
	RateKiBPerSecond int64  `yaml:"rate_kib_per_second,omitempty"`
	ResetAfter       string `yaml:"reset_after,omitempty"`
	MinMatches       int64  `yaml:"min_matches,omitempty"`
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
		if rule.MinMatches < 0 {
			return chaos.Plan{}, fmt.Errorf("chaos rule %d min_matches must be zero or greater", i+1)
		}
		converted := chaos.Rule{
			CorrelationScope: rule.CorrelationScope,
			From:             rule.From, To: rule.To, Kind: rule.Kind, Percent: rule.Percent,
			StatusCode: rule.StatusCode, Seed: rule.Seed, Port: rule.Port,
			Direction: rule.Direction, RateKiBPerSecond: rule.RateKiBPerSecond,
		}
		if rule.RetryAfter != "" {
			wait, parseErr := time.ParseDuration(rule.RetryAfter)
			if parseErr != nil || wait < time.Second || wait%time.Second != 0 {
				return chaos.Plan{}, fmt.Errorf("chaos rule %d retry_after must be a positive whole-second duration", i+1)
			}
			converted.RetryAfterSeconds = int64(wait / time.Second)
		}
		if rule.Latency != "" {
			latency, parseErr := time.ParseDuration(rule.Latency)
			if parseErr != nil || latency%time.Millisecond != 0 {
				return chaos.Plan{}, fmt.Errorf("chaos rule %d latency must be a whole-millisecond duration", i+1)
			}
			converted.LatencyMS = latency.Milliseconds()
		}
		if rule.ResetAfter != "" {
			resetAfter, parseErr := time.ParseDuration(rule.ResetAfter)
			if parseErr != nil || resetAfter%time.Millisecond != 0 {
				return chaos.Plan{}, fmt.Errorf("chaos rule %d reset_after must be a whole-millisecond duration", i+1)
			}
			converted.ResetAfterMS = resetAfter.Milliseconds()
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
	return validateScenarioChaosSpec(scenario, scenario.Chaos)
}

func validateScenarioChaosSpec(scenario testScenario, spec *testChaosSpec) error {
	plan, err := spec.plan()
	if err != nil {
		return err
	}
	workloads := make(map[string]struct{}, len(scenario.Services)+1)
	workloads[scenario.Project] = struct{}{}
	for workload := range scenario.Services {
		workloads[workload] = struct{}{}
	}
	if err := plan.ValidateWorkloads(workloads); err != nil {
		return err
	}
	for _, rule := range plan.Rules {
		if rule.IsTCP() && !slices.Contains(scenarioTCPPorts(scenario, rule.To), rule.Port) {
			return fmt.Errorf("TCP chaos target %q must declare port %d in tcp_ports", rule.To, rule.Port)
		}
	}
	return nil
}

func validateTestScenarioSteps(scenario testScenario, dataFields []string) error {
	var err error
	scenario, err = expandTestOutageCycles(scenario)
	if err != nil {
		return err
	}
	if len(scenario.Steps) == 0 {
		return nil
	}
	if len(scenario.Steps) < 3 || len(scenario.Steps) > 32 {
		return errors.New("staged scenarios need between 3 and 32 steps, including baseline, disruption, and recovery")
	}
	if scenario.Chaos != nil || len(scenario.Trigger) != 0 || len(scenario.Command) != 0 || len(scenario.Requests) != 0 || len(scenario.Checks) != 0 ||
		scenario.WaitFor.QueueIdle || len(scenario.WaitFor.Invocations) != 0 || len(scenario.WaitFor.Objects) != 0 || len(scenario.WaitFor.Deliveries) != 0 ||
		scenario.Load != nil || scenario.Local != nil || len(scenario.Simulation) != 0 {
		return errors.New("steps cannot be combined with top-level chaos, trigger, command, requests, checks, wait_for, load, local, or simulation fields")
	}
	seenSteps := make(map[string]bool, len(scenario.Steps))
	previousLoadSteps := make(map[string]bool, len(scenario.Steps))
	previousLoadHTTPNames := make(map[string]map[string]bool, len(scenario.Steps))
	seenHTTPNames := make(map[string]bool)
	seenCaptures := make(map[string]bool)
	faultActive, sawFault, sawClear := false, false, false
	lastClear, lastRestart := -1, -1
	var totalLoadDuration time.Duration
	for i, step := range scenario.Steps {
		if err := validateTestRestart(scenario, i, step, dataFields); err != nil {
			return fmt.Errorf("step %q: %w", step.Name, err)
		}
		if !testHTTPNamePattern.MatchString(step.Name) || seenSteps[step.Name] {
			return fmt.Errorf("step %d has invalid or duplicate name %q", i+1, step.Name)
		}
		seenSteps[step.Name] = true
		if i == 0 && (step.Chaos != nil || step.ClearChaos) {
			return errors.New("the first step must establish the baseline without changing chaos")
		}
		if step.Chaos != nil && step.ClearChaos {
			return fmt.Errorf("step %q cannot install and clear chaos at the same time", step.Name)
		}
		if step.Chaos != nil {
			if err := validateScenarioChaosSpec(scenario, step.Chaos); err != nil {
				return fmt.Errorf("step %q chaos: %w", step.Name, err)
			}
			faultActive, sawFault = true, true
		}
		if step.ClearChaos {
			if !faultActive {
				return fmt.Errorf("step %q clears chaos when no fault plan is active", step.Name)
			}
			faultActive, sawClear, lastClear = false, true, i
		}
		currentHTTPNames := make(map[string]bool, len(step.Requests)+len(step.Checks))
		registeredRequests := append(append([]testHTTPRequest{}, step.Requests...), step.Checks...)
		if step.Restart != nil {
			registeredRequests = append(registeredRequests, step.Restart.Readiness)
			registeredRequests = append(registeredRequests, step.Restart.StorageChecks...)
			if f := step.Restart.InFlight; f != nil {
				registeredRequests = append(registeredRequests, f.Request)
				for _, checkpoint := range f.Checkpoints {
					registeredRequests = append(registeredRequests, checkpoint.Request)
				}
			}
		}
		for _, request := range registeredRequests {
			currentHTTPNames[request.Name] = true
		}
		if len(step.Command) == 0 && len(step.Requests) == 0 && len(step.Checks) == 0 && !step.ClearChaos && step.Restart == nil {
			return fmt.Errorf("step %q needs a command, requests, checks, or clear_chaos", step.Name)
		}
		if len(step.Command) > 0 && step.Command[0] == "" {
			return fmt.Errorf("step %q has an empty command", step.Name)
		}
		temp := testScenario{Consumers: scenario.Consumers, Requests: step.Requests, Checks: step.Checks}
		if err := validateTestHTTPRequestsWithData(temp, dataFields); err != nil {
			return fmt.Errorf("step %q: %w", step.Name, err)
		}
		if step.Load != nil {
			if spec := step.Load.PoolExhaustion; spec != nil {
				observer := testScenario{Consumers: scenario.Consumers, Requests: []testHTTPRequest{{Name: "pool-observation", Method: "GET", Path: spec.Path, As: spec.As, Expect: testHTTPExpect{Status: 200}}}}
				if err := validateTestHTTPRequestsWithData(observer, dataFields); err != nil {
					return fmt.Errorf("step %q pool_exhaustion: %w", step.Name, err)
				}
			}
			if spec := step.Load.ConnectionLeaks; spec != nil {
				observer := testScenario{Consumers: scenario.Consumers, Requests: []testHTTPRequest{{Name: "connection-observation", Method: "GET", Path: spec.Path, As: spec.As, Expect: testHTTPExpect{Status: 200}}}}
				if err := validateTestHTTPRequestsWithData(observer, dataFields); err != nil {
					return fmt.Errorf("step %q connection_leaks: %w", step.Name, err)
				}
			}
			if len(step.Requests)+len(step.Checks) == 0 {
				return fmt.Errorf("step %q load needs requests or checks to repeat", step.Name)
			}
			if step.Load.Workload != "" || step.Load.Regression != nil {
				return fmt.Errorf("step %q load cannot declare workload or regression; staged SLOs use direct thresholds", step.Name)
			}
			if relative := step.Load.Relative; relative != nil && !previousLoadSteps[relative.CompareTo] {
				return fmt.Errorf("step %q load.relative.compare_to must name an earlier step with load evidence", step.Name)
			}
			if relative := step.Load.Relative; relative != nil {
				baselineHTTPNames := previousLoadHTTPNames[relative.CompareTo]
				for comparisonName, comparison := range relative.Steps {
					if !baselineHTTPNames[comparison.BaselineStep] {
						return fmt.Errorf("step %q load.relative.steps.%s.baseline_step %q is not an HTTP step in compare_to step %q", step.Name, comparisonName, comparison.BaselineStep, relative.CompareTo)
					}
					if !currentHTTPNames[comparison.CurrentStep] {
						return fmt.Errorf("step %q load.relative.steps.%s.current_step %q is not an HTTP step in the current staged step", step.Name, comparisonName, comparison.CurrentStep)
					}
				}
			}
			loadScenario := temp
			loadScenario.Load = step.Load
			loadScenario.Timeout = scenario.Timeout
			loadConfig, err := resolveTestLoadConfig(loadScenario, testLoadOverrides{})
			if err != nil {
				return fmt.Errorf("step %q load: %w", step.Name, err)
			}
			if len(step.Load.ChaosSchedule) > 0 {
				if i == 0 {
					return fmt.Errorf("step %q cannot schedule chaos in the baseline step", step.Name)
				}
				if step.Chaos != nil || step.ClearChaos || faultActive {
					return fmt.Errorf("step %q chaos_schedule requires no active plan and cannot share a step with chaos or clear_chaos", step.Name)
				}
				for _, phase := range loadConfig.ChaosSchedule {
					if phase.CircuitBreaker != nil {
						counter := testHTTPRequest{Name: "circuit-counter", Method: "GET", Path: phase.CircuitBreaker.CounterPath, As: phase.CircuitBreaker.As, Expect: testHTTPExpect{Status: 200}}
						if err := validateTestHTTPRequests(testScenario{Consumers: scenario.Consumers, Requests: []testHTTPRequest{counter}}); err != nil {
							return fmt.Errorf("step %q circuit_breaker: %w", step.Name, err)
						}
					}
					if len(phase.Rules) == 0 {
						continue
					}
					spec := &testChaosSpec{Duration: loadConfig.Duration.String(), Rules: phase.Rules}
					if err := validateScenarioChaosSpec(scenario, spec); err != nil {
						return fmt.Errorf("step %q load.chaos_schedule phase %q: %w", step.Name, phase.Name, err)
					}
				}
				sawFault, sawClear, lastClear = true, true, i
			}
			stepLoadDuration := loadConfig.Duration
			if relative := step.Load.Relative; relative != nil && relative.Retry != nil {
				retry := relative.Retry
				retryTimeout, _ := time.ParseDuration(retry.Timeout)
				if loadConfig.Duration > 0 && loadConfig.Duration >= retryTimeout {
					return fmt.Errorf("step %q load.relative.retry_until_passes.timeout must exceed its load scheduling window", step.Name)
				}
				if retryTimeout > stepLoadDuration {
					stepLoadDuration = retryTimeout
				}
			}
			totalLoadDuration += stepLoadDuration
			previousLoadSteps[step.Name] = true
			previousLoadHTTPNames[step.Name] = currentHTTPNames
		}
		if step.Restart != nil {
			lastRestart = i
			d, _ := time.ParseDuration(step.Restart.Timeout)
			totalLoadDuration += d
		}
		for _, request := range registeredRequests {
			if seenHTTPNames[request.Name] {
				return fmt.Errorf("step %q has duplicate HTTP step name %q", step.Name, request.Name)
			}
			seenHTTPNames[request.Name] = true
			for capture := range request.Capture {
				if seenCaptures[capture] {
					return fmt.Errorf("step %q has duplicate capture %q", step.Name, capture)
				}
				seenCaptures[capture] = true
			}
		}
	}
	if scenario.Timeout != "" {
		timeout, _ := time.ParseDuration(scenario.Timeout)
		if totalLoadDuration > timeout {
			return fmt.Errorf("staged load and restart budgets total %s, exceeding scenario timeout %s", totalLoadDuration, timeout)
		}
	}
	if faultActive || (sawFault && !sawClear) || (!sawFault && lastRestart < 0) {
		return errors.New("staged scenarios must restart a workload or install and explicitly clear a fault plan")
	}
	if lastRestart == len(scenario.Steps)-1 {
		return errors.New("a recovery step must run after the last restart")
	}
	if lastClear == len(scenario.Steps)-1 {
		return errors.New("a recovery step must run after clear_chaos")
	}
	return nil
}

func scenarioChaosAPIRules(rules []chaos.Rule) []api.ScenarioTestChaosRule {
	result := make([]api.ScenarioTestChaosRule, len(rules))
	for i, rule := range rules {
		result[i] = api.ScenarioTestChaosRule(rule)
	}
	return result
}

func newTestChaosEvidence(spec *testChaosSpec, plan chaos.Plan, installed api.InjectScenarioTestChaosResponse) *testChaosEvidence {
	evidence := &testChaosEvidence{
		ExpiresAt: installed.ExpiresAt, RulesInstalled: installed.RulesInstalled,
		Generation: installed.Generation, Rules: append([]chaos.Rule(nil), plan.Rules...),
		Matches: make([]testChaosRuleEvidence, len(plan.Rules)),
	}
	for i, rule := range plan.Rules {
		minMatches := int64(0)
		if spec != nil && i < len(spec.Rules) {
			minMatches = spec.Rules[i].MinMatches
		}
		evidence.Matches[i] = testChaosRuleEvidence{Rule: rule, MinMatches: minMatches}
	}
	return evidence
}

func captureTestChaosMatches(ctx context.Context, client *Client, runID string, evidence *testChaosEvidence) error {
	if evidence == nil || evidence.Generation == "" {
		return errors.New("installed chaos plan omitted its generation")
	}
	deadline := time.Now().Add(4 * time.Second)
	started := time.Now()
	var previous map[string]int64
	stablePolls := 0
	var lastErr error
	for time.Now().Before(deadline) {
		matches, err := client.ScenarioTestChaosMatches(ctx, runID)
		if err != nil {
			lastErr = err
		} else if matches.Generation != evidence.Generation {
			return fmt.Errorf("chaos evidence generation changed from %s to %s", evidence.Generation, matches.Generation)
		} else {
			lastErr = nil
			current := make(map[string]int64, len(matches.Matches))
			for _, match := range matches.Matches {
				current[match.RuleID] = match.Count
			}
			evidence.CorrelationCounts = current
			for i := range evidence.Matches {
				evidence.Matches[i].Matches = current[chaos.RuleID(evidence.Matches[i].Rule)]
				if count, ok := current[chaos.TCPAttemptID(evidence.Matches[i].Rule)]; ok && evidence.Matches[i].Rule.IsTCP() {
					evidence.Matches[i].TCPAttempts = &count
				}
			}
			if mapsEqualCounts(previous, current) {
				stablePolls++
			} else {
				stablePolls = 0
			}
			previous = current
			if time.Since(started) >= 350*time.Millisecond && stablePolls >= 2 {
				return validateTestChaosMinimums(evidence)
			}
		}
		timer := time.NewTimer(150 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if lastErr != nil {
		return fmt.Errorf("read chaos match evidence: %w", lastErr)
	}
	return validateTestChaosMinimums(evidence)
}

func mapsEqualCounts(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for key, count := range a {
		if b[key] != count {
			return false
		}
	}
	return true
}

func validateTestChaosMinimums(evidence *testChaosEvidence) error {
	var failures []string
	for _, rule := range evidence.Matches {
		if rule.MinMatches > rule.Matches {
			port := ""
			if rule.Rule.IsTCP() {
				port = fmt.Sprintf(" port %d", rule.Rule.Port)
			}
			failures = append(failures, fmt.Sprintf("%s to %s%s matched %d time(s); min_matches requires %d", rule.Rule.Kind, rule.Rule.To, port, rule.Matches, rule.MinMatches))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("chaos match assertion failed: %s", strings.Join(failures, "; "))
	}
	return nil
}

func cmdChaos(args []string) int {
	if len(args) == 0 || args[0] != "inject" {
		PrintUsage(osStderr, "usage: gregale test chaos --scenario NAME --target SERVICE (--latency D|--error CODE|--bandwidth N|--timeout|--reset|--connect-timeout|--connect-refused|--dns-latency D|--dns-timeout|--dns-servfail|--dns-nxdomain|--malformed-json|--truncated-body|--response-lost) [--tcp-port N] [--direction upstream|downstream|both] --percent N [--duration D] [--from SERVICE] [--profile warm|cold|restored]", "chaos")
		return 1
	}
	fs := newFlagSet("chaos inject", flag.ContinueOnError)
	scenario := fs.String("scenario", "", "scenario name from the test manifest")
	manifest := fs.String("manifest", "gregale-test.yaml", "scenario manifest path")
	target := fs.String("target", "", "scenario service workload to affect")
	from := fs.String("from", "", "only affect calls from this workload")
	latency := fs.String("latency", "", "add this delay to selected requests, such as 1500ms")
	errorCode := fs.Int("error", 0, "return synthetic HTTP 429 or 5xx, such as 503")
	retryAfter := fs.String("retry-after", "", "Retry-After delay for HTTP 429 or 503, whole seconds from 1s to 30s")
	tcpPort := fs.Int("tcp-port", 0, "TCP target port; selects TCP latency rather than HTTP latency")
	direction := fs.String("direction", "", "TCP direction: upstream, downstream, or both")
	bandwidth := fs.Int64("bandwidth", 0, "TCP bandwidth in KiB/s")
	stall := fs.Bool("timeout", false, "stall TCP traffic until the fault lease expires")
	reset := fs.Bool("reset", false, "reset matching TCP connections")
	resetAfter := fs.String("reset-after", "", "delay before a TCP reset, such as 1s")
	connectTimeout := fs.Bool("connect-timeout", false, "hold new service connections until timeout or fault clear")
	connectRefused := fs.Bool("connect-refused", false, "reset new service connections before reaching the target")
	malformedJSON := fs.Bool("malformed-json", false, "return HTTP 200 with malformed JSON from the scenario dependency")
	responseLost := fs.Bool("response-lost", false, "forward to the dependency, then lose a successful HTTP response body")
	truncatedBody := fs.Bool("truncated-body", false, "return HTTP 200 with an incomplete Content-Length body")
	dnsLatency := fs.String("dns-latency", "", "delay private service-name DNS responses, such as 250ms")
	dnsTimeout := fs.Bool("dns-timeout", false, "drop matching private service-name DNS queries")
	dnsServFail := fs.Bool("dns-servfail", false, "return SERVFAIL for matching private service-name DNS queries")
	dnsNXDomain := fs.Bool("dns-nxdomain", false, "return NXDOMAIN for matching private service-name DNS queries")
	percent := fs.Int("percent", 100, "fraction of matching requests affected (1..100)")
	duration := fs.String("duration", "5m", "maximum fault lease duration (1s..5m)")
	profile := fs.String("profile", "warm", "real-VM lifecycle profile: warm, cold, or restored")
	seed := fs.Uint64("seed", 0, "deterministic fault-selection seed")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 {
		PrintUsage(osStderr, "usage: gregale test chaos --scenario NAME --target SERVICE (--latency D|--error CODE|--bandwidth N|--timeout|--reset|--connect-timeout|--connect-refused|--dns-latency D|--dns-timeout|--dns-servfail|--dns-nxdomain|--malformed-json|--truncated-body|--response-lost) [--tcp-port N] [--direction upstream|downstream|both] --percent N [--duration D] [--from SERVICE] [--profile warm|cold|restored]", "chaos")
		return 1
	}
	if *scenario == "" || *target == "" {
		return printErr("Scenario and target required", errors.New("--scenario and --target are required"))
	}
	faults := 0
	for _, chosen := range []bool{*latency != "", *errorCode != 0, *bandwidth != 0, *stall, *reset, *connectTimeout, *connectRefused, *dnsLatency != "", *dnsTimeout, *dnsServFail, *dnsNXDomain, *malformedJSON, *truncatedBody, *responseLost} {
		if chosen {
			faults++
		}
	}
	if faults != 1 {
		return printErr("Choose one chaos fault", errors.New("provide exactly one of --latency, --error, --bandwidth, --timeout, --reset, --connect-timeout, --connect-refused, --dns-latency, --dns-timeout, --dns-servfail, --dns-nxdomain, --malformed-json, --truncated-body, or --response-lost"))
	}
	kind := chaos.KindLatency
	statusCode := 0
	selectedLatency := *latency
	if *errorCode != 0 {
		kind = chaos.KindHTTPStatus
		statusCode = *errorCode
	}
	if *tcpPort != 0 && *latency != "" {
		kind = chaos.KindTCPLatency
	}
	if *bandwidth != 0 {
		kind = chaos.KindTCPBandwidth
	}
	if *stall {
		kind = chaos.KindTCPTimeout
	}
	if *reset {
		kind = chaos.KindTCPReset
	}
	if *connectTimeout {
		kind = chaos.KindTCPConnectTimeout
	}
	if *connectRefused {
		kind = chaos.KindTCPConnectRefused
	}
	if *dnsLatency != "" {
		kind = chaos.KindDNSLatency
		selectedLatency = *dnsLatency
	}
	if *dnsTimeout {
		kind = chaos.KindDNSTimeout
	}
	if *dnsServFail {
		kind = chaos.KindDNSServFail
	}
	if *dnsNXDomain {
		kind = chaos.KindDNSNXDomain
	}
	if *malformedJSON {
		kind = chaos.KindHTTPMalformedJSON
	}
	if *responseLost {
		kind = chaos.KindHTTPResponseLost
	}
	if *truncatedBody {
		kind = chaos.KindHTTPTruncatedBody
	}
	spec := &testChaosSpec{Duration: *duration, Rules: []testChaosRule{{
		From: *from, To: *target, Kind: kind, Percent: *percent,
		Latency: selectedLatency, StatusCode: statusCode, RetryAfter: *retryAfter, Seed: *seed, Port: *tcpPort,
		Direction: *direction, RateKiBPerSecond: *bandwidth, ResetAfter: *resetAfter,
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
