package main

// adr: 1190 — typed settings for application Auth.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authsettings"
)

const (
	authConfigUsage    = "usage: gregale auth config NAME [--site-url URL] [--redirect-urls URLS] [--signup on|off] [--autoconfirm on|off] [--password-min-length N] [--password-require CLASSES] [--leaked-password-check on|off] [--mfa on|off] [--mfa-phone on|off] [--recovery-codes on|off] [--email-provider resend|postmark|none --email-from FROM --email-api-key-stdin] [--authz FILE] [--events on|off] [--passkeys on|off] [--passkey-rp-id DOMAIN|none] [--brand-name NAME] [--brand-color #RRGGBB] [--brand-logo-url URL] [--captcha turnstile|hcaptcha|off --captcha-site-key KEY --captcha-secret-stdin] [--activity-retention DAYS|forever] [--sms twilio|twilio_verify|messagebird|textlocal|vonage|off --sms-account ID --sms-sender SENDER --sms-secret-stdin] [--sms-countries CODES|all] [--sso on|off] [--anonymous on|off] [--anonymous-retention DAYS|forever] [--identity-linking on|off] [--oauth-server on|off] [--oauth-registration on|off] [--org-sso on|off]"
	authProvidersUsage = "usage: gregale auth providers list NAME | gregale auth providers enable NAME PROVIDER --client-id ID [--client-secret-stdin] [--url URL] | gregale auth providers disable NAME PROVIDER"
)

var authEmailSecrets = authsettings.EmailSecrets

// cmdAuthConfig shows the running app's settings, or changes them when any
// setting flag is given. Secrets are write-only, so settings are read back
// from the app's public /settings/v1.
func cmdAuthConfig(args []string) int {
	fs := newFlagSet("auth config", flag.ContinueOnError)
	scope := fs.String("scope", api.DefaultEnvScope, "environment scope")
	siteURL := fs.String("site-url", "", "application URL users return to after sign-in")
	redirects := fs.String("redirect-urls", "", "comma-separated additional allowed redirect URLs; replaces the list (none clears it)")
	signup := fs.String("signup", "", "on or off: whether anyone may sign up")
	autoconfirm := fs.String("autoconfirm", "", "on or off: sign users in without email confirmation (development only)")
	minLength := fs.Int("password-min-length", 0, fmt.Sprintf("minimum password length (%d-%d)", api.AuthPasswordMinLengthFloor, api.AuthPasswordMinLengthCeiling))
	require := fs.String("password-require", "", "character classes every password needs: lower,upper,digit,symbol, or none")
	leaked := fs.String("leaked-password-check", "", "on or off: refuse passwords found in known breaches (Have I Been Pwned)")
	mfa := fs.String("mfa", "", "on or off: let users enrol an authenticator app (TOTP); enrolled users keep verifying when off")
	passkeys := fs.String("passkeys", "", "on or off: let users sign in with a passkey")
	mfaPhone := fs.String("mfa-phone", "", "on or off: let users enrol SMS codes as a second factor (needs an SMS provider)")
	recoveryCodes := fs.String("recovery-codes", "", "on or off: let users create one-time recovery codes for their second factor")
	passkeyRPID := fs.String("passkey-rp-id", "", "parent domain shared by the Auth app and your site for passkeys (example.com), or none for the Auth app's host")
	extras := authHostedFlags{
		name:            fs.String("brand-name", "", "name shown on the hosted pages and in emails, or none for the site's host"),
		color:           fs.String("brand-color", "", "#rrggbb accent colour for the hosted pages, or none"),
		logo:            fs.String("brand-logo-url", "", "HTTPS URL of the logo on the hosted pages, or none"),
		captcha:         fs.String("captcha", "", "CAPTCHA on sign-up and sign-in: turnstile, hcaptcha or off"),
		captchaSiteKey:  fs.String("captcha-site-key", "", "the CAPTCHA provider's site key (public)"),
		captchaSecretIn: fs.Bool("captcha-secret-stdin", false, "read the CAPTCHA provider's secret key from stdin (prompted on a terminal)"),
		retention:       fs.String("activity-retention", "", "days to keep sign-in activity, or forever"),
	}
	emailProvider := fs.String("email-provider", "", "HTTPS email provider: resend, postmark, or none to remove it")
	emailFrom := fs.String("email-from", "", "sender address, or a display name with the address in angle brackets")
	emailKeyStdin := fs.Bool("email-api-key-stdin", false, "read the email provider API key from stdin (prompted on a terminal)")
	authzFile := fs.String("authz", "", "JSON file declaring custom roles and policies (applied on restart)")
	events := fs.String("events", "", "on or off: publish user lifecycle events (auth.user.*) to Gregale's event bus")
	sso := fs.String("sso", "", "on or off: organization single sign-on (SAML); manage identity providers with `gregale auth sso`")
	orgSSO := fs.String("org-sso", "", "on or off: let organizations verify their email domains and connect their own identity provider (needs --sso on)")
	oauthServer := fs.String("oauth-server", "", "on or off: the OAuth 2.1 authorization server, for third-party apps and MCP clients; manage clients with `gregale auth oauth-clients`")
	oauthRegistration := fs.String("oauth-registration", "", "on or off: let OAuth clients register themselves (RFC 7591), as MCP clients do")
	anonymous := fs.String("anonymous", "", "on or off: let people start without an account (anonymous users); turn on CAPTCHA with it")
	anonymousRetention := fs.String("anonymous-retention", "", "days an anonymous user may stay idle before it is deleted, or forever")
	identityLinking := fs.String("identity-linking", "", "on or off: let signed-in users link and unlink sign-in methods")
	sms := authSMSFlags{
		provider:  fs.String("sms", "", "phone sign-in by text message through your own SMS account: twilio, twilio_verify, messagebird, textlocal, vonage, or off"),
		account:   fs.String("sms-account", "", "Twilio account SID or Vonage API key"),
		sender:    fs.String("sms-sender", "", "Twilio messaging or Verify service SID (or phone number), MessageBird originator, Textlocal sender or Vonage from"),
		secretIn:  fs.Bool("sms-secret-stdin", false, "read the Twilio auth token, MessageBird access key, Textlocal API key or Vonage API secret from stdin"),
		countries: fs.String("sms-countries", "", "country calling codes phone sign-in may text, comma-separated (1,44,49), or all"),
	}
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || api.ValidateScope(*scope) != nil {
		PrintUsage(os.Stderr, authConfigUsage, "auth")
		return 1
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	var change authsettings.Change
	if err := parseAuthChange(given, &change, *siteURL, *redirects, *signup, *autoconfirm, *minLength, *require, *leaked, *mfa, *passkeys); err != nil {
		return printErr("Invalid Auth settings", err)
	}
	if err := extras.apply(given, &change); err != nil {
		return printErr("Invalid Auth settings", err)
	}
	if err := sms.apply(given, &change); err != nil {
		return printErr("Invalid Auth settings", err)
	}
	for name, target := range map[string]struct {
		value *string
		field **bool
	}{"sso": {sso, &change.SSO}, "org-sso": {orgSSO, &change.OrgSSO}, "oauth-server": {oauthServer, &change.OAuthServer}, "oauth-registration": {oauthRegistration, &change.OAuthRegistration},
		"mfa-phone": {mfaPhone, &change.MFAPhone}, "recovery-codes": {recoveryCodes, &change.RecoveryCodes},
		"anonymous": {anonymous, &change.Anonymous}, "identity-linking": {identityLinking, &change.IdentityLinking}} {
		if !given[name] {
			continue
		}
		on, err := parseOnOff(name, *target.value)
		if err != nil {
			return printErr("Invalid Auth settings", err)
		}
		*target.field = &on
	}
	if given["anonymous-retention"] {
		days, err := parseRetentionDays("anonymous-retention", *anonymousRetention)
		if err != nil {
			return printErr("Invalid Auth settings", err)
		}
		change.AnonymousRetentionDays = &days
	}
	if change.Anonymous != nil && *change.Anonymous && !given["captcha"] {
		PrintWarn(osStderr, "anyone can now create users in %s; turn on CAPTCHA (--captcha) unless it already is", fs.Arg(0))
	}
	if given["passkey-rp-id"] {
		rpID := *passkeyRPID
		if rpID == "none" {
			rpID = ""
		}
		change.PasskeyRPID = &rpID
	}
	set, unset, err := change.Secrets()
	if err != nil {
		return printErr("Invalid Auth settings", err)
	}
	if err := authConfigExtras(given, set, &unset, *emailProvider, *emailFrom, *emailKeyStdin, *authzFile); err != nil {
		return printErr("Invalid Auth settings", err)
	}
	if given["events"] {
		on, err := parseOnOff("events", *events)
		if err != nil {
			return printErr("Invalid Auth settings", err)
		}
		set["AUTH_LIFECYCLE_EVENTS"] = authFlag(on)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	app, err := client.GetApp(ctx, fs.Arg(0))
	if err != nil {
		return printErr("Could not find the Auth app", err)
	}
	if len(set) == 0 && len(unset) == 0 {
		return showAuthConfig(ctx, app)
	}
	if set["AUTH_SSO"] == "1" {
		if err := ensureAuthSAMLKey(ctx, client, app.Slug, *scope, set); err != nil {
			return printErr("Could not create the SAML key", err)
		}
	}
	// Lifecycle events publish with the same events:publish key as workflow
	// starts; the key is minted once and kept when events are turned off.
	if set["AUTH_LIFECYCLE_EVENTS"] == "1" {
		if err := ensureAuthEventsKey(ctx, client, app.Slug, *scope); err != nil {
			return printErr("Could not create the events key", err)
		}
	}
	if err := applyAuthSecrets(ctx, client, app.Slug, *scope, set, unset); err != nil {
		return printErr("Could not update Auth settings", err)
	}
	restartAuthApp(ctx, client, app.Slug)
	if jsonOutput {
		names := append(sortedKeys(set), unset...)
		return jsonOut(writeJSON(map[string]any{"app": app.Slug, "updated": names}))
	}
	PrintOK(osStdout, "Updated %d Auth settings for %s; it restarts to apply them", len(set)+len(unset), app.Slug)
	if set["GOTRUE_EXTERNAL_PHONE_ENABLED"] == "true" && !given["captcha"] {
		if running, err := fetchAuthSettings(ctx, app); err == nil && running.Captcha == nil {
			PrintWarn(osStderr, "CAPTCHA is off: anyone can make %s text codes at your SMS account's expense. Turn it on with --captcha.", app.Slug)
		}
	}
	return 0
}

// ensureAuthSAMLKey adds the SAML signing key the first time SSO is turned
// on. An existing key is kept, so identity providers keep trusting the app.
func ensureAuthSAMLKey(ctx context.Context, client *api.Client, app, scope string, set map[string]string) error {
	list, err := client.ListSecretsWithScope(ctx, app, scope)
	if err != nil {
		return err
	}
	for _, secret := range list.Secrets {
		if secret.Key == authsettings.SAMLKeySecret && (secret.Scope == "" || secret.Scope == scope) {
			return nil
		}
	}
	key, err := authsettings.NewSAMLKey()
	if err != nil {
		return err
	}
	set[authsettings.SAMLKeySecret] = key
	return nil
}

func authMFASummary(settings authPublicSettings) string {
	methods := []string{}
	for _, m := range []struct {
		on   bool
		name string
	}{{settings.MFA.TOTP, "authenticator app"}, {settings.MFA.Phone, "SMS code"}, {settings.MFA.RecoveryCodes, "recovery codes"}} {
		if m.on {
			methods = append(methods, m.name)
		}
	}
	if len(methods) == 0 {
		return "off (enrolled users keep verifying)"
	}
	return strings.Join(methods, ", ")
}

// authSMSFlags are phone sign-in's settings. The provider's secret comes
// only from stdin or a terminal prompt.
type authSMSFlags struct {
	provider, account, sender, countries *string
	secretIn                             *bool
}

func (f authSMSFlags) apply(given map[string]bool, change *authsettings.Change) error {
	switch {
	case given["sms"] && *f.provider == "off":
		if given["sms-account"] || given["sms-sender"] || *f.secretIn {
			return errors.New("--sms off takes no account, sender or secret")
		}
		change.SMS = &authsettings.SMS{}
	case given["sms"]:
		secret, _, err := readRealtimeEndpointSecret("", *f.secretIn, "sms-secret", "SMS provider secret: ", api.AuthProviderSecretMaxBytes, true)
		if err != nil {
			return err
		}
		change.SMS = &authsettings.SMS{Provider: *f.provider, Account: *f.account, Sender: *f.sender, Secret: secret}
	case given["sms-account"] || given["sms-sender"] || *f.secretIn:
		return errors.New("--sms-account, --sms-sender and --sms-secret-stdin need --sms PROVIDER")
	}
	if given["sms-countries"] {
		codes := []string{}
		if *f.countries != "all" {
			codes = strings.Split(*f.countries, ",")
		}
		change.SMSCountries = &codes
	}
	return nil
}

func parseAuthChange(given map[string]bool, change *authsettings.Change, siteURL, redirects, signup, autoconfirm string, minLength int, require, leaked, mfa, passkeys string) error {
	if given["site-url"] {
		change.SiteURL = &siteURL
	}
	if given["redirect-urls"] {
		list := []string{}
		if redirects != "none" {
			for _, value := range strings.Split(redirects, ",") {
				if value = strings.TrimSpace(value); value != "" {
					list = append(list, value)
				}
			}
		}
		change.RedirectURLs = &list
	}
	if given["password-min-length"] {
		change.PasswordMinLength = &minLength
	}
	if given["password-require"] {
		classes := []string{}
		if require != "none" {
			for _, class := range strings.Split(require, ",") {
				classes = append(classes, strings.TrimSpace(class))
			}
		}
		change.PasswordRequire = &classes
	}
	for name, target := range map[string]**bool{"signup": &change.Signup, "autoconfirm": &change.Autoconfirm, "leaked-password-check": &change.LeakedPasswordCheck, "mfa": &change.MFA, "passkeys": &change.Passkeys} {
		if !given[name] {
			continue
		}
		value := map[string]string{"signup": signup, "autoconfirm": autoconfirm, "leaked-password-check": leaked, "mfa": mfa, "passkeys": passkeys}[name]
		on, err := parseOnOff(name, value)
		if err != nil {
			return err
		}
		*target = &on
	}
	return nil
}

// authHostedFlags are the hosted-page and activity settings.
type authHostedFlags struct {
	name, color, logo, captcha, captchaSiteKey, retention *string
	captchaSecretIn                                       *bool
}

func (f authHostedFlags) apply(given map[string]bool, change *authsettings.Change) error {
	clearable := func(flag string, value *string, target **string) {
		if given[flag] {
			v := *value
			if v == "none" {
				v = ""
			}
			*target = &v
		}
	}
	clearable("brand-name", f.name, &change.UIName)
	clearable("brand-color", f.color, &change.UIColor)
	clearable("brand-logo-url", f.logo, &change.UILogoURL)
	switch {
	case given["captcha"] && *f.captcha == "off":
		if given["captcha-site-key"] || *f.captchaSecretIn {
			return errors.New("--captcha off takes no site key or secret")
		}
		change.Captcha = &authsettings.Captcha{}
	case given["captcha"]:
		secret, _, err := readRealtimeEndpointSecret("", *f.captchaSecretIn, "captcha-secret", "CAPTCHA secret key: ", api.AuthProviderSecretMaxBytes, true)
		if err != nil {
			return err
		}
		change.Captcha = &authsettings.Captcha{Provider: *f.captcha, SiteKey: *f.captchaSiteKey, Secret: secret}
	case given["captcha-site-key"] || *f.captchaSecretIn:
		return errors.New("--captcha-site-key and --captcha-secret-stdin need --captcha turnstile or hcaptcha")
	}
	if given["activity-retention"] {
		days, err := parseRetentionDays("activity-retention", *f.retention)
		if err != nil {
			return err
		}
		change.ActivityRetentionDays = &days
	}
	return nil
}

// parseRetentionDays reads a number of days, or forever as 0.
func parseRetentionDays(name, value string) (int, error) {
	if value == "forever" {
		return 0, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("--%s must be a number of days or forever", name)
	}
	return n, nil
}

func parseOnOff(name, value string) (bool, error) {
	switch value {
	case "on":
		return true, nil
	case "off":
		return false, nil
	}
	return false, fmt.Errorf("--%s must be on or off", name)
}

// authConfigExtras adds the email provider and roles document, which only
// the CLI sets: their inputs are a secret from stdin and a local file.
func authConfigExtras(given map[string]bool, set map[string]string, unset *[]string, provider, from string, keyFromStdin bool, authzFile string) error {
	switch {
	case provider == "none":
		if given["email-from"] || keyFromStdin {
			return errors.New("--email-provider none takes no --email-from or API key")
		}
		*unset = append(*unset, authEmailSecrets...)
	case given["email-provider"] || given["email-from"] || keyFromStdin:
		var opts authCreateOptions
		if err := authEmailSettings(&opts, provider, from, keyFromStdin); err != nil {
			return err
		}
		set["AUTH_EMAIL_PROVIDER"], set["AUTH_EMAIL_FROM"], set["AUTH_EMAIL_API_KEY"] = opts.emailProvider, opts.emailFrom, opts.emailAPIKey
	}
	if given["authz"] {
		authz, err := readAuthzFile(authzFile)
		if err != nil {
			return fmt.Errorf("--authz: %w", err)
		}
		set["AUTH_AUTHZ"] = authz
	}
	return nil
}

func applyAuthSecrets(ctx context.Context, client *api.Client, app, scope string, set map[string]string, unset []string) error {
	for _, name := range sortedKeys(set) {
		if err := client.SetSecretWithScope(ctx, app, name, set[name], scope); err != nil {
			return fmt.Errorf("set %s: %w", name, err)
		}
	}
	for _, name := range unset {
		if err := client.UnsetSecretWithScope(ctx, app, name, scope); err != nil && !secretAbsent(err) {
			return fmt.Errorf("remove %s: %w", name, err)
		}
	}
	return nil
}

// secretAbsent reports a removal of a secret the app does not have, which
// apid answers with 400 secret_not_found; removing it is then already done.
func secretAbsent(err error) bool {
	var apiErr *api.APIError
	return isNotFound(err) || (errors.As(err, &apiErr) && apiErr.Problem.Code == api.CodeSecretNotFound)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func showAuthConfig(ctx context.Context, app api.AppResponse) int {
	settings, err := fetchAuthSettings(ctx, app)
	if err != nil {
		return printErr("Could not read Auth settings", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(settings))
	}
	onOff := func(on bool) string {
		if on {
			return "on"
		}
		return "off"
	}
	redirects := "none"
	if len(settings.RedirectURLs) > 0 {
		redirects = strings.Join(settings.RedirectURLs, ", ")
	}
	signup := onOff(settings.Signup.Enabled)
	if settings.Signup.Enabled && settings.Signup.Autoconfirm {
		signup += " (no email confirmation)"
	}
	providers := append([]string{"email"}, settings.Providers...)
	passwords := fmt.Sprintf("at least %d characters", settings.Password.MinLength)
	if len(settings.Password.Required) > 0 {
		passwords += "; must include " + strings.Join(settings.Password.Required, ", ")
	}
	passwords += "; leaked-password check " + onOff(settings.Password.LeakedPasswordCheck)
	branding := settings.Branding.Name
	if settings.Branding.Color != nil {
		branding += ", " + *settings.Branding.Color
	}
	if settings.Branding.LogoURL != nil {
		branding += ", logo " + *settings.Branding.LogoURL
	}
	captchaState := "off"
	if settings.Captcha != nil {
		captchaState = settings.Captcha.Provider
	}
	retention := "forever"
	if settings.Activity.RetentionDays != nil {
		retention = fmt.Sprintf("%d days", *settings.Activity.RetentionDays)
	}
	passkeys := "off"
	if settings.Passkeys.Enabled {
		passkeys = "on, for " + settings.Passkeys.RPID
	}
	events := "off"
	if settings.Events.Lifecycle {
		events = "on (auth.user.* events from this app)"
	}
	email := "not configured (email sign-in links and invitations fail)"
	if settings.Email.Configured {
		email = "configured"
		if settings.Email.Provider != "" {
			email = settings.Email.Provider + ", from " + settings.Email.From
		}
	}
	sso := "off"
	if settings.SSO.Enabled {
		sso = "on; identity providers trust entity ID " + settings.SSO.EntityID + " with ACS URL " + settings.SSO.ACSURL
		if settings.SSO.SelfService {
			sso += "; organizations may connect their own"
		}
	}
	oauth := "off"
	if settings.OAuth.Enabled {
		oauth = "on; issuer " + settings.OAuth.Issuer + ", clients register themselves: " + onOff(settings.OAuth.DynamicRegistration)
	}
	anonymousUsers := "off"
	if settings.Anonymous.Enabled {
		anonymousUsers = "on, kept forever"
		if settings.Anonymous.RetentionDays != nil {
			anonymousUsers = fmt.Sprintf("on, deleted after %d idle days", *settings.Anonymous.RetentionDays)
		}
	}
	phone := "off"
	if settings.Phone.Enabled {
		phone = "on, through " + settings.Phone.Provider + "; texts any country"
		if len(settings.Phone.Countries) > 0 {
			phone = "on, through " + settings.Phone.Provider + "; texts only +" + strings.Join(settings.Phone.Countries, ", +")
		}
	}
	keys := settings.Keys.Signing
	if n := len(settings.Keys.Verifying); n > 0 {
		keys += fmt.Sprintf(" (%d more verifying)", n)
	}
	PrintOK(osStdout, "Auth settings for %s", app.Slug)
	for _, row := range [][2]string{
		{"site URL", settings.SiteURL}, {"redirect URLs", redirects}, {"sign-up", signup},
		{"sign-in methods", strings.Join(providers, ", ")}, {"passwords", passwords},
		{"MFA", authMFASummary(settings)}, {"passkeys", passkeys}, {"phone (SMS)", phone}, {"organization SSO", sso}, {"OAuth server", oauth}, {"anonymous users", anonymousUsers}, {"identity linking", onOff(settings.IdentityLinking)}, {"CAPTCHA", captchaState}, {"hosted pages", branding},
		{"email delivery", email}, {"lifecycle events", events}, {"activity kept", retention}, {"signing key", keys},
	} {
		_, _ = fmt.Fprintf(osStdout, "  %-16s %s\n", row[0]+":", row[1])
	}
	return 0
}

// cmdAuthProviders lists, enables or disables OAuth sign-in providers.
func cmdAuthProviders(args []string) int {
	verb := "list"
	if len(args) > 0 && (args[0] == "list" || args[0] == "enable" || args[0] == "disable") {
		verb, args = args[0], args[1:]
	}
	fs := newFlagSet("auth providers "+verb, flag.ContinueOnError)
	scope := fs.String("scope", api.DefaultEnvScope, "environment scope")
	clientID := fs.String("client-id", "", "OAuth client ID from the provider")
	secretStdin := fs.Bool("client-secret-stdin", false, "read the OAuth client secret from stdin (prompted on a terminal)")
	serverURL := fs.String("url", "", "provider server URL (Keycloak, or a self-hosted GitLab, Azure tenant or WorkOS)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	want := map[string]int{"list": 1, "enable": 2, "disable": 2}[verb]
	if fs.NArg() != want || !api.ValidAppSlug(fs.Arg(0)) || api.ValidateScope(*scope) != nil || (want == 2 && !slices.Contains(authsettings.Providers, fs.Arg(1))) {
		PrintUsage(os.Stderr, authProvidersUsage+"\nproviders: "+strings.Join(authsettings.Providers, ", "), "auth")
		return 1
	}
	var set map[string]string
	if verb == "enable" {
		secret, _, err := readRealtimeEndpointSecret("", *secretStdin, "client-secret", "Client secret: ", api.AuthProviderSecretMaxBytes, true)
		if err == nil {
			set, err = authsettings.Provider(fs.Arg(1), *clientID, secret, *serverURL)
		}
		if err != nil {
			return printErr("Invalid provider settings", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	app, err := client.GetApp(ctx, fs.Arg(0))
	if err != nil {
		return printErr("Could not find the Auth app", err)
	}
	if verb == "list" {
		return listAuthProviders(ctx, app)
	}
	// Enabling replaces the provider's settings and disabling removes them
	// all, so no stale client secret or server URL is left behind.
	list, err := client.ListSecretsWithScope(ctx, app.Slug, *scope)
	if err != nil {
		return printErr("Could not update the provider", err)
	}
	prefix := authsettings.ProviderPrefix(fs.Arg(1))
	unset := []string{}
	for _, secret := range list.Secrets {
		if _, kept := set[secret.Key]; strings.HasPrefix(secret.Key, prefix) && !kept && (secret.Scope == "" || secret.Scope == *scope) {
			unset = append(unset, secret.Key)
		}
	}
	if err := applyAuthSecrets(ctx, client, app.Slug, *scope, set, unset); err != nil {
		return printErr("Could not update the provider", err)
	}
	restartAuthApp(ctx, client, app.Slug)
	external, _ := authExternalOrigin(app)
	callback := external + "/auth/v1/callback"
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"app": app.Slug, "provider": fs.Arg(1), "enabled": verb == "enable", "callback_url": callback}))
	}
	if verb == "disable" {
		PrintOK(osStdout, "Disabled %s sign-in for %s and removed its credentials", fs.Arg(1), app.Slug)
		return 0
	}
	PrintOK(osStdout, "Enabled %s sign-in for %s; it restarts to apply it", fs.Arg(1), app.Slug)
	_, _ = fmt.Fprintf(osStdout, "Register this callback URL with %s:\n  %s\n", fs.Arg(1), callback)
	return 0
}

func listAuthProviders(ctx context.Context, app api.AppResponse) int {
	settings, err := fetchAuthSettings(ctx, app)
	if err != nil {
		return printErr("Could not read Auth settings", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"providers": settings.Providers}))
	}
	if len(settings.Providers) == 0 {
		_, _ = fmt.Fprintf(osStdout, "Only email sign-in is enabled for %s. Add one with `gregale auth providers enable %s PROVIDER --client-id ID --client-secret-stdin`.\n", app.Slug, app.Slug)
		return 0
	}
	for _, provider := range settings.Providers {
		_, _ = fmt.Fprintln(osStdout, provider)
	}
	return 0
}
