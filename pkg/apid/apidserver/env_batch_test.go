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
	appID := apidserver.MustSeedApp(t, e, "batch-agent")
	apidserver.MustSeedApp(t, e, "batch-other")
	created := e.Do(t, http.MethodPost, "/v1/apps/batch-agent/deploy-tokens", api.CreateDeployTokenRequest{Agent: true, Scopes: []string{api.ScopeEnvWrite}}, nil)
	var token api.DeployTokenResponse
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &token) != nil {
		t.Fatalf("mint app agent: %d %s", created.Code, created.Body.String())
	}
	zero := int64(0)
	proof := strings.Repeat("a", 64)
	data, err := json.Marshal(api.EnvBatchRequest{ExpectedAppID: appID, ReceiptProof: proof, RequestID: uuid.NewString(), ExpectedRevision: &zero, Values: map[string]string{"FIRST": "one", "SECOND": "two"}})
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
	readOnly := e.Do(t, http.MethodPost, "/v1/apps/batch-agent/deploy-tokens", api.CreateDeployTokenRequest{Agent: true, Scopes: []string{api.ScopeAppsRead}}, nil)
	var reader api.DeployTokenResponse
	if readOnly.Code != 201 || json.Unmarshal(readOnly.Body.Bytes(), &reader) != nil {
		t.Fatal("mint reader")
	}
	for _, tc := range []struct {
		app    string
		status int
	}{{"batch-agent", 200}, {"batch-other", 404}} {
		out := e.DoAs(t, http.MethodGet, "/v1/apps/"+tc.app+"/env/batch/requests/"+bodyRequestID(data), nil, map[string]string{api.EnvBatchReceiptProofHeader: proof}, reader.Plaintext)
		if out.Code != tc.status || strings.Contains(out.Body.String(), `"one"`) || strings.Contains(out.Body.String(), `"two"`) || strings.Contains(out.Body.String(), proof) {
			t.Fatalf("%s receipt=%d %s", tc.app, out.Code, out.Body.String())
		}
	}
}

func TestEnvBatchReceiptRequiresOriginalProof(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "batch-proof")
	zero := int64(0)
	proof := strings.Repeat("a", 64)
	request := api.EnvBatchRequest{ExpectedAppID: appID, ReceiptProof: proof, RequestID: uuid.NewString(), ExpectedRevision: &zero, Values: map[string]string{"MODE": "private-value"}}
	path := "/v1/apps/batch-proof/env/batch"
	write := e.Do(t, http.MethodPost, path, request, nil)
	if write.Code != 200 || strings.Contains(write.Body.String(), proof) || strings.Contains(write.Body.String(), "private-value") {
		t.Fatalf("write=%d %s", write.Code, write.Body.String())
	}
	for _, tc := range []struct {
		proof  string
		status int
	}{{"", 404}, {strings.Repeat("b", 64), 404}, {proof, 200}} {
		headers := map[string]string{}
		if tc.proof != "" {
			headers[api.EnvBatchReceiptProofHeader] = tc.proof
		}
		out := e.Do(t, http.MethodGet, path+"/requests/"+request.RequestID, nil, headers)
		if out.Code != tc.status || strings.Contains(out.Body.String(), proof) || strings.Contains(out.Body.String(), "private-value") {
			t.Fatalf("read=%d %s", out.Code, out.Body.String())
		}
	}
}

func bodyRequestID(raw []byte) string {
	var request api.EnvBatchRequest
	_ = json.Unmarshal(raw, &request)
	return request.RequestID
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
	read := e.Do(t, http.MethodGet, path+"/requests/"+body.RequestID, nil, nil)
	if read.Code != 200 || read.Body.String() != first.Body.String() {
		t.Fatalf("read original receipt %d %s", read.Code, read.Body.String())
	}
	if invalid := e.Do(t, http.MethodGet, path+"/requests/not-a-uuid", nil, nil); invalid.Code != 400 {
		t.Fatalf("malformed receipt id=%d", invalid.Code)
	}
	if missing := e.Do(t, http.MethodGet, path+"/requests/"+uuid.NewString(), nil, nil); missing.Code != 404 {
		t.Fatalf("unknown receipt=%d", missing.Code)
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
