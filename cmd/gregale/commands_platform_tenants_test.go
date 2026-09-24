package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestCmdPlatformTenantsApplyBundle(t *testing.T) {
	var received api.ApplyPlatformTenantRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/account/platform-tenants/apply" {
			t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		_ = json.NewEncoder(w).Encode(api.ApplyPlatformTenantResponse{
			ExternalRef: received.ExternalRef, Name: received.Name, Status: "active", Action: "create", DryRun: received.DryRun,
			Consumers: []api.ApplyPlatformTenantConsumerResponse{{AppID: "app-id", Action: "create"}},
			Surfaces:  []api.ApplyPlatformTenantSurfaceResponse{},
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	path := filepath.Join(t.TempDir(), "customer.json")
	if err := os.WriteFile(path, []byte(`{"external_ref":"customer-42","name":"Customer 42"}`), 0600); err != nil {
		t.Fatal(err)
	}
	oldOut, oldJSON := osStdout, jsonOutput
	var stdout bytes.Buffer
	osStdout, jsonOutput = &stdout, false
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	if code := cmdPlatformTenants([]string{"apply", "--file", path, "--dry-run"}); code != 0 {
		t.Fatalf("apply exit = %d", code)
	}
	if !received.DryRun || received.ExternalRef != "customer-42" || !strings.Contains(stdout.String(), "create") {
		t.Fatalf("apply request = %+v output=%q", received, stdout.String())
	}
}

func TestCmdPlatformTenantCredentialsApplyAndList(t *testing.T) {
	var received api.ApplyPlatformTenantCredentialsRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/account/platform-tenants/tenant-id/credentials/apply":
			if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
				t.Error(err)
			}
			_ = json.NewEncoder(w).Encode(api.ApplyPlatformTenantCredentialsResponse{TenantID: "tenant-id", DryRun: received.DryRun,
				Keys: []api.PlatformTenantCredentialResult{{PlatformTenantCredentialMetadata: api.PlatformTenantCredentialMetadata{ID: "key-id", Prefix: "cafebabe"}, Action: "create"}}})
		case "GET /v1/account/platform-tenants/tenant-id/credentials":
			_ = json.NewEncoder(w).Encode(api.PlatformTenantCredentialsResponse{Keys: []api.PlatformTenantCredentialMetadata{{ID: "key-id", Prefix: "cafebabe"}}})
		default:
			t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(path, []byte(`{"keys":[{"consumer_id":"consumer-id","name":"v1","prefix":"cafebabe","hash":"001122","scopes":["read"]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	oldOut, oldJSON := osStdout, jsonOutput
	var stdout bytes.Buffer
	osStdout, jsonOutput = &stdout, false
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	if code := cmdPlatformTenants([]string{"credentials-apply", "--id", "tenant-id", "--file", path, "--dry-run"}); code != 0 {
		t.Fatalf("apply exit = %d", code)
	}
	if !received.DryRun || received.Keys[0].Prefix != "cafebabe" || !strings.Contains(stdout.String(), "create") {
		t.Fatalf("apply = %+v, output=%q", received, stdout.String())
	}
	if code := cmdPlatformTenants([]string{"credentials-list", "--id", "tenant-id"}); code != 0 || !strings.Contains(stdout.String(), "cafebabe") {
		t.Fatalf("list exit = %d, output=%q", code, stdout.String())
	}
}

func TestCmdPlatformTenantsActivationSnapshotAndWait(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/account/platform-tenants/tenant-id/activation" {
			t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		calls++
		_ = json.NewEncoder(w).Encode(api.PlatformTenantActivationResponse{TenantID: "tenant-id", Status: "active", Enabled: true, Ready: true,
			Surfaces: []api.PlatformTenantActivationSurfaceResponse{{ID: "surface-id", Status: "active", CertState: "issued", Ready: true,
				Hostnames: []api.TenantHostnameResponse{{Hostname: "customer.example", Verified: true}}}}})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	oldOut, oldJSON := osStdout, jsonOutput
	var stdout bytes.Buffer
	osStdout, jsonOutput = &stdout, false
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	if code := cmdPlatformTenants([]string{"activation", "--id", "tenant-id", "--wait", "--timeout", "1s"}); code != 0 {
		t.Fatalf("activation exit = %d", code)
	}
	if calls != 1 || !strings.Contains(stdout.String(), "ready=true") || !strings.Contains(stdout.String(), "customer.example") {
		t.Fatalf("activation calls=%d output=%q", calls, stdout.String())
	}
}
