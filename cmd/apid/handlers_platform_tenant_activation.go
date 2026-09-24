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
	surfaces, err := store.ListPlatformTenantSurfaces(r.Context(), acct.ID, tenant.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list platform tenant surfaces"))
		return
	}
	enabled := s.runtimeBool(runtimeConfigTenantSurfaces, api.TenantSurfacesEnabled())
	out := api.PlatformTenantActivationResponse{TenantID: tenant.ID, Status: tenant.Status, Enabled: enabled,
		Ready:    enabled && tenant.Status == state.PlatformTenantActive && len(surfaces) > 0,
		Surfaces: make([]api.PlatformTenantActivationSurfaceResponse, 0, len(surfaces))}
	for _, surface := range surfaces {
		hostnames, err := s.store.ListTenantHostnamesForSurface(r.Context(), surface.ID)
		if err != nil {
			api.WriteProblem(w, api.ErrInternal("could not list platform tenant hostnames"))
			return
		}
		row := platformTenantActivationSurface(surface, hostnames, enabled, tenant.Status == state.PlatformTenantActive)
		if !row.Ready {
			out.Ready = false
		}
		out.Surfaces = append(out.Surfaces, row)
	}
	writeJSON(w, http.StatusOK, out)
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
