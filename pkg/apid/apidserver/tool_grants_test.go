package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant/granttest"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func TestToolGrantNativeProjectBoundary(t *testing.T) {
	env, _ := apidserver.SetupChangePlan(t, api.PlanPro, "")
	store, acct := *env.Store(), *env.Acct()
	ctx := t.Context()
	project, err := store.CreateProject(ctx, state.Project{ID: uuid.NewString(), AccountID: acct.ID, Slug: "tool-project"})
	if err != nil {
		t.Fatal(err)
	}
	_, publication := granttest.Publication(t, store, acct, "scoped-tools", project.ID, strings.Repeat("a", 64))
	_, outside := granttest.Publication(t, store, acct, "outside-tools", "", strings.Repeat("b", 64))
	call := func(token, method, path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		(*env.H()).ServeHTTP(w, r)
		return w
	}
	keyReply := call(*env.Key(), http.MethodPost, "/v1/keys", map[string]any{"label": "tool-agent", "scopes": []string{api.ScopeAgent}, "projects": []string{project.Slug}})
	var key api.APIKeyResponse
	if keyReply.Code != http.StatusCreated || json.Unmarshal(keyReply.Body.Bytes(), &key) != nil {
		t.Fatalf("key: %d %s", keyReply.Code, keyReply.Body.String())
	}
	in := api.ToolGrantIntent{KeyID: key.ID, Version: publication.ID, Name: "visible", ExpiresAt: time.Now().UTC().Add(time.Hour), AllowInvocation: true}
	id := uuid.NewString()
	path := "/v1/tool-grants/" + id
	if w := call(key.Plaintext, http.MethodPut, path, in); w.Code != http.StatusForbidden {
		t.Fatalf("self-grant: %d %s", w.Code, w.Body.String())
	}
	if w := call(*env.Key(), http.MethodPut, path, in); w.Code != http.StatusOK {
		t.Fatalf("owner grant: %d %s", w.Code, w.Body.String())
	}
	for endpoint, want := range map[string]int{
		"/v1/tool-registry": http.StatusOK,
		"/v1/tool-registry/" + publication.ID + "/tools/visible": http.StatusOK,
		"/v1/tool-registry/" + publication.ID + "/tools/hidden":  http.StatusNotFound,
		"/v1/tool-registry/" + outside.ID + "/tools/visible":     http.StatusNotFound,
		"/v1/tool-publications/" + publication.ID:                http.StatusForbidden,
		"/v1/tool-grants":                          http.StatusForbidden,
		"/v1/tool-grants/" + id:                    http.StatusForbidden,
		"/v1/tool-invocations/" + uuid.NewString(): http.StatusForbidden,
	} {
		if w := call(key.Plaintext, http.MethodGet, endpoint, nil); w.Code != want {
			t.Errorf("GET %s: %d %s; want %d", endpoint, w.Code, w.Body.String(), want)
		}
	}
	wrong := in
	wrong.Version = outside.ID
	if w := call(*env.Key(), http.MethodPut, "/v1/tool-grants/"+uuid.NewString(), wrong); w.Code != http.StatusNotFound {
		t.Fatalf("grant widened project scope: %d %s", w.Code, w.Body.String())
	}
	if w := call(key.Plaintext, http.MethodDelete, path, nil); w.Code != http.StatusForbidden {
		t.Fatal("agent revoked delegation")
	}
	invocationID := uuid.NewString()
	invocationPath := "/v1/delegated-tool-invocations/" + invocationID
	invocation := api.DelegatedToolInvocationIntent{GrantID: id, Intent: api.ToolInvocationIntent{Version: publication.ID, Name: "visible", ArgumentsSHA256: toolpublication.Digest([]byte(`{}`)), AccessMode: "open", TimeoutSeconds: 60, AllowToolExecution: true}}
	if w := call(key.Plaintext, http.MethodPut, invocationPath, invocation); w.Code != http.StatusOK {
		t.Fatalf("delegated creation through project middleware: %d %s", w.Code, w.Body.String())
	}
	if w := call(key.Plaintext, http.MethodGet, invocationPath, nil); w.Code != http.StatusOK {
		t.Fatalf("delegated receipt through project middleware: %d %s", w.Code, w.Body.String())
	}
	if w := call(key.Plaintext, http.MethodPost, invocationPath+"/dispatch", map[string]any{}); w.Code != http.StatusOK {
		t.Fatalf("delegated dispatch without deploy-write: %d %s", w.Code, w.Body.String())
	}
	if w := call(*env.Key(), http.MethodPost, "/v1/tool-invocations/"+invocationID+"/dispatch", map[string]any{}); w.Code != http.StatusConflict {
		t.Fatalf("owner dispatch bypassed grant fence: %d %s", w.Code, w.Body.String())
	}
	if w := call(key.Plaintext, http.MethodPost, invocationPath+"/outcome", api.ToolInvocationOutcome{Status: "outcome_unknown", Code: "worker_interrupted"}); w.Code != http.StatusOK {
		t.Fatalf("delegated outcome: %d %s", w.Code, w.Body.String())
	}
	if w := call(*env.Key(), http.MethodDelete, path, nil); w.Code != http.StatusOK {
		t.Fatalf("owner revoke: %d %s", w.Code, w.Body.String())
	}
	if w := call(key.Plaintext, http.MethodGet, "/v1/tool-registry/"+publication.ID+"/tools/visible", nil); w.Code != http.StatusNotFound {
		t.Fatalf("revoked discovery: %d %s", w.Code, w.Body.String())
	}
	if w := call(key.Plaintext, http.MethodGet, invocationPath, nil); w.Code != http.StatusNotFound {
		t.Fatalf("revoked invocation receipt: %d %s", w.Code, w.Body.String())
	}
}
