package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/role"
)

// The private control listener (ADR-20261022004500) runs only on compute
// nodes and serves only the control routes apid calls across hosts.
func TestPrivateControlAddr(t *testing.T) {
	env := func(v string) func(string) string { return func(string) string { return v } }
	tests := []struct {
		name string
		role role.Role
		env  string
		want string
	}{
		{"compute node with the listener set", role.RoleComputeOnly, " 0.0.0.0:9097 ", "0.0.0.0:9097"},
		{"compute node without it", role.RoleComputeOnly, "", ""},
		{"single box ignores it", role.RoleSingleBox, "0.0.0.0:9097", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := privateControlAddr(tt.role, env(tt.env)); got != tt.want {
				t.Fatalf("privateControlAddr = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrivateControlMuxServesOnlyApidRoutes(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	mux := newPrivateControlMux(ok, ok, ok)
	tests := []struct {
		path string
		want int
	}{
		{"/v1/internal/apps/web/mcp-health", http.StatusTeapot},
		{"/v1/internal/apps/web/mcp-probe", http.StatusTeapot},
		{"/v1/internal/apps/web/routes", http.StatusTeapot},
		{"/v1/internal/apps/web/streaming-cap", http.StatusTeapot},
		{api.DeploymentResponseCheckCapabilitiesControlPath, http.StatusTeapot},
		{"/v1/internal/realtime/internal/connections", http.StatusTeapot},
		{"/metrics", http.StatusNotFound},
		{"/v1/internal/quota", http.StatusNotFound},
		{"/v1/internal/services/web", http.StatusNotFound},
		{"/debug/pprof/", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if rec.Code != tt.want {
				t.Fatalf("%s = %d, want %d", tt.path, rec.Code, tt.want)
			}
		})
	}
}
