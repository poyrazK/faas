// adr: 650 — schema-generated APIs reuse durable app, binding and task lifecycles.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDataAPITypesAtomicWriteAndCheck(t *testing.T) {
	file := filepath.Join(t.TempDir(), "database.types.ts")
	if err := writeDataAPITypes(file, false, "first"); err != nil {
		t.Fatal(err)
	}
	if err := writeDataAPITypes(file, true, "first"); err != nil {
		t.Fatal(err)
	}
	if err := writeDataAPITypes(file, true, "changed"); err == nil {
		t.Fatal("stale contract accepted")
	}
	current, _ := os.ReadFile(file)
	if string(current) != "first" {
		t.Fatal("check modified the source")
	}
	if err := writeDataAPITypes(file, false, "changed"); err != nil {
		t.Fatal(err)
	}
	current, _ = os.ReadFile(file)
	if string(current) != "changed" {
		t.Fatal("generation did not replace the file")
	}
}

func TestDataAPICreateDeploysRuntimeAfterScopedConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		resume     bool
	}{
		{name: "create"},
		{name: "resume-bearer", resume: true, mode: "bearer"},
		{name: "resume-ip-allowlist", resume: true, mode: "ip_allowlist"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testDataAPICreateDeploysRuntime(t, tc.resume, tc.mode)
		})
	}
}

func testDataAPICreateDeploysRuntime(t *testing.T, resume bool, mode string) {
	resetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Chdir(t.TempDir())
	const database = "00000000-0000-0000-0000-000000000001"
	var dockerfile, source int32
	base := zeroConfigDeployServer(t, "notes-data", &dockerfile, &source, nil)
	defer base.Close()
	settings := 0
	created := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/postgres/databases":
			writeJSONTest(w, api.ManagedPostgresDatabaseList{Items: []api.ManagedPostgresDatabase{{ID: database, State: "ready"}}})
		case r.Method == "POST" && r.URL.Path == "/v1/apps":
			var body api.CreateAppRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if body.RequireAuthn == nil || *body.RequireAuthn {
				t.Error("application JWTs would be blocked by the paid-plan owner-key default")
			}
			created++
			writeJSONTest(w, api.AppResponse{ID: "app", Slug: "notes-data", Type: "app"})
		case r.Method == "GET" && r.URL.Path == "/v1/apps/notes-data":
			writeJSONTest(w, map[string]any{"id": "app", "slug": "notes-data", "type": "app", "require_authn": true, "public_auth": map[string]string{"mode": mode}})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/bindings"):
			writeJSONTest(w, api.ManagedPostgresBinding{ID: "binding", State: "ready"})
		case r.Method == "PATCH" || r.Method == "PUT":
			if r.Method == "PATCH" && r.URL.Path == "/v1/apps/notes-data" {
				var body api.UpdateAppRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if body.RequireAuthn == nil || *body.RequireAuthn {
					t.Error("application JWTs would be blocked by inherited edge authentication")
				}
				if mode == "ip_allowlist" {
					if body.PublicAuth != nil {
						t.Error("resume removed an explicit ingress restriction")
					}
				} else if body.PublicAuth == nil || body.PublicAuth.Mode != "open" {
					t.Error("application JWTs would be blocked by owner-key public authentication")
				}
			}
			settings++
			writeJSONTest(w, map[string]any{})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/deployments"):
			if settings != 6 {
				t.Errorf("deployment preceded configuration: %d writes", settings)
			}
			base.Config.Handler.ServeHTTP(w, r)
		default:
			base.Config.Handler.ServeHTTP(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	args := []string{"create", "notes-data", "--database", database, "--issuer", "https://issuer.example", "--jwks-url", "https://issuer.example/jwks", "--audience", "notes"}
	if resume {
		args = append(args, "--resume")
	}
	if code := cmdDataAPI(args); code != 0 {
		t.Fatal("create failed", code)
	}
	wantCreated := 1
	if resume {
		wantCreated = 0
	}
	if created != wantCreated || atomic.LoadInt32(&dockerfile) != 1 || atomic.LoadInt32(&source) != 1 {
		t.Fatalf("incomplete create: created=%d dockerfile=%d source=%d", created, dockerfile, source)
	}
}

func TestDataAPIRefreshUsesFreshLifecycle(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"app_id":"id","wake_id":"wake"}`, http.StatusOK)
	if code := cmdDataAPI([]string{"refresh", "notes"}); code != 0 {
		t.Fatal("refresh failed", code)
	}
	if f.sawMethod != "POST" || f.sawPath != "/v1/apps/notes/restart" || f.sawQuery != "fresh=true" {
		t.Fatalf("wrong restart: %s %s?%s", f.sawMethod, f.sawPath, f.sawQuery)
	}
}

func TestDataAPIRefreshJSON(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	authedFakeAPI(t, `{"app_id":"id","wake_id":"wake"}`, http.StatusOK)
	if code := cmdDataAPI([]string{"refresh", "notes"}); code != 0 {
		t.Fatal("JSON refresh failed", code)
	}
}

func TestDataAPITypesUsesBoundedPrivateTaskAndRejectsTruncation(t *testing.T) {
	for _, truncated := range []bool{false, true} {
		t.Run(fmt.Sprint(truncated), func(t *testing.T) {
			resetJSONOut(t)
			content := "// Generated by gregale data-api types. Do not edit.\n// Schema fingerprint: " + strings.Repeat("a", 64) + "\nexport type Database = {}\n"
			response, _ := json.Marshal(map[string]any{"id": "task", "status": "succeeded", "stdout_tail": content, "output_truncated": truncated, "deployment_id": "deploy"})
			f := authedFakeAPI(t, string(response), http.StatusCreated)
			path := filepath.Join(t.TempDir(), "types.ts")
			_ = os.WriteFile(path, []byte("original"), 0600)
			code := cmdDataAPI([]string{"types", "notes", "--output", path})
			if (code != 0) != truncated {
				t.Fatalf("exit=%d truncated=%v", code, truncated)
			}
			if f.sawMethod != "POST" || f.sawPath != "/v1/apps/notes/tasks" {
				t.Fatalf("wrong task endpoint %s %s", f.sawMethod, f.sawPath)
			}
			var request api.CreateAppTaskRequest
			if err := json.Unmarshal(f.sawBody, &request); err != nil {
				t.Fatal(err)
			}
			if strings.Join(request.Command, " ") != "node /app/types.mjs" || request.MaxOutputBytes != api.DataAPIMaxOutputBytes || request.TimeoutSeconds != 60 {
				t.Fatalf("unbounded task: %+v", request)
			}
			got, _ := os.ReadFile(path)
			want := content
			if truncated {
				want = "original"
			}
			if string(got) != want {
				t.Fatal("invalid task output replaced the contract")
			}
		})
	}
}

func TestDataAPIConfiguresBindingEgressAndScopedSecrets(t *testing.T) {
	var writes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writes = append(writes, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/v1/postgres/databases/db/bindings":
			var body api.CreateManagedPostgresBindingRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.AppID != "app" || body.Scope != "staging" || body.Access != "data_api" || body.EnvironmentKey != "DATABASE_URL" || r.Header.Get("Idempotency-Key") == "" {
				t.Errorf("wrong binding: %+v", body)
			}
			writeJSONTest(w, api.ManagedPostgresBinding{ID: "binding", State: "ready"})
		case r.Method == "PATCH":
			var body api.UpdateAppRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.EgressPorts == nil || len(*body.EgressPorts) != 1 || (*body.EgressPorts)[0] != 5432 {
				t.Error("missing plan-gated egress")
			}
			if body.RequireAuthn == nil || *body.RequireAuthn || body.PublicAuth == nil || body.PublicAuth.Mode != "open" {
				t.Error("application JWT authentication was not configured")
			}
			writeJSONTest(w, api.AppResponse{ID: "app", Slug: "notes"})
		case r.Method == "PUT":
			if r.URL.Query().Get("scope") != "staging" {
				t.Error("secret scope lost")
			}
			writeJSONTest(w, map[string]any{})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	err := configureDataAPI(context.Background(), api.NewClient(server.URL, "token"), api.AppResponse{ID: "app", Slug: "notes"}, "db", "staging", map[string]string{"DATA_API_SCHEMAS": "api"})
	if err != nil {
		t.Fatal(err)
	}
	if len(writes) != 7 || !strings.HasSuffix(writes[0], "/bindings") || writes[1] != "PATCH /v1/apps/notes" {
		t.Fatalf("wrong configuration order: %v", writes)
	}
}

func TestDataAPIRuntimeBounds(t *testing.T) {
	config, err := os.ReadFile("templates/data-api/config.mjs")
	if err != nil {
		t.Fatal(err)
	}
	for name, bound := range map[string]int{"pool": api.DataAPIMaxPoolConnections, "rows": api.DataAPIMaxRows, "bodyBytes": api.DataAPIMaxRequestBytes, "outputBytes": api.DataAPIMaxOutputBytes, "queryMs": api.DataAPIQueryTimeoutMS, "relations": api.DataAPIMaxRelations, "columns": api.DataAPIMaxColumns, "types": api.DataAPIMaxTypes} {
		if !strings.Contains(string(config), fmt.Sprintf("%s: %d", name, bound)) {
			t.Fatalf("runtime %s differs from canonical limit", name)
		}
	}
}

func TestDataAPIRejectsInvalidIssuerConfiguration(t *testing.T) {
	for _, value := range []string{"http://issuer.example", "https://user:password@issuer.example", "https:///missing", "https://issuer.example/#fragment"} {
		if dataAPIHTTPS(value) {
			t.Fatalf("accepted %q", value)
		}
	}
	if !dataAPIHTTPS("https://issuer.example/jwks") {
		t.Fatal("valid issuer rejected")
	}
	for _, value := range []string{"*", "https://app.example/path", "https://app.example?token=x"} {
		if dataAPIOrigin(value) {
			t.Fatalf("accepted origin %q", value)
		}
	}
}
