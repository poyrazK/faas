// adr: 1190 — the dashboard Auth page is owner-scoped and may call only the
// Auth app's own origin; it carries no key or user data from apid.
package apidserver_test

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestDashboardAuthPageIsOwnerScopedAndAllowsOnlyTheAuthOrigin(t *testing.T) {
	t.Setenv("FAAS_APPLICATION_AUTH_ENABLED", "1")
	e := apidserver.Setup(t, api.PlanPro)
	app, _ := apidserver.SeedAppTaskDeployment(t, e, "shop-auth")
	cookie := apidserver.RestartProgressDashboardCookie(t, e, (*e.Acct()).ID, false)
	path := "/dashboard/apps/" + app.Slug + "/auth"
	origin := "https://shop-auth.gregale.dev"

	plain := apidserver.RestartProgressDashboardGET(e, cookie, path)
	if plain.Code != http.StatusOK || !strings.Contains(plain.Body.String(), "not an application Auth app") ||
		strings.Contains(plain.Body.String(), "key-form") || strings.Contains(plain.Header().Get("Content-Security-Policy"), origin) {
		t.Fatalf("non-Auth app page = %d csp=%q\n%s", plain.Code, plain.Header().Get("Content-Security-Policy"), plain.Body)
	}

	apidserver.AddAuthBindingForTest(t, e, app.ID)
	page := apidserver.RestartProgressDashboardGET(e, cookie, path)
	body := page.Body.String()
	csp := page.Header().Get("Content-Security-Policy")
	if page.Code != http.StatusOK || !strings.Contains(csp, "connect-src 'self' "+origin+";") || !strings.Contains(body, `data-origin="`+origin+`"`) || !strings.Contains(body, `id="key-form"`) {
		t.Fatalf("Auth page = %d csp=%q\n%s", page.Code, csp, body)
	}
	// The inline script must run under the page's nonce and nothing else.
	nonce := regexp.MustCompile(`'nonce-([A-Za-z0-9_-]+)'`).FindStringSubmatch(csp)
	if nonce == nil || !strings.Contains(body, `<script nonce="`+nonce[1]+`">`) || strings.Count(body, "<script") != 1 {
		t.Fatalf("script nonce does not match the policy %q", csp)
	}
	if !strings.Contains(csp, "script-src 'self' 'nonce-") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("dashboard policy weakened: %q", csp)
	}

	other, _ := (*e.Store()).CreateAccount(t.Context(), "other-auth-admin@example.com", api.PlanPro)
	if rec := apidserver.RestartProgressDashboardGET(e, apidserver.RestartProgressDashboardCookie(t, e, other.ID, false), path); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account page = %d", rec.Code)
	}
	for _, auth := range []*http.Cookie{nil, apidserver.RestartProgressDashboardCookie(t, e, (*e.Acct()).ID, true)} {
		if rec := apidserver.RestartProgressDashboardGET(e, auth, path); rec.Code == http.StatusOK {
			t.Fatal("anonymous or pending-MFA read allowed")
		}
	}
}
