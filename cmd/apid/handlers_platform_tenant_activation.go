package main

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// getPlatformTenantActivation reports observed DNS, cert, and route readiness.
// It never starts issuance; database triggers and the existing workers do that.
func (s *server) getPlatformTenantActivation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.platformTenantStore(w, acct)
	if !ok {
		return
	}
	tenant, ok := s.platformTenantByPath(w, r, acct, store)
	if !ok {
		return
	}
	out, ok := s.loadPlatformTenantActivation(w, r, acct, tenant, store)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getPlatformTenantSelfActivation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenantID, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	r.SetPathValue("id", tenantID)
	store, ok := s.platformTenantStore(w, acct)
	if !ok {
		return
	}
	tenant, ok := s.platformTenantByPath(w, r, acct, store)
	if !ok {
		return
	}
	snapshot, ok := s.loadPlatformTenantActivation(w, r, acct, tenant, store)
	if !ok {
		return
	}
	latestByApp := make(map[string]state.Deployment, len(snapshot.Surfaces))
	if len(snapshot.Surfaces) > 0 {
		latest, err := s.store.ListLatestDeploymentPerApp(r.Context(), acct.ID)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not load tenant deployment status"))
			return
		}
		latestByApp = latest
	}
	out := api.PlatformTenantSelfActivationResponse{Status: snapshot.Status, Enabled: snapshot.Enabled,
		Ready: snapshot.Ready, Surfaces: make([]api.PlatformTenantSelfActivationSurfaceResponse, 0, len(snapshot.Surfaces))}
	for _, surface := range snapshot.Surfaces {
		row := api.PlatformTenantSelfActivationSurfaceResponse{ID: surface.ID, Name: surface.Name,
			Status: surface.Status, CertState: surface.CertState, CertNotAfter: surface.CertNotAfter,
			Ready: surface.Ready, Hostnames: make([]api.PlatformTenantSelfActivationHostnameResponse, 0, len(surface.Hostnames))}
		if deployment, found := latestByApp[surface.AppID]; found {
			row.LatestDeployment = &api.PlatformTenantSelfDeploymentResponse{
				Status: string(deployment.Status), Revision: deployment.Revision,
				StartedAt: deployment.CreatedAt.UTC().Format(time.RFC3339Nano),
			}
		}
		for _, hostname := range surface.Hostnames {
			row.Hostnames = append(row.Hostnames, api.PlatformTenantSelfActivationHostnameResponse{
				Hostname: hostname.Hostname, Verified: hostname.Verified, VerifiedAt: hostname.VerifiedAt,
			})
		}
		out.Surfaces = append(out.Surfaces, row)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *server) loadPlatformTenantActivation(w http.ResponseWriter, r *http.Request, acct state.Account,
	tenant state.PlatformTenant, store state.PlatformTenantStore) (api.PlatformTenantActivationResponse, bool) {
	surfaces, err := store.ListPlatformTenantSurfaces(r.Context(), acct.ID, tenant.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list platform tenant surfaces"))
		return api.PlatformTenantActivationResponse{}, false
	}
	enabled := s.runtimeBool(runtimeConfigTenantSurfaces, api.TenantSurfacesEnabled())
	out := api.PlatformTenantActivationResponse{TenantID: tenant.ID, Status: tenant.Status, Enabled: enabled,
		Ready:    enabled && tenant.Status == state.PlatformTenantActive && len(surfaces) > 0,
		Surfaces: make([]api.PlatformTenantActivationSurfaceResponse, 0, len(surfaces))}
	for _, surface := range surfaces {
		hostnames, err := s.store.ListTenantHostnamesForSurface(r.Context(), surface.ID)
		if err != nil {
			api.WriteProblem(w, api.ErrInternal("could not list platform tenant hostnames"))
			return api.PlatformTenantActivationResponse{}, false
		}
		row := platformTenantActivationSurface(surface, hostnames, enabled, tenant.Status == state.PlatformTenantActive)
		if !row.Ready {
			out.Ready = false
		}
		out.Surfaces = append(out.Surfaces, row)
	}
	return out, true
}

func platformTenantActivationSurface(surface state.TenantSurface, hostnames []state.TenantHostname, enabled, tenantActive bool) api.PlatformTenantActivationSurfaceResponse {
	row := api.PlatformTenantActivationSurfaceResponse{ID: surface.ID, AppID: surface.AppID, Name: surface.Name,
		Status: string(surface.Status), CertState: string(surface.CertState), CertLastError: surface.CertLastError,
		Ready: enabled && tenantActive && surface.Active() && surface.CertState == state.CertStateIssued &&
			surface.CertValid(time.Now().UTC()) && len(hostnames) > 0,
		Hostnames: make([]api.TenantHostnameResponse, 0, len(hostnames))}
	if !surface.CertNotAfter.IsZero() {
		row.CertNotAfter = surface.CertNotAfter.UTC().Format(time.RFC3339)
	}
	for _, host := range hostnames {
		row.Hostnames = append(row.Hostnames, hostnameResponse(host))
		if !host.Verified() {
			row.Ready = false
		}
	}
	return row
}
