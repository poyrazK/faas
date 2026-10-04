package main

// adr: 485
import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) requireRegionalBindingApp(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		w.Header().Set("Cache-Control", "no-store")
		if !globaldirectory.ValidID(s.controlPlaneCell.ID) || s.controlPlaneCell.Region == "" {
			api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeGlobalAppDirectoryUnavailable, "Regional cell not configured", "This control plane does not provide a regional cell identity."))
			return
		}
		app, err := s.store.AppBySlug(r.Context(), r.PathValue("slug"))
		if errors.Is(err, state.ErrNotFound) || (err == nil && (app.Status == state.AppDeleted || app.DeletedAt != nil)) {
			s.notFound(w, "no such regional app")
			return
		}
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not verify regional app identity"))
			return
		}
		s.authorizeRegionalBindingApp(w, r, acct, app, next)
	}
}

func (s *server) authorizeRegionalBindingApp(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, next accountHandler) {
	_, key, _ := middleware.AccountFromContext(r)
	if key == nil || key.PlatformTenantID != "" || (key.AppID != "" && (key.RegionalReleaseGrantID == "" || key.AppID != app.ID || key.RegionalReleaseCellID != s.controlPlaneCell.ID)) {
		api.WriteProblem(w, api.ErrOrgRoleForbidden("regional binding access"))
		return
	}
	personal, err := s.personalDirectoryOrg(r.Context(), app.AccountID, app.OrgID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not verify regional owner"))
		return
	}
	if !personal {
		s.authorizeRegionalGrantApp(w, r, acct, key, app, next)
		return
	}
	s.requireRegionalReleasePrincipal(func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if acct.ID != app.AccountID {
			s.notFound(w, "no such regional app")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), cellDeploymentAppContextKey{}, app)), acct)
	})(w, r, acct)
}

func (s *server) regionalBindingResponse(w http.ResponseWriter, r *http.Request, _ state.Account) {
	a, _ := r.Context().Value(cellDeploymentAppContextKey{}).(state.App)
	account, e1 := uuid.Parse(a.AccountID)
	app, e2 := uuid.Parse(a.ID)
	if e1 != nil || e2 != nil || account == uuid.Nil || app == uuid.Nil {
		api.WriteProblem(w, api.ErrCapacity("regional identities are invalid"))
		return
	}
	personal, err := s.personalDirectoryOrg(r.Context(), a.AccountID, a.OrgID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not verify regional owner"))
		return
	}
	out := api.CellAppBindingResponse{CellID: s.controlPlaneCell.ID, Region: s.controlPlaneCell.Region, AccountID: account.String(), AppID: app.String(), AppSlug: a.Slug}
	if !personal {
		org, err := uuid.Parse(a.OrgID)
		if err != nil || org == uuid.Nil {
			api.WriteProblem(w, api.ErrCapacity("regional organization identity is invalid"))
			return
		}
		out.OrgID = org.String()
	}
	writeJSON(w, http.StatusOK, out)
}
