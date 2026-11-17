package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"
)

// StoredWorkflowLiveSuiteComparison is a read-only observation of two retained
// suites. Historical targets may be outdated without invalidating their reports.
type StoredWorkflowLiveSuiteComparison struct {
	Baseline          StoredWorkflowLiveSuiteSummary          `json:"baseline"`
	Candidate         StoredWorkflowLiveSuiteSummary          `json:"candidate"`
	BaselineOutdated  bool                                    `json:"baseline_outdated"`
	CandidateOutdated bool                                    `json:"candidate_outdated"`
	ObservedAt        time.Time                               `json:"observed_at"`
	Summary           StoredWorkflowLiveSuiteComparisonCounts `json:"summary"`
	Cases             []StoredWorkflowLiveSuiteCaseComparison `json:"cases"`
}

type StoredWorkflowLiveSuiteComparisonCounts struct {
	Total        int `json:"total"`
	Regressions  int `json:"regressions"`
	Recoveries   int `json:"recoveries"`
	Unchanged    int `json:"unchanged"`
	Added        int `json:"added"`
	Removed      int `json:"removed"`
	Incompatible int `json:"incompatible"`
	Unknown      int `json:"unknown"`
}

type StoredWorkflowLiveSuiteCaseComparison struct {
	Name      string                              `json:"name"`
	Change    string                              `json:"change"`
	Baseline  *StoredWorkflowLiveSuiteObservation `json:"baseline,omitempty"`
	Candidate *StoredWorkflowLiveSuiteObservation `json:"candidate,omitempty"`
}

// Result describes the original linked run, independently of target drift.
// An absent side means the case was not selected in that suite.
type StoredWorkflowLiveSuiteObservation struct {
	RunID     string     `json:"run_id,omitempty"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	Result    string     `json:"result"`
}

func (c *Client) CompareStoredWorkflowLiveSuites(ctx context.Context, slug, name, suiteID, baselineID string) (StoredWorkflowLiveSuiteComparison, error) {
	var out struct {
		StoredWorkflowLiveSuiteComparison
		BaselineOutdated  *bool `json:"baseline_outdated"`
		CandidateOutdated *bool `json:"candidate_outdated"`
	}
	err := c.do(ctx, http.MethodGet, storedWorkflowLiveSuitesPath(slug, name)+"/"+url.PathEscape(suiteID)+"/compare/"+url.PathEscape(baselineID), nil, &out)
	if err != nil {
		return StoredWorkflowLiveSuiteComparison{}, err
	}
	// Freshness must be explicit. Missing/null booleans cannot become false and
	// accidentally certify an unobserved current target in a downstream CI gate.
	if out.BaselineOutdated == nil || out.CandidateOutdated == nil {
		return StoredWorkflowLiveSuiteComparison{}, errors.New("comparison response omitted required freshness observations")
	}
	view := out.StoredWorkflowLiveSuiteComparison
	view.BaselineOutdated, view.CandidateOutdated = *out.BaselineOutdated, *out.CandidateOutdated
	return view, nil
}
