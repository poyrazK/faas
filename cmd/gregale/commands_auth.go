package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authsettings"
)

// Application Auth (ADR-1190): a pinned GoTrue server deployed as an ordinary
// app, bound to the customer's database through the `auth` credential mode.
const (
	authAudience      = "authenticated"
	authJWTSecretName = "AUTH_JWT_SECRET"
	authEventsKeyName = "AUTH_EVENTS_KEY"
)

func cmdAuth(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale auth <create|status|config|providers|keys|admin-key|emails|import|activity|sso|oauth-clients>", "auth")
		return 1
	}
	switch args[0] {
	case "config":
		return cmdAuthConfig(args[1:])
	case "providers":
		return cmdAuthProviders(args[1:])
	case "keys":
		return cmdAuthKeys(args[1:])
	case "create":
		return cmdAuthCreate(args[1:])
	case "status":
		return cmdAuthStatus(args[1:])
	case "admin-key":
		return cmdAuthAdminKey(args[1:])
	case "emails":
		return cmdAuthEmails(args[1:])
	case "import":
		return cmdAuthImport(args[1:])
	case "activity":
		return cmdAuthActivity(args[1:])
	case "sso":
		return cmdAuthSSO(args[1:])
	case "oauth-clients":
		return cmdAuthOAuthClients(args[1:])
	case "login", "logout", "whoami", "token":
		// `gregale auth` manages an application's own users, not the
		// Gregale account; other CLIs use the same verbs for account login.
		return printErr("Not an account command", errors.New("`gregale auth` manages application Auth; use `gregale "+args[0]+"` for your Gregale account"))
	default:
		return printErr("Unknown auth command", errors.New("use create, status, config, providers, keys, admin-key, emails, import, activity, sso or oauth-clients"))
	}
}

type authCreateOptions struct {
	database, scope, siteURL, authz       string
	emailProvider, emailFrom, emailAPIKey string
	redirectURLs                          []string
	autoconfirm, noSignup, workflows      bool
}

func cmdAuthCreate(args []string) int {
	fs := newFlagSet("auth create", flag.ContinueOnError)
	database := fs.String("database", "", "managed PostgreSQL name or ID")
	scope := fs.String("scope", api.DefaultEnvScope, "environment scope")
	siteURL := fs.String("site-url", "", "application URL users return to after sign-in")
	redirects := fs.String("redirect-urls", "", "comma-separated additional allowed redirect URLs")
	autoconfirm := fs.Bool("autoconfirm", false, "sign users in without email confirmation (development only)")
	noSignup := fs.Bool("disable-signup", false, "allow only invited or administrator-created users")
	resume := fs.Bool("resume", false, "resume configuration and deployment of an existing app")
	authzFile := fs.String("authz", "", "JSON file declaring custom roles and policies (applied on deploy)")
	emailProvider := fs.String("email-provider", "", "HTTPS email provider: resend or postmark")
	emailFrom := fs.String("email-from", "", "sender address, or a display name with the address in angle brackets")
	emailKeyStdin := fs.Bool("email-api-key-stdin", false, "read the email provider API key from stdin (prompted on a terminal)")
	workflows := fs.Bool("workflows", false, "let authorized users start workflows (mints an events:publish key sealed in the app)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	opts := authCreateOptions{database: *database, scope: *scope, siteURL: *siteURL, autoconfirm: *autoconfirm, noSignup: *noSignup, workflows: *workflows}
	for _, value := range strings.Split(*redirects, ",") {
		if value = strings.TrimSpace(value); value != "" {
			opts.redirectURLs = append(opts.redirectURLs, value)
		}
	}
	if fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || opts.database == "" || api.ValidateScope(opts.scope) != nil || !authRedirectURL(opts.siteURL) {
		PrintUsage(os.Stderr, "usage: gregale auth create NAME --database DATABASE --site-url URL [--redirect-urls URLS] [--scope SCOPE] [--autoconfirm] [--disable-signup] [--resume]", "auth")
		return 1
	}
	if *emailProvider != "" || *emailFrom != "" || *emailKeyStdin {
		if err := authEmailSettings(&opts, *emailProvider, *emailFrom, *emailKeyStdin); err != nil {
			return printErr("Invalid email settings", err)
		}
	}
	if *authzFile != "" {
		authz, err := readAuthzFile(*authzFile)
		if err != nil {
			return printErr("Invalid --authz", err)
		}
		opts.authz = authz
	}
	if len(opts.redirectURLs) > api.AuthMaxRedirectURLs {
		return printErr("Too many redirect URLs", fmt.Errorf("at most %d redirect URLs are allowed; got %d", api.AuthMaxRedirectURLs, len(opts.redirectURLs)))
	}
	for _, value := range opts.redirectURLs {
		if !authRedirectURL(value) {
			return printErr("Invalid redirect URL", fmt.Errorf("%q must be HTTPS or a loopback HTTP URL without credentials or fragments", value))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	db, err := resolveManagedPostgresDatabase(ctx, client, opts.database)
	if err != nil {
		return printErr("Could not resolve PostgreSQL database", err)
	}
	if db.State != "ready" {
		return printErr("Database unavailable", errors.New("the managed database must be ready"))
	}
	var app api.AppResponse
	if *resume {
		app, err = client.GetApp(ctx, fs.Arg(0))
	} else {
		requireAuthn := false
		app, err = client.CreateApp(ctx, api.CreateAppRequest{Slug: fs.Arg(0), Type: "app", RequireAuthn: &requireAuthn})
	}
	if err != nil {
		return printErr("Could not create Auth app", err)
	}
	if app.Type != "" && app.Type != "app" {
		return printErr("Invalid Auth app", errors.New("application Auth requires an app workload"))
	}
	external, err := authExternalOrigin(app)
	if err != nil {
		return printErr("Could not determine the Auth URL", err)
	}
	// Retain partially configured intent on failure: the ordinary app and
	// binding recovery paths own cleanup and retries, never an ad-hoc rollback.
	if err := configureAuth(ctx, client, app, db.ID, external, opts); err != nil {
		return printErr("Auth configuration failed; the app is retained for recovery", err)
	}
	deployArgs := []string{"--name", app.Slug, "--template", "auth", "--dockerfile", "--healthcheck-path", "/healthz", "--no-doctor"}
	if opts.scope != api.DefaultEnvScope {
		deployArgs = append(deployArgs, "--environment", opts.scope)
	}
	if code := cmdDeployTarballToExisting(ctx, deployArgs, true); code != 0 {
		return code
	}
	if !jsonOutput {
		issuer := external + "/auth/v1"
		PrintOK(osStdout, "Auth is deployed for %s", app.Slug)
		_, _ = fmt.Fprintf(osStdout, "  issuer:   %s\n  jwks:     %s/.well-known/jwks.json\n  audience: %s\n", issuer, issuer, authAudience)
		_, _ = fmt.Fprintf(osStdout, "Verify these tokens in a Data API:\n  gregale data-api create <name> --database %s --issuer %s --jwks-url %s/.well-known/jwks.json --audience %s\n", opts.database, issuer, issuer, authAudience)
	}
	return 0
}

func configureAuth(ctx context.Context, client *api.Client, app api.AppResponse, database, external string, opts authCreateOptions) error {
	binding, err := client.CreateManagedPostgresBinding(api.ContextWithIdempotencyKey(ctx, addResourceIdempotencyKey("auth", app.ID, database, opts.scope)), database, api.CreateManagedPostgresBindingRequest{AppID: app.ID, Scope: opts.scope, EnvironmentKey: "DATABASE_URL", Access: "auth"})
	if err != nil {
		return err
	}
	if _, err = waitForManagedPostgresBinding(ctx, client, binding, addResourceDefaultWait); err != nil {
		return err
	}
	// PostgreSQL's extra egress port remains subject to the normal plan gate.
	ports := []int{5432}
	// End users reach the Auth app anonymously; owner-key edge defaults
	// would reject every sign-in before GoTrue sees it.
	requireAuthn := false
	publicAuth := openPublicAuthAfterTokenRemoval(app)
	if _, err = client.UpdateApp(ctx, app.Slug, api.UpdateAppRequest{EgressPorts: &ports, RequireAuthn: &requireAuthn, PublicAuth: publicAuth, SetPublicAuth: publicAuth != nil}); err != nil {
		return err
	}
	settings := map[string]string{
		// Workflow starts publish app-scoped events back through this API.
		"AUTH_APP_SLUG":        app.Slug,
		"AUTH_GREGALE_API_URL": strings.TrimRight(client.BaseURL(), "/"),
		"AUTH_EXTERNAL_URL":    external,
		"AUTH_SITE_URL":        opts.siteURL,
		"AUTH_REDIRECT_URLS":   strings.Join(opts.redirectURLs, ","),
		"AUTH_AUTOCONFIRM":     authFlag(opts.autoconfirm),
		"AUTH_DISABLE_SIGNUP":  authFlag(opts.noSignup),
	}
	// AUTH_AUTHZ and the email settings are kept when the flags are left out.
	if opts.authz != "" {
		settings["AUTH_AUTHZ"] = opts.authz
	}
	if opts.emailProvider != "" {
		settings["AUTH_EMAIL_PROVIDER"], settings["AUTH_EMAIL_FROM"], settings["AUTH_EMAIL_API_KEY"] = opts.emailProvider, opts.emailFrom, opts.emailAPIKey
	}
	// Signing material is generated once. A resumed or repeated create must
	// not replace keys that already signed live sessions.
	existing, err := client.ListSecretsWithScope(ctx, app.Slug, opts.scope)
	if err != nil {
		return err
	}
	present := map[string]bool{}
	hasSigningKey := false
	for _, secret := range existing.Secrets {
		if secret.Scope == "" || secret.Scope == opts.scope {
			present[secret.Key] = true
			hasSigningKey = hasSigningKey || authSigningKeyName.MatchString(secret.Key)
		}
	}
	if !hasSigningKey {
		// The key is written before the marker that makes it sign.
		id, jwk, err := newAuthSigningKey()
		if err != nil {
			return err
		}
		if err := client.SetSecretWithScope(ctx, app.Slug, authSigningKeyPrefix+id, jwk, opts.scope); err != nil {
			return err
		}
		if err := client.SetSecretWithScope(ctx, app.Slug, authSignerPrefix+id, strconv.FormatInt(time.Now().Unix(), 10), opts.scope); err != nil {
			return err
		}
	}
	if opts.workflows && !present[authEventsKeyName] {
		if err := mintAuthEventsKey(ctx, client, app.Slug, opts.scope); err != nil {
			return err
		}
	}
	if !present[authJWTSecretName] {
		secret, err := randomAuthSecret()
		if err != nil {
			return err
		}
		settings[authJWTSecretName] = secret
	}
	for _, key := range []string{"AUTH_APP_SLUG", "AUTH_GREGALE_API_URL", "AUTH_EXTERNAL_URL", "AUTH_SITE_URL", "AUTH_REDIRECT_URLS", "AUTH_AUTOCONFIRM", "AUTH_DISABLE_SIGNUP", "AUTH_AUTHZ", "AUTH_EMAIL_PROVIDER", "AUTH_EMAIL_FROM", "AUTH_EMAIL_API_KEY", authJWTSecretName} {
		value, ok := settings[key]
		if !ok {
			continue
		}
		if err = client.SetSecretWithScope(ctx, app.Slug, key, value, opts.scope); err != nil {
			return err
		}
	}
	return nil
}

// authEmailSettings validates the HTTPS email provider. The API key comes
// only from stdin or a terminal prompt, never from an argument.
func authEmailSettings(opts *authCreateOptions, provider, from string, keyFromStdin bool) error {
	if !slices.Contains(authsettings.EmailProviders, provider) {
		return errors.New("--email-provider must be resend or postmark")
	}
	if !authsettings.EmailFrom(from) {
		return errors.New(`--email-from must be an address or "Name <address>"`)
	}
	key, _, err := readRealtimeEndpointSecret("", keyFromStdin, "email-api-key", "Email provider API key: ", api.AuthEmailAPIKeyMaxBytes, true)
	if err != nil {
		return err
	}
	if _, _, err := (authsettings.Change{Email: &authsettings.Email{Provider: provider, From: from, APIKey: key}}).Secrets(); err != nil {
		return err
	}
	opts.emailProvider, opts.emailFrom, opts.emailAPIKey = provider, from, key
	return nil
}

// readAuthzFile checks the declarative roles and policies document's shape;
// gregale_auth.apply_authz validates its contents in the release task.
func readAuthzFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Size() > api.AuthAuthzConfigBytes {
		return "", fmt.Errorf("%s exceeds %d bytes", path, api.AuthAuthzConfigBytes)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil || doc == nil {
		return "", errors.New("must be a JSON object with roles and/or policies")
	}
	for key := range doc {
		if key != "roles" && key != "policies" {
			return "", fmt.Errorf("unknown key %q; use roles and policies", key)
		}
	}
	compact, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(compact), nil
}

// authIssuer is what a token verifier needs to accept an Auth app's tokens.
type authIssuer struct{ Issuer, JWKSURL, Audience string }

// resolveAuthIssuer backs the --auth flags of the Data API and edge JWT rules.
func resolveAuthIssuer(ctx context.Context, client *api.Client, name string) (authIssuer, error) {
	if !api.ValidAppSlug(name) {
		return authIssuer{}, fmt.Errorf("invalid Auth app name %q", name)
	}
	app, err := client.GetApp(ctx, name)
	if err != nil {
		return authIssuer{}, err
	}
	external, err := authExternalOrigin(app)
	if err != nil {
		return authIssuer{}, err
	}
	issuer := external + "/auth/v1"
	return authIssuer{Issuer: issuer, JWKSURL: issuer + "/.well-known/jwks.json", Audience: authAudience}, nil
}

func authFlag(enabled bool) string {
	if enabled {
		return "1"
	}
	return "0"
}

// authRedirectURL mirrors the template's validation: HTTPS, or HTTP only on a
// loopback development host; never credentials or fragments.
func authRedirectURL(value string) bool { return authsettings.RedirectURL(value) }

func authExternalOrigin(app api.AppResponse) (string, error) {
	address := app.CanonicalURL
	if address == "" {
		address = app.URL
	}
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("application Auth requires an HTTPS app URL without credentials, paths, queries or fragments")
	}
	return "https://" + u.Host, nil
}

// newAuthSigningKey returns a key ID and one ES256 private JWK for the
// AUTH_JWT_KEY_<ID> secret. The key leaves this process only as a sealed app
// secret and is never printed.
func newAuthSigningKey() (string, string, error) { return authsettings.NewSigningKey() }

// mintAuthEventsKey seals a key that can only publish events, for workflow
// starts and lifecycle events: a compromised Auth app cannot deploy, read
// secrets or administer the account with it.
func mintAuthEventsKey(ctx context.Context, client *api.Client, app, scope string) error {
	key, err := client.CreateKey(ctx, "auth-"+app+"-events", []string{api.ScopeEventsPublish})
	if err != nil {
		return fmt.Errorf("mint the events key: %w", err)
	}
	if err := client.SetSecretWithScope(ctx, app, authEventsKeyName, key.Plaintext, scope); err != nil {
		_ = client.DeleteKey(ctx, key.ID)
		return err
	}
	return nil
}

// ensureAuthEventsKey mints the events key unless the app already has one.
func ensureAuthEventsKey(ctx context.Context, client *api.Client, app, scope string) error {
	list, err := client.ListSecretsWithScope(ctx, app, scope)
	if err != nil {
		return err
	}
	for _, secret := range list.Secrets {
		if secret.Key == authEventsKeyName && (secret.Scope == "" || secret.Scope == scope) {
			return nil
		}
	}
	return mintAuthEventsKey(ctx, client, app, scope)
}

func randomAuthSecret() (string, error) {
	var secret [48]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", fmt.Errorf("generate JWT secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(secret[:]), nil
}

type authStatus struct {
	App      string `json:"app"`
	Issuer   string `json:"issuer"`
	JWKSURL  string `json:"jwks_url"`
	Audience string `json:"audience"`
	Ready    bool   `json:"ready"`
}

func cmdAuthStatus(args []string) int {
	fs := newFlagSet("auth status", flag.ContinueOnError)
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) {
		PrintUsage(os.Stderr, "usage: gregale auth status NAME", "auth")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	app, err := client.GetApp(ctx, fs.Arg(0))
	if err != nil {
		return printErr("Could not inspect Auth app", err)
	}
	external, err := authExternalOrigin(app)
	if err != nil {
		return printErr("Could not inspect Auth status", err)
	}
	issuer := external + "/auth/v1"
	status := authStatus{App: app.Slug, Issuer: issuer, JWKSURL: issuer + "/.well-known/jwks.json", Audience: authAudience, Ready: authReady(ctx, external+"/healthz")}
	if jsonOutput {
		return jsonOut(writeJSON(status))
	}
	state := "not ready"
	if status.Ready {
		state = "ready"
	}
	PrintOK(osStdout, "Auth for %s is %s", status.App, state)
	_, _ = fmt.Fprintf(osStdout, "  issuer:   %s\n  jwks:     %s\n  audience: %s\n", status.Issuer, status.JWKSURL, status.Audience)
	return 0
}

// authReady probes the public health endpoint without credentials or
// redirects; a parked app wakes to answer it.
func authReady(ctx context.Context, endpoint string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode == http.StatusOK
}
