package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const profileInvestigationCSRFAction = "profile_investigation"
const profileInvestigationCSRFCookie = "faas_csrf_profile_investigation"

func (s *server) savedProfilePage(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App) (profilePageData, *http.Request, bool) {
	view := &dashboard.ProfileInvestigationsView{TitleMaxBytes: api.ProfileInvestigationMaxTitleBytes, TextMaxBytes: api.ProfileInvestigationMaxTextBytes}
	store, ok := s.store.(state.ProfileInvestigationStore)
	if !ok {
		data := s.profilePageData(r, acct, app)
		data.Investigations = view
		view.Error = "Saved investigations are unavailable."
		return data, r, true
	}
	rows, err := store.ListProfileInvestigations(r.Context(), acct.ID, app.ID)
	if err != nil {
		view.Error = "Saved investigations could not be listed."
	}
	for _, row := range rows {
		view.Items = append(view.Items, s.profileInvestigationResponse(r.Context(), acct, app, row))
	}
	if id := r.URL.Query().Get("investigation_id"); id != "" {
		if !validProfileInvestigationID(id) {
			http.NotFound(w, r)
			return profilePageData{}, r, false
		}
		row, err := store.GetProfileInvestigation(r.Context(), acct.ID, app.ID, id)
		if err != nil {
			s.profileInvestigationError(w, err)
			return profilePageData{}, r, false
		}
		out := s.profileInvestigationResponse(r.Context(), acct, app, row)
		view.Saved = &out
		if row.Assessment != nil && row.Assessment.RequestMix != nil {
			mix := dashboard.ProfileRequestMixSnapshotView(row.Assessment.RequestMix, api.NormalizeProfileRegressionOptions(row.Assessment.Options).MinimumRequests)
			linkProfileMixSnapshot(&mix, row.Assessment.RequestMix, app.Slug, acct.Plan)
			view.RequestMix = &mix
		}
		view.SelectedPath = row.Investigation.SelectedPath
		path, _ := json.Marshal(view.SelectedPath)
		view.SelectedPathJSON = string(path)
		r = profileInvestigationRequest(r, row.Investigation)
	}
	data := s.savedProfileData(r, acct, app, view)
	s.includeSavedProfileDeployments(r, app, &data)
	view.CanSave = data.Baseline.DeploymentID != "" && data.Query.DeploymentID != "" && (api.MustLimitsFor(acct.Plan).Profiling.Enabled || view.Saved != nil)
	if view.CanSave && s.sessions != nil {
		token, err := middleware.IssueForAuthenticatedNamed(s.sessions, profileInvestigationCSRFAction, acct.ID, profileInvestigationCSRFCookie)
		if err != nil {
			view.Error = "Saving is temporarily unavailable. Reload to try again."
			view.CanSave = false
		} else {
			view.CSRF = token
			// #nosec G124 -- configured production domains use Secure; empty domain supports local HTTP development.
			http.SetCookie(w, &http.Cookie{Name: profileInvestigationCSRFCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
		}
	} else {
		view.CanSave = false
	}
	return data, r, true
}

// Older saved selections can fall outside the bounded recent-deployment list.
func (s *server) includeSavedProfileDeployments(r *http.Request, app state.App, data *profilePageData) {
	for _, id := range []string{data.Query.DeploymentID, data.Baseline.DeploymentID} {
		if id == "" {
			continue
		}
		found := false
		for _, dep := range data.Deployments {
			if dep.ID == id {
				found = true
				break
			}
		}
		if found {
			continue
		}
		dep, err := s.store.DeploymentByID(r.Context(), id)
		if err != nil || dep.AppID != app.ID {
			continue
		}
		data.Deployments = append([]state.Deployment{dep}, data.Deployments...)
		if len(data.Deployments) > api.ProfileMaxDeploymentChoices {
			data.Deployments = data.Deployments[:api.ProfileMaxDeploymentChoices]
		}
	}
}

func profileInvestigationRequest(r *http.Request, in api.ProfileInvestigationInput) *http.Request {
	clone := r.Clone(r.Context())
	u := *r.URL
	clone.URL = &u
	// Saved route selections remain scoped to the same route on both sides.
	v := url.Values{"deployment_id": {in.Candidate.DeploymentID}, "runtime": {in.Candidate.Runtime}, "start": {in.Candidate.Start.Format(time.RFC3339Nano)}, "end": {in.Candidate.End.Format(time.RFC3339Nano)}, "baseline_id": {in.Baseline.DeploymentID}, "baseline_start": {in.Baseline.Start.Format(time.RFC3339Nano)}, "baseline_end": {in.Baseline.End.Format(time.RFC3339Nano)}}
	clone.URL.RawQuery = v.Encode()
	if in.Candidate.Route != "" {
		v.Set("route", in.Candidate.Route)
		clone.URL.RawQuery = v.Encode()
	}
	return clone
}

func (s *server) savedProfileData(r *http.Request, acct state.Account, app state.App, view *dashboard.ProfileInvestigationsView) profilePageData {
	if saved := view.Saved; saved != nil && (saved.BaselineStatus.Status != "retained" || saved.CandidateStatus.Status != "retained") {
		data := profilePageData{AppSlug: app.Slug, Query: saved.Saved.Investigation.Candidate, Baseline: saved.Saved.Investigation.Baseline, Investigations: view}
		data.Deployments, _ = s.store.ListDeploymentsForApp(r.Context(), app.ID, api.ProfileMaxDeploymentChoices, 0)
		data.Error = "Baseline: " + saved.BaselineStatus.Detail + " Candidate: " + saved.CandidateStatus.Detail
		return data
	}
	data := s.profilePageData(r, acct, app)
	data.Investigations = view
	return data
}

func (s *server) dashboardSaveProfileInvestigation(w http.ResponseWriter, r *http.Request) {
	acct, app, store, ok := s.dashboardProfileInvestigationTarget(w, r)
	if !ok {
		return
	}
	id := r.PostForm.Get("id")
	if id != "" && !validProfileInvestigationID(id) {
		http.NotFound(w, r)
		return
	}
	revision, err := strconv.ParseInt(r.PostForm.Get("expected_revision"), 10, 64)
	if err != nil || revision < 0 || revision > api.ProfileInvestigationMaxRevision {
		api.WriteProblem(w, api.ErrValidation("invalid saved revision"))
		return
	}
	if r.PostForm.Get("action") == "delete" {
		s.dashboardDeleteProfileInvestigation(w, r, acct, app, store, id, revision)
		return
	}
	req, err := profileInvestigationForm(r.PostForm, revision)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid investigation selection or call path"))
		return
	}
	if err := s.attachCanaryAssessment(r, acct, app, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("the canary assessment is unavailable or does not match these selections"))
		return
	}
	row, problem := s.saveOwnedProfileInvestigation(r.Context(), acct, app, store, id, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.audit.Emit(r.Context(), "profile_investigation.saved", &acct.ID, map[string]any{"app_id": app.ID, "investigation_id": row.ID, "revision": row.Revision})
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(app.Slug)+"/profiles?investigation_id="+row.ID, http.StatusSeeOther)
}

func profileInvestigationForm(v url.Values, revision int64) (api.SaveProfileInvestigationRequest, error) {
	q, err := parseProfileQuery(v)
	if err != nil {
		return api.SaveProfileInvestigationRequest{}, err
	}
	b := url.Values{"route": {v.Get("route")}, "deployment_id": {v.Get("baseline_id")}, "runtime": {v.Get("runtime")}, "start": {v.Get("baseline_start")}, "end": {v.Get("baseline_end")}}
	baseline, err := parseProfileQuery(b)
	if err != nil {
		return api.SaveProfileInvestigationRequest{}, err
	}
	in := api.ProfileInvestigationInput{Title: v.Get("title"), Findings: v.Get("findings"), Notes: v.Get("notes"), Baseline: baseline, Candidate: q}
	if raw := v.Get("selected_path"); raw != "" && raw != "null" {
		err = json.Unmarshal([]byte(raw), &in.SelectedPath)
	}
	return api.SaveProfileInvestigationRequest{ExpectedRevision: &revision, Investigation: in}, err
}

func (s *server) dashboardDeleteProfileInvestigation(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, store state.ProfileInvestigationStore, id string, revision int64) {
	if err := store.DeleteProfileInvestigation(r.Context(), acct.ID, app.ID, id, revision); err != nil {
		s.profileInvestigationError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "profile_investigation.deleted", &acct.ID, map[string]any{"app_id": app.ID, "investigation_id": id})
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(app.Slug)+"/profiles", http.StatusSeeOther)
}

func (s *server) dashboardProfileInvestigationTarget(w http.ResponseWriter, r *http.Request) (acct state.Account, app state.App, store state.ProfileInvestigationStore, ok bool) {
	var err error
	acct, ok = AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		ok = false
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, api.ProfileInvestigationMaxFormBytes)
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid investigation form"))
		ok = false
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, profileInvestigationCSRFAction, acct.ID, profileInvestigationCSRFCookie); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid CSRF token; reload the page before saving"))
		ok = false
		return
	}
	app, err = s.store.AppBySlug(r.Context(), r.PathValue("slug"))
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		ok = false
		return
	}
	store, ok = s.store.(state.ProfileInvestigationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("saved investigations are unavailable"))
		ok = false
		return
	}
	return acct, app, store, true
}
