package main

import (
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
)

const (
	dashboardRollbackAction     = "app_rollback"
	dashboardRollbackCSRFCookie = "faas_csrf_app_rollback"
)

// dashboardRollback handles the app-detail rollback form. It verifies a
// dedicated named CSRF envelope, re-checks app ownership, and delegates the
// state transition to the same core used by POST /v1/apps/{slug}/rollback.
func (s *server) dashboardRollback(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !validSlug(slug) {
		http.NotFound(w, r)
		return
	}
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardRollbackAction, acct.ID, dashboardRollbackCSRFCookie); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid CSRF token", "please reload the page and try again"))
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad form", err.Error()))
		return
	}
	targetID := strings.TrimSpace(r.FormValue("deployment_id"))
	if !dashboardRetryDeploymentIDRe.MatchString(targetID) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid deployment", "deployment_id must be a deployment identifier"))
		return
	}
	app, err := s.store.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	_, problem := s.rollbackAppCore(r.Context(), acct, app, api.RollbackRequest{TargetDeploymentID: &targetID})
	if problem != nil {
		http.Redirect(w, r, "/dashboard/apps/"+slug+"?rollback=error", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+slug+"?rollback=1", http.StatusSeeOther)
}
