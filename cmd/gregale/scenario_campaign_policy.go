package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type testCampaignPolicy struct {
	RequiredCoverage []testCampaignCoverageRequirement `yaml:"required_coverage,omitempty"`
	Rules []testCampaignScopedRule `yaml:"rules,omitempty"`
	SchemaVersion         int      `yaml:"schema_version"`
	FailNewFailures       bool     `yaml:"fail_new_failures"`
	MaxFrequencyIncrease  *float64 `yaml:"max_frequency_increase_percentage_points"`
	MinExecutedRuns       int      `yaml:"min_executed_runs"`
	NotComparable         string   `yaml:"on_not_comparable"`
	InsufficientSamples   string   `yaml:"on_insufficient_samples"`
}

func readTestCampaignPolicy(path string) (testCampaignPolicy, error) {
	var policy testCampaignPolicy
	file, err := os.Open(path)
	if err != nil {
		return policy, err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return policy, err
	}
	if len(body) > 1<<20 {
		return policy, errors.New("policy exceeds 1 MiB")
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(body)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&policy); err != nil {
		return policy, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return policy, errors.New("policy must contain exactly one YAML document")
	}
	if policy.SchemaVersion != 1 || policy.MinExecutedRuns < 1 {
		return policy, errors.New("schema_version must be 1 and min_executed_runs must be positive")
	}
	for _, action := range []string{policy.NotComparable, policy.InsufficientSamples} {
		if action != "fail" && action != "ignore" {
			return policy, errors.New("on_not_comparable and on_insufficient_samples must explicitly be fail or ignore")
		}
	}
	if limit := policy.MaxFrequencyIncrease; limit != nil && (math.IsNaN(*limit) || math.IsInf(*limit, 0) || *limit < 0 || *limit > 100) {
		return policy, errors.New("frequency increase limit must be finite and in 0..100 percentage points")
	}
	if err := validateTestCampaignScopedRules(policy.Rules); err != nil {
		return policy, err
	}
	if err := validateTestCampaignRequiredCoverage(policy.RequiredCoverage); err != nil {
		return policy, err
	}
	return policy, nil
}

type testCampaignPolicyViolation struct {
	Requirement *testCampaignRequiredCoverageResult `json:"requirement,omitempty"`
	PolicyRule string `json:"policy_rule"`
	EffectivePolicy *testCampaignPolicySettings `json:"effective_policy,omitempty"`
	Rule          string                      `json:"rule"`
	Reason        string                      `json:"reason"`
	Configuration string                      `json:"configuration_sha256,omitempty"`
	Context       testFailureGroupKey         `json:"context"`
	BeforeRuns    int                         `json:"baseline_executed_runs"`
	AfterRuns     int                         `json:"current_executed_runs"`
	Comparison    *testDigestComparisonRecord `json:"comparison,omitempty"`
}

type testCampaignPolicyResult struct {
	RequiredCoverage []testCampaignRequiredCoverageResult `json:"required_coverage"`
	Decisions []testCampaignPolicyDecision `json:"decisions"`
	SchemaVersion int                           `json:"schema_version"`
	BaselineURL   string                        `json:"baseline_evidence_url,omitempty"`
	CurrentURL    string                        `json:"current_evidence_url,omitempty"`
	Passed        bool                          `json:"passed"`
	Violations    []testCampaignPolicyViolation `json:"violations"`
	Comparisons   testDigestComparisonReport    `json:"comparison_report"`
}

// cmdTestCompare routes here only when --policy explicitly selects campaign mode.
func cmdTestCampaignPolicy(args []string) int {
	fs := newFlagSet("test compare --policy", flag.ContinueOnError)
	from := fs.String("from", "", "current raw JSON report")
	baseline := fs.String("baseline", "", "baseline raw JSON report")
	junitPath := fs.String("junit", "", "write campaign policy violations as JUnit XML")
	policyPath := fs.String("policy", "", "versioned YAML campaign regression policy")
	evidence := fs.String("evidence-url", "", "current HTML evidence URL")
	baselineEvidence := fs.String("baseline-evidence-url", "", "baseline HTML evidence URL")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 || *from == "" || *baseline == "" || *policyPath == "" {
		return printErr("Invalid campaign policy options", errors.New("--from, --baseline and --policy are required; positional reports and legacy budget flags are not supported in policy mode"))
	}
	if *junitPath != "" {
		for _, input := range []string{*from, *baseline, *policyPath} {
			aliases, err := testReportPathAliases(*junitPath, input)
			if err != nil {
				return printErr("Invalid JUnit output path", err)
			}
			if aliases {
				return printErr("Invalid JUnit output path", fmt.Errorf("JUnit output must not overwrite input %q", input))
			}
		}
		if output, err := os.Stat(*junitPath); err == nil {
			if stdout, err := os.Stdout.Stat(); err == nil && os.SameFile(output, stdout) {
				return printErr("Invalid JUnit output path", errors.New("JUnit output must not alias JSON stdout"))
			}
		}
	}
	policy, err := readTestCampaignPolicy(*policyPath)
	if err != nil {
		return printErr("Invalid campaign policy", err)
	}
	currentURL, err := validateTestDigestURL(*evidence)
	if err != nil {
		return printErr("Invalid current evidence URL", err)
	}
	oldURL, err := validateTestDigestURL(*baselineEvidence)
	if err != nil {
		return printErr("Invalid baseline evidence URL", err)
	}
	before, _, err := readTestBaselineReport(*baseline, nil)
	if err != nil {
		return printErr("Could not read baseline", err)
	}
	after, _, err := readTestBaselineReport(*from, nil)
	if err != nil {
		return printErr("Could not read current report", err)
	}
	result, err := evaluateTestCampaignPolicy(policy, before, after, oldURL, currentURL)
	if err != nil {
		return printErr("Could not evaluate policy", err)
	}
	if *junitPath != "" {
		body, err := marshalTestCampaignPolicyJUnit(policy, result)
		if err != nil {
			return printErr("Could not encode policy JUnit", err)
		}
		if err := os.WriteFile(*junitPath, body, 0o600); err != nil {
			return printErr("Could not write policy JUnit", err)
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return printErr("Could not write policy result", err)
	}
	if !result.Passed {
		return 2
	}
	return 0
}

func evaluateTestCampaignPolicy(policy testCampaignPolicy, before, after []testRunReceipt, baselineURL, currentURL string) (testCampaignPolicyResult, error) {
	result := testCampaignPolicyResult{SchemaVersion: 1, BaselineURL: baselineURL, CurrentURL: currentURL, Violations: make([]testCampaignPolicyViolation, 0), Decisions: make([]testCampaignPolicyDecision, 0)}
	body, err := marshalTestDigestComparison(before, after, baselineURL, currentURL)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(body, &result.Comparisons); err != nil {
		return result, err
	}
	for index, row := range result.Comparisons.Comparisons {
		selected, ruleID := selectTestCampaignPolicy(policy, row.Key, row.ConfigurationSHA256, false)
		settings := testCampaignSettings(selected)
		result.Decisions = append(result.Decisions, testCampaignPolicyDecision{ComparisonIndex: index, PolicyRule: ruleID, EffectivePolicy: settings})
		violate := func(rule, reason string) {
			copy := row
			result.Violations = append(result.Violations, testCampaignPolicyViolation{
				Rule: rule, Reason: reason, PolicyRule: ruleID, EffectivePolicy: &settings, Configuration: row.ConfigurationSHA256, Context: row.Key,
				BeforeRuns: row.Baseline.ExecutedRuns, AfterRuns: row.Current.ExecutedRuns, Comparison: &copy,
			})
		}
		if row.Status == "not_comparable" {
			if selected.NotComparable == "fail" {
				violate("not_comparable", row.Reason)
			}
			continue
		}
		if row.Baseline.ExecutedRuns < selected.MinExecutedRuns || row.Current.ExecutedRuns < selected.MinExecutedRuns {
			if selected.InsufficientSamples == "fail" {
				violate("insufficient_samples", fmt.Sprintf("both reports require at least %d executed runs for this assertion", selected.MinExecutedRuns))
			}
			continue
		}
		if selected.FailNewFailures && row.Status == "newly_observed" {
			violate("new_failure_group", "a first-failure group is newly observed with matching exercised coverage")
		}
		if selected.MaxFrequencyIncrease != nil && row.FrequencyDeltaPP != nil && *row.FrequencyDeltaPP > *selected.MaxFrequencyIncrease {
			violate("frequency_increase", fmt.Sprintf("frequency increased by %.6g percentage points, exceeding %.6g", *row.FrequencyDeltaPP, *selected.MaxFrequencyIncrease))
		}
	}
	for _, coverage := range testCampaignPolicyCoverage(policy, before, after) {
		duplicate := false
		for _, violation := range result.Violations {
			if coverage.Rule == "insufficient_samples" && violation.Rule == coverage.Rule &&
				violation.PolicyRule == coverage.PolicyRule && violation.Configuration == coverage.Configuration &&
				violation.Context.Scenario == coverage.Context.Scenario && violation.Context.Profile == coverage.Context.Profile &&
				violation.Context.Engine == coverage.Context.Engine && violation.Context.Case == coverage.Context.Case {
				duplicate = true
				break
			}
		}
		if !duplicate {
			result.Violations = append(result.Violations, coverage)
		}
	}
	result.RequiredCoverage = evaluateTestCampaignRequiredCoverage(policy, before, after)
	for _, coverage := range result.RequiredCoverage {
		if coverage.Passed {
			continue
		}
		settings := testCampaignSettings(policy)
		settings.MinExecutedRuns = coverage.MinExecutedRuns
		settings.NotComparable, settings.InsufficientSamples = "fail", "fail"
		requirement := coverage
		result.Violations = append(result.Violations, testCampaignPolicyViolation{
			Rule: "explicit_required_coverage", PolicyRule: "required_coverage/" + coverage.Name,
			Reason: coverage.Reason, Configuration: coverage.Configuration, Context: coverage.Context,
			BeforeRuns: coverage.Baseline.CoveredRuns, AfterRuns: coverage.Current.CoveredRuns,
			EffectivePolicy: &settings, Requirement: &requirement,
		})
	}
	result.Passed = len(result.Violations) == 0
	return result, nil
}

type testCampaignCoverageKey struct {
	Hash  string
	Scope testFailureGroupScope
}

// All executed baseline contexts are required in the current report, including
// contexts that have never produced a failure group. New contexts need baselines.
func testCampaignPolicyCoverage(policy testCampaignPolicy, before, after []testRunReceipt) []testCampaignPolicyViolation {
	collect := func(receipts []testRunReceipt) map[testCampaignCoverageKey]int {
		counts := make(map[testCampaignCoverageKey]int)
		for _, r := range receipts {
			var leaves []testRunReceipt
			collectTestFailureLeaves(r, "", &leaves)
			for _, leaf := range leaves {
				key := testCampaignCoverageKey{Hash: testDigestScopeFingerprint([]testRunReceipt{leaf}), Scope: testFailureScope(leaf)}
				// Retain skipped-only contexts with zero executed runs.
				if _, exists := counts[key]; !exists {
					counts[key] = 0
				}
				if leaf.Status == "passed" || leaf.Status == "failed" {
					counts[key]++
				}
			}
		}
		return counts
	}
	old, current := collect(before), collect(after)
	keys := make([]testCampaignCoverageKey, 0)
	seen := make(map[testCampaignCoverageKey]bool)
	for _, counts := range []map[testCampaignCoverageKey]int{old, current} {
		for key := range counts {
			if !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		left := []string{a.Hash, a.Scope.scenario, a.Scope.profile, a.Scope.engine, a.Scope.caseName}
		right := []string{b.Hash, b.Scope.scenario, b.Scope.profile, b.Scope.engine, b.Scope.caseName}
		for index := range left {
			if left[index] != right[index] {
				return left[index] < right[index]
			}
		}
		return false
	})
	var violations []testCampaignPolicyViolation
	for _, key := range keys {
		context := testFailureGroupKey{Scenario: key.Scope.scenario, Profile: key.Scope.profile, Engine: key.Scope.engine, Case: key.Scope.caseName}
		selected, ruleID := selectTestCampaignPolicy(policy, context, key.Hash, true)
		settings := testCampaignSettings(selected)
		rule, reason := "", ""
		if key.Hash == "" || old[key] == 0 || current[key] == 0 {
			if selected.NotComparable == "fail" {
				rule, reason = "required_coverage", "configuration fingerprint or matching executed context is absent"
			}
		} else if (old[key] < selected.MinExecutedRuns || current[key] < selected.MinExecutedRuns) && selected.InsufficientSamples == "fail" {
			rule, reason = "insufficient_samples", fmt.Sprintf("both reports require at least %d executed runs per configuration/context", selected.MinExecutedRuns)
		}
		if rule != "" {
			violations = append(violations, testCampaignPolicyViolation{
				Rule: rule, Reason: reason, PolicyRule: ruleID, EffectivePolicy: &settings, Configuration: key.Hash,
				Context: testFailureGroupKey{Scenario: key.Scope.scenario, Profile: key.Scope.profile, Engine: key.Scope.engine, Case: key.Scope.caseName},
				BeforeRuns: old[key], AfterRuns: current[key],
			})
		}
	}
	if len(keys) == 0 && policy.NotComparable == "fail" {
		violations = append(violations, testCampaignPolicyViolation{Rule: "required_coverage", PolicyRule: "default", Reason: "neither report contains campaign contexts"})
	}
	return violations
}
