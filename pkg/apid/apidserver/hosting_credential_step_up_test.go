package apidserver_test

import (
	"bytes"
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
)

// Issuing or revoking a site automation credential or a GitHub OIDC trust
// goes through the provider-admin mutation policy: an operator session needs
// a step-up from the last five minutes, an operator's own API key is not
// step-up proof, and a cross-site browser request is refused.
func TestHostingCredentialWritesRequireFreshStepUp(t *testing.T) {
	t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", "true")
	env := apidserver.Setup(t, api.PlanHobby)
	(*env.S()).WithAdminAllowlist(env.Acct().Email)
	values := map[string]string{"FAAS_FRONTEND_HOSTING_ENABLED": "true", "FAAS_FRONTEND_HOSTING_AUTOMATION_ENABLED": "true", "FAAS_FRONTEND_HOSTING_DURABLE_ROUTING_ENABLED": "true", "FAAS_FRONTEND_HOSTING_OIDC_ENABLED": "true", "FAAS_FRONTEND_HOSTING_DOMAIN": "hosting.example", "FAAS_FRONTEND_HOSTING_R2_ENDPOINT": "https://example.r2.cloudflarestorage.com", "FAAS_FRONTEND_HOSTING_R2_BUCKET": "hosting", "FAAS_FRONTEND_HOSTING_R2_ACCESS_KEY": "test-key", "FAAS_FRONTEND_HOSTING_R2_SECRET_KEY": "test-secret", "FAAS_FRONTEND_HOSTING_CF_ACCOUNT_ID": strings.Repeat("a", 32), "FAAS_FRONTEND_HOSTING_KV_NAMESPACE_ID": strings.Repeat("b", 32), "FAAS_FRONTEND_HOSTING_CF_TOKEN": "test-token"}
	getenv := func(key string) string { return values[key] }
	if err := (*env.S()).ConfigureHostingForTest(getenv); err != nil {
		t.Fatal(err)
	}
	(*env.S()).WithRuntimeConfigManager(apidserver.NewRuntimeConfigManager(getenv))
	store := *env.Store()
	if _, err := store.CreateProject(t.Context(), state.Project{AccountID: env.Acct().ID, Slug: "step-up"}); err != nil {
		t.Fatal(err)
	}
	created := env.Do(t, "POST", "/v1/internal/hosting/sites", api.CreateHostingSiteRequest{Project: "step-up", Environment: "production", Name: "web"}, nil)
	var site api.HostingSite
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &site) != nil {
		t.Fatal(created.Code, created.Body)
	}
	root := "/v1/internal/hosting/sites/" + site.ID
	rules := api.HostingOIDCTrustRules{RepositoryOwnerID: "123", RepositoryID: "456", Repository: "org/site", Ref: "refs/heads/main", WorkflowRef: "org/site/.github/workflows/preview.yml@refs/heads/main", EventName: "push"}
	now := time.Now().UTC()
	trust, err := store.CreateHostingOIDCTrust(t.Context(), api.HostingOIDCTrust{ID: uuid.NewString(), AccountID: env.Acct().ID, SiteID: site.ID, HostingOIDCTrustRules: rules, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	_, hash, err := api.GenerateHostingAutomationToken()
	if err != nil {
		t.Fatal(err)
	}
	credential, err := store.CreateHostingAutomationCredential(t.Context(), api.HostingAutomationCredential{ID: uuid.NewString(), AccountID: env.Acct().ID, SiteID: site.ID, Label: "CI", Hash: hash, Operations: []string{"stage", "preview"}, CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	// AddAdminSession enrolls the operator's authenticator, as login requires.
	env.AddAdminSession(t, httptest.NewRequest(http.MethodGet, "/", nil))
	cookieAt := func(stepUp time.Time) *http.Cookie {
		sid := uuid.NewString()
		if _, err := store.CreateSession(t.Context(), sid, env.Acct().ID, "192.0.2.40", "hosting-step-up-test"); err != nil {
			t.Fatal(err)
		}
		token, err := (*(*env.S()).Sessions()).IssueWithSessionAndBindingHashAndStepUp(sid, env.Acct().ID, "", stepUp, false)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Cookie{Name: apidserver.SessionCookie, Value: token}
	}
	send := func(method, path string, body any, cookie *http.Cookie, hdrs map[string]string) *httptest.ResponseRecorder {
		var payload []byte
		if body != nil {
			payload, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		for k, v := range hdrs {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		(*env.H()).ServeHTTP(rec, req)
		return rec
	}
	crossSite := map[string]string{"Origin": "https://attacker.example", "Sec-Fetch-Site": "cross-site"}
	for _, tc := range []struct {
		name, method, path string
		body               any
		want               int
	}{
		{"create OIDC trust", http.MethodPost, root + "/oidc-trusts", rules, http.StatusCreated},
		{"revoke OIDC trust", http.MethodDelete, root + "/oidc-trusts/" + trust.ID, nil, http.StatusNoContent},
		{"create automation credential", http.MethodPost, root + "/automation-credentials", api.CreateHostingAutomationCredentialRequest{Label: "CI"}, http.StatusCreated},
		{"revoke automation credential", http.MethodDelete, root + "/automation-credentials/" + credential.ID, nil, http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			apidserver.AssertProblem(t, env.Do(t, tc.method, tc.path, tc.body, nil), http.StatusForbidden, api.CodeStepUpRequired)
			apidserver.AssertProblem(t, send(tc.method, tc.path, tc.body, cookieAt(time.Now().Add(-10*time.Minute)), nil), http.StatusForbidden, api.CodeStepUpRequired)
			apidserver.AssertProblem(t, send(tc.method, tc.path, tc.body, cookieAt(time.Now()), crossSite), http.StatusForbidden, api.CodeForbidden)
			if fresh := send(tc.method, tc.path, tc.body, cookieAt(time.Now()), nil); fresh.Code != tc.want {
				t.Fatalf("fresh step-up = %d, want %d: %s", fresh.Code, tc.want, fresh.Body)
			}
		})
	}
}
