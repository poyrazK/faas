// adr: 1190 — the dashboard rotates signing keys and sets email delivery with
// the same rules as `gregale auth keys` and `gregale auth config`.
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func TestDashboardAuthSigningKeysAndEmail(t *testing.T) {
	identity, restore := apidserver.WithTestIdentities(t)
	t.Cleanup(restore)
	e := apidserver.Setup(t, api.PlanPro)
	app, _ := apidserver.SeedAppTaskDeployment(t, e, "shop-auth")
	apidserver.AddAuthBindingForTest(t, e, app.ID)
	acct := (*e.Acct()).ID
	for _, key := range []string{"AUTH_JWT_KEY_0A1B2C3D", "AUTH_JWT_SIGNER_0A1B2C3D"} {
		if err := (*e.Store()).UpsertAppSecret(t.Context(), acct, app.ID, key, []byte("sealed")); err != nil {
			t.Fatal(err)
		}
	}
	cookie := apidserver.RestartProgressDashboardCookie(t, e, acct, false)
	base := "/dashboard/apps/" + app.Slug + "/auth"
	page := apidserver.RestartProgressDashboardGET(e, cookie, base)
	token := apidserver.DashboardInputToken(page.Body.String())
	csrf := apidserver.FindDashboardCookie(page.Result().Cookies(), apidserver.DashboardAuthSettingsCookie)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `<li data-id="0A1B2C3D" data-created="`) || !strings.Contains(page.Body.String(), `data-signer="true"`) || !strings.Contains(page.Body.String(), `<option value="postmark">`) {
		t.Fatalf("page = %d\n%s", page.Code, page.Body)
	}
	secrets := func() map[string][]byte {
		list, err := (*e.Store()).ListAppSecretsInScope(t.Context(), acct, app.ID, api.DefaultEnvScope)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string][]byte{}
		for _, secret := range list {
			out[secret.Key] = secret.Ciphertext
		}
		return out
	}
	names := func() string {
		keys := []string{}
		for key := range secrets() {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		return strings.Join(keys, ",")
	}
	form := url.Values{"csrf_token": {token}}
	post := func(path string) (int, string) {
		rec := authSettingsPost(e, cookie, csrf, base+path, form)
		return rec.Code, rec.Header().Get("Location")
	}

	// A standby key is sealed as its own secret; the signer is unchanged.
	if code, location := post("/keys"); code != http.StatusSeeOther || !strings.Contains(location, "changed=key-added") {
		t.Fatalf("add key = %d %q", code, location)
	}
	standby := ""
	for key := range secrets() {
		if id, ok := strings.CutPrefix(key, "AUTH_JWT_KEY_"); ok && id != "0A1B2C3D" {
			standby = id
		}
	}
	if standby == "" || strings.Contains(names(), "AUTH_JWT_SIGNER_"+standby) {
		t.Fatalf("standby key not added: %s", names())
	}
	envelope, err := secretbox.Open(identity, secrets()["AUTH_JWT_KEY_"+standby])
	if err != nil || len(envelope) != 1 {
		t.Fatalf("open standby key: %v", err)
	}
	var jwk map[string]string
	for _, value := range envelope {
		if err := json.Unmarshal([]byte(value), &jwk); err != nil || jwk["kid"] != "gregale-"+strings.ToLower(standby) || jwk["d"] == "" {
			t.Fatalf("standby key is not a private JWK: %v %v", jwk["kid"], err)
		}
	}

	// The signer cannot be revoked; activating moves the only marker.
	if code, location := post("/keys/0A1B2C3D/revoke"); code != http.StatusSeeOther || !strings.Contains(location, "error=") || !strings.Contains(names(), "AUTH_JWT_KEY_0A1B2C3D") {
		t.Fatalf("revoke signer = %d %q", code, location)
	}
	if code, location := post("/keys/" + standby + "/activate"); code != http.StatusSeeOther || !strings.Contains(location, "changed=key-activated") {
		t.Fatalf("activate = %d %q", code, location)
	}
	if got := names(); !strings.Contains(got, "AUTH_JWT_SIGNER_"+standby) || strings.Contains(got, "AUTH_JWT_SIGNER_0A1B2C3D") {
		t.Fatalf("markers after activation: %s", got)
	}
	if code, location := post("/keys/" + standby + "/activate"); code != http.StatusSeeOther || !strings.Contains(location, "changed=unchanged") {
		t.Fatalf("activate the signer again = %d %q", code, location)
	}
	if code, _ := post("/keys/0A1B2C3D/revoke"); code != http.StatusSeeOther || strings.Contains(names(), "AUTH_JWT_KEY_0A1B2C3D") {
		t.Fatalf("revoke previous key = %d, secrets %s", code, names())
	}
	for _, path := range []string{"/keys/0A1B2C3D/activate", "/keys/FFFFFFFF/revoke", "/keys/lowercase/activate"} {
		if code, _ := post(path); code != http.StatusNotFound {
			t.Fatalf("%s = %d", path, code)
		}
	}
	// At most api.AuthMaxSigningKeys keys.
	for i := 0; i < api.AuthMaxSigningKeys-1; i++ {
		post("/keys")
	}
	before := names()
	if code, location := post("/keys"); code != http.StatusSeeOther || !strings.Contains(location, "error=") || names() != before {
		t.Fatalf("key beyond the limit = %d %q", code, location)
	}

	email := url.Values{"csrf_token": {token}, "email_provider": {"resend"}, "email_from": {"Shop <auth@shop.example.com>"}, "email_api_key": {"re_test_key_123"}}
	if rec := authSettingsPost(e, cookie, csrf, base+"/settings", email); rec.Code != http.StatusSeeOther || !strings.Contains(names(), "AUTH_EMAIL_API_KEY,AUTH_EMAIL_FROM,AUTH_EMAIL_PROVIDER") {
		t.Fatalf("email = %d %q, secrets %s", rec.Code, rec.Header().Get("Location"), names())
	}
	email.Set("email_api_key", "")
	email.Set("email_provider", "postmark")
	if rec := authSettingsPost(e, cookie, csrf, base+"/settings", email); !strings.Contains(rec.Header().Get("Location"), "error=") {
		t.Fatalf("email without a key = %q", rec.Header().Get("Location"))
	}
	if rec := authSettingsPost(e, cookie, csrf, base+"/settings", url.Values{"csrf_token": {token}, "email_provider": {""}}); rec.Code != http.StatusSeeOther || strings.Contains(names(), "AUTH_EMAIL") {
		t.Fatalf("remove email = %d, secrets %s", rec.Code, names())
	}

	other, _ := (*e.Store()).CreateAccount(t.Context(), "other-auth-keys@example.com", api.PlanPro)
	otherCookie := apidserver.RestartProgressDashboardCookie(t, e, other.ID, false)
	if rec := authSettingsPost(e, otherCookie, csrf, base+"/keys", form); rec.Code == http.StatusSeeOther {
		t.Fatal("cross-account key change accepted")
	}
	if rec := authSettingsPost(e, cookie, nil, base+"/keys", form); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing CSRF cookie = %d", rec.Code)
	}
}
