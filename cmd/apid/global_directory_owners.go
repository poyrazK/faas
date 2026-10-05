package main

// adr: 609
import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/state"
)

type globalOwnerContextKey struct{}
type globalOwnerPrincipal struct {
	Owner globaldirectory.Owner
	Plan  api.Plan
}

func globalOwner(r *http.Request, acct state.Account) globalOwnerPrincipal {
	if p, ok := r.Context().Value(globalOwnerContextKey{}).(globalOwnerPrincipal); ok {
		return p
	}
	return globalOwnerPrincipal{Owner: globaldirectory.Owner{ID: acct.ID, AccountID: acct.ID}, Plan: acct.Plan}
}
func globalOwnerID(r *http.Request, acct state.Account) string { return globalOwner(r, acct).Owner.ID }

func (s *server) requireGlobalDirectoryOwner(write bool, next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if !s.globalDirectoryAvailable(w) {
			return
		}
		_, key, _ := middleware.AccountFromContext(r)
		if key != nil && (key.AppID != "" || key.PlatformTenantID != "") {
			api.WriteProblem(w, api.ErrOrgRoleForbidden("global directory access"))
			return
		}
		org, selected, err := s.selectedDirectoryOrg(r, key)
		if err != nil {
			s.globalDirectoryError(w, r, acct, err)
			return
		}
		if !selected {
			next(w, r.WithContext(context.WithValue(r.Context(), globalOwnerContextKey{}, globalOwner(r, acct))), acct)
			return
		}
		// LoadOrg owns membership resolution. Remove the global-app slug only for
		// this adapter; it must never be interpreted as an organization selector.
		selectedRequest := r.Clone(r.Context())
		selectedRequest.SetPathValue("slug", "")
		selectedRequest.Header.Set("X-Active-Org", org.Slug)
		mw := authz.LoadOrgWithResolver(authz.LoadOrgConfig{Log: s.log, Audit: loadOrgAuditFrom(s.audit)}, authz.NewStoreBackedResolver(s.store))
		mw(http.HandlerFunc(func(w http.ResponseWriter, loaded *http.Request) {
			loaded.SetPathValue("slug", r.PathValue("slug"))
			s.directoryOrgPrincipal(w, loaded, acct, key, write, next)
		})).ServeHTTP(w, selectedRequest)
	}
}

func (s *server) selectedDirectoryOrg(r *http.Request, key *state.APIKey) (state.Org, bool, error) {
	slug := strings.TrimSpace(r.Header.Get("X-Active-Org"))
	if slug == "" {
		slug = strings.TrimSpace(r.URL.Query().Get("org"))
	}
	if slug != "" {
		org, err := s.store.OrgBySlug(r.Context(), slug)
		if errors.Is(err, state.ErrNotFound) {
			return state.Org{}, false, globaldirectory.ErrNotFound
		}
		if err != nil {
			return state.Org{}, false, err
		}
		if key != nil && key.OrgID != org.ID {
			return state.Org{}, false, globaldirectory.ErrOwnerForbidden
		}
		return org, true, nil
	}
	if key == nil || key.OrgID == "" {
		return state.Org{}, false, nil
	}
	org, err := s.store.OrgByID(r.Context(), key.OrgID)
	if errors.Is(err, state.ErrNotFound) {
		return state.Org{}, false, globaldirectory.ErrOwnerForbidden
	}
	if err != nil {
		return state.Org{}, false, err
	}
	if org.Personal {
		personal, err := s.personalDirectoryOrg(r.Context(), key.AccountID, org.ID)
		if err != nil {
			return state.Org{}, false, err
		}
		if !personal {
			return state.Org{}, false, globaldirectory.ErrOwnerForbidden
		}
		return state.Org{}, false, nil
	}
	return org, true, nil
}

func (s *server) directoryOrgPrincipal(w http.ResponseWriter, r *http.Request, acct state.Account, key *state.APIKey, write bool, next accountHandler) {
	mem, ok := authz.MembershipFrom(r)
	if !ok {
		api.WriteProblem(w, api.ErrOrgRoleForbidden("global directory access"))
		return
	}
	action := authz.OrgActionView
	if write {
		action = authz.OrgActionCreateApp
	}
	if !s.requireOrgAction(w, r, action) {
		return
	}
	p, err := s.resolveGlobalDirectoryTenant(r, acct, key, mem.OrgID)
	if err != nil {
		s.globalDirectoryError(w, r, acct, err)
		return
	}
	next(w, r.WithContext(context.WithValue(r.Context(), globalOwnerContextKey{}, p)), acct)
}

func (s *server) resolveGlobalDirectoryTenant(r *http.Request, acct state.Account, key *state.APIKey, orgID string) (globalOwnerPrincipal, error) {
	org, err := s.store.OrgByID(r.Context(), orgID)
	if err != nil {
		return globalOwnerPrincipal{}, err
	}
	if org.Status != state.OrgStatusActive || org.DeletedPending {
		return globalOwnerPrincipal{}, globaldirectory.ErrOwnerInactive
	}
	if key != nil && key.OrgID != org.ID {
		return globalOwnerPrincipal{}, globaldirectory.ErrOwnerForbidden
	}
	if org.Personal {
		personal, err := s.personalDirectoryOrg(r.Context(), acct.ID, org.ID)
		if err != nil {
			return globalOwnerPrincipal{}, err
		}
		if !personal {
			return globalOwnerPrincipal{}, globaldirectory.ErrOwnerForbidden
		}
		return globalOwnerPrincipal{Owner: globaldirectory.Owner{ID: acct.ID, AccountID: acct.ID}, Plan: acct.Plan}, nil
	}
	roots, ok := s.globalDirectory.(globaldirectory.OwnerStore)
	if !ok {
		return globalOwnerPrincipal{}, globaldirectory.ErrOwnerInactive
	}
	owner, err := roots.ResolveOrgOwner(r.Context(), org.ID)
	return globalOwnerPrincipal{Owner: owner, Plan: org.Plan}, err
}

func (s *server) requirePersonalGlobalTarget(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if globalOwner(r, acct).Owner.Shared() {
			s.globalDirectoryError(w, r, acct, globaldirectory.ErrOrgTargetsUnavailable)
			return
		}
		next(w, r, acct)
	}
}
func (s *server) createGlobalDirectoryApp(r *http.Request, acct state.Account, slug string) (globaldirectory.App, error) {
	p := globalOwner(r, acct)
	limit := api.MustLimitsFor(p.Plan).DeployedApps
	if !p.Owner.Shared() {
		return s.globalDirectory.CreateApp(r.Context(), p.Owner.ID, slug, limit)
	}
	roots, ok := s.globalDirectory.(globaldirectory.OwnerStore)
	if !ok {
		return globaldirectory.App{}, globaldirectory.ErrOwnerInactive
	}
	return roots.CreateOrgApp(r.Context(), p.Owner.ID, acct.ID, p.Plan, slug, limit)
}

func globalOwnerResponse(owner globaldirectory.Owner) api.GlobalAppOwnerResponse {
	owner.ID = canonicalGlobalOwnerID(owner.ID)
	owner.AccountID = canonicalGlobalOwnerID(owner.AccountID)
	owner.OrgID = canonicalGlobalOwnerID(owner.OrgID)
	out := api.GlobalAppOwnerResponse{ID: owner.ID, Kind: "account"}
	if owner.Shared() {
		out.Kind = "organization"
		id := owner.OrgID
		out.OrgID = &id
	} else {
		id := owner.AccountID
		out.AccountID = &id
	}
	return out
}

func canonicalGlobalOwnerID(id string) string {
	if parsed, err := uuid.Parse(id); err == nil {
		return parsed.String()
	}
	return id
}
