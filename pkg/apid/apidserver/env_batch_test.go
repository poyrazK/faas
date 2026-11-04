// adr: 20261103171917
package apidserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestAtomicEnvBatchAppBoundAuthorization(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "batch-agent")
	apidserver.MustSeedApp(t, e, "batch-other")
	created := e.Do(t, http.MethodPost, "/v1/apps/batch-agent/deploy-tokens", api.CreateDeployTokenRequest{Agent: true, Scopes: []string{api.ScopeEnvWrite}}, nil)
	var token api.DeployTokenResponse
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &token) != nil {
		t.Fatalf("mint app agent: %d %s", created.Code, created.Body.String())
	}
	zero := int64(0)
	data, err := json.Marshal(api.EnvBatchRequest{RequestID: uuid.NewString(), ExpectedRevision: &zero, Values: map[string]string{"FIRST": "one", "SECOND": "two"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		app    string
		status int
	}{{"batch-agent", 200}, {"batch-other", 404}} {
		r := httptest.NewRequest(http.MethodPost, "/v1/apps/"+tc.app+"/env/batch", bytes.NewReader(data))
		r.Header.Set("Authorization", "Bearer "+token.Plaintext)
		r.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		(*e.H()).ServeHTTP(out, r)
		if out.Code != tc.status {
			t.Fatalf("%s batch status %d: %s", tc.app, out.Code, out.Body.String())
		}
	}
}

func TestAtomicEnvBatchHTTP(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "atomic-config")
	zero := int64(0)
	body := api.EnvBatchRequest{RequestID: uuid.NewString(), ExpectedRevision: &zero, Values: map[string]string{"FIRST": "one", "SECOND": "two"}}
	path := "/v1/apps/atomic-config/env/batch"
	first := e.Do(t, http.MethodPost, path, body, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("batch %d %s", first.Code, first.Body.String())
	}
	var receipt api.EnvBatchResponse
	if err := json.Unmarshal(first.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.AppID != appID || receipt.ConfigChange.Revision != 2 || len(receipt.UpdatedKeys) != 2 || strings.Contains(first.Body.String(), "\"one\"") {
		t.Fatalf("invalid receipt: %s", first.Body.String())
	}
	if replay := e.Do(t, http.MethodPost, path, body, nil); replay.Code != 200 || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay %d %s", replay.Code, replay.Body.String())
	}
	changed := body
	changed.Values = map[string]string{"FIRST": "changed"}
	if out := e.Do(t, http.MethodPost, path, changed, nil); out.Code != 409 || !strings.Contains(out.Body.String(), api.CodeEnvBatchRequestConflict) {
		t.Fatalf("changed request %d %s", out.Code, out.Body.String())
	}
	changed.RequestID = uuid.NewString()
	if out := e.Do(t, http.MethodPost, path, changed, nil); out.Code != 409 || !strings.Contains(out.Body.String(), api.CodeEnvBatchRevisionConflict) {
		t.Fatalf("stale revision %d %s", out.Code, out.Body.String())
	}
	changed.ExpectedRevision = &receipt.ConfigChange.Revision
	changed.Values = nil
	changed.Delete = []string{"SECOND"}
	if out := e.Do(t, http.MethodPost, path, changed, nil); out.Code != 200 || !strings.Contains(out.Body.String(), `"updated_keys":[]`) {
		t.Fatalf("delete batch %d %s", out.Code, out.Body.String())
	}
	if out := e.Do(t, http.MethodPost, path+"?scope=__all__", body, nil); out.Code != 400 {
		t.Fatalf("all scopes write=%d", out.Code)
	}
	if out := e.Do(t, http.MethodPost, "/v1/apps/missing/env/batch", body, nil); out.Code != 404 {
		t.Fatalf("missing app=%d", out.Code)
	}
}

func TestManagedPostgresReservedCreationIdentityHTTP(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.AttachTestManagedPostgres(t, *e.S(), 3)
	*e.H() = (*e.S()).Handler()
	id := uuid.NewString()
	body := api.CreateManagedPostgresDatabaseRequest{RequestID: id, Name: "durable-agent-db", Region: "eu", PostgresMajor: 17}
	path := "/v1/postgres/databases"
	for range 2 {
		out := e.Do(t, http.MethodPost, path, body, nil)
		if out.Code != http.StatusCreated {
			t.Fatalf("create %d %s", out.Code, out.Body.String())
		}
		var database api.ManagedPostgresDatabase
		if err := json.Unmarshal(out.Body.Bytes(), &database); err != nil || database.ID != id {
			t.Fatalf("reserved ID not preserved: %+v %v", database, err)
		}
	}
	body.RequestID = uuid.NewString()
	if out := e.Do(t, http.MethodPost, path, body, nil); out.Code != 409 {
		t.Fatalf("adopted another reservation: %d %s", out.Code, out.Body.String())
	}
	body.RequestID = id
	body.Name = "another-name"
	if out := e.Do(t, http.MethodPost, path, body, nil); out.Code != 409 {
		t.Fatalf("reused identity changed database name: %d %s", out.Code, out.Body.String())
	}
	body.RequestID = "invalid"
	if out := e.Do(t, http.MethodPost, path, body, nil); out.Code != 400 {
		t.Fatalf("invalid reserved ID: %d", out.Code)
	}
}
