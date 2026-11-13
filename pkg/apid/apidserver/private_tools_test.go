package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"filippo.io/age"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant/granttest"
)

func TestPrivateToolInvocationOperatorGate(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			flag := ""
			if enabled {
				flag = "1"
			}
			t.Setenv("FAAS_MCP_PRIVATE_TOOL_INVOCATION", flag)
			env, _ := apidserver.SetupChangePlan(t, api.PlanPro, "")
			req := httptest.NewRequest(http.MethodPost, "/v1/keys", strings.NewReader(`{"label":"private-agent","scopes":["agent"]}`))
			req.Header.Set("Authorization", "Bearer "+*env.Key())
			req.Header.Set("Content-Type", "application/json")
			created := httptest.NewRecorder()
			(*env.H()).ServeHTTP(created, req)
			var key api.APIKeyResponse
			if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &key) != nil {
				t.Fatalf("agent key: %d %s", created.Code, created.Body.String())
			}
			for _, tc := range []struct {
				method, path, token string
				want                int
			}{
				{http.MethodGet, "/v1/tool-oauth-connections", *env.Key(), http.StatusOK},
				{http.MethodGet, "/v1/tool-oauth-connections", key.Plaintext, http.StatusForbidden},
				{http.MethodPut, "/v1/tool-oauth-connections/" + uuid.NewString(), key.Plaintext, http.StatusForbidden},
				{http.MethodDelete, "/v1/tool-oauth-connections/" + uuid.NewString(), key.Plaintext, http.StatusForbidden},
				{http.MethodGet, "/v1/tool-oauth-connections/" + uuid.NewString() + "/authorization", key.Plaintext, http.StatusForbidden},
				{http.MethodPost, "/v1/tool-oauth-connections/" + uuid.NewString() + "/authorization", key.Plaintext, http.StatusForbidden},
				{http.MethodGet, "/v1/tool-oauth-connections/" + uuid.NewString() + "/authorization", *env.Key(), http.StatusNotFound},
				{http.MethodGet, "/v1/delegated-tool-connections/" + uuid.NewString(), key.Plaintext, http.StatusNotFound},
			} {
				request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
				request.Header.Set("Authorization", "Bearer "+tc.token)
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				(*env.H()).ServeHTTP(response, request)
				if response.Code != tc.want {
					t.Fatalf("%s %s: %d %s", tc.method, tc.path, response.Code, response.Body.String())
				}
			}
			req = httptest.NewRequest(http.MethodPost, "/v1/delegated-tool-invocations/"+uuid.NewString()+"/execute", strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+key.Plaintext)
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			(*env.H()).ServeHTTP(response, req)
			want := http.StatusServiceUnavailable
			if enabled {
				want = http.StatusBadRequest
			}
			if response.Code != want {
				t.Fatalf("private API gate: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestToolConnectionRequiresSeparateAccountSealDomain(t *testing.T) {
	fleet, account := mustSealIdentity(t), mustSealIdentity(t)
	oldIDs, oldRuntime, oldRecipient := *apidserver.AccountDomainIdentities, *apidserver.RuntimeDomainRecipient, *apidserver.SetSecretRecipient
	t.Cleanup(func() {
		*apidserver.AccountDomainIdentities = oldIDs
		*apidserver.RuntimeDomainRecipient = oldRuntime
		*apidserver.SetSecretRecipient = oldRecipient
	})
	*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return fleet.Recipient() }
	*apidserver.RuntimeDomainRecipient = func() *age.X25519Recipient { return fleet.Recipient() }
	for _, tc := range []struct {
		name      string
		ids       []*age.X25519Identity
		wantError bool
	}{
		{"unstaged", nil, true}, {"fleet fallback", []*age.X25519Identity{fleet}, true}, {"separate account", []*age.X25519Identity{account}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			*apidserver.AccountDomainIdentities = tc.ids
			recipients, err := apidserver.ToolConnectionRecipientsForTest()
			if (err != nil) != tc.wantError {
				t.Fatalf("account recipient isolation: %v", err)
			}
			if !tc.wantError && (len(recipients) != 1 || recipients[0].String() != account.Recipient().String()) {
				t.Fatal("execution credentials included a legacy runtime recipient")
			}
		})
	}
	*apidserver.AccountDomainIdentities = []*age.X25519Identity{account}
	*apidserver.RuntimeDomainRecipient = func() *age.X25519Recipient { return account.Recipient() }
	if _, err := apidserver.ToolConnectionRecipientsForTest(); err == nil {
		t.Fatal("runtime key reused for execution credentials")
	}
}

func TestPrivateToolPinChecksExactReplicas(t *testing.T) {
	env, server := apidserver.SetupChangePlan(t, api.PlanPro, "")
	store, acct := *env.Store(), *env.Acct()
	app, p := granttest.Publication(t, store, acct, "private-pin", "", strings.Repeat("f", 64))
	instance, err := store.CreateInstance(t.Context(), app.ID, p.Outcome.DeploymentID, string(state.StateRunning), 256, "", "")
	if err != nil {
		t.Fatal(err)
	}
	target := api.ToolInvocationTarget{App: app.Slug, AppID: app.ID, DeploymentID: p.Outcome.DeploymentID, CatalogSHA256: p.Outcome.CatalogSHA256}
	var mode atomic.Value
	mode.Store("valid")
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("deployment_id") != target.DeploymentID || r.URL.Query().Get("instance_id") != instance.ID || r.URL.Query().Get("host") != "preview.example" {
			t.Error("pin probe escaped exact native target")
		}
		selected := mode.Load().(string)
		sha := target.CatalogSHA256
		if selected == "changed pin" {
			sha = strings.Repeat("0", 64)
		}
		health := map[string]any{"status": "ok", "catalog_sha256": sha, "catalog_revision": 1, "instance_id": instance.ID}
		if selected == "missing revision" {
			delete(health, "catalog_revision")
		}
		body, _ := json.Marshal(health)
		result := api.MCPInstanceHealthProbeResponse{AppID: app.ID, DeploymentID: target.DeploymentID, InstanceID: instance.ID, HTTPStatus: http.StatusOK, HealthBody: string(body)}
		if selected == "wrong identity" {
			result.InstanceID = uuid.NewString()
		}
		_ = json.NewEncoder(w).Encode(result)
	}))
	t.Cleanup(gateway.Close)
	server.WithGatewaydControlURL(gateway.URL)
	for _, test := range []string{"valid", "changed pin", "missing revision", "wrong identity"} {
		mode.Store(test)
		err := server.VerifyPrivateToolPinForTest(t.Context(), target, "https://preview.example/mcp")
		if (err == nil) != (test == "valid") {
			t.Fatalf("%s pin check: %v", test, err)
		}
	}
}
