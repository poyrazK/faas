package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant/granttest"
)

func TestHostedToolPublisherNativeAuthority(t *testing.T) {
	env, _ := apidserver.SetupChangePlan(t, api.PlanPro, "")
	store, acct := *env.Store(), *env.Acct()
	_, published := granttest.Publication(t, store, acct, "published-tools", "", strings.Repeat("a", 64))
	project, err := store.CreateProject(t.Context(), state.Project{ID: uuid.NewString(), AccountID: acct.ID, Slug: "limited"})
	if err != nil {
		t.Fatal(err)
	}
	call := func(token, method, path string, body any) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, strings.NewReader(string(data)))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		(*env.H()).ServeHTTP(w, req)
		return w
	}
	createKey := func(label string, scopes, projects []string) api.APIKeyResponse {
		response := call(*env.Key(), http.MethodPost, "/v1/keys", map[string]any{"label": label, "scopes": scopes, "projects": projects})
		var key api.APIKeyResponse
		if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &key) != nil {
			t.Fatalf("key: %d %s", response.Code, response.Body.String())
		}
		return key
	}
	publisher := createKey("publisher", []string{api.ScopeAgent, api.ScopeDeployWrite}, nil)
	reader := createKey("reader", []string{api.ScopeAgent}, nil)
	limited := createKey("limited publisher", []string{api.ScopeAgent, api.ScopeDeployWrite}, []string{project.Slug})
	id := strings.Repeat("b", 64)
	path := "/v1/tool-publications/" + id
	in := api.ToolPublicationIntent{App: "new-tools", SourceSHA256: strings.Repeat("c", 64), FixturesSHA256: strings.Repeat("d", 64), AccessMode: "open", EndpointPath: "/mcp", ToolScopes: map[string][]string{"add": {}}, TestCases: []string{"adds"}}
	for _, key := range []api.APIKeyResponse{reader, limited} {
		for _, method := range []string{http.MethodGet, http.MethodPut} {
			response := call(key.Plaintext, method, path, in)
			if response.Code != http.StatusForbidden {
				t.Fatalf("restricted publisher %s %s: %d %s", key.Label, method, response.Code, response.Body.String())
			}
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		response := call(publisher.Plaintext, http.MethodPut, path, in)
		var begin api.BeginToolPublicationResponse
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &begin) != nil || begin.Created != (attempt == 0) {
			t.Fatalf("begin: %d %s", response.Code, response.Body.String())
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		target := path
		if method == http.MethodPost {
			target += "/outcome"
		}
		response := call(publisher.Plaintext, method, target, api.ToolPublicationOutcome{Status: "failed"})
		if response.Code != http.StatusOK {
			t.Fatalf("publisher journal: %d %s", response.Code, response.Body.String())
		}
	}
	if response := call(publisher.Plaintext, http.MethodGet, "/v1/tool-publications/"+published.ID, nil); response.Code != http.StatusOK {
		t.Fatalf("account journal authority: %d", response.Code)
	}
	response := call(publisher.Plaintext, http.MethodGet, "/v1/tool-registry", nil)
	var page api.ToolRegistryPage
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Versions) != 0 {
		t.Fatalf("publishing opened registry: %d %s", response.Code, response.Body.String())
	}
	for _, target := range []string{"/v1/tool-registry/" + published.ID + "/tools/visible", "/v1/tool-grants", "/v1/tool-invocations/" + uuid.NewString()} {
		response := call(publisher.Plaintext, http.MethodGet, target, nil)
		if response.Code != http.StatusForbidden && response.Code != http.StatusNotFound {
			t.Fatalf("publishing widened authority: %s %d", target, response.Code)
		}
	}
	if response := call(*env.Key(), http.MethodDelete, "/v1/keys/"+publisher.ID, nil); response.Code != http.StatusNoContent && response.Code != http.StatusOK {
		t.Fatalf("revoke key: %d", response.Code)
	}
	if response := call(publisher.Plaintext, http.MethodGet, path, nil); response.Code != http.StatusUnauthorized && response.Code != http.StatusForbidden {
		t.Fatalf("revoked journal read: %d", response.Code)
	}
}
