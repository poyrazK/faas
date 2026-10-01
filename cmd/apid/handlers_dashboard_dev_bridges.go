package main

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/devbridge"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const dashboardDevBridgeAction = "dev_bridge_revoke"
const dashboardDevBridgeCSRFCookie = "faas_dev_bridge_csrf"

func (s *server) renderDevBridgesDashboard(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	data, err := s.dashboardDevBridgesData(r, acct)
	if err != nil {
		s.notFound(w, "development bridge unavailable")
		return
	}
	if data.Enabled {
		data.CSRFToken, err = middleware.IssueForAuthenticatedNamed(s.sessions, dashboardDevBridgeAction, acct.ID, dashboardDevBridgeCSRFCookie)
		if err != nil {
			renderProblem(w, s.log, err)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: dashboardDevBridgeCSRFCookie, Value: data.CSRFToken, Path: "/", HttpOnly: true, Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
	}
	w.Header().Set("Cache-Control", "no-store")
	page := dashboard.Page{Title: "Dev Bridge", Body: "dev_bridges", Account: dashboardAccountView(acct, 0), Data: data}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, s.log, err)
	}
}

func (s *server) dashboardDevBridgesData(r *http.Request, acct state.Account) (dashboard.DevBridgesData, error) {
	data := dashboard.DevBridgesData{Enabled: s.devBridgeEnabled}
	if !data.Enabled {
		return data, nil
	}
	store, ok := s.store.(state.DevBridgeStore)
	if !ok {
		return data, state.ErrNotFound
	}
	sessions, err := store.ListDevBridges(r.Context(), acct.ID, api.DevBridgeInventoryLimit)
	if err != nil {
		return data, err
	}
	for _, session := range sessions {
		item := s.dashboardDevBridgeItem(r.Context(), session)
		if project := r.URL.Query().Get("project"); project != "" && item.Project != project {
			continue
		}
		data.Sessions = append(data.Sessions, item)
	}
	if id := r.PathValue("id"); id != "" {
		session, err := store.DevBridgeByID(r.Context(), acct.ID, id)
		if err != nil {
			return data, err
		}
		item := s.dashboardDevBridgeItem(r.Context(), session)
		data.Selected = &item
	}
	if r.URL.Query().Get("revoked") == "1" {
		data.Flash = "The session has been revoked. Its laptop connection will close."
	}
	return data, nil
}

func (s *server) dashboardDevBridgeItem(ctx context.Context, session devbridge.Session) dashboard.DevBridgeItem {
	item := dashboard.DevBridgeItem{Session: session, Activity: s.bridgeActivity(session), App: session.Scope.TargetAppID, Project: session.Scope.ProjectID, Environment: session.Scope.EnvironmentID}
	if app, err := s.store.AppByID(ctx, session.Scope.TargetAppID); err == nil && app.AccountID == session.Scope.AccountID {
		item.App = app.Slug
	}
	if project, err := s.store.ProjectByID(ctx, session.Scope.ProjectID); err == nil && project.AccountID == session.Scope.AccountID {
		item.Project = project.Slug
	}
	if lookup, ok := s.store.(interface {
		ProjectEnvironmentByID(context.Context, string) (state.ProjectEnvironment, error)
	}); ok {
		if env, err := lookup.ProjectEnvironmentByID(ctx, session.Scope.EnvironmentID); err == nil && env.AccountID == session.Scope.AccountID {
			item.Environment = env.Slug
		}
	}
	return item
}

func (s *server) revokeDevBridgeDashboard(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardDevBridgeAction, acct.ID, dashboardDevBridgeCSRFCookie); err != nil {
		http.Error(w, "reload the bridge page and try again", http.StatusForbidden)
		return
	}
	store, ok := s.devBridgeStore(w)
	if !ok {
		return
	}
	if err := store.RevokeDevBridge(r.Context(), acct.ID, r.PathValue("id"), time.Now()); err != nil {
		s.notFound(w, "no such bridge session")
		return
	}
	s.audit.Emit(r.Context(), "dev_bridge.revoked", &acct.ID, map[string]any{"session_id": r.PathValue("id"), "surface": "dashboard"})
	http.Redirect(w, r, "/dashboard/dev-bridges?revoked=1", http.StatusSeeOther)
}
