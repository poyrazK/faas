// adr: 1190 — the dashboard turns organization SSO on with a SAML key it
// creates once, as `gregale auth config --sso on` does.
package apidserver_test

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestDashboardAuthSSOCreatesTheSAMLKeyOnce(t *testing.T) {
	_, restore := apidserver.WithTestIdentities(t)
	t.Cleanup(restore)
	e := apidserver.Setup(t, api.PlanPro)
	app, _ := apidserver.SeedAppTaskDeployment(t, e, "shop-auth")
	apidserver.AddAuthBindingForTest(t, e, app.ID)
	acct := (*e.Acct()).ID
	cookie := apidserver.RestartProgressDashboardCookie(t, e, acct, false)
	base := "/dashboard/apps/" + app.Slug + "/auth"
	page := apidserver.RestartProgressDashboardGET(e, cookie, base)
	token := apidserver.DashboardInputToken(page.Body.String())
	csrf := apidserver.FindDashboardCookie(page.Result().Cookies(), apidserver.DashboardAuthSettingsCookie)
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
	set := func(value string) {
		t.Helper()
		if rec := authSettingsPost(e, cookie, csrf, base+"/settings", url.Values{"csrf_token": {token}, "sso": {value}}); rec.Code != http.StatusSeeOther {
			t.Fatalf("sso %s = %d %s", value, rec.Code, rec.Body)
		}
	}
	set("on")
	first := secrets()["AUTH_SAML_PRIVATE_KEY"]
	if len(first) == 0 || secrets()["AUTH_SSO"] == nil {
		t.Fatalf("SSO on wrote %v", secrets())
	}
	set("off")
	set("on")
	if !bytes.Equal(secrets()["AUTH_SAML_PRIVATE_KEY"], first) {
		t.Fatal("turning SSO on again replaced the SAML key, which breaks every identity provider's trust")
	}
}
