package api_test

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAppAgentTokenScopesStayAppBound(t *testing.T) {
	for _, scopes := range [][]string{nil, {api.ScopeAppsRead}, {api.ScopeDeployWrite, api.ScopeEnvWrite}, {api.ScopeSecretsRequest, api.ScopeStorageManage}, {api.ScopeUsageRead}} {
		got, err := api.NormalizeAppAgentScopes(scopes)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(got, api.ScopeAgent) || !slices.Contains(got, api.ScopeAppsRead) {
			t.Fatalf("missing agent/read identity: %v", got)
		}
	}
	for _, scope := range []string{api.ScopeAdmin, api.ScopeManagedPostgresManage, api.ScopeSecretsWrite, "unknown"} {
		if _, err := api.NormalizeAppAgentScopes([]string{scope}); err == nil {
			t.Errorf("app-agent token accepted %s", scope)
		}
	}
	if err := api.ValidateDeployTokenScopes([]string{api.ScopeAppsRead}); err == nil {
		t.Fatal("ordinary deploy token gained inspection scope")
	}
	if err := api.ValidateDeployTokenScopes([]string{api.ScopeAgent}); err == nil {
		t.Fatal("agent token without read scope accepted")
	}
}
