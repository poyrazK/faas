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
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authsettings"
)

const (
	authConfigUsage    = "usage: gregale auth config NAME [--site-url URL] [--redirect-urls URLS] [--signup on|off] [--autoconfirm on|off] [--password-min-length N] [--password-require CLASSES] [--leaked-password-check on|off] [--mfa on|off] [--email-provider resend|postmark|none --email-from FROM --email-api-key-stdin] [--authz FILE]"
	authProvidersUsage = "usage: gregale auth providers list NAME | gregale auth providers enable NAME PROVIDER --client-id ID [--client-secret-stdin] [--url URL] | gregale auth providers disable NAME PROVIDER"
)

var authEmailSecrets = []string{"AUTH_EMAIL_PROVIDER", "AUTH_EMAIL_FROM", "AUTH_EMAIL_API_KEY"}

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
	emailProvider := fs.String("email-provider", "", "HTTPS email provider: resend, postmark, or none to remove it")
	emailFrom := fs.String("email-from", "", "sender address, or a display name with the address in angle brackets")
	emailKeyStdin := fs.Bool("email-api-key-stdin", false, "read the email provider API key from stdin (prompted on a terminal)")
	authzFile := fs.String("authz", "", "JSON file declaring custom roles and policies (applied on restart)")
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
	if err := parseAuthChange(given, &change, *siteURL, *redirects, *signup, *autoconfirm, *minLength, *require, *leaked, *mfa); err != nil {
		return printErr("Invalid Auth settings", err)
	}
	set, unset, err := change.Secrets()
	if err != nil {
		return printErr("Invalid Auth settings", err)
	}
	if err := authConfigExtras(given, set, &unset, *emailProvider, *emailFrom, *emailKeyStdin, *authzFile); err != nil {
		return printErr("Invalid Auth settings", err)
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
	if err := applyAuthSecrets(ctx, client, app.Slug, *scope, set, unset); err != nil {
		return printErr("Could not update Auth settings", err)
	}
	restartAuthApp(ctx, client, app.Slug)
	if jsonOutput {
		names := append(sortedKeys(set), unset...)
		return jsonOut(writeJSON(map[string]any{"app": app.Slug, "updated": names}))
	}
	PrintOK(osStdout, "Updated %d Auth settings for %s; it restarts to apply them", len(set)+len(unset), app.Slug)
	return 0
}

func parseAuthChange(given map[string]bool, change *authsettings.Change, siteURL, redirects, signup, autoconfirm string, minLength int, require, leaked, mfa string) error {
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
	for name, target := range map[string]**bool{"signup": &change.Signup, "autoconfirm": &change.Autoconfirm, "leaked-password-check": &change.LeakedPasswordCheck, "mfa": &change.MFA} {
		if !given[name] {
			continue
		}
		value := map[string]string{"signup": signup, "autoconfirm": autoconfirm, "leaked-password-check": leaked, "mfa": mfa}[name]
		on, err := parseOnOff(name, value)
		if err != nil {
			return err
		}
		*target = &on
	}
	return nil
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
		if err := client.UnsetSecretWithScope(ctx, app, name, scope); err != nil && !isNotFound(err) {
			return fmt.Errorf("remove %s: %w", name, err)
		}
	}
	return nil
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
	email := "not configured (email sign-in links and invitations fail)"
	if settings.Email.Configured {
		email = "configured"
	}
	keys := settings.Keys.Signing
	if n := len(settings.Keys.Verifying); n > 0 {
		keys += fmt.Sprintf(" (%d more verifying)", n)
	}
	PrintOK(osStdout, "Auth settings for %s", app.Slug)
	for _, row := range [][2]string{
		{"site URL", settings.SiteURL}, {"redirect URLs", redirects}, {"sign-up", signup},
		{"sign-in methods", strings.Join(providers, ", ")}, {"passwords", passwords},
		{"MFA (TOTP)", onOff(settings.MFA.TOTP)}, {"email delivery", email}, {"signing key", keys},
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
