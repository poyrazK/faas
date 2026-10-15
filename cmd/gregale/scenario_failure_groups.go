package main

import (
	"encoding/json"
	"sort"
)

type testFailureGroupKey struct {
	Scenario   string `json:"scenario"`
	Profile    string `json:"profile"`
	Engine     string `json:"engine"`
	Case       string `json:"case,omitempty"`
	Step       string `json:"step,omitempty"`
	Phase      string `json:"phase,omitempty"`
	Kind       string `json:"kind"`
	Assertion  string `json:"assertion"`
	Request    string `json:"request,omitempty"`
	FaultKinds string `json:"fault_kinds"`
}

type testFailureOccurrence struct {
	RunID   string `json:"run_id,omitempty"`
	Attempt int    `json:"attempt"`
	Failure string `json:"failure"`
	Replay  string `json:"replay_command,omitempty"`
}

type testFailureGroup struct {
	Version       int                     `json:"version"`
	Key           testFailureGroupKey     `json:"key"`
	Failures      int                     `json:"failures"`
	ExecutedRuns  int                     `json:"executed_runs"`
	WorstObserved *float64                `json:"worst_observed,omitempty"`
	WorstLimit    *float64                `json:"worst_limit,omitempty"`
	Operator      string                  `json:"operator,omitempty"`
	Occurrences   []testFailureOccurrence `json:"occurrences"`
}

type testFailureGroupScope struct {
	scenario string
	profile  string
	engine   string
	caseName string
}

// FailureGroups groups each failed terminal receipt's first recorded assertion.
// Tolerated failures remain visible without changing their parent's verdict.
func (r testRunReceipt) FailureGroups() []testFailureGroup {
	if r.FaultMatrix == nil && r.Resilience == nil {
		return nil
	}
	var leaves []testRunReceipt
	collectTestFailureLeaves(r, "", &leaves)
	counts := make(map[testFailureGroupScope]int)
	for _, leaf := range leaves {
		if leaf.Status == "passed" || leaf.Status == "failed" {
			counts[testFailureScope(leaf)]++
		}
	}
	var groups []testFailureGroup
	index := make(map[testFailureGroupKey]int)
	for _, leaf := range leaves {
		summary := leaf.FailureSummary()
		if summary == nil {
			continue
		}
		key := testFailureGroupingKey(leaf, summary)
		i, ok := index[key]
		if !ok {
			i = len(groups)
			index[key] = i
			groups = append(groups, testFailureGroup{Version: 1, Key: key, ExecutedRuns: counts[testFailureScope(leaf)]})
		}
		group := &groups[i]
		group.Failures++
		group.Occurrences = append(group.Occurrences, testFailureOccurrence{RunID: leaf.RunID, Attempt: leaf.Attempt, Failure: summary.Failure, Replay: summary.Replay})
		updateTestFailureWorst(group, summary.Assertion)
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].Failures > groups[j].Failures })
	return groups
}

func testFailureScope(r testRunReceipt) testFailureGroupScope {
	return testFailureGroupScope{scenario: r.Scenario, profile: r.Profile, engine: r.Engine, caseName: r.Case}
}

func collectTestFailureLeaves(r testRunReceipt, inheritedCase string, leaves *[]testRunReceipt) {
	if r.Case == "" {
		r.Case = inheritedCase
	}
	if r.FaultMatrix != nil {
		for _, child := range r.FaultMatrix.Cases {
			collectTestFailureLeaves(child, r.Case, leaves)
		}
		return
	}
	if r.Resilience != nil {
		for _, child := range r.Resilience.History {
			collectTestFailureLeaves(child, r.Case, leaves)
		}
		return
	}
	*leaves = append(*leaves, r)
}

func testFailureGroupingKey(r testRunReceipt, summary *testFailureSummary) testFailureGroupKey {
	key := testFailureGroupKey{Scenario: r.Scenario, Profile: r.Profile, Engine: r.Engine, Case: r.Case, Step: summary.Step, Phase: summary.Phase, Kind: "unclassified", Assertion: summary.Failure}
	if a := summary.Assertion; a != nil {
		key.Kind, key.Assertion, key.Request = a.Kind, a.Name, a.Request
	}
	kinds := make(map[string]bool)
	for _, e := range summary.FaultEvidence {
		kinds[e.Kind] = true
	}
	var ordered []string
	for kind := range kinds {
		ordered = append(ordered, kind)
	}
	sort.Strings(ordered)
	// Canonical JSON avoids delimiter collisions in saved report strings.
	if len(ordered) == 0 {
		key.FaultKinds = "unavailable"
	} else {
		body, _ := json.Marshal(ordered)
		key.FaultKinds = string(body)
	}
	return key
}

func updateTestFailureWorst(group *testFailureGroup, a *testFailureAssertionSummary) {
	if a == nil || a.Observed == nil || (a.Operator != "<=" && a.Operator != ">=") ||
		testPhaseComparisonNumber(*a.Observed) == nil {
		return
	}
	// Opposing comparison directions have no single meaningful worst value.
	if group.Operator == "mixed" {
		return
	}
	if group.Operator != "" && group.Operator != a.Operator {
		group.Operator, group.WorstObserved, group.WorstLimit = "mixed", nil, nil
		return
	}
	group.Operator = a.Operator
	if group.WorstObserved == nil || (a.Operator == "<=" && *a.Observed > *group.WorstObserved) ||
		(a.Operator == ">=" && *a.Observed < *group.WorstObserved) {
		group.WorstObserved = testPhaseComparisonNumber(*a.Observed)
		group.WorstLimit = nil
		if a.Expected != nil {
			group.WorstLimit = testPhaseComparisonNumber(*a.Expected)
		}
	}
}
