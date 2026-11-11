package storedworkflowconsole

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func (s *Service) inspectLiveSuiteComparison(r *http.Request, scope Scope, data *dashboard.StoredWorkflowConsoleData) {
	id := r.URL.Query().Get("baseline")
	if id == "" {
		return
	}
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil {
		data.LiveSuiteComparisonError = "Choose a retained baseline suite for this stored workflow."
		return
	}
	data.LiveSuiteBaselineID = parsed.String()
	var comparison api.StoredWorkflowLiveSuiteComparison
	op := Operation{Kind: "live-suite-compare", Name: data.Name, RunID: data.LiveSuiteID, BaselineID: parsed.String()}
	if !read(s.Host.Request(r, scope, op), &comparison) || comparison.Candidate.ID != data.LiveSuiteID || comparison.Baseline.ID != parsed.String() || comparison.Candidate.Name != data.Name || comparison.Baseline.Name != data.Name {
		data.LiveSuiteComparisonError = "The comparison is unavailable for these retained suites."
		return
	}
	data.LiveSuiteComparison = &comparison
}
