package deploymentread

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func declaration() json.RawMessage {
	return json.RawMessage(`[{"name":"metrics","type":"sidecar","image":"registry.example.test/metrics@sha256:` + strings.Repeat("a", 64) + `","preset":"opentelemetry","ram_mb":128,"cpu_millicores":250,"scratch_mb":32,"port":9090,"depends_on":[{"name":"main","condition":"started"}],"env":{"Z_TOKEN":"never-return","A_CONFIG":"sealed:never-return"},"env_secrets":{"DB_URL":"secret:never-return"},"cmd":["never-return"],"unexpected_token":"never-return"}]`)
}

func TestProjectionIncludesOnlyDeclaredMetadata(t *testing.T) {
	status, reason, helpers := projectCompanions(declaration())
	if status != "available" || reason != "" || len(helpers) != 1 {
		t.Fatalf("projection=%s %s %+v", status, reason, helpers)
	}
	helper := helpers[0]
	if helper.Name != "metrics" || helper.RAMMB != 128 || helper.CPUMillicores != 250 || helper.Port != 9090 || strings.Join(helper.EnvironmentKeys, ",") != "A_CONFIG,DB_URL,Z_TOKEN" || len(helper.DependsOn) != 1 {
		t.Fatalf("declared metadata=%+v", helper)
	}
	encoded, _ := json.Marshal(helpers)
	if bytes.Contains(encoded, []byte("never-return")) || bytes.Contains(encoded, []byte(`"cmd"`)) || bytes.Contains(encoded, []byte(`"env"`)) {
		t.Fatal("unsafe projection", string(encoded))
	}
}

func TestProjectionDistinguishesKnownEmptyFromUnavailable(t *testing.T) {
	for _, raw := range []string{"", "null", "[]"} {
		status, reason, helpers := projectCompanions(json.RawMessage(raw))
		if status != "available" || reason != "" || helpers == nil || len(helpers) != 0 {
			t.Fatalf("known empty %q=%s %s %+v", raw, status, reason, helpers)
		}
	}
	for _, raw := range []string{`{}`, `[`, `[null]`, `[{}]`, strings.Replace(string(declaration()), "registry.example.test/metrics@sha256:", "user:credential@registry.example.test/metrics@sha256:", 1)} {
		status, reason, helpers := projectCompanions(json.RawMessage(raw))
		if status != "unavailable" || reason == "" || len(helpers) != 0 || strings.Contains(reason, "credential") {
			t.Fatalf("malformed metadata=%s %s %+v", status, reason, helpers)
		}
	}
}

func ownedFixture(t *testing.T) (Handler, state.Account, state.App, state.Deployment) {
	t.Helper()
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "companions@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "declared-helpers", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "default", Sidecars: declaration()})
	if err != nil {
		t.Fatal(err)
	}
	return Handler{Store: store, Response: func(_ context.Context, d state.Deployment, _ state.App) api.DeploymentResponse {
		return api.DeploymentResponse{ID: d.ID, AppID: d.AppID}
	}}, account, app, deployment
}

func TestOwnedDeploymentReadPreservesIdentityAndLegacyGet(t *testing.T) {
	handler, account, app, deployment := ownedFixture(t)
	request := httptest.NewRequest(http.MethodGet, "/v1/deployments/"+deployment.ID+"/companions", nil)
	request.SetPathValue("id", deployment.ID)
	response := httptest.NewRecorder()
	handler.Companions(response, request, account)
	var result api.DeploymentCompanionsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != 200 || result.AccountID != account.ID || result.AppID != app.ID || result.AppSlug != app.Slug || result.DeploymentID != deployment.ID || result.Scope != "default" || len(result.Companions) != 1 {
		t.Fatalf("owned metadata=%d %+v %v", response.Code, result, err)
	}
	if strings.Contains(response.Body.String(), "never-return") {
		t.Fatal("unsafe response")
	}
	response = httptest.NewRecorder()
	handler.Get(response, request, account)
	var original api.DeploymentResponse
	if err := json.Unmarshal(response.Body.Bytes(), &original); err != nil || response.Code != 200 || original.ID != deployment.ID {
		t.Fatalf("legacy deployment GET=%d %+v %v", response.Code, original, err)
	}
}

func TestForeignAndUnknownScopeExposeNoDeclaration(t *testing.T) {
	handler, account, _, deployment := ownedFixture(t)
	request := httptest.NewRequest(http.MethodGet, "/v1/deployments/"+deployment.ID+"/companions", nil)
	request.SetPathValue("id", deployment.ID)
	foreign := account
	foreign.ID = "another-account"
	for _, read := range []func(http.ResponseWriter, *http.Request, state.Account){handler.Companions, handler.Get} {
		response := httptest.NewRecorder()
		read(response, request, foreign)
		if response.Code != 404 || strings.Contains(response.Body.String(), "metrics") {
			t.Fatalf("foreign read=%d %s", response.Code, response.Body)
		}
	}
	request = httptest.NewRequest(http.MethodGet, "/v1/deployments/"+deployment.ID+"/companions?environment=staging", nil)
	request.SetPathValue("id", deployment.ID)
	response := httptest.NewRecorder()
	handler.Companions(response, request, account)
	if response.Code != 400 {
		t.Fatalf("fake stage scope=%d", response.Code)
	}
}
