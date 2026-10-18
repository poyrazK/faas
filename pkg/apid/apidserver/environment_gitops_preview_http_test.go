// adr: 1155 — candidate assessment and customer status feedback contracts.
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGitOpsCandidatePlanHTTPReadScopeAndFreshObservation(t *testing.T) {
	server, store, account, project, app := apidserver.NewProjectLifecycleFixture(t)
	if _, err := store.UpdateProjectBinding(t.Context(), account.ID, project.ID, "example/shop", "main", 42); err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateEnvironmentGitSource(t.Context(), account.ID, project.ID, "production", state.EnvironmentGitSourceSpec{
		Provider: "github", Repository: "example/shop", RepositoryID: 123, InstallationID: 42,
		Ref: "refs/heads/main", ManifestPath: "environments/production.yaml", Mode: "report", ApprovalPolicy: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	definition := "api_version: gregale.dev/environment/v1\nproject: shop\nenvironment: production\nworkloads:\n  api:\n    app: shop-api\n    variables:\n      MODE: PRIVATE_CANDIDATE_VALUE\n"
	server.GitOpsPreviewArchiveForTest(t, definition, sha)
	key := func(accountID string, scopes []string) string {
		t.Helper()
		token, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateAPIKey(t.Context(), accountID, hash, "candidate-plan", scopes); err != nil {
			t.Fatal(err)
		}
		return token
	}
	reader := key(account.ID, []string{api.ScopeProjectEnvironmentRead})
	other, err := store.CreateAccount(t.Context(), "other-plan@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	request := func(token, commit string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/v1/projects/shop/environments/production/gitops/plan", strings.NewReader(`{"commit_sha":"`+commit+`"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "read-only-plan-repeated")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		name, token string
		want        int
	}{
		{"anonymous", "", http.StatusUnauthorized},
		{"wrong scope", key(account.ID, []string{api.ScopeUsageRead}), http.StatusForbidden},
		{"foreign account", key(other.ID, []string{api.ScopeProjectEnvironmentRead}), http.StatusNotFound},
		{"environment reader", reader, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := request(tc.token, sha)
			if w.Code != tc.want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
	preview := func() api.PreviewEnvironmentGitPlanResponse {
		t.Helper()
		w := request(reader, sha)
		var result api.PreviewEnvironmentGitPlanResponse
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil || !result.ReadOnly || !result.ValuesRedacted || result.Plan.Generation != 0 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("preview: %d %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "PRIVATE_") || strings.Contains(w.Body.String(), `"before"`) || strings.Contains(w.Body.String(), `"after"`) {
			t.Fatal("preview disclosed field values")
		}
		return result
	}
	first := preview()
	if err := store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "production", "MODE", "PRIVATE_CONSOLE_VALUE"); err != nil {
		t.Fatal(err)
	}
	second := preview()
	// Before adoption, unmanaged edits do not advance the managed intent counter.
	// The plan hash still binds their fresh observed values on every request.
	if second.Plan.ObservedVersion < first.Plan.ObservedVersion || second.Plan.Hash == first.Plan.Hash {
		t.Fatal("repeated idempotency header replayed a stale observation")
	}
	current, err := store.EnvironmentGitSource(t.Context(), account.ID, project.ID, "production")
	if err != nil || current.Generation != source.Generation || current.ApprovedRevisionID != "" || current.AppliedRevisionID != "" {
		t.Fatalf("preview wrote authority: %+v %v", current, err)
	}
	server.GitOpsPreviewArchiveForTest(t, definition, strings.Repeat("b", 40))
	if w := request(reader, sha); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unverified commit accepted: %d %s", w.Code, w.Body.String())
	}
}
