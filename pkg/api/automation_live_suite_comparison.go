package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// AutomationLiveSuiteComparison is a read-only observation of two retained
// suites. Historical targets may be outdated without invalidating their reports.
type AutomationLiveSuiteComparison struct {
	Baseline          AutomationLiveSuiteSummary          `json:"baseline"`
	Candidate         AutomationLiveSuiteSummary          `json:"candidate"`
	BaselineOutdated  bool                                `json:"baseline_outdated"`
	CandidateOutdated bool                                `json:"candidate_outdated"`
	ObservedAt        time.Time                           `json:"observed_at"`
	Summary           AutomationLiveSuiteComparisonCounts `json:"summary"`
	Cases             []AutomationLiveSuiteCaseComparison `json:"cases"`
}

type AutomationLiveSuiteComparisonCounts struct {
	Total        int `json:"total"`
	Regressions  int `json:"regressions"`
	Recoveries   int `json:"recoveries"`
	Unchanged    int `json:"unchanged"`
	Added        int `json:"added"`
	Removed      int `json:"removed"`
	Incompatible int `json:"incompatible"`
	Unknown      int `json:"unknown"`
}

type AutomationLiveSuiteCaseComparison struct {
	Name      string                          `json:"name"`
	Change    string                          `json:"change"`
	Baseline  *AutomationLiveSuiteObservation `json:"baseline,omitempty"`
	Candidate *AutomationLiveSuiteObservation `json:"candidate,omitempty"`
}

// Result describes the original linked run, independently of target drift.
// An absent side means the case was not selected in that suite.
type AutomationLiveSuiteObservation struct {
	RunID  string `json:"run_id,omitempty"`
	Result string `json:"result"`
}

func (c *Client) CompareAutomationLiveSuites(ctx context.Context, slug, name, suiteID, baselineID string) (AutomationLiveSuiteComparison, error) {
	var out AutomationLiveSuiteComparison
	err := c.do(ctx, http.MethodGet, automationLiveSuitesPath(slug, name)+"/"+url.PathEscape(suiteID)+"/compare/"+url.PathEscape(baselineID), nil, &out)
	return out, err
}
