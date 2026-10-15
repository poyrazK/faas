package main

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

type testCampaignCoverageRequirement struct {
	Name            string                     `yaml:"name" json:"name"`
	Match           testCampaignPolicySelector `yaml:"match" json:"match"`
	MinExecutedRuns int                        `yaml:"min_executed_runs" json:"min_executed_runs"`
}

type testCampaignRequiredCoverageSide struct {
	ExecutedRuns       int   `json:"executed_runs"`
	CoveredRuns        int   `json:"covered_runs"`
	SupportingReceipts []int `json:"supporting_receipt_indexes"`
}

type testCampaignRequiredCoverageResult struct {
	Name            string                           `json:"name"`
	Match           testCampaignPolicySelector       `json:"match"`
	Configuration   string                           `json:"configuration_sha256,omitempty"`
	Context         testFailureGroupKey              `json:"context"`
	MinExecutedRuns int                              `json:"min_executed_runs"`
	Baseline        testCampaignRequiredCoverageSide `json:"baseline"`
	Current         testCampaignRequiredCoverageSide `json:"current"`
	Passed          bool                             `json:"passed"`
	Reason          string                           `json:"reason,omitempty"`
}

func testRequiredSelectorValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func validateTestCampaignRequiredCoverage(requirements []testCampaignCoverageRequirement) error {
	if len(requirements) > 100 {
		return fmt.Errorf("at most 100 coverage requirements are allowed")
	}
	seen := make(map[string]bool)
	for _, r := range requirements {
		m := r.Match
		if strings.TrimSpace(r.Name) == "" || seen[r.Name] || r.MinExecutedRuns < 1 || m.Scenario == nil || *m.Scenario == "" {
			return fmt.Errorf("coverage requirements need unique nonempty names, a nonempty scenario selector and a positive min_executed_runs")
		}
		seen[r.Name] = true
		if m.Configuration != nil {
			decoded, err := hex.DecodeString(*m.Configuration)
			if err != nil || len(decoded) != 32 || *m.Configuration != strings.ToLower(*m.Configuration) {
				return fmt.Errorf("requirement %q configuration must be a lowercase SHA-256 fingerprint", r.Name)
			}
		}
		if m.Phase != nil && *m.Phase == "" {
			return fmt.Errorf("requirement %q phase must be nonempty", r.Name)
		}
		if m.Kind != nil || m.Assertion != nil || m.Request != nil {
			if m.Phase == nil || m.Kind == nil || m.Assertion == nil || *m.Assertion == "" {
				return fmt.Errorf("requirement %q assertion coverage requires phase, kind and assertion", r.Name)
			}
			kind, assertion := *m.Kind, *m.Assertion
			valid := false
			switch kind {
			case "threshold":
				valid = true
			case "http_attempts", "tcp_attempts":
				valid = assertion == "attempts_per_request"
			case "per_request_attempts":
				valid = assertion == "max_attempts"
			case "recovery":
				valid = assertion == "recovery" || (assertion == "step_recovery" && testRequiredSelectorValue(m.Request) != "")
			case "request_deadline":
				valid = assertion == "request_deadlines" && testRequiredSelectorValue(m.Request) != ""
			case "circuit_breaker", "connection_leaks", "pool_exhaustion":
				valid = assertion == kind
			}
			if !valid {
				return fmt.Errorf("requirement %q uses an unsupported assertion kind/name", r.Name)
			}
		}
	}
	return nil
}

// Requirements count each leaf at most once, independently in every matching
// exact configuration/context. They cannot be waived by regression exceptions.
func evaluateTestCampaignRequiredCoverage(policy testCampaignPolicy, before, after []testRunReceipt) []testCampaignRequiredCoverageResult {
	results := make([]testCampaignRequiredCoverageResult, 0)
	for _, requirement := range policy.RequiredCoverage {
		sides := [2]map[testCampaignCoverageKey]testCampaignRequiredCoverageSide{
			make(map[testCampaignCoverageKey]testCampaignRequiredCoverageSide),
			make(map[testCampaignCoverageKey]testCampaignRequiredCoverageSide),
		}
		for side, receipts := range [][]testRunReceipt{before, after} {
			for root, receipt := range receipts {
				var leaves []testRunReceipt
				collectTestFailureLeaves(receipt, "", &leaves)
				for _, leaf := range leaves {
					hash := testDigestScopeFingerprint([]testRunReceipt{leaf})
					context := testFailureGroupingKeyForRequirement(leaf, requirement.Match)
					// Match only identity dimensions here; evidence is checked below.
					selector := requirement.Match
					selector.Step, selector.Phase, selector.Kind, selector.Assertion, selector.Request = nil, nil, nil, nil, nil
					single := testCampaignPolicy{Rules: []testCampaignScopedRule{{Name: "requirement", Match: selector}}}
					if _, matched := selectTestCampaignPolicy(single, context, hash, true); matched != "requirement" {
						continue
					}
					key := testCampaignCoverageKey{Hash: hash, Scope: testFailureScope(leaf)}
					counts, exists := sides[side][key]
					if !exists {
						counts.SupportingReceipts = make([]int, 0)
					}
					if leaf.Status == "passed" || leaf.Status == "failed" {
						counts.ExecutedRuns++
						if hash != "" && testCampaignRequirementRunCovers(leaf, requirement.Match) {
							counts.CoveredRuns++
							if len(counts.SupportingReceipts) == 0 || counts.SupportingReceipts[len(counts.SupportingReceipts)-1] != root {
								counts.SupportingReceipts = append(counts.SupportingReceipts, root)
							}
						}
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
		if len(keys) == 0 {
			keys = append(keys, testCampaignCoverageKey{Hash: testRequiredSelectorValue(requirement.Match.Configuration), Scope: testFailureGroupScope{
				scenario: testRequiredSelectorValue(requirement.Match.Scenario), profile: testRequiredSelectorValue(requirement.Match.Profile),
				engine: testRequiredSelectorValue(requirement.Match.Engine), caseName: testRequiredSelectorValue(requirement.Match.Case),
			}})
		}
		for _, key := range keys {
			old, current := sides[0][key], sides[1][key]
			if old.SupportingReceipts == nil {
				old.SupportingReceipts = make([]int, 0)
			}
			if current.SupportingReceipts == nil {
				current.SupportingReceipts = make([]int, 0)
			}
			context := testFailureGroupingKeyForRequirement(testRunReceipt{Scenario: key.Scope.scenario, Profile: key.Scope.profile, Engine: key.Scope.engine, Case: key.Scope.caseName}, requirement.Match)
			result := testCampaignRequiredCoverageResult{Name: requirement.Name, Match: requirement.Match, Configuration: key.Hash, Context: context,
				MinExecutedRuns: requirement.MinExecutedRuns, Baseline: old, Current: current}
			result.Passed = key.Hash != "" && old.CoveredRuns >= requirement.MinExecutedRuns && current.CoveredRuns >= requirement.MinExecutedRuns
			if !result.Passed {
				result.Reason = fmt.Sprintf("explicit coverage %q requires at least %d evidence-covered runs on both sides per configuration/context; baseline %d of %d executed, current %d of %d executed",
					requirement.Name, requirement.MinExecutedRuns, old.CoveredRuns, old.ExecutedRuns, current.CoveredRuns, current.ExecutedRuns)
			}
			results = append(results, result)
		}
	}
	return results
}

func testFailureGroupingKeyForRequirement(r testRunReceipt, m testCampaignPolicySelector) testFailureGroupKey {
	return testFailureGroupKey{Scenario: r.Scenario, Profile: r.Profile, Engine: r.Engine, Case: r.Case,
		Step: testRequiredSelectorValue(m.Step), Phase: testRequiredSelectorValue(m.Phase), Kind: testRequiredSelectorValue(m.Kind),
		Assertion: testRequiredSelectorValue(m.Assertion), Request: testRequiredSelectorValue(m.Request)}
}

func testCampaignRequirementRunCovers(r testRunReceipt, m testCampaignPolicySelector) bool {
	load := r.Load
	if m.Step != nil && *m.Step != "" {
		found := 0
		for _, step := range r.Steps {
			if step.Name == *m.Step {
				if step.Status != "passed" && step.Status != "failed" {
					return false
				}
				found++
				load = step.Load
			}
		}
		if found != 1 {
			return false
		}
	}
	if m.Phase == nil {
		return true
	}
	if load == nil {
		return false
	}
	index, count := -1, 0
	for i, phase := range load.ChaosPhases {
		if phase.Name == *m.Phase {
			index, count = i, count+1
		}
	}
	if count != 1 {
		return false
	}
	phase := load.ChaosPhases[index]
	if phase.ActualDurationMS <= 0 || phase.Requests <= 0 {
		return false
	}
	role := phase.Role
	if role == "" {
		role = testRecordedPhaseRole(index, load.ChaosPhases)
	}
	faultIndex := index
	if role == "recovery" {
		faultIndex--
	}
	signature := "unavailable"
	if faultIndex >= 0 {
		chaos := load.ChaosPhases[faultIndex].Chaos
		if chaos != nil {
			signature = testFailureGroupingKey(testRunReceipt{}, &testFailureSummary{FaultEvidence: testFailureFaultRecords(chaos)}).FaultKinds
		}
	}
	if !testDigestFaultCoverage(load.ChaosPhases, index, signature) {
		return false
	}
	if m.Kind == nil {
		return true
	}
	observed, _ := testDigestAssertionCoverage(phase, testFailureGroupingKeyForRequirement(r, m))
	return observed
}
