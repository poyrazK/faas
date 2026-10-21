package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGitOpsRecoveryHTTPRequiresReadScopeAndVerifiedTarget(t *testing.T) {
	server, store, account, project, _ := apidserver.NewProjectLifecycleFixture(t)
	if _, err := store.UpdateProjectBinding(t.Context(), account.ID, project.ID, "example/shop", "main", 42); err != nil {
		t.Fatal(err)
	}
	spec := state.EnvironmentGitSourceSpec{Provider: "github", Repository: "example/shop", RepositoryID: 123, InstallationID: 42, Ref: "refs/heads/main", ManifestPath: "environments/production.yaml", Mode: "report", ApprovalPolicy: "manual"}
	if _, err := store.CreateEnvironmentGitSource(t.Context(), account.ID, project.ID, "production", spec); err != nil {
		t.Fatal(err)
	}
	definition := api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "shop", Environment: "production", Workloads: map[string]api.EnvironmentWorkload{"api": {App: "shop-api", Source: &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/api@sha256:" + strings.Repeat("a", 64)}}}}
	raw, _ := json.Marshal(definition)
	sha := strings.Repeat("c", 40)
	server.GitOpsPreviewArchiveForTest(t, string(raw), sha)
	key := func(accountID string, scopes []string) string {
		t.Helper()
		token, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateAPIKey(t.Context(), accountID, hash, "recovery-reader", scopes); err != nil {
			t.Fatal(err)
		}
		return token
	}
	reader := key(account.ID, []string{api.ScopeProjectEnvironmentRead})
	other, err := store.CreateAccount(t.Context(), "foreign-recovery@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	for _, tc := range []struct {
		name, token, commit string
		status              int
	}{
		{"anonymous", "", sha, 401}, {"wrong scope", key(account.ID, []string{api.ScopeUsageRead}), sha, 403}, {"foreign account", key(other.ID, []string{api.ScopeProjectEnvironmentRead}), sha, 404}, {"reader", reader, sha, 200}, {"mutable target", reader, "main", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/v1/projects/shop/environments/production/gitops/releases?target_commit_sha="+tc.commit, nil)
			if tc.token != "" {
				request.Header.Set("Authorization", "Bearer "+tc.token)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, request)
			if rec.Code != tc.status {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if tc.status == 200 {
				var out api.ListEnvironmentGitReleasesResponse
				if json.Unmarshal(rec.Body.Bytes(), &out) != nil || !out.ReadOnly || !out.ValuesRedacted || !out.RequiresTargetQualification || out.TargetCommitSHA != sha || len(out.Releases) != 0 || rec.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("unsafe scoped empty catalog", rec.Body.String())
				}
			}
		})
	}
}
