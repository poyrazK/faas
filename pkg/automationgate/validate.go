package automationgate

import (
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

func validateComparison(s Selection, view api.AutomationLiveSuiteComparison) error {
	invalid := errors.New("comparison response is incomplete or inconsistent for the requested suites")
	if !validSummary(view.Baseline, s.Name, s.BaselineID) || !validSummary(view.Candidate, s.Name, s.CandidateID) || view.ObservedAt.IsZero() || len(view.Cases) == 0 || len(view.Cases) > api.AutomationLiveSuiteComparisonMaxCases {
		return invalid
	}
	seen := map[string]bool{}
	var counts api.AutomationLiveSuiteComparisonCounts
	var before, after, beforeLinked, afterLinked int
	for _, entry := range view.Cases {
		if !validName(entry.Name, api.AutomationTestCaseNameMaxBytes) || seen[entry.Name] || !validObservation(entry.Baseline) || !validObservation(entry.Candidate) || !validChange(entry) {
			return invalid
		}
		seen[entry.Name] = true
		if entry.Baseline != nil {
			before++
			if entry.Baseline.RunID != "" {
				beforeLinked++
			}
		}
		if entry.Candidate != nil {
			after++
			if entry.Candidate.RunID != "" {
				afterLinked++
			}
		}
		counts.Total++
		switch entry.Change {
		case "regression":
			counts.Regressions++
		case "recovery":
			counts.Recoveries++
		case "unchanged":
			counts.Unchanged++
		case "added":
			counts.Added++
		case "removed":
			counts.Removed++
		case "incompatible":
			counts.Incompatible++
		case "unknown":
			counts.Unknown++
		}
	}
	if counts != view.Summary || before != view.Baseline.CaseCount || after != view.Candidate.CaseCount || beforeLinked != view.Baseline.LinkedRuns || afterLinked != view.Candidate.LinkedRuns {
		return invalid
	}
	return nil
}

func validSummary(summary api.AutomationLiveSuiteSummary, name, id string) bool {
	return sameID(summary.ID, id) && summary.Name == name && summary.Version > 0 && summary.PublishedVersion > 0 && summary.PublishedVersion <= summary.Version && validID(summary.DeploymentID) &&
		!summary.CreatedAt.IsZero() && !summary.UpdatedAt.Before(summary.CreatedAt) && summary.CaseCount > 0 && summary.CaseCount <= api.AutomationLiveSuiteMaxCases && summary.LinkedRuns >= 0 && summary.LinkedRuns <= summary.CaseCount
}

func validObservation(side *api.AutomationLiveSuiteObservation) bool {
	if side == nil {
		return true
	}
	if side.Result == "not_started" {
		return side.RunID == ""
	}
	if !validID(side.RunID) {
		return false
	}
	switch side.Result {
	case "passed", "failed", "pending", "unavailable":
		return true
	default:
		return false
	}
}

func validChange(entry api.AutomationLiveSuiteCaseComparison) bool {
	if entry.Change == "added" {
		return entry.Baseline == nil && entry.Candidate != nil
	}
	if entry.Change == "removed" {
		return entry.Baseline != nil && entry.Candidate == nil
	}
	if entry.Baseline == nil || entry.Candidate == nil {
		return false
	}
	before, after := entry.Baseline.Result, entry.Candidate.Result
	switch entry.Change {
	case "regression":
		return before == "passed" && after == "failed"
	case "recovery":
		return before == "failed" && after == "passed"
	case "unchanged":
		return before == after && (before == "passed" || before == "failed")
	case "incompatible":
		return entry.Baseline.RunID != "" && entry.Candidate.RunID != ""
	case "unknown":
		return true
	default:
		return false
	}
}
