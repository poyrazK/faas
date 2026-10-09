package main

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) attachCanaryAssessment(r *http.Request, acct state.Account, app state.App, req *api.SaveProfileInvestigationRequest) error {
	v := r.PostForm
	if !v.Has("canary_deployment") {
		return nil
	}
	store, ok := s.store.(state.ProfileCanaryCheckStore)
	step, e1 := strconv.Atoi(v.Get("canary_step"))
	revision, e2 := strconv.ParseInt(v.Get("canary_revision"), 10, 64)
	started, e3 := time.Parse(time.RFC3339Nano, v.Get("canary_started_at"))
	if !ok || e1 != nil || e2 != nil || e3 != nil || step < 0 || revision <= 0 {
		return errors.New("invalid canary reference")
	}
	check, err := store.GetProfileCanaryCheck(r.Context(), acct.ID, app.ID, state.ProfileCanaryCheckKey{DeploymentID: v.Get("canary_deployment"), CanaryStep: step, CanaryStepStartedAt: started, PolicyRevision: revision})
	if err != nil || check.CompletedAt == nil || check.CheckedAt == nil || check.Baseline == nil || check.Candidate == nil || !profileSelectionEqual(*check.Baseline, req.Investigation.Baseline) || !profileSelectionEqual(*check.Candidate, req.Investigation.Candidate) {
		return errors.New("canary assessment does not match")
	}
	req.InitialAssessment = &api.ProfileRegressionAssessment{InvestigationRevision: *req.ExpectedRevision + 1, CheckedAt: *check.CheckedAt, Status: check.Status, Reason: check.Reason, Options: check.Options, Baseline: *check.Baseline, Candidate: *check.Candidate, BaselineRequests: check.BaselineRequests, CandidateRequests: check.CandidateRequests, BaselineCoverage: check.BaselineCoverage, CandidateCoverage: check.CandidateCoverage, Total: check.Total, Evidence: check.Evidence, UncomparableEntries: check.UncomparableEntries, RequestMix: check.RequestMix, RouteChecks: check.RouteChecks, Attribution: check.Attribution}
	return nil
}
