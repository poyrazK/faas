package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Both ways of adding a tenant hostname, a surface created with hostnames
// and a hostname added to a surface, refuse a name that is not a DNS
// hostname and a name below a verified wildcard custom domain (ADR-167).
// The surface path used to skip both checks.
func TestTenantHostnames_BothPathsValidateAndRespectWildcards(t *testing.T) {
	apidserver.WithTenantSurfacesEnabled(t)
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanScale)
	ctx := context.Background()
	store := *e.Store()
	appID := apidserver.MustSeedApp(t, e, "hostname-validation")
	if _, err := store.CreateCustomDomain(ctx, "*.zone.example.com", appID, "tok"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(ctx, "*.zone.example.com"); err != nil {
		t.Fatal(err)
	}
	surface := func(name string, hostnames ...string) *httptest.ResponseRecorder {
		return e.Do(t, http.MethodPost, "/v1/apps/hostname-validation/tenant-surfaces", api.CreateTenantSurfaceRequest{
			AppID: appID, Name: name, CertKind: string(state.CertKindPerHostSAN), Hostnames: hostnames,
		}, nil)
	}
	for _, tc := range []struct {
		host   string
		status int
	}{
		{"*.wild.example.com", http.StatusBadRequest},
		{"under_score.example.com", http.StatusBadRequest},
		{"singlelabel", http.StatusBadRequest},
		{"203.0.113.9", http.StatusBadRequest},
		{"trailing.example.com.", http.StatusBadRequest},
		{"shop.zone.example.com", http.StatusConflict},
	} {
		if got := surface("bad-"+tc.host, tc.host).Code; got != tc.status {
			t.Errorf("surface with %q: status %d, want %d", tc.host, got, tc.status)
		}
	}
	created := surface("good", "tenant.example.com")
	if created.Code != http.StatusAccepted {
		t.Fatalf("surface with a valid hostname: status %d", created.Code)
	}
	var surf api.TenantSurfaceResponse
	if err := json.Unmarshal(created.Body.Bytes(), &surf); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		host   string
		status int
	}{
		{"*.wild.example.com", http.StatusBadRequest},
		{"shop.zone.example.com", http.StatusConflict},
		{"second.example.com", http.StatusAccepted},
	} {
		rec := e.Do(t, http.MethodPost, "/v1/apps/hostname-validation/tenant-surfaces/"+surf.ID+"/hostnames",
			api.AddTenantHostnameRequest{Hostname: tc.host}, nil)
		if rec.Code != tc.status {
			t.Errorf("add %q: status %d, want %d; body=%s", tc.host, rec.Code, tc.status, rec.Body.String())
		}
	}
}
