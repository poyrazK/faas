package apidserver

import (
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
)

func (s *server) registerOrgPostgresRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/orgs/{organization}/postgres/databases", s.authLimited(s.requireMFA(s.requireScope(api.ScopesManagedPostgresReadSurface...)(s.listManagedPostgresDatabases))))
	mux.HandleFunc("GET /v1/orgs/{organization}/postgres/capabilities", s.authLimited(s.requireMFA(s.requireScope(api.ScopesManagedPostgresReadSurface...)(s.getManagedPostgresCapabilities))))
	mux.HandleFunc("POST /v1/orgs/{organization}/postgres/databases", s.authLimited(s.requireMFA(s.requireScope(api.ScopesManagedPostgresManageSurface...)(s.requireVerifiedEmail(s.idempotent(s.createManagedPostgresDatabase))))))
	mux.HandleFunc("POST /v1/orgs/{organization}/postgres/databases/{id}/compute-policy", s.authLimited(s.requireMFA(s.requireScope(api.ScopesManagedPostgresManageSurface...)(s.requireVerifiedEmail(s.changeManagedPostgresComputePolicy)))))
	mux.HandleFunc("GET /v1/orgs/{organization}/postgres/databases/{id}/compute-policy-changes/{change_id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesManagedPostgresReadSurface...)(s.getManagedPostgresComputePolicyChange))))
	mux.HandleFunc("POST /v1/orgs/{organization}/postgres/databases/{id}/resize", s.authLimited(s.requireMFA(s.requireScope(api.ScopesManagedPostgresManageSurface...)(s.requireVerifiedEmail(s.resizeManagedPostgresDatabase)))))
	mux.HandleFunc("GET /v1/orgs/{organization}/postgres/databases/{id}/resizes/{resize_id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesManagedPostgresReadSurface...)(s.getManagedPostgresResize))))
	mux.HandleFunc("GET /v1/orgs/{organization}/postgres/databases/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesManagedPostgresReadSurface...)(s.getManagedPostgresDatabase))))
	mux.HandleFunc("DELETE /v1/orgs/{organization}/postgres/databases/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesManagedPostgresManageSurface...)(s.idempotent(s.deleteManagedPostgresDatabase)))))
	mux.HandleFunc("POST /v1/orgs/{organization}/postgres/databases/{id}/restore", s.authLimited(s.requireMFA(s.requireScope(api.ScopesManagedPostgresManageSurface...)(s.requireVerifiedEmail(s.idempotent(s.restoreManagedPostgresDatabase))))))
	mux.HandleFunc("GET /v1/orgs/{organization}/postgres/databases/{id}/bindings", s.authLimited(s.requireMFA(s.requireScope(api.ScopesManagedPostgresReadSurface...)(s.listManagedPostgresBindings))))
	mux.HandleFunc("POST /v1/orgs/{organization}/postgres/databases/{id}/bindings", s.authLimited(s.requireMFA(s.requireScope(api.ScopesManagedPostgresManageSurface...)(s.requireVerifiedEmail(s.idempotent(s.createManagedPostgresBinding))))))
	mux.HandleFunc("GET /v1/orgs/{organization}/postgres/bindings/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesManagedPostgresReadSurface...)(s.getManagedPostgresBinding))))
	mux.HandleFunc("DELETE /v1/orgs/{organization}/postgres/bindings/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesManagedPostgresManageSurface...)(s.idempotent(s.deleteManagedPostgresBinding)))))
	mux.HandleFunc("POST /v1/orgs/{organization}/postgres/bindings/{id}/rotate", s.authLimited(s.requireMFA(s.requireScope(api.ScopesManagedPostgresManageSurface...)(s.requireVerifiedEmail(s.idempotent(s.rotateManagedPostgresBinding))))))
	mux.HandleFunc("GET /v1/orgs/{organization}/postgres/usage", s.authLimited(s.requireMFA(s.requireScope(api.ScopesUsageReadSurface...)(s.getManagedPostgresUsage))))
}
