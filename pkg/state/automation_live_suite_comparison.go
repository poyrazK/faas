package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// AutomationLiveSuiteComparisonStore observes both suites in one snapshot. It
// never checks assertions, admits workflows, or changes suite metadata.
type AutomationLiveSuiteComparisonStore interface {
	CompareAutomationLiveSuites(context.Context, string, string, string, string) (api.AutomationLiveSuiteComparison, error)
}

func liveSuiteComparisonIDs(candidate, baseline string) (string, string, error) {
	c, err := uuid.Parse(candidate)
	if err != nil || c == uuid.Nil {
		return "", "", ErrNotFound
	}
	b, err := uuid.Parse(baseline)
	if err != nil || b == uuid.Nil {
		return "", "", ErrNotFound
	}
	return c.String(), b.String(), nil
}

func compareLiveSuites(baseline, candidate api.AutomationLiveSuite) api.AutomationLiveSuiteComparison {
	result := api.AutomationLiveSuiteComparison{Baseline: baseline.AutomationLiveSuiteSummary, Candidate: candidate.AutomationLiveSuiteSummary,
		BaselineOutdated: baseline.Outdated, CandidateOutdated: candidate.Outdated, ObservedAt: time.Now().UTC(), Cases: []api.AutomationLiveSuiteCaseComparison{}}
	before := make(map[string]api.AutomationLiveSuiteCase, len(baseline.Cases))
	after := make(map[string]api.AutomationLiveSuiteCase, len(candidate.Cases))
	for _, c := range baseline.Cases {
		before[c.Name] = c
	}
	for _, c := range candidate.Cases {
		after[c.Name] = c
		prior, exists := before[c.Name]
		entry := api.AutomationLiveSuiteCaseComparison{Name: c.Name, Candidate: liveSuiteObservation(c), Change: "added"}
		if exists {
			entry.Baseline = liveSuiteObservation(prior)
			entry.Change = liveSuiteChange(prior, c, entry.Baseline.Result, entry.Candidate.Result)
		}
		result.Cases = append(result.Cases, entry)
	}
	// Candidate order first, then removed cases in their original baseline order.
	for _, c := range baseline.Cases {
		if _, exists := after[c.Name]; !exists {
			result.Cases = append(result.Cases, api.AutomationLiveSuiteCaseComparison{Name: c.Name, Baseline: liveSuiteObservation(c), Change: "removed"})
		}
	}
	for _, entry := range result.Cases {
		result.Summary.Total++
		switch entry.Change {
		case "regression":
			result.Summary.Regressions++
		case "recovery":
			result.Summary.Recoveries++
		case "unchanged":
			result.Summary.Unchanged++
		case "added":
			result.Summary.Added++
		case "removed":
			result.Summary.Removed++
		case "incompatible":
			result.Summary.Incompatible++
		default:
			result.Summary.Unknown++
		}
	}
	return result
}

func liveSuiteObservation(c api.AutomationLiveSuiteCase) *api.AutomationLiveSuiteObservation {
	result := &api.AutomationLiveSuiteObservation{RunID: c.RunID, Result: "unavailable"}
	if c.RunID != "" && c.Verification == nil {
		return result
	}
	if c.Verification != nil && !c.Verification.CreatedAt.IsZero() {
		created := c.Verification.CreatedAt
		result.CreatedAt = &created
	}
	switch {
	case c.RunID == "":
		result.Result = "not_started"
	case !liveRunTerminal(c.Verification.RunStatus):
		result.Result = "pending"
	case c.Verification.ReportCurrent && c.Verification.Report != nil:
		result.Result = "failed"
		if c.Verification.Report.Passed {
			result.Result = "passed"
		}
	}
	return result
}

func liveSuiteChange(before, after api.AutomationLiveSuiteCase, beforeResult, afterResult string) string {
	if before.Verification == nil || after.Verification == nil || before.Verification.InputsHash == "" || after.Verification.InputsHash == "" {
		return "unknown"
	}
	// This hash binds the frozen published definition, input and assertions. A
	// matching name or revision alone cannot establish comparable evidence.
	if before.Verification.InputsHash != after.Verification.InputsHash {
		return "incompatible"
	}
	if (beforeResult != "passed" && beforeResult != "failed") || (afterResult != "passed" && afterResult != "failed") {
		return "unknown"
	}
	if beforeResult == afterResult {
		return "unchanged"
	}
	if beforeResult == "passed" {
		return "regression"
	}
	return "recovery"
}
