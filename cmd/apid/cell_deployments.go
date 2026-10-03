package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/state"
)

type cellDeploymentAppContextKey struct{}

// requireCellDeploymentApp runs outside replay and pins the immutable target
// for both deployment input paths. The store locks this same app UUID on insert.
func (s *server) requireCellDeploymentApp(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		w.Header().Set("Cache-Control", "no-store")
		cellID, appID := r.PathValue("cell_id"), r.PathValue("app_id")
		if !globaldirectory.ValidID(cellID) || !globaldirectory.ValidID(appID) {
			api.WriteProblem(w, api.ErrValidation("cell_id and app_id must be canonical nonzero UUIDs"))
			return
		}
		if !globaldirectory.ValidID(s.controlPlaneCell.ID) || s.controlPlaneCell.Region == "" {
			api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeGlobalAppDirectoryUnavailable, "Regional cell not configured", "This control plane does not provide a regional cell identity."))
			return
		}
		if cellID != s.controlPlaneCell.ID {
			s.notFound(w, "no such regional app")
			return
		}
		app, ok := s.loadCellDeploymentApp(w, r, acct, appID)
		if !ok {
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), cellDeploymentAppContextKey{}, app))
		next(w, r, acct)
	}
}

func (s *server) loadCellDeploymentApp(w http.ResponseWriter, r *http.Request, acct state.Account, appID string) (state.App, bool) {
	app, err := s.store.AppByID(r.Context(), appID)
	if errors.Is(err, state.ErrNotFound) || (err == nil && (app.AccountID != acct.ID || app.Status == state.AppDeleted || app.DeletedAt != nil)) {
		s.notFound(w, "no such regional app")
		return state.App{}, false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not verify regional app identity"))
		return state.App{}, false
	}
	personal, err := s.personalDirectoryOrg(r.Context(), acct.ID, app.OrgID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not verify personal app ownership"))
		return state.App{}, false
	}
	if !personal {
		s.notFound(w, "no such regional app")
		return state.App{}, false
	}
	return app, true
}

func (s *server) deploymentAppForRequest(r *http.Request) (state.App, error) {
	if app, ok := r.Context().Value(cellDeploymentAppContextKey{}).(state.App); ok {
		return app, nil
	}
	return s.store.AppBySlug(r.Context(), r.PathValue("slug"))
}
