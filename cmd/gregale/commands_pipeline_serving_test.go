package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPipelineNeonGuideWorksOffline(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	if code := main.Run([]string{"--json", "pipeline", "connection", "guide", "neon"}); code != 0 {
		t.Fatal("provider guidance required a login", code)
	}
}

func TestPipelineServingValidationPrecedesRemoteAllocation(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, "unexpected", 500) }))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	file := filepath.Join(t.TempDir(), "serving.json")
	for _, contents := range []string{`null`, `{"version":2,"columns":{"orders":["id"]}}`, `{"version":1,"unknown":true}`, `{"version":1,"tenant_access":{"column":"tenant_id","memberships":[]}} {}`} {
		if err := os.WriteFile(file, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if code := main.Run([]string{"--json", "pipeline", "create", "orders", "--connection", uuid.NewString(), "--tables", "orders", "--serving-file", file}); code == 0 {
			t.Fatal("invalid serving file accepted", contents)
		}
	}
	if calls != 0 {
		t.Fatal("invalid local contract reached the API", calls)
	}
}

func TestPipelineTenantApprovalUsesFrozenChangeID(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	id, changeID := uuid.NewString(), "saved-preview"
	mutations := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/data/pipelines/" + id:
			main.WriteJSONTest(w, api.DataPipeline{ID: id, Revision: 7})
		case "POST /v1/data/pipelines/" + id + "/tenant-readers":
			mutations++
			var req api.DataPipelineTenantReadersRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.Action != "approve" || req.ChangeID != changeID || req.ExpectedRevision != 7 || len(req.Memberships) != 0 {
				t.Error("approval replaced or lost the saved preview", req)
			}
			main.WriteJSONTest(w, api.DataPipeline{ID: id, Revision: 8, TenantChange: &api.DataPipelineTenantChange{ID: changeID, State: "applied"}})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	if code := main.Run([]string{"--json", "pipeline", "access", id, "--approve", changeID, "--wait"}); code != 0 || mutations != 1 {
		t.Fatal("membership approval failed", code, mutations)
	}
}

func TestPipelineServingCreateChecksProjectionBeforeMirrorAllocation(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	source, database := uuid.NewString(), uuid.NewString()
	allocations, creates := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/data/connections":
			main.WriteJSONTest(w, api.DataConnectionList{Items: []api.DataConnection{{ID: source, Revision: 1}}})
		case "GET /v1/data/pipelines":
			main.WriteJSONTest(w, api.DataPipelineList{Items: []api.DataPipeline{}})
		case "POST /v1/data/connections/" + source + "/check":
			main.WriteJSONTest(w, api.DataConnectionCheck{ID: uuid.NewString(), ConnectionID: source, State: "passed", Fresh: true, Capacity: fixtureReadyPipelineCapacity(), Tables: []api.DataSourceTable{{Name: "orders", Columns: []api.DataSourceColumn{{Name: "id", Type: "integer", PrimaryKey: true}, {Name: "tenant_id", Type: "text"}, {Name: "status", Type: "text"}, {Name: "secret", Unsupported: true}}, Issues: []api.DataConnectionIssue{{Column: "secret", Code: "source_column_unsupported", State: "blocked"}}}}})
		case "POST /v1/postgres/databases":
			allocations++
			main.WriteJSONTest(w, api.ManagedPostgresDatabase{ID: database, State: "ready"})
		case "POST /v1/data/pipelines":
			creates++
			var req api.CreateDataPipelineRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.Serving == nil || req.Serving.TenantAccess == nil || len(req.ReadSubjects) != 0 || len(req.Serving.Columns["orders"]) != 3 || r.Header.Get("Idempotency-Key") == "" {
				t.Error("serving contract lost during creation", req)
			}
			main.WriteJSONTest(w, api.DataPipeline{ID: uuid.NewString(), State: "validating"})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	file := filepath.Join(t.TempDir(), "serving.json")
	for _, columns := range [][]string{{"tenant_id", "status"}, {"id", "tenant_id", "status"}} {
		profile := api.DataPipelineServing{Version: 1, Columns: map[string][]string{"orders": columns}, TenantAccess: &api.DataPipelineTenantAccess{Column: "tenant_id", Memberships: []api.DataPipelineTenantMembership{{Subject: "alice", Tenant: "a"}}}}
		b, _ := json.Marshal(profile)
		if err := os.WriteFile(file, b, 0600); err != nil {
			t.Fatal(err)
		}
		code := main.Run([]string{"--json", "pipeline", "create", "portal-orders", "--connection", source, "--tables", "orders", "--region", "test-region", "--serving-file", file})
		if len(columns) == 2 && (code == 0 || allocations != 0) {
			t.Fatal("projection without key allocated a mirror", code, allocations)
		}
		if len(columns) == 3 && (code != 0 || allocations != 1 || creates != 1) {
			t.Fatal("valid projection failed", code, allocations, creates)
		}
	}
}
