package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const dashboardEnvironmentGitOpsAction = "environment_gitops"
const dashboardEnvironmentGitOpsCookie = "faas_gitops_csrf"

func (s *server) dashboardEnvironmentGitOps(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	s.renderDashboardEnvironmentGitOps(w, r, acct, nil)
}

func (s *server) environmentGitOpsDashboardData(r *http.Request, acct state.Account) (dashboard.EnvironmentGitOpsData, error) {
	data := dashboard.EnvironmentGitOpsData{Project: r.PathValue("slug"), Environment: r.PathValue("environment")}
	if !api.ValidProjectSlug(data.Project) || !api.ValidProjectEnvironmentSlug(data.Environment) {
		return data, state.ErrNotFound
	}
	if _, _, _, problem := s.loadProjectEnvironmentConfig(r.Context(), acct, data.Project, data.Environment); problem != nil {
		if problem.Status == http.StatusNotFound {
			return data, state.ErrNotFound
		}
		return data, errors.New("environment scope unavailable")
	}
	source, problem := s.environmentGitSourceForRequest(r, acct)
	if problem != nil {
		if problem.Status == http.StatusNotFound {
			return data, nil
		}
		return data, errors.New("environment Git source unavailable")
	}
	runs, err := s.store.(state.EnvironmentGitOpsStore).ListEnvironmentGitOpsRuns(r.Context(), acct.ID, source.ID, 20)
	if err != nil {
		return data, err
	}
	data.Status = &api.EnvironmentGitOpsStatusResponse{Source: source, Runs: runs}
	data.Status.Approval, err = s.environmentGitApprovalForSource(r.Context(), source)
	if err != nil {
		return data, err
	}
	setEnvironmentGitOpsDashboardFreshness(&data, time.Now().UTC())
	for _, run := range runs {
		view := dashboard.EnvironmentGitOpsRunView{Run: run}
		var plan api.EnvironmentGitOpsPlan
		if json.Unmarshal(run.Plan, &plan) == nil && plan.Hash != "" {
			view.Plan = &plan
		}
		data.Runs = append(data.Runs, view)
	}
	if source.ApprovedRevisionID != "" && !source.Suspended {
		store, ok := s.store.(state.EnvironmentGitOpsIntentStore)
		if !ok {
			return data, errors.New("environment intent storage unavailable")
		}
		plan, err := store.PreviewEnvironmentGitOpsAdoption(r.Context(), acct.ID, source.ID)
		if err != nil {
			return data, err
		}
		data.Adoption = &plan
	}
	return data, nil
}

func setEnvironmentGitOpsDashboardFreshness(data *dashboard.EnvironmentGitOpsData, now time.Time) {
	data.SourcePollStale, data.SourceVerificationStale = false, false
	if data.Status == nil || data.Status.Source.Suspended {
		return
	}
	source := data.Status.Source
	checked, verified := source.CreatedAt, source.CreatedAt
	if source.SourceCheckedAt != nil {
		checked = *source.SourceCheckedAt
	}
	if source.SourceVerifiedAt != nil {
		verified = *source.SourceVerifiedAt
	}
	cutoff := now.Add(-api.EnvironmentGitSourceStaleAfter)
	data.SourcePollStale, data.SourceVerificationStale = !checked.After(cutoff), !verified.After(cutoff)
}

func (s *server) renderDashboardEnvironmentGitOps(w http.ResponseWriter, r *http.Request, acct state.Account, review *api.PreviewEnvironmentGitRevisionResponse) {
	data, err := s.environmentGitOpsDashboardData(r, acct)
	if errors.Is(err, state.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("Could not read environment GitOps status."))
		return
	}
	token, err := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardEnvironmentGitOpsAction, acct.ID, dashboardEnvironmentGitOpsCookie)
	if err != nil {
		renderProblem(w, s.log, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: dashboardEnvironmentGitOpsCookie, Value: token, Path: "/", HttpOnly: true,
		Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
	data.CSRFToken, data.Review = token, review
	if review != nil {
		data.Definition = dashboard.PrettyAuditData(review.Definition)
	}
	view, _ := AccountFrom(r.Context())
	page := dashboard.Page{Title: "GitOps · " + data.Project + "/" + data.Environment, Body: "environment_gitops", Account: dashboardAccountView(view, 0), Data: data}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, s.log, err)
	}
}

func (s *server) dashboardEnvironmentGitOpsMutation(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64(api.EnvironmentGitOpsMaxDefinitionBytes))
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid environment GitOps form."))
		return
	}
	if !s.verifyDashboardConfigCSRF(w, r, dashboardEnvironmentGitOpsAction, dashboardEnvironmentGitOpsCookie, acct.ID) {
		return
	}
	body, handler, method, err := s.environmentGitOpsDashboardRequest(r)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid environment GitOps form."))
		return
	}
	if r.PathValue("action") != "review" {
		handler = dashboardJSONHandler(s.requireVerifiedEmail(accountHandler(handler)))
	}
	resp := s.forwardDashboardJSON(r, acct, method, r.URL.Path, "", "", body, handler)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	if r.PathValue("action") == "review" {
		var review api.PreviewEnvironmentGitRevisionResponse
		if json.Unmarshal(resp.Body.Bytes(), &review) != nil {
			api.WriteProblem(w, api.ErrCapacity("Could not read the reviewed Git revision."))
			return
		}
		s.renderDashboardEnvironmentGitOps(w, r, acct, &review)
		return
	}
	target := "/dashboard/projects/" + url.PathEscape(r.PathValue("slug")) + "/environments/" + url.PathEscape(r.PathValue("environment")) + "/gitops"
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *server) environmentGitOpsDashboardRequest(r *http.Request) (any, dashboardJSONHandler, string, error) {
	action := r.PathValue("action")
	if action == "bind" {
		return api.CreateEnvironmentGitSourceRequest{Ref: r.FormValue("ref"), ManifestPath: r.FormValue("manifest_path"), Mode: "report", ApprovalPolicy: r.FormValue("approval_policy")}, s.createEnvironmentGitSource, http.MethodPost, nil
	}
	if action == "review" {
		return api.PreviewEnvironmentGitRevisionRequest{CommitSHA: r.FormValue("commit_sha")}, s.previewEnvironmentGitRevision, http.MethodPost, nil
	}
	if action == "adopt" {
		return api.AdoptEnvironmentGitOpsRequest{PlanHash: r.FormValue("plan_hash")}, s.adoptEnvironmentGitOps, http.MethodPost, nil
	}
	if action == "remove-override" {
		return api.RemoveEnvironmentGitOpsOverrideRequest{Resource: r.FormValue("resource"), Path: r.FormValue("path")}, s.removeEnvironmentGitOpsOverride, http.MethodDelete, nil
	}
	if action == "override" {
		expiry, err := time.Parse(time.RFC3339, r.FormValue("expires_at"))
		return api.EnvironmentGitOpsOverrideRequest{Resource: r.FormValue("resource"), Path: r.FormValue("path"), Reason: r.FormValue("reason"), ExpiresAt: expiry}, s.createEnvironmentGitOpsOverride, http.MethodPost, err
	}
	generation, err := strconv.ParseInt(r.FormValue("expected_generation"), 10, 64)
	if err != nil || generation < 0 {
		return nil, nil, "", state.ErrInvalidArgument
	}
	if action == "approve" {
		return api.ApproveEnvironmentGitRevisionRequest{CommitSHA: r.FormValue("commit_sha"), DefinitionDigest: r.FormValue("definition_digest"), ExpectedGeneration: generation}, s.approveEnvironmentGitRevision, http.MethodPost, nil
	}
	if action == "rebind" {
		return api.RebindEnvironmentGitSourceRequest{ExpectedGeneration: generation, Ref: r.FormValue("ref"), ManifestPath: r.FormValue("manifest_path"), ApprovalPolicy: r.FormValue("approval_policy")}, s.rebindEnvironmentGitSource, http.MethodPost, nil
	}
	if action == "unbind" {
		return api.DetachEnvironmentGitSourceRequest{ExpectedGeneration: generation}, s.detachEnvironmentGitSource, http.MethodDelete, nil
	}
	if action == "controls" {
		prune, suspended := r.FormValue("prune") == "true", r.FormValue("suspended") == "true"
		return api.EnvironmentGitSourceUpdate{ExpectedGeneration: generation, Mode: r.FormValue("mode"), Prune: &prune, Suspended: &suspended}, s.updateEnvironmentGitSource, http.MethodPatch, nil
	}
	return nil, nil, "", state.ErrInvalidArgument
}
