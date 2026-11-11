package githubconnect

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBrowserRecoveryKeepsSecretsOutOfDashboardURL(t *testing.T) {
	for _, code := range []string{"csrf_missing", "csrf_mismatch", "github_access_denied", "github_org_admin_required", "github_permission_pending", "github_unreachable"} {
		t.Run(code, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/oauth/code-callback?code=private-code&state=private-state&return_to=https://attacker.invalid", nil)
			request.Header.Set("Accept", "text/html,application/xhtml+xml")
			response := httptest.NewRecorder()
			WriteProblem(response, request, api.NewProblem(502, code, "private diagnostic", "secret credentials").WithHint("private hint"))
			location, err := url.Parse(response.Header().Get("Location"))
			if err != nil || response.Code != http.StatusSeeOther || location.Path != "/dashboard/settings" || location.Host != "" {
				t.Fatalf("recovery response: %d %q %v", response.Code, location, err)
			}
			q := location.Query()
			if q.Get("section") != "integrations" || q.Get("github") != "connect-failed" || q.Get("github_error") != code || len(q) != 3 {
				t.Fatalf("recovery query: %v", q)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("OAuth error is cacheable")
			}
			if strings.Contains(response.Body.String(), "private") || strings.Contains(response.Body.String(), "secret") {
				t.Fatal("private diagnostics in response")
			}
		})
	}
}

func TestAPIRecoveryPreservesProblemContract(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/oauth/code-callback", nil)
	request.Header.Set("Accept", "application/json")
	response := httptest.NewRecorder()
	WriteProblem(response, request, api.ErrGitHubOrgAdminRequired("acme"))
	if response.Code != 403 || response.Header().Get("Location") != "" || !strings.Contains(response.Body.String(), api.CodeGitHubOrgAdminRequired) {
		t.Fatalf("problem contract: %d %s", response.Code, response.Body.String())
	}
}
