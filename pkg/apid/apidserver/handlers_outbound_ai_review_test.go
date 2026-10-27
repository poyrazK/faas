package apidserver_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

// TestAIRequestContentNeedsSecretsRead checks that captured prompts and
// answers need secrets:read, not just apps:read (ADR 20261027132936).
func TestAIRequestContentNeedsSecretsRead(t *testing.T) {
	path := "/v1/outbound/integrations/" + uuid.NewString() + "/ai-requests/" + uuid.NewString() + "/content"
	readOnly := apidserver.SetupWithScopes(t, []string{api.ScopeAppsRead})
	(*readOnly.S()).EnablePlatformFlagForAll(platformflags.AIGateway)
	apidserver.AssertProblem(t, readOnly.Do(t, http.MethodGet, path, nil, nil), http.StatusForbidden, api.CodeForbidden)

	secrets := apidserver.SetupWithScopes(t, []string{api.ScopeSecretsRead})
	(*secrets.S()).EnablePlatformFlagForAll(platformflags.AIGateway)
	if got := secrets.Do(t, http.MethodGet, path, nil, nil); strings.Contains(got.Body.String(), api.CodeForbidden) {
		t.Fatalf("secrets:read could not read captured content: %d %s", got.Code, got.Body.String())
	}
}
