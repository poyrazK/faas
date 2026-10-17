// adr: 1190 — the dashboard turns phone sign-in on with the customer's SMS
// account, validated like `gregale auth config --sms`.
package apidserver_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestDashboardAuthPhoneSignIn(t *testing.T) {
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
	if !strings.Contains(page.Body.String(), `<option value="twilio_verify">`) {
		t.Fatalf("page lists no SMS providers\n%s", page.Body)
	}
	names := func() string {
		secrets, err := (*e.Store()).ListAppSecretsInScope(t.Context(), acct, app.ID, api.DefaultEnvScope)
		if err != nil {
			t.Fatal(err)
		}
		keys := []string{}
		for _, secret := range secrets {
			keys = append(keys, secret.Key)
		}
		return strings.Join(keys, ",")
	}
	twilio := url.Values{"csrf_token": {token}, "sms_provider": {"twilio"}, "sms_account": {"AC123"}, "sms_sender": {"MG456"}, "sms_secret": {"auth-token"}}
	if rec := authSettingsPost(e, cookie, csrf, base+"/settings", twilio); rec.Code != http.StatusSeeOther || strings.Contains(rec.Header().Get("Location"), "error=") {
		t.Fatalf("twilio = %d %q %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	for _, key := range []string{"GOTRUE_EXTERNAL_PHONE_ENABLED", "GOTRUE_SMS_PROVIDER", "GOTRUE_SMS_TWILIO_ACCOUNT_SID", "GOTRUE_SMS_TWILIO_AUTH_TOKEN", "GOTRUE_SMS_TWILIO_MESSAGE_SERVICE_SID"} {
		if !strings.Contains(names(), key) {
			t.Fatalf("%s not written: %s", key, names())
		}
	}
	// Switching providers removes the previous provider's credentials.
	vonage := url.Values{"csrf_token": {token}, "sms_provider": {"vonage"}, "sms_account": {"key"}, "sms_sender": {"Shop"}, "sms_secret": {"secret"}}
	if rec := authSettingsPost(e, cookie, csrf, base+"/settings", vonage); rec.Code != http.StatusSeeOther || strings.Contains(names(), "TWILIO") || !strings.Contains(names(), "GOTRUE_SMS_VONAGE_API_SECRET") {
		t.Fatalf("vonage = %d, secrets %s", rec.Code, names())
	}
	missing := url.Values{"csrf_token": {token}, "sms_provider": {"messagebird"}, "sms_sender": {"Shop"}, "sms_secret": {""}}
	if rec := authSettingsPost(e, cookie, csrf, base+"/settings", missing); !strings.Contains(rec.Header().Get("Location"), "error=") || strings.Contains(names(), "MESSAGEBIRD") {
		t.Fatalf("SMS without a secret = %q, secrets %s", rec.Header().Get("Location"), names())
	}
	if rec := authSettingsPost(e, cookie, csrf, base+"/settings", url.Values{"csrf_token": {token}, "sms_provider": {""}}); rec.Code != http.StatusSeeOther || strings.Contains(names(), "GOTRUE_SMS_") {
		t.Fatalf("off = %d, secrets %s", rec.Code, names())
	}
}
