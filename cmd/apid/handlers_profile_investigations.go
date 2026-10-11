package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) profileInvestigationTarget(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.ProfileInvestigationStore, bool) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return state.App{}, nil, false
	}
	store, ok := s.store.(state.ProfileInvestigationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("saved profiling investigations are unavailable"))
	}
	return app, store, ok
}

func validProfileInvestigationID(id string) bool {
	value, err := uuid.Parse(id)
	return err == nil && value.String() == id
}

func (s *server) listProfileInvestigations(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.profileInvestigationTarget(w, r, acct)
	if !ok {
		return
	}
	rows, err := store.ListProfileInvestigations(r.Context(), acct.ID, app.ID)
	if err != nil {
		s.profileInvestigationError(w, err)
		return
	}
	out := api.ListProfileInvestigationsResponse{Investigations: make([]api.ProfileInvestigationResponse, 0, len(rows))}
	for _, row := range rows {
		out.Investigations = append(out.Investigations, s.profileInvestigationResponse(r.Context(), acct, app, row))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getProfileInvestigation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.profileInvestigationTarget(w, r, acct)
	if !ok {
		return
	}
	if !validProfileInvestigationID(r.PathValue("id")) {
		s.notFound(w, "profiling investigation")
		return
	}
	row, err := store.GetProfileInvestigation(r.Context(), acct.ID, app.ID, r.PathValue("id"))
	if err != nil {
		s.profileInvestigationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.profileInvestigationResponse(r.Context(), acct, app, row))
}

func (s *server) saveProfileInvestigation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.profileInvestigationTarget(w, r, acct)
	if !ok {
		return
	}
	var req api.SaveProfileInvestigationRequest
	if err := decodeJSONSized(r, &req, api.ProfileInvestigationMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid saved investigation request"))
		return
	}
	id := r.PathValue("id")
	if id != "" && !validProfileInvestigationID(id) {
		s.notFound(w, "profiling investigation")
		return
	}
	row, problem := s.saveOwnedProfileInvestigation(r.Context(), acct, app, store, id, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.audit.Emit(r.Context(), "profile_investigation.saved", &acct.ID, map[string]any{"app_id": app.ID, "investigation_id": row.ID, "revision": row.Revision})
	status := http.StatusOK
	if id == "" {
		status = http.StatusCreated
	}
	writeJSON(w, status, s.profileInvestigationResponse(r.Context(), acct, app, row))
}

func (s *server) saveOwnedProfileInvestigation(ctx context.Context, acct state.Account, app state.App, store state.ProfileInvestigationStore, id string, req api.SaveProfileInvestigationRequest) (api.ProfileInvestigation, *api.Problem) {
	if err := state.ValidateProfileInvestigation(req); err != nil {
		return api.ProfileInvestigation{}, api.ErrValidation(err.Error())
	}
	previous := api.ProfileInvestigation{}
	if id != "" {
		var err error
		previous, err = store.GetProfileInvestigation(ctx, acct.ID, app.ID, id)
		if err != nil {
			return previous, profileInvestigationProblem(err)
		}
	}
	for _, q := range []api.ProfileQuery{req.Investigation.Baseline, req.Investigation.Candidate} {
		// Commentary remains editable after expiry or a plan downgrade.
		if profileSelectionEqual(q, previous.Investigation.Baseline) || profileSelectionEqual(q, previous.Investigation.Candidate) {
			continue
		}
		if _, problem := s.profileQueryDeployment(ctx, acct, app, q); problem != nil {
			return api.ProfileInvestigation{}, problem
		}
	}
	row, err := store.SaveProfileInvestigation(ctx, acct.ID, app.ID, id, req)
	if err != nil {
		return row, profileInvestigationProblem(err)
	}
	return row, nil
}

func profileSelectionEqual(a, b api.ProfileQuery) bool {
	return a.Route == b.Route && a.DeploymentID == b.DeploymentID && a.Runtime == b.Runtime && a.Start.Equal(b.Start) && a.End.Equal(b.End)
}

func (s *server) deleteProfileInvestigation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.profileInvestigationTarget(w, r, acct)
	if !ok {
		return
	}
	if !validProfileInvestigationID(r.PathValue("id")) {
		s.notFound(w, "profiling investigation")
		return
	}
	revision, err := strconv.ParseInt(r.URL.Query().Get("expected_revision"), 10, 64)
	if err != nil || revision < 1 || revision > api.ProfileInvestigationMaxRevision {
		api.WriteProblem(w, api.ErrValidation("expected_revision must name the current saved revision"))
		return
	}
	if err = store.DeleteProfileInvestigation(r.Context(), acct.ID, app.ID, r.PathValue("id"), revision); err != nil {
		s.profileInvestigationError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "profile_investigation.deleted", &acct.ID, map[string]any{"app_id": app.ID, "investigation_id": r.PathValue("id")})
	w.WriteHeader(http.StatusNoContent)
}

func profileInvestigationProblem(err error) *api.Problem {
	switch {
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Investigation not found", "app, investigation or selected deployment is unavailable")
	case errors.Is(err, state.ErrProfileInvestigationRevision):
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Investigation changed", "Reload the current revision before saving or deleting.")
	case errors.Is(err, state.ErrProfileInvestigationQuota):
		return api.NewProblem(http.StatusTooManyRequests, api.CodeProfileInvestigationLimit, "Saved investigation limit reached", "Delete an investigation before creating another.").WithLimit(api.ProfileInvestigationMaxPerApp, api.ProfileInvestigationMaxPerApp+1).WithDocs("https://gregale.dev/docs/profiling")
	default:
		return api.ErrCapacity("saved investigation could not be read or updated")
	}
}

func (s *server) profileInvestigationError(w http.ResponseWriter, err error) {
	api.WriteProblem(w, profileInvestigationProblem(err))
}

func (s *server) profileInvestigationResponse(ctx context.Context, acct state.Account, app state.App, row api.ProfileInvestigation) api.ProfileInvestigationResponse {
	if row.Assessment != nil {
		assessment := *row.Assessment
		assessment.RouteChecks = s.enrichRouteCodeSources(ctx, app, assessment.Baseline, assessment.Candidate, assessment.RouteChecks)
		assessment.RouteChecks = linkedProfileRouteChecks(app.Slug, assessment.Baseline, assessment.Candidate, assessment.RouteChecks)
		row.Assessment = &assessment
	}
	return api.ProfileInvestigationResponse{Saved: row, URL: "/dashboard/apps/" + url.PathEscape(app.Slug) + "/profiles?investigation_id=" + row.ID,
		BaselineStatus: s.profileInvestigationWindow(ctx, acct, app, row.Investigation.Baseline), CandidateStatus: s.profileInvestigationWindow(ctx, acct, app, row.Investigation.Candidate)}
}

func (s *server) profileInvestigationWindow(ctx context.Context, acct state.Account, app state.App, q api.ProfileQuery) api.ProfileInvestigationWindowStatus {
	limits := api.MustLimitsFor(acct.Plan).Profiling
	if !limits.Enabled {
		return api.ProfileInvestigationWindowStatus{Status: "plan_unavailable", Detail: "Your plan does not include profile history. Saved notes remain available."}
	}
	if err := profiling.ValidateQuery(q, time.Now(), limits.RetentionDays); err != nil {
		return api.ProfileInvestigationWindowStatus{Status: "expired", Detail: "This profile window has expired under your current plan's retention policy. Saved notes remain available."}
	}
	dep, err := s.store.DeploymentByID(ctx, q.DeploymentID)
	if err != nil || dep.AppID != app.ID {
		return api.ProfileInvestigationWindowStatus{Status: "deployment_unavailable", Detail: "The selected deployment is unavailable. Saved notes remain available."}
	}
	if s.profileBackend == nil {
		return api.ProfileInvestigationWindowStatus{Status: "backend_unavailable", Detail: "CPU profiling is unavailable on this installation. Saved notes remain available."}
	}
	return api.ProfileInvestigationWindowStatus{Status: "retained", Detail: "This window is within profile history; samples may be absent."}
}
