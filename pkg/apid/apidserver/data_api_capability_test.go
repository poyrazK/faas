// adr: 20261027123814 — the Data API is a managed PostgreSQL sub-feature, a
// data_api binding needs the Data API, and its contract tasks belong to it
// rather than to the app-task capability.
package apidserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

var (
	dataAPITestReleased = platformflags.Definition{Enabled: true, Default: true}
	dataAPITestHidden   = platformflags.Definition{Enabled: true, Default: false}
)

func TestDataAPIContractTasksBelongToTheDataAPI(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	srv := *e.S()
	srv.WithAppTaskAPIEnabled(true)
	if err := srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	apidserver.SeedAppTaskDeployment(t, e, "contract-api")
	srv.SeedPlatformFlag(capabilityFlag(t, "managed-postgres"), dataAPITestReleased)
	srv.SeedPlatformFlag(capabilityFlag(t, "data-api"), dataAPITestReleased)
	srv.SeedPlatformFlag(capabilityFlag(t, "app-tasks"), dataAPITestHidden)

	if r := e.Do(t, http.MethodPost, "/v1/apps/contract-api/tasks", api.CreateAppTaskRequest{Command: []string{"node", "/app/types.mjs"}}, nil); !missingRoute(r) {
		t.Fatalf("app tasks are hidden here: %d %s", r.Code, r.Body)
	}
	for _, kind := range api.DataAPIContractTaskKinds {
		r := e.Do(t, http.MethodPost, "/v1/apps/contract-api/data-api/contract-tasks", api.CreateDataAPIContractTaskRequest{Kind: kind}, nil)
		var task api.AppTaskResponse
		if r.Code != http.StatusAccepted || json.Unmarshal(r.Body.Bytes(), &task) != nil {
			t.Fatalf("contract task %s = %d %s", kind, r.Code, r.Body)
		}
		if !slices.Equal(task.Command, kind.Command()) || task.CommandShell || task.TimeoutSeconds != api.DataAPIContractTaskTimeoutSeconds {
			t.Fatalf("contract task %s ran %+v", kind, task)
		}
		if got := e.Do(t, http.MethodGet, "/v1/apps/contract-api/data-api/contract-tasks/"+task.ID, nil, nil); got.Code != http.StatusOK {
			t.Fatalf("read contract task %s = %d %s", kind, got.Code, got.Body)
		}
	}
	for _, body := range []any{map[string]string{"kind": "shell"}, map[string]any{"kind": "types", "command": []string{"sh"}}, map[string]string{}} {
		if r := e.Do(t, http.MethodPost, "/v1/apps/contract-api/data-api/contract-tasks", body, nil); r.Code == http.StatusAccepted {
			t.Fatalf("contract task %v was admitted: %s", body, r.Body)
		}
	}

	// Any other task of the app is not found through the Data API.
	srv.SeedPlatformFlag(capabilityFlag(t, "app-tasks"), dataAPITestReleased)
	manual := e.Do(t, http.MethodPost, "/v1/apps/contract-api/tasks", api.CreateAppTaskRequest{Command: []string{"bin/maintenance"}}, nil)
	var task api.AppTaskResponse
	if manual.Code != http.StatusAccepted || json.Unmarshal(manual.Body.Bytes(), &task) != nil {
		t.Fatalf("manual task = %d %s", manual.Code, manual.Body)
	}
	if r := e.Do(t, http.MethodGet, "/v1/apps/contract-api/data-api/contract-tasks/"+task.ID, nil, nil); r.Code != http.StatusNotFound || missingRoute(r) {
		t.Fatalf("manual task through the Data API = %d %s, want not found", r.Code, r.Body)
	}

	// The Data API follows managed PostgreSQL.
	srv.SeedPlatformFlag(capabilityFlag(t, "managed-postgres"), dataAPITestHidden)
	if r := e.Do(t, http.MethodPost, "/v1/apps/contract-api/data-api/contract-tasks", api.CreateDataAPIContractTaskRequest{Kind: api.DataAPIContractTypes}, nil); !missingRoute(r) {
		t.Fatalf("contract tasks with managed PostgreSQL hidden = %d %s", r.Code, r.Body)
	}
}

func TestDataAPIBindingNeedsTheDataAPI(t *testing.T) {
	env := apidserver.NewSourceRefTestServer(t, api.PlanPro, "bound-api", 9003)
	_, _, databaseID := apidserver.ConfigureSourceRefManagedPostgres(t, env)
	srv := *env.Srv()
	if err := srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	srv.SeedPlatformFlag(capabilityFlag(t, "managed-postgres"), dataAPITestReleased)
	srv.SeedPlatformFlag(capabilityFlag(t, "data-api"), dataAPITestHidden)
	acct, err := (*env.Store()).AccountByID(context.Background(), *env.AcctID())
	if err != nil {
		t.Fatal(err)
	}
	create := func(access string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(api.CreateManagedPostgresBindingRequest{AppID: *env.AppID(), Scope: "default", EnvironmentKey: "DATABASE_URL", Access: access})
		req := httptest.NewRequest(http.MethodPost, "/v1/postgres/databases/"+databaseID+"/bindings", bytes.NewReader(body))
		req.SetPathValue("id", databaseID)
		response := httptest.NewRecorder()
		srv.CreateManagedPostgresBinding(response, req, acct)
		return response
	}
	if r := create("data_api"); !capabilityRefused(r) {
		t.Fatalf("data_api binding with the Data API hidden = %d %s", r.Code, r.Body)
	}
	if r := create("read_write"); capabilityRefused(r) {
		t.Fatalf("an ordinary binding needs no Data API: %s", r.Body)
	}
	srv.SeedPlatformFlag(capabilityFlag(t, "data-api"), dataAPITestReleased)
	if r := create("data_api"); capabilityRefused(r) {
		t.Fatalf("data_api binding with the Data API released: %s", r.Body)
	}
}
