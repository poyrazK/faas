package main

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) checkProfileRegression(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.profileInvestigationTarget(w, r, acct)
	if !ok {
		return
	}
	if !validProfileInvestigationID(r.PathValue("id")) {
		s.notFound(w, "profiling investigation")
		return
	}
	var req api.CheckProfileRegressionRequest
	if err := decodeJSONSized(r, &req, api.ProfileControlMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid profile regression check"))
		return
	}
	row, problem := s.checkOwnedProfileRegression(r.Context(), acct, app, store, r.PathValue("id"), req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, s.profileInvestigationResponse(r.Context(), acct, app, row))
}

func (s *server) checkOwnedProfileRegression(ctx context.Context, acct state.Account, app state.App, store state.ProfileInvestigationStore, id string, req api.CheckProfileRegressionRequest) (api.ProfileInvestigation, *api.Problem) {
	if req.ExpectedRevision == nil || *req.ExpectedRevision < 1 || *req.ExpectedRevision >= api.ProfileInvestigationMaxRevision {
		return api.ProfileInvestigation{}, api.ErrValidation("expected_revision must name a current saved revision")
	}
	options := api.DefaultProfileRegressionOptions()
	if req.Options != nil {
		options = *req.Options
	}
	if err := profiling.ValidateRegressionOptions(options); err != nil {
		return api.ProfileInvestigation{}, api.ErrValidation(err.Error())
	}
	row, err := store.GetProfileInvestigation(ctx, acct.ID, app.ID, id)
	if err != nil {
		return row, profileInvestigationProblem(err)
	}
	if row.Revision != *req.ExpectedRevision {
		return row, profileInvestigationProblem(state.ErrProfileInvestigationRevision)
	}
	assessment := s.assessOwnedProfileRegression(ctx, acct, app, row, options)
	assessment.RequestMix = s.captureProfileRequestMix(ctx, acct.ID, app.ID, assessment)
	out, err := store.SaveProfileRegressionAssessment(ctx, acct.ID, app.ID, id, row.Revision, assessment)
	if err != nil {
		return out, profileInvestigationProblem(err)
	}
	s.audit.Emit(ctx, "profile_investigation.checked", &acct.ID, map[string]any{"app_id": app.ID, "investigation_id": id, "revision": out.Revision, "status": assessment.Status})
	return out, nil
}

func (s *server) assessOwnedProfileRegression(ctx context.Context, acct state.Account, app state.App, row api.ProfileInvestigation, options api.ProfileRegressionOptions) api.ProfileRegressionAssessment {
	out := profiling.NewRegressionAssessment(row, options, time.Now())
	for _, q := range []api.ProfileQuery{out.Baseline, out.Candidate} {
		status := s.profileInvestigationWindow(ctx, acct, app, q)
		if status.Status != "retained" {
			out.Reason = status.Detail
			return out
		}
	}
	// The pair shares one deadline; each query also uses the existing slots.
	queryCtx, cancel := context.WithTimeout(ctx, api.ProfileQueryTimeout)
	defer cancel()
	a, problem := s.queryAppProfile(queryCtx, acct, app, out.Baseline)
	if problem != nil {
		out.Reason = "Baseline profile query could not complete. Retry when profile history is available."
		return out
	}
	out.Attribution = profiling.CompareAttribution(a, api.ProfileResponse{})
	b, problem := s.queryAppProfile(queryCtx, acct, app, out.Candidate)
	if problem != nil {
		out.BaselineCoverage = a.Coverage
		out.Reason = "Candidate profile query could not complete. Retry when profile history is available."
		return out
	}
	s.attachSelectedRouteRequests(queryCtx, acct, app, out.Options.Routes, &a, &b)
	profiling.AttachRouteLabelCoverage(&a)
	profiling.AttachRouteLabelCoverage(&b)
	out.RouteChecks = profiling.AssessRouteRegressions(out.Options, a, b)
	out = s.assessProfileRegression(queryCtx, acct, app, out, a, b)
	s.attachRouteCodeEvidence(queryCtx, acct, app, &out)
	return out
}
