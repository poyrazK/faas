package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 226 — the CLI uses the account-level customer routes.
func TestCmdPlatformTenantsAddListAndSuspend(t *testing.T) {
	var created api.CreatePlatformTenantRequest
	var status api.SetPlatformTenantStatusRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/account/platform-tenants":
			_ = json.NewDecoder(r.Body).Decode(&created)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.PlatformTenantResponse{ID: "tenant-id", ExternalRef: created.ExternalRef, Name: created.Name, Status: "active"})
		case "GET /v1/account/platform-tenants":
			_ = json.NewEncoder(w).Encode(api.PlatformTenantListResponse{Tenants: []api.PlatformTenantResponse{{ID: "tenant-id", ExternalRef: "customer-42", Name: "Customer 42", Status: "active"}}})
		case "PATCH /v1/account/platform-tenants/tenant-id":
			_ = json.NewDecoder(r.Body).Decode(&status)
			_ = json.NewEncoder(w).Encode(api.PlatformTenantResponse{ID: "tenant-id", Status: status.Status})
		default:
			t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	oldOut, oldJSON := osStdout, jsonOutput
	var stdout bytes.Buffer
	osStdout, jsonOutput = &stdout, false
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	if code := cmdPlatformTenants([]string{"add", "--external-ref", "customer-42", "--name", "Customer 42"}); code != 0 {
		t.Fatalf("add exit = %d", code)
	}
	if created.ExternalRef != "customer-42" || created.Name != "Customer 42" {
		t.Fatalf("create body = %+v", created)
	}
	if code := cmdPlatformTenants([]string{"list"}); code != 0 || !strings.Contains(stdout.String(), "customer-42") {
		t.Fatalf("list exit=%d output=%q", code, stdout.String())
	}
	if code := cmdPlatformTenants([]string{"suspend", "--id", "tenant-id"}); code != 0 || status.Status != "suspended" {
		t.Fatalf("suspend exit=%d body=%+v", code, status)
	}
}

func TestCmdPlatformTenantsRejectsIncompleteLink(t *testing.T) {
	if code := cmdPlatformTenants([]string{"link-consumer", "--id", "tenant-id"}); code == 0 {
		t.Fatal("accepted missing consumer ID")
	}
}
