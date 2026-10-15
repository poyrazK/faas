package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type testRequiredEnvironment struct {
	Name   string                     `yaml:"name" json:"name"`
	Match  testCampaignPolicySelector `yaml:"match" json:"match"`
	Expect testEnvironmentExpectation `yaml:"expect" json:"expect"`
}

type testEnvironmentExpectation struct {
	PlatformVersion        *string                            `yaml:"platform_version,omitempty" json:"platform_version,omitempty"`
	RequirePlatformVersion bool                               `yaml:"require_platform_version,omitempty" json:"require_platform_version,omitempty"`
	Workloads              map[string]testWorkloadExpectation `yaml:"workloads,omitempty" json:"workloads,omitempty"`
}

type testWorkloadExpectation struct {
	Type                  *string `yaml:"type,omitempty" json:"type,omitempty"`
	Runtime               *string `yaml:"runtime,omitempty" json:"runtime,omitempty"`
	RuntimeVersion        *string `yaml:"runtime_version,omitempty" json:"runtime_version,omitempty"`
	RequireRuntimeVersion bool    `yaml:"require_runtime_version,omitempty" json:"require_runtime_version,omitempty"`
	RAMMB                 *int    `yaml:"ram_mb,omitempty" json:"ram_mb,omitempty"`
	VCPU                  *int    `yaml:"vcpu,omitempty" json:"vcpu,omitempty"`
	CPUMillicores         *int    `yaml:"cpu_millicores,omitempty" json:"cpu_millicores,omitempty"`
	MaxConcurrency        *int    `yaml:"max_concurrency,omitempty" json:"max_concurrency,omitempty"`
	ConcurrencyPerVM      *int    `yaml:"concurrency_per_vm,omitempty" json:"concurrency_per_vm,omitempty"`
	MinInstances          *int    `yaml:"min_instances,omitempty" json:"min_instances,omitempty"`
	Postgres              *bool   `yaml:"postgres,omitempty" json:"postgres,omitempty"`
	Fixture               *string `yaml:"fixture,omitempty" json:"fixture,omitempty"`
	FailFirst             *int    `yaml:"fail_first,omitempty" json:"fail_first,omitempty"`
	TCPPorts              *[]int  `yaml:"tcp_ports,omitempty" json:"tcp_ports,omitempty"`
}

type testRequiredEnvironmentIssue struct {
	Code         string          `json:"code"`
	ReceiptIndex *int            `json:"receipt_index"`
	RunID        string          `json:"run_id,omitempty"`
	Field        string          `json:"field"`
	Expected     json.RawMessage `json:"expected"`
	Observed     json.RawMessage `json:"observed"`
	Reason       string          `json:"reason"`
}

type testRequiredEnvironmentSide struct {
	ExecutedRuns       int                            `json:"executed_runs"`
	SatisfiedRuns      int                            `json:"satisfied_runs"`
	SupportingReceipts []int                          `json:"supporting_receipt_indexes"`
	Issues             []testRequiredEnvironmentIssue `json:"issues"`
	OmittedIssues      int                            `json:"omitted_issues"`
}

type testRequiredEnvironmentResult struct {
	Name          string                      `json:"name"`
	Match         testCampaignPolicySelector  `json:"match"`
	Expect        testEnvironmentExpectation  `json:"expect"`
	Configuration string                      `json:"configuration_sha256,omitempty"`
	Context       testFailureGroupKey         `json:"context"`
	Baseline      testRequiredEnvironmentSide `json:"baseline"`
	Current       testRequiredEnvironmentSide `json:"current"`
	Passed        bool                        `json:"passed"`
	Reason        string                      `json:"reason,omitempty"`
}

func validateTestRequiredEnvironments(requirements []testRequiredEnvironment) error {
	if len(requirements) > 100 {
		return fmt.Errorf("at most 100 environment requirements are supported")
	}
	seen := make(map[string]bool)
	for _, requirement := range requirements {
		m, expected := requirement.Match, requirement.Expect
		if strings.TrimSpace(requirement.Name) == "" || seen[requirement.Name] || m.Scenario == nil || *m.Scenario == "" {
			return fmt.Errorf("environment requirements need unique nonempty names and a nonempty scenario selector")
		}
		seen[requirement.Name] = true
		if m.Step != nil || m.Phase != nil || m.Kind != nil || m.Assertion != nil || m.Request != nil {
			return fmt.Errorf("environment requirement %q supports only configuration/scenario/profile/engine/case selectors", requirement.Name)
		}
		if m.Configuration != nil {
			decoded, err := hex.DecodeString(*m.Configuration)
			if err != nil || len(decoded) != 32 || *m.Configuration != strings.ToLower(*m.Configuration) {
				return fmt.Errorf("environment requirement %q needs a lowercase configuration SHA-256", requirement.Name)
			}
		}
		if expected.PlatformVersion != nil && *expected.PlatformVersion == "" {
			return fmt.Errorf("environment requirement %q platform version must be nonempty", requirement.Name)
		}
		if expected.PlatformVersion == nil && !expected.RequirePlatformVersion && len(expected.Workloads) == 0 {
			return fmt.Errorf("environment requirement %q needs at least one expectation", requirement.Name)
		}
		if len(expected.Workloads) > 32 {
			return fmt.Errorf("environment requirement %q exceeds 32 workload expectations", requirement.Name)
		}
		for name, workload := range expected.Workloads {
			if strings.TrimSpace(name) == "" || len(name) > 80 {
				return fmt.Errorf("environment requirement %q has an invalid workload name", requirement.Name)
			}
			if workload.Type != nil && *workload.Type != "app" && *workload.Type != "function" {
				return fmt.Errorf("environment requirement %q type must be app or function", requirement.Name)
			}
			for _, value := range []*string{workload.Runtime, workload.RuntimeVersion} {
				if value != nil && *value == "" {
					return fmt.Errorf("environment requirement %q runtime labels and versions must be nonempty", requirement.Name)
				}
			}
			for _, value := range []*int{workload.RAMMB, workload.VCPU, workload.CPUMillicores, workload.MaxConcurrency, workload.ConcurrencyPerVM} {
				if value != nil && *value <= 0 {
					return fmt.Errorf("environment requirement %q resource expectations must be positive", requirement.Name)
				}
			}
			for _, value := range []*int{workload.MinInstances, workload.FailFirst} {
				if value != nil && *value < 0 {
					return fmt.Errorf("environment requirement %q count expectations must be nonnegative", requirement.Name)
				}
			}
			if workload.TCPPorts != nil {
				ports := make(map[int]bool)
				for _, port := range *workload.TCPPorts {
					if port < 1 || port > 65535 || ports[port] {
						return fmt.Errorf("environment requirement %q needs unique TCP ports in 1..65535", requirement.Name)
					}
					ports[port] = true
				}
			}
		}
	}
	return nil
}

func emptyTestRequiredEnvironmentSide() testRequiredEnvironmentSide {
	return testRequiredEnvironmentSide{SupportingReceipts: make([]int, 0), Issues: make([]testRequiredEnvironmentIssue, 0)}
}

func addTestRequiredEnvironmentIssue(side *testRequiredEnvironmentSide, issue testRequiredEnvironmentIssue) {
	if len(side.Issues) >= 50 {
		side.OmittedIssues++
		return
	}
	side.Issues = append(side.Issues, issue)
}

func testRequiredEnvironmentRun(r testRunReceipt, root int, expected testEnvironmentExpectation, side *testRequiredEnvironmentSide) {
	side.ExecutedRuns++
	valid := true
	check := func(field string, want, got any, known bool) {
		left, _ := json.Marshal(want)
		right, _ := json.Marshal(got)
		if known && string(left) == string(right) {
			return
		}
		valid = false
		code := "mismatch"
		reason := "recorded value differs from required value"
		if !known {
			code = "unavailable"
			reason = "required evidence is unavailable"
			right = json.RawMessage("null")
		}
		index := root
		addTestRequiredEnvironmentIssue(side, testRequiredEnvironmentIssue{
			Code: code, ReceiptIndex: &index, RunID: r.RunID, Field: field, Expected: left, Observed: right, Reason: reason,
		})
	}
	if testDigestScopeFingerprint([]testRunReceipt{r}) == "" {
		check("configuration_sha256", "known", nil, false)
	}
	checkTestEnvironmentExpectation(r.Environment, expected, check)
	if valid {
		side.SatisfiedRuns++
		if len(side.SupportingReceipts) == 0 || side.SupportingReceipts[len(side.SupportingReceipts)-1] != root {
			side.SupportingReceipts = append(side.SupportingReceipts, root)
		}
	}
}

// Shared field checks do not create receipts or execution counts.
func checkTestEnvironmentExpectation(snapshot *testEnvironmentSnapshot, expected testEnvironmentExpectation, check func(string, any, any, bool)) {
	if _, _, present := testEnvironmentFields(snapshot); !present {
		check("environment", "complete version 1 snapshot", nil, false)
		return
	}
	if expected.PlatformVersion != nil {
		check("platform_version", expected.PlatformVersion, snapshot.PlatformVersion, snapshot.PlatformVersion != nil && *snapshot.PlatformVersion != "")
	} else if expected.RequirePlatformVersion {
		known := snapshot.PlatformVersion != nil && *snapshot.PlatformVersion != ""
		check("platform_version.known", true, known, known)
	}
	names := make([]string, 0, len(expected.Workloads))
	for name := range expected.Workloads {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		want := expected.Workloads[name]
		got, exists := snapshot.Workloads[name]
		prefix := "workloads." + name + "."
		if !exists {
			check(prefix+"present", true, false, true)
			continue
		}
		if want.Type != nil {
			check(prefix+"type", want.Type, got.Type, got.Type != "")
		}
		if want.Runtime != nil {
			check(prefix+"runtime", want.Runtime, got.Runtime, got.Runtime != "")
		}
		if want.RuntimeVersion != nil {
			check(prefix+"runtime_version", want.RuntimeVersion, got.RuntimeVersion, got.RuntimeVersion != nil && *got.RuntimeVersion != "")
		} else if want.RequireRuntimeVersion {
			known := got.RuntimeVersion != nil && *got.RuntimeVersion != ""
			check(prefix+"runtime_version.known", true, known, known)
		}
		if want.RAMMB != nil {
			check(prefix+"ram_mb", want.RAMMB, got.RAMMB, got.RAMMB > 0)
		}
		if want.VCPU != nil {
			check(prefix+"vcpu", want.VCPU, got.VCPU, got.VCPU > 0)
		}
		if want.CPUMillicores != nil {
			check(prefix+"cpu_millicores", want.CPUMillicores, got.CPUMillicores, got.CPUMillicores > 0)
		}
		if want.MaxConcurrency != nil {
			check(prefix+"max_concurrency", want.MaxConcurrency, got.MaxConcurrency, got.MaxConcurrency > 0)
		}
		if want.ConcurrencyPerVM != nil {
			check(prefix+"concurrency_per_vm", want.ConcurrencyPerVM, got.ConcurrencyPerVM, got.ConcurrencyPerVM > 0)
		}
		if want.MinInstances != nil {
			check(prefix+"min_instances", want.MinInstances, got.MinInstances, got.MinInstances >= 0)
		}
		if want.Postgres != nil {
			check(prefix+"postgres", want.Postgres, got.Postgres, true)
		}
		if want.Fixture != nil {
			check(prefix+"fixture", want.Fixture, got.Fixture, true)
		}
		if want.FailFirst != nil {
			check(prefix+"fail_first", want.FailFirst, got.FailFirst, got.FailFirst >= 0)
		}
		if want.TCPPorts != nil {
			ports, observed := append([]int{}, (*want.TCPPorts)...), append([]int{}, got.TCPPorts...)
			sort.Ints(ports)
			sort.Ints(observed)
			check(prefix+"tcp_ports", ports, observed, true)
		}
	}
}

func evaluateTestRequiredEnvironments(policy testCampaignPolicy, before, after []testRunReceipt) []testRequiredEnvironmentResult {
	results := make([]testRequiredEnvironmentResult, 0)
	for _, requirement := range policy.RequiredEnvironments {
		sides := [2]map[testCampaignCoverageKey]testRequiredEnvironmentSide{make(map[testCampaignCoverageKey]testRequiredEnvironmentSide), make(map[testCampaignCoverageKey]testRequiredEnvironmentSide)}
		for side, receipts := range [][]testRunReceipt{before, after} {
			for root, receipt := range receipts {
				var leaves []testRunReceipt
				collectTestFailureLeaves(receipt, "", &leaves)
				for _, leaf := range leaves {
					hash := testDigestScopeFingerprint([]testRunReceipt{leaf})
					context := testFailureGroupingKeyForRequirement(leaf, requirement.Match)
					single := testCampaignPolicy{Rules: []testCampaignScopedRule{{Name: "requirement", Match: requirement.Match}}}
					if _, selected := selectTestCampaignPolicy(single, context, hash, true); selected != "requirement" {
						continue
					}
					key := testCampaignCoverageKey{Hash: hash, Scope: testFailureScope(leaf)}
					counts, exists := sides[side][key]
					if !exists {
						counts = emptyTestRequiredEnvironmentSide()
					}
					if leaf.Status == "passed" || leaf.Status == "failed" {
						testRequiredEnvironmentRun(leaf, root, requirement.Expect, &counts)
					}
					sides[side][key] = counts
				}
			}
		}
		keys := make([]testCampaignCoverageKey, 0)
		seen := make(map[testCampaignCoverageKey]bool)
		for _, counts := range sides {
			for key := range counts {
				if !seen[key] {
					seen[key] = true
					keys = append(keys, key)
				}
			}
		}
		sort.Slice(keys, func(i, j int) bool {
			a, _ := json.Marshal([]string{keys[i].Hash, keys[i].Scope.scenario, keys[i].Scope.profile, keys[i].Scope.engine, keys[i].Scope.caseName})
			b, _ := json.Marshal([]string{keys[j].Hash, keys[j].Scope.scenario, keys[j].Scope.profile, keys[j].Scope.engine, keys[j].Scope.caseName})
			return string(a) < string(b)
		})
		if len(keys) == 0 {
			keys = append(keys, testCampaignCoverageKey{Hash: testRequiredSelectorValue(requirement.Match.Configuration), Scope: testFailureGroupScope{
				scenario: testRequiredSelectorValue(requirement.Match.Scenario), profile: testRequiredSelectorValue(requirement.Match.Profile),
				engine: testRequiredSelectorValue(requirement.Match.Engine), caseName: testRequiredSelectorValue(requirement.Match.Case),
			}})
		}
		for _, key := range keys {
			counts := [2]testRequiredEnvironmentSide{}
			for side := range sides {
				value, exists := sides[side][key]
				if !exists {
					value = emptyTestRequiredEnvironmentSide()
				}
				if value.ExecutedRuns == 0 {
					addTestRequiredEnvironmentIssue(&value, testRequiredEnvironmentIssue{
						Code: "missing_context", Field: "executed_runs", Expected: json.RawMessage("1"), Observed: json.RawMessage("0"), Reason: "no matching executed context",
					})
				}
				counts[side] = value
			}
			result := testRequiredEnvironmentResult{Name: requirement.Name, Match: requirement.Match, Expect: requirement.Expect, Configuration: key.Hash,
				Context:  testFailureGroupKey{Scenario: key.Scope.scenario, Profile: key.Scope.profile, Engine: key.Scope.engine, Case: key.Scope.caseName},
				Baseline: counts[0], Current: counts[1]}
			result.Passed = counts[0].ExecutedRuns > 0 && counts[1].ExecutedRuns > 0 &&
				counts[0].SatisfiedRuns == counts[0].ExecutedRuns && counts[1].SatisfiedRuns == counts[1].ExecutedRuns
			if !result.Passed {
				result.Reason = fmt.Sprintf("environment requirement %q requires every executed run on both sides to satisfy its expectations; baseline %d/%d, current %d/%d",
					requirement.Name, counts[0].SatisfiedRuns, counts[0].ExecutedRuns, counts[1].SatisfiedRuns, counts[1].ExecutedRuns)
			}
			results = append(results, result)
		}
	}
	return results
}
