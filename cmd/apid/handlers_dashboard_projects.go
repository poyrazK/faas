package main

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const dashboardProjectManageAction = "project_manage"

func projectDashboardFlash(r *http.Request) string {
	switch r.URL.Query().Get("project") {
	case "updated":
		return "Project source settings updated."
	case "deleted":
		return "Project deleted. Its workloads remain live as standalone apps."
	case "invalid":
		return "Project settings were invalid. Check the repository and production branch."
	case "conflict":
		return "That repository is already bound to another project."
	case "forbidden":
		return "The project action expired. Reload the page and try again."
	case "confirm":
		return "Type the project slug exactly before deleting it."
	case "error":
		return "The project action failed. Try again."
	default:
		return ""
	}
}

func (s *server) renderProjects(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account) {
	projects, err := s.store.ListProjectsForAccount(r.Context(), acct.ID)
	if err != nil {
		renderProblem(w, log, err)
		return
	}
	items := make([]api.ProjectSummaryResponse, 0, len(projects))
	for _, project := range projects {
		apps, listErr := s.store.AppsForProject(r.Context(), acct.ID, project.ID)
		if listErr != nil {
			renderProblem(w, log, listErr)
			return
		}
		items = append(items, projectSummaryResponse(project, len(apps)))
	}
	view, _ := AccountFrom(r.Context())
	page := dashboard.Page{
		Title:   "Projects",
		Body:    "projects",
		Account: dashboardAccountView(view, 0),
		Data: dashboard.ProjectsData{
			Projects: items,
			Flash:    projectDashboardFlash(r),
		},
	}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}

func (s *server) renderProjectDetail(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	project, err := s.store.ProjectBySlug(r.Context(), acct.ID, slug)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		renderProblem(w, log, err)
		return
	}
	response, problem := s.projectResponse(r, acct, project)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	preview, problem := s.projectDeletePreview(r, acct, project, response)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	token, err := middleware.IssueForAuthenticated(s.sessions, dashboardProjectManageAction, acct.ID)
	if err != nil {
		renderProblem(w, log, err)
		return
	}
	setDashboardCSRFCookie(w, s, token)
	view, _ := AccountFrom(r.Context())
	page := dashboard.Page{
		Title:   "Project " + slug,
		Body:    "projects",
		Account: dashboardAccountView(view, 0),
		Data: dashboard.ProjectsData{
			Project:       &response,
			DeletePreview: &preview,
			CSRFToken:     token,
			Flash:         projectDashboardFlash(r),
		},
	}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}

func projectDashboardRedirect(w http.ResponseWriter, r *http.Request, slug, result string) {
	target := "/dashboard/projects"
	if slug != "" {
		target += "/" + url.PathEscape(slug)
	}
	if result != "" {
		target += "?project=" + url.QueryEscape(result)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *server) dashboardUpdateProject(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	slug := r.PathValue("slug")
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !api.ValidProjectSlug(slug) {
		http.NotFound(w, r)
		return
	}
	if err := middleware.VerifyAuthenticated(s.sessions, r, dashboardProjectManageAction, acct.ID); err != nil {
		projectDashboardRedirect(w, r, slug, "forbidden")
		return
	}
	project, err := s.store.ProjectBySlug(r.Context(), acct.ID, slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	repo := strings.TrimSpace(r.FormValue("repo_full_name"))
	branch := strings.TrimSpace(r.FormValue("production_branch"))
	if !validProjectBranch(branch) || (repo != "" && !validProjectRepoFullName(repo)) {
		projectDashboardRedirect(w, r, slug, "invalid")
		return
	}
	installID := project.InstallID
	if repo == "" {
		installID = 0
	} else if repo != project.RepoFullName || installID == 0 {
		resolved, problem := s.resolveRepositoryInstallation(r.Context(), acct.ID, repo)
		if problem != nil {
			projectDashboardRedirect(w, r, slug, "error")
			return
		}
		installID = resolved
	}
	updated, err := s.store.UpdateProjectBinding(r.Context(), acct.ID, project.ID, repo, branch, installID)
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			projectDashboardRedirect(w, r, slug, "conflict")
			return
		}
		projectDashboardRedirect(w, r, slug, "error")
		return
	}
	s.audit.Emit(r.Context(), "project.updated", &acct.ID, map[string]any{
		"project_id": updated.ID, "slug": updated.Slug, "repo_full_name": updated.RepoFullName,
		"production_branch": updated.ProductionBranch, "surface": "dashboard",
	})
	projectDashboardRedirect(w, r, slug, "updated")
}

func (s *server) dashboardDeleteProject(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	slug := r.PathValue("slug")
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !api.ValidProjectSlug(slug) {
		http.NotFound(w, r)
		return
	}
	if err := middleware.VerifyAuthenticated(s.sessions, r, dashboardProjectManageAction, acct.ID); err != nil {
		projectDashboardRedirect(w, r, slug, "forbidden")
		return
	}
	if strings.TrimSpace(r.FormValue("confirm_slug")) != slug {
		projectDashboardRedirect(w, r, slug, "confirm")
		return
	}
	project, err := s.store.ProjectBySlug(r.Context(), acct.ID, slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.store.DeleteProject(r.Context(), project.ID); err != nil {
		projectDashboardRedirect(w, r, slug, "error")
		return
	}
	s.audit.Emit(r.Context(), "project.deleted", &acct.ID, map[string]any{
		"project_id": project.ID, "slug": slug, "surface": "dashboard",
	})
	projectDashboardRedirect(w, r, "", "deleted")
}
