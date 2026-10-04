package main

// adr: 484
import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/state"
)

// Grant management resolves an immutable regional app before loading its org.
// Image release acceptance retains its separate personal-only admission gate.
func (s *server) requireRegionalGrantApp(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		w.Header().Set("Cache-Control", "no-store")
		cell, id := r.PathValue("cell_id"), r.PathValue("app_id")
		if !globaldirectory.ValidID(cell) || !globaldirectory.ValidID(id) {
			api.WriteProblem(w, api.ErrValidation("cell_id and app_id must be canonical nonzero UUIDs"))
			return
		}
		if !globaldirectory.ValidID(s.controlPlaneCell.ID) || s.controlPlaneCell.Region == "" {
			api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeGlobalAppDirectoryUnavailable, "Regional cell not configured", "This control plane does not provide a regional cell identity."))
			return
		}
		if cell != s.controlPlaneCell.ID {
			s.notFound(w, "no such regional app")
			return
		}
		_, key, _ := middleware.AccountFromContext(r)
		if key == nil || key.PlatformTenantID != "" || (key.AppID != "" && (key.RegionalReleaseGrantID == "" || key.AppID != id || key.RegionalReleaseCellID != cell)) {
			api.WriteProblem(w, api.ErrOrgRoleForbidden("regional grant access"))
			return
		}
		app, err := s.store.AppByID(r.Context(), id)
		if errors.Is(err, state.ErrNotFound) || (err == nil && (app.Status == state.AppDeleted || app.DeletedAt != nil)) {
			s.notFound(w, "no such regional app")
			return
		}
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not verify regional app identity"))
			return
		}
		s.authorizeRegionalGrantApp(w, r, acct, key, app, next)
	}
}

func (s *server) authorizeRegionalGrantApp(w http.ResponseWriter, r *http.Request, acct state.Account, key *state.APIKey, app state.App, next accountHandler) {
	personal, err := s.personalDirectoryOrg(r.Context(), app.AccountID, app.OrgID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not verify regional owner"))
		return
	}
	if personal {
		s.requireRegionalReleasePrincipal(s.requireCellDeploymentApp(next))(w, r, acct)
		return
	}
	if key.OrgID == "" || key.OrgID != app.OrgID {
		s.notFound(w, "no such regional app")
		return
	}
	org, err := s.store.OrgByID(r.Context(), app.OrgID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not verify regional organization"))
		return
	}
	if org.Personal || org.Status != state.OrgStatusActive || org.DeletedPending || !regionalGrantOrgHintMatches(r, org.Slug) {
		api.WriteProblem(w, api.ErrOrgRoleForbidden("regional grant access"))
		return
	}
	selected := r.Clone(r.Context())
	selected.SetPathValue("slug", "")
	selected.Header.Set("X-Active-Org", org.Slug)
	mw := authz.LoadOrgWithResolver(authz.LoadOrgConfig{Log: s.log, Audit: loadOrgAuditFrom(s.audit)}, authz.NewStoreBackedResolver(s.store))
	mw(http.HandlerFunc(func(w http.ResponseWriter, loaded *http.Request) {
		action := authz.OrgActionCreateApiKey
		if r.Method == http.MethodDelete {
			action = authz.OrgActionRevokeApiKey
		}
		if !s.requireOrgAction(w, loaded, action) {
			return
		}
		loaded.SetPathValue("slug", r.PathValue("slug"))
		next(w, loaded.WithContext(context.WithValue(loaded.Context(), cellDeploymentAppContextKey{}, app)), acct)
	})).ServeHTTP(w, selected)
}

func regionalGrantOrgHintMatches(r *http.Request, slug string) bool {
	for _, hint := range []string{r.Header.Get("X-Active-Org"), r.URL.Query().Get("org")} {
		if hint = strings.TrimSpace(hint); hint != "" && hint != slug {
			return false
		}
	}
	return true
}
