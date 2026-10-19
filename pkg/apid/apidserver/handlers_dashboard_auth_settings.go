// adr: 1190 — the dashboard edits an Auth app's settings as app secrets, with
// the same validation as `gregale auth config` and `gregale auth providers`.
// Writes go through the secrets API handlers, so authorization, sealing and
// audit are unchanged, and the app restarts fresh to load them.
package apidserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authsettings"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	dashboardAuthSettingsAction = "dashboard_auth_settings"
	dashboardAuthSettingsCookie = "gregale_auth_settings_csrf"
)

// dashboardAuthSettingsApp authorizes a settings form: an owned app with an
// `auth` binding, a valid CSRF token and a parsed form.
func (s *server) dashboardAuthSettingsApp(w http.ResponseWriter, r *http.Request) (state.Account, state.App, bool) {
	acct, app, ok := s.dashboardDataAPIApp(w, r)
	if !ok {
		return acct, app, false
	}
	if !s.applicationAuthEnabled(acct) {
		http.NotFound(w, r)
		return acct, app, false
	}
	if !s.verifyDashboardConfigCSRF(w, r, dashboardAuthSettingsAction, dashboardAuthSettingsCookie, acct.ID) {
		return acct, app, false
	}
	if isAuth, err := s.appHasAuthBinding(r.Context(), acct, app); err != nil || !isAuth {
		http.NotFound(w, r)
		return acct, app, false
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid form body"))
		return acct, app, false
	}
	return acct, app, true
}

func (s *server) dashboardAuthSettings(w http.ResponseWriter, r *http.Request) {
	acct, app, ok := s.dashboardAuthSettingsApp(w, r)
	if !ok {
		return
	}
	change, err := authSettingsChange(r.PostForm)
	var set map[string]string
	var unset []string
	if err == nil {
		set, unset, err = change.Secrets()
	}
	if err != nil {
		redirectDashboardAuth(w, r, app.Slug, "", err.Error())
		return
	}
	if len(set) == 0 && len(unset) == 0 {
		redirectDashboardAuth(w, r, app.Slug, "unchanged", "")
		return
	}
	if set["AUTH_SSO"] == "1" && !s.ensureDashboardSAMLKey(w, r, acct, app, set) {
		return
	}
	s.applyDashboardAuthSecrets(w, r, acct, app, set, unset, "settings")
}

// ensureDashboardSAMLKey adds the SAML signing key the first time SSO is
// turned on, as `gregale auth config --sso on` does. An existing key is
// kept, so identity providers keep trusting the app.
func (s *server) ensureDashboardSAMLKey(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, set map[string]string) bool {
	resp := s.forwardDashboardJSON(r, acct, http.MethodGet, "/v1/apps/"+url.PathEscape(app.Slug)+"/secrets", api.DefaultEnvScope, "", nil, s.listSecrets)
	if !dashboardMutationSucceeded(w, resp) {
		return false
	}
	var list api.AppSecretListResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &list); err != nil {
		renderProblem(w, s.log, err)
		return false
	}
	for _, secret := range list.Secrets {
		if secret.Key == authsettings.SAMLKeySecret {
			return true
		}
	}
	key, err := authsettings.NewSAMLKey()
	if err != nil {
		renderProblem(w, s.log, err)
		return false
	}
	set[authsettings.SAMLKeySecret] = key
	return true
}

// authSettingsChange reads only the fields the form sent: the page omits
// fields the owner left unchanged.
func authSettingsChange(form url.Values) (authsettings.Change, error) {
	var change authsettings.Change
	if form.Has("site_url") {
		value := strings.TrimSpace(form.Get("site_url"))
		change.SiteURL = &value
	}
	if form.Has("redirect_urls") {
		list := strings.FieldsFunc(form.Get("redirect_urls"), func(r rune) bool { return r == '\n' || r == '\r' || r == ',' || r == ' ' })
		change.RedirectURLs = &list
	}
	for name, target := range map[string]**[]string{"signup_domains": &change.SignupAllowedDomains, "blocked_domains": &change.SignupBlockedDomains} {
		if form.Has(name) {
			list := strings.FieldsFunc(form.Get(name), func(r rune) bool { return r == '\n' || r == '\r' || r == ',' || r == ' ' })
			*target = &list
		}
	}
	if form.Has("signup_access") {
		value := form.Get("signup_access")
		change.SignupAccess = &value
	}
	if form.Has("password_min_length") {
		n, err := strconv.Atoi(form.Get("password_min_length"))
		if err != nil {
			return change, errors.New("minimum password length must be a number")
		}
		change.PasswordMinLength = &n
	}
	if form.Has("password_require") {
		classes := []string{}
		if value := form.Get("password_require"); value != "none" {
			classes = strings.Split(value, ",")
		}
		change.PasswordRequire = &classes
	}
	if err := hostedPageChange(form, &change); err != nil {
		return change, err
	}
	for name, target := range map[string]**bool{"signup": &change.Signup, "autoconfirm": &change.Autoconfirm, "leaked_password_check": &change.LeakedPasswordCheck, "mfa": &change.MFA, "mfa_phone": &change.MFAPhone, "recovery_codes": &change.RecoveryCodes, "passkeys": &change.Passkeys, "sso": &change.SSO, "org_sso": &change.OrgSSO, "oauth_server": &change.OAuthServer, "oauth_registration": &change.OAuthRegistration, "anonymous": &change.Anonymous, "identity_linking": &change.IdentityLinking, "self_deletion": &change.SelfDeletion, "org_claims": &change.OrgClaims, "block_disposable": &change.BlockDisposable, "impersonation": &change.Impersonation} {
		if !form.Has(name) {
			continue
		}
		value := form.Get(name)
		if value != "on" && value != "off" {
			return change, errors.New(strings.ReplaceAll(name, "_", " ") + " must be on or off")
		}
		on := value == "on"
		*target = &on
	}
	return change, nil
}

// hostedPageChange reads branding, CAPTCHA, SMS, email delivery and
// activity retention. The page sends each of CAPTCHA, SMS and email with all
// of its values, because their secrets are write-only.
func hostedPageChange(form url.Values, change *authsettings.Change) error {
	for name, target := range map[string]**string{"brand_name": &change.UIName, "brand_color": &change.UIColor, "brand_logo_url": &change.UILogoURL} {
		if form.Has(name) {
			value := strings.TrimSpace(form.Get(name))
			*target = &value
		}
	}
	if form.Has("captcha_provider") {
		change.Captcha = &authsettings.Captcha{Provider: form.Get("captcha_provider"), SiteKey: strings.TrimSpace(form.Get("captcha_site_key")), Secret: strings.TrimSpace(form.Get("captcha_secret"))}
	}
	if form.Has("sms_countries") {
		codes := strings.FieldsFunc(form.Get("sms_countries"), func(r rune) bool { return r == ',' || r == ' ' })
		change.SMSCountries = &codes
	}
	if form.Has("sms_provider") {
		change.SMS = &authsettings.SMS{Provider: form.Get("sms_provider"), Account: strings.TrimSpace(form.Get("sms_account")), Sender: strings.TrimSpace(form.Get("sms_sender")), Secret: strings.TrimSpace(form.Get("sms_secret"))}
	}
	if form.Has("email_provider") {
		change.Email = &authsettings.Email{Provider: form.Get("email_provider"), From: strings.TrimSpace(form.Get("email_from")), APIKey: strings.TrimSpace(form.Get("email_api_key"))}
	}
	for name, target := range map[string]**int{"activity_retention_days": &change.ActivityRetentionDays, "anonymous_retention_days": &change.AnonymousRetentionDays} {
		if !form.Has(name) {
			continue
		}
		days := 0
		if value := form.Get(name); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil {
				return errors.New(strings.ReplaceAll(strings.TrimSuffix(name, "_days"), "_", " ") + " must be a number of days")
			}
			days = n
		}
		*target = &days
	}
	return nil
}

func (s *server) dashboardAuthProviderEnable(w http.ResponseWriter, r *http.Request) {
	acct, app, ok := s.dashboardAuthSettingsApp(w, r)
	if !ok {
		return
	}
	provider := r.PostFormValue("provider")
	set, err := authsettings.Provider(provider, strings.TrimSpace(r.PostFormValue("client_id")), strings.TrimSpace(r.PostFormValue("client_secret")), strings.TrimSpace(r.PostFormValue("url")))
	if err != nil {
		redirectDashboardAuth(w, r, app.Slug, "", err.Error())
		return
	}
	stale, ok := s.dashboardProviderSecrets(w, r, acct, app, provider, set)
	if !ok {
		return
	}
	s.applyDashboardAuthSecrets(w, r, acct, app, set, stale, "provider-enabled")
}

func (s *server) dashboardAuthProviderDisable(w http.ResponseWriter, r *http.Request) {
	acct, app, ok := s.dashboardAuthSettingsApp(w, r)
	if !ok {
		return
	}
	provider := r.PathValue("provider")
	if !slices.Contains(authsettings.Providers, provider) {
		http.NotFound(w, r)
		return
	}
	stale, ok := s.dashboardProviderSecrets(w, r, acct, app, provider, nil)
	if !ok {
		return
	}
	s.applyDashboardAuthSecrets(w, r, acct, app, nil, stale, "provider-disabled")
}

// dashboardProviderSecrets lists the provider's secrets that keep is not
// about to replace, so enabling leaves no stale server URL and disabling
// removes every credential.
func (s *server) dashboardProviderSecrets(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, provider string, keep map[string]string) ([]string, bool) {
	resp := s.forwardDashboardJSON(r, acct, http.MethodGet, "/v1/apps/"+url.PathEscape(app.Slug)+"/secrets", api.DefaultEnvScope, "", nil, s.listSecrets)
	if !dashboardMutationSucceeded(w, resp) {
		return nil, false
	}
	var list api.AppSecretListResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &list); err != nil {
		renderProblem(w, s.log, err)
		return nil, false
	}
	stale := []string{}
	for _, secret := range list.Secrets {
		if _, kept := keep[secret.Key]; strings.HasPrefix(secret.Key, authsettings.ProviderPrefix(provider)) && !kept {
			stale = append(stale, secret.Key)
		}
	}
	return stale, true
}

// applyDashboardAuthSecrets writes and removes secrets through the secrets
// API handlers, then restarts the app fresh so it loads them.
func (s *server) applyDashboardAuthSecrets(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, set map[string]string, unset []string, changed string) {
	base := "/v1/apps/" + url.PathEscape(app.Slug) + "/secrets/"
	for key, value := range set {
		resp := s.forwardDashboardJSON(r, acct, http.MethodPut, base+url.PathEscape(key), api.DefaultEnvScope, key, api.PutAppSecretRequest{Value: value}, s.setSecret)
		if !dashboardMutationSucceeded(w, resp) {
			return
		}
	}
	for _, key := range unset {
		resp := s.forwardDashboardJSON(r, acct, http.MethodDelete, base+url.PathEscape(key), api.DefaultEnvScope, key, nil, s.deleteSecret)
		if !secretAlreadyAbsent(resp) && !dashboardMutationSucceeded(w, resp) {
			return
		}
	}
	restart := r.Clone(r.Context())
	restart.Method = http.MethodPost
	restart.URL = &url.URL{Path: "/v1/apps/" + url.PathEscape(app.Slug) + "/restart", RawQuery: "fresh=true"}
	restart.Body, restart.ContentLength = io.NopCloser(strings.NewReader("")), 0
	restart.SetPathValue("slug", app.Slug)
	resp := httptest.NewRecorder()
	s.restartApp(resp, restart, acct)
	if resp.Code == http.StatusConflict {
		// A restart already running may have read the configuration before
		// this change; the page asks the owner to restart once it finishes.
		redirectDashboardAuth(w, r, app.Slug, changed+"-restart-pending", "")
		return
	}
	if resp.Code < http.StatusOK || resp.Code >= http.StatusMultipleChoices {
		redirectDashboardAuth(w, r, app.Slug, "", "Saved, but the restart failed. Restart the app to apply the change.")
		return
	}
	redirectDashboardAuth(w, r, app.Slug, changed, "")
}

// secretAlreadyAbsent reports a removal of a secret the app does not have,
// which the secrets API answers with 400 secret_not_found.
func secretAlreadyAbsent(resp *httptest.ResponseRecorder) bool {
	if resp.Code == http.StatusNotFound {
		return true
	}
	var problem api.Problem
	return resp.Code == http.StatusBadRequest && json.Unmarshal(resp.Body.Bytes(), &problem) == nil && problem.Code == api.CodeSecretNotFound
}

func redirectDashboardAuth(w http.ResponseWriter, r *http.Request, slug, changed, message string) {
	values := url.Values{}
	if changed != "" {
		values.Set("changed", changed)
	}
	if message != "" {
		values.Set("error", message)
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/auth?"+values.Encode()+"#settings", http.StatusSeeOther)
}
