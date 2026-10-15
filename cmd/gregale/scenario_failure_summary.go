package main

import (
	"fmt"
	"strings"
)

type testFailureSummary struct {
	phaseSelected bool
	GroupError    string
	RunID         string
	Scope         string
	Failure       string
	Measurements  []string
	Matches       []string
	Recovery      []string
	Cleanup       []string
	Replay        string
}

// FailureSummary describes recorded failures without changing the verdict.
func (r testRunReceipt) FailureSummary() *testFailureSummary {
	if r.Status != "failed" {
		return nil
	}
	failed, cleanup := firstTestFailedReceipt(r)
	summary := &testFailureSummary{RunID: failed.RunID, Scope: failed.Scenario, Failure: failed.Error, Cleanup: cleanup}
	if r.FaultMatrix != nil || r.Resilience != nil {
		summary.GroupError = r.Error
	}
	failedStep := false
	for _, step := range failed.Steps {
		if step.Status != "failed" {
			continue
		}
		failedStep = true
		summary.Scope += " / " + step.Name
		if step.Error != "" {
			summary.Failure = step.Error
		}
		summarizeTestFailedLoad(summary, step.Load)
		if !summary.phaseSelected && len(summary.Matches) == 0 {
			summary.Matches = testFailureMatchSummary(step.Chaos)
		}
		break
	}
	if !failedStep && failed.Load != nil {
		summarizeTestFailedLoad(summary, failed.Load)
	}
	if !failedStep && !summary.phaseSelected && len(summary.Matches) == 0 {
		summary.Matches = testFailureMatchSummary(failed.Chaos)
	}
	if summary.Failure == "" {
		summary.Failure = "No assertion detail recorded; inspect the full receipt."
	}
	summary.Replay = testFailureReplayCommand(failed)
	return summary
}

// Traverse stored execution order. An allowed failed child of a passing group
// does not produce a failure summary, and absent children are never invented.
func firstTestFailedReceipt(r testRunReceipt) (testRunReceipt, []string) {
	var cleanup []string
	if r.CleanupError != "" {
		cleanup = append(cleanup, r.Scenario+": "+r.CleanupError)
	}
	var children []testRunReceipt
	if r.FaultMatrix != nil {
		children = r.FaultMatrix.Cases
	} else if r.Resilience != nil {
		children = r.Resilience.History
	}
	for _, child := range children {
		if child.Status == "failed" {
			failed, childCleanup := firstTestFailedReceipt(child)
			return failed, append(cleanup, childCleanup...)
		}
	}
	return r, cleanup
}

func summarizeTestFailedLoad(summary *testFailureSummary, load *testLoadEvidence) {
	if load == nil {
		return
	}
	rows := testLoadPhaseComparisons(load)
	selected := -1
	for i, row := range rows {
		summary.Recovery = append(summary.Recovery, row.Recovery...)
		if selected < 0 && len(row.Findings) > 0 {
			selected = i
			summary.Scope += " / " + row.Name
			summary.Failure = row.Findings[0]
			summary.Measurements = append(summary.Measurements, fmt.Sprintf("%d requests; p95 %s ms; errors %s%%; request rate %s/s", row.Requests, testPhaseComparisonValue(row.P95MS), testPhaseComparisonValue(row.ErrorRatePercent), testPhaseComparisonValue(row.RequestsPerSecond)))
			summary.Measurements = append(summary.Measurements, row.Attempts...)
		}
	}
	if selected < 0 {
		return
	}
	summary.phaseSelected = true
	phase := load.ChaosPhases[selected]
	// A recovery assertion belongs to its immediately preceding fault.
	if rows[selected].Role == "recovery" && selected > 0 {
		phase = load.ChaosPhases[selected-1]
	}
	summary.Matches = testFailureMatchSummary(phase.Chaos)
}

func testFailureMatchSummary(e *testChaosEvidence) []string {
	if e == nil || len(e.Matches) == 0 {
		return nil
	}
	matches := make([]string, 0, len(e.Matches))
	for _, match := range e.Matches {
		matches = append(matches, fmt.Sprintf("%s %s -> %s: %d matches (minimum %d)", match.Rule.Kind, match.Rule.From, match.Rule.To, match.Matches, match.MinMatches))
	}
	return matches
}

// Report and manifest paths are explicit placeholders because receipts do not
// retain their local filenames. Quote selectors as shell data, never code.
func testFailureReplayCommand(r testRunReceipt) string {
	if r.Status != "failed" || r.Engine != "real-vm" || r.RunID == "" || r.Replay == nil ||
		r.Replay.Scenario != r.Scenario || r.Replay.Profile != r.Profile ||
		r.Replay.Version < 1 || r.Replay.Version > 10 || r.Replay.Version != testFaultReplayVersion(r.Replay) {
		return ""
	}
	quotedID := "'" + strings.ReplaceAll(r.RunID, "'", "'\"'\"'") + "'"
	return "gregale test replay --from '<report.json>' --manifest '<original-manifest.yaml>' --run-id " + quotedID
}
