// adr: 20261104085712
package apidserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// A hosted MCP sign-in limited to a project on the approval page gets a key
// limited to it, without database capabilities, that lists only the
// project's apps; a project of another account is refused at approval.
func TestHandlers_HostedSignInLimitedToAProject(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	const email = "project-agent@example.com"
	acct, err := store.CreateAccount(ctx, email, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{ID: uuid.NewString(), AccountID: acct.ID, Slug: "api"})
	if err != nil {
		t.Fatal(err)
	}
	for slug, projectID := range map[string]string{"api-svc": project.ID, "other-svc": ""} {
		if _, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Type: state.AppTypeApp, ProjectID: projectID}); err != nil {
			t.Fatal(err)
		}
	}

	code := mintScopedMCPCode(t, srv, api.ScopeDeployWrite, api.ScopeManagedPostgresManage)
	cookie, csrf := apidserver.RenderCliAuthForTestAs(t, srv, code, email)
	if claim := apidserver.PostCliAuthFormForTest(t, srv, code, email, cookie, csrf, url.Values{"project": {project.ID}}); claim.Code != http.StatusFound {
		t.Fatalf("approve limited to a project = %d %s", claim.Code, claim.Body)
	}
	exchangeBody, _ := json.Marshal(api.CliAuthExchangeRequest{Code: code})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/cli-auth/exchange", bytes.NewReader(exchangeBody)))
	var exchange api.CliAuthExchangeResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &exchange) != nil {
		t.Fatalf("exchange = %d %s", rec.Code, rec.Body)
	}
	key, err := store.APIKeyByHash(ctx, api.HashToken([]byte(exchange.Plaintext)))
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(key.Scopes, api.ScopeManagedPostgresManage) || !slices.Contains(key.Scopes, api.ScopeDeployWrite) {
		t.Fatalf("limited sign-in key scopes = %v; want deploy:write without postgres:manage", key.Scopes)
	}
	if ids, limited, err := store.APIKeyProjectLimit(ctx, key.ID); err != nil || !limited || !slices.Equal(ids, []string{project.ID}) {
		t.Fatalf("key limit = %v, %v, %v; want [%s]", ids, limited, err, project.ID)
	}
	list := httptest.NewRequest(http.MethodGet, "/v1/apps", nil)
	list.Header.Set("Authorization", "Bearer "+exchange.Plaintext)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, list)
	var apps []api.AppResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &apps) != nil || len(apps) != 1 || apps[0].Slug != "api-svc" {
		t.Fatalf("GET /v1/apps with the limited sign-in = %d %s; want only api-svc", rec.Code, rec.Body)
	}

	stranger, err := store.CreateAccount(ctx, "stranger-projects@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateProject(ctx, state.Project{ID: uuid.NewString(), AccountID: stranger.ID, Slug: "theirs"})
	if err != nil {
		t.Fatal(err)
	}
	refused := mintScopedMCPCode(t, srv)
	cookie, csrf = apidserver.RenderCliAuthForTestAs(t, srv, refused, email)
	claim := apidserver.PostCliAuthFormForTest(t, srv, refused, email, cookie, csrf, url.Values{"project": {foreign.ID}})
	if claim.Code != http.StatusOK || !strings.Contains(claim.Body.String(), "Project not in this organization") {
		t.Fatalf("approve with another account's project = %d %s", claim.Code, claim.Body)
	}
}
