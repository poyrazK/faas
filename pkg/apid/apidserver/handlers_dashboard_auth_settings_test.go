// adr: 1190 — dashboard Auth settings are owner-scoped, CSRF-bound and
// validated like `gregale auth config` before any secret is written.
package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func authSettingsPost(e apidserver.TestEnv, session, csrf *http.Cookie, path string, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(session)
	if csrf != nil {
		r.AddCookie(csrf)
	}
	w := httptest.NewRecorder()
	(*e.H()).ServeHTTP(w, r)
	return w
}

func TestDashboardAuthSettingsWriteValidatedSecrets(t *testing.T) {
	t.Setenv("FAAS_APPLICATION_AUTH_ENABLED", "1")
	_, restore := apidserver.WithTestIdentities(t)
	t.Cleanup(restore)
	e := apidserver.Setup(t, api.PlanPro)
	app, _ := apidserver.SeedAppTaskDeployment(t, e, "shop-auth")
	apidserver.AddAuthBindingForTest(t, e, app.ID)
	cookie := apidserver.RestartProgressDashboardCookie(t, e, (*e.Acct()).ID, false)
	base := "/dashboard/apps/" + app.Slug + "/auth"
	page := apidserver.RestartProgressDashboardGET(e, cookie, base)
	token := apidserver.DashboardInputToken(page.Body.String())
	csrf := apidserver.FindDashboardCookie(page.Result().Cookies(), apidserver.DashboardAuthSettingsCookie)
	if page.Code != http.StatusOK || token == "" || csrf == nil || !strings.Contains(page.Body.String(), `id="settings-form"`) {
		t.Fatalf("page = %d token=%q\n%s", page.Code, token, page.Body)
	}
	keys := func() []string {
		secrets, err := (*e.Store()).ListAppSecretsInScope(t.Context(), (*e.Acct()).ID, app.ID, api.DefaultEnvScope)
		if err != nil {
			t.Fatal(err)
		}
		names := []string{}
		for _, secret := range secrets {
			names = append(names, secret.Key)
		}
		sort.Strings(names)
		return names
	}
	settings := url.Values{"csrf_token": {token}, "signup": {"off"}, "password_min_length": {"12"}, "password_require": {"lower,digit"}, "mfa": {"off"}, "mfa_phone": {"on"}, "recovery_codes": {"on"}, "self_deletion": {"on"}}
	if rec := authSettingsPost(e, cookie, nil, base+"/settings", settings); rec.Code != http.StatusBadRequest || len(keys()) != 0 {
		t.Fatalf("missing CSRF cookie = %d, secrets %v", rec.Code, keys())
	}
	invalid := url.Values{"csrf_token": {token}, "password_min_length": {"4"}}
	if rec := authSettingsPost(e, cookie, csrf, base+"/settings", invalid); rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "error=") || len(keys()) != 0 {
		t.Fatalf("invalid settings = %d %q, secrets %v", rec.Code, rec.Header().Get("Location"), keys())
	}
	rec := authSettingsPost(e, cookie, csrf, base+"/settings", settings)
	want := []string{"AUTH_DISABLE_SIGNUP", "AUTH_SELF_DELETION", "GOTRUE_MFA_PHONE_ENROLL_ENABLED", "GOTRUE_MFA_PHONE_VERIFY_ENABLED", "GOTRUE_MFA_RECOVERY_CODES_ENROLL_ENABLED", "GOTRUE_MFA_RECOVERY_CODES_VERIFY_ENABLED", "GOTRUE_MFA_TOTP_ENROLL_ENABLED", "GOTRUE_MFA_TOTP_VERIFY_ENABLED", "GOTRUE_PASSWORD_MIN_LENGTH", "GOTRUE_PASSWORD_REQUIRED_CHARACTERS"}
	if rec.Code != http.StatusSeeOther || strings.Join(keys(), ",") != strings.Join(want, ",") {
		t.Fatalf("settings = %d %q, secrets %v\n%s", rec.Code, rec.Header().Get("Location"), keys(), rec.Body)
	}
	// The app restarted fresh, so the page reports the change as applied.
	if location := rec.Header().Get("Location"); location != "/dashboard/apps/shop-auth/auth?changed=settings#settings" {
		t.Fatalf("settings redirect = %q", location)
	}

	hosted := url.Values{"csrf_token": {token}, "passkeys": {"on"}, "brand_name": {"Shop"}, "brand_color": {"#4F46E5"}, "brand_logo_url": {"https://cdn.example.com/logo.svg"},
		"captcha_provider": {"turnstile"}, "captcha_site_key": {"1x00000000000000000000AA"}, "captcha_secret": {"1x0000000000000000000000000000000AA"}, "activity_retention_days": {"90"}}
	// The first save's restart is still running in this test, so the
	// second is saved and reported as waiting for a restart.
	if rec := authSettingsPost(e, cookie, csrf, base+"/settings", hosted); rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "changed=settings-restart-pending") {
		t.Fatalf("hosted-page settings = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	for _, key := range []string{"AUTH_AUDIT_RETENTION_DAYS", "AUTH_CAPTCHA_SITE_KEY", "AUTH_UI_COLOR", "AUTH_UI_LOGO_URL", "AUTH_UI_NAME", "GOTRUE_PASSKEY_ENABLED", "GOTRUE_SECURITY_CAPTCHA_SECRET"} {
		if !strings.Contains(strings.Join(keys(), ","), key) {
			t.Fatalf("%s not written: %v", key, keys())
		}
	}
	missingSecret := url.Values{"csrf_token": {token}, "captcha_provider": {"hcaptcha"}, "captcha_site_key": {"10000000-ffff-ffff-ffff-000000000001"}, "captcha_secret": {""}}
	if rec := authSettingsPost(e, cookie, csrf, base+"/settings", missingSecret); !strings.Contains(rec.Header().Get("Location"), "error=") {
		t.Fatalf("CAPTCHA without a secret = %q", rec.Header().Get("Location"))
	}
	enable := url.Values{"csrf_token": {token}, "provider": {"keycloak"}, "client_id": {"id"}, "client_secret": {"secret"}}
	if rec := authSettingsPost(e, cookie, csrf, base+"/providers", enable); !strings.Contains(rec.Header().Get("Location"), "error=") {
		t.Fatalf("keycloak without URL = %q", rec.Header().Get("Location"))
	}
	enable.Set("provider", "google")
	if rec := authSettingsPost(e, cookie, csrf, base+"/providers", enable); rec.Code != http.StatusSeeOther || !strings.Contains(strings.Join(keys(), ","), "GOTRUE_EXTERNAL_GOOGLE_CLIENT_ID,GOTRUE_EXTERNAL_GOOGLE_ENABLED,GOTRUE_EXTERNAL_GOOGLE_SECRET") {
		t.Fatalf("enable google = %d, secrets %v", rec.Code, keys())
	}
	if rec := authSettingsPost(e, cookie, csrf, base+"/providers/google/disable", url.Values{"csrf_token": {token}}); rec.Code != http.StatusSeeOther || strings.Contains(strings.Join(keys(), ","), "GOOGLE") {
		t.Fatalf("disable google = %d, secrets %v", rec.Code, keys())
	}
	if rec := authSettingsPost(e, cookie, csrf, base+"/providers/myspace/disable", url.Values{"csrf_token": {token}}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown provider = %d", rec.Code)
	}

	other, _ := (*e.Store()).CreateAccount(t.Context(), "other-auth-settings@example.com", api.PlanPro)
	otherCookie := apidserver.RestartProgressDashboardCookie(t, e, other.ID, false)
	if rec := authSettingsPost(e, otherCookie, csrf, base+"/settings", settings); rec.Code == http.StatusSeeOther {
		t.Fatal("cross-account settings change accepted")
	}
	plain, _ := apidserver.SeedAppTaskDeployment(t, e, "plain-app")
	if rec := authSettingsPost(e, cookie, csrf, "/dashboard/apps/"+plain.Slug+"/auth/settings", settings); rec.Code != http.StatusNotFound {
		t.Fatalf("non-Auth app settings = %d", rec.Code)
	}
}
