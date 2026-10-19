package main

// adr: 1190 — signing-key rotation for application Auth.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authsettings"
)

// Key naming is shared with the dashboard in pkg/authsettings: the newest
// AUTH_JWT_SIGNER_<ID> names the key that signs; the others only verify.
const (
	authSigningKeyPrefix = authsettings.SigningKeyPrefix
	authSignerPrefix     = authsettings.SignerPrefix
)

var (
	authSigningKeyName = authsettings.SigningKeyName
	authSignerName     = authsettings.SignerName
	// authKeyChangePoll is how often a key command re-reads the restarted
	// app; tests shorten it.
	authKeyChangePoll = 3 * time.Second
)

const authKeysUsage = "usage: gregale auth keys <list|create|activate|revoke|rotate> NAME [ID]"

func authKeyKID(id string) string { return authsettings.KeyKID(id) }

func cmdAuthKeys(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, authKeysUsage, "auth")
		return 1
	}
	fs := newFlagSet("auth keys "+args[0], flag.ContinueOnError)
	scope := fs.String("scope", api.DefaultEnvScope, "environment scope")
	if err := parseInterspersed(fs, args[1:]); err != nil {
		return 1
	}
	want := map[string]int{"list": 1, "create": 1, "rotate": 1, "activate": 2, "revoke": 2}[args[0]]
	if want == 0 || fs.NArg() != want || !api.ValidAppSlug(fs.Arg(0)) || (want == 2 && !authAdminKeyID.MatchString(fs.Arg(1))) || api.ValidateScope(*scope) != nil {
		PrintUsage(os.Stderr, authKeysUsage, "auth")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*api.AuthKeyChangeWait)
	defer cancel()
	app, err := client.GetApp(ctx, fs.Arg(0))
	if err != nil {
		return printErr("Could not find the Auth app", err)
	}
	keys := authKeyCommand{client: client, app: app, scope: *scope}
	switch args[0] {
	case "list":
		return keys.list(ctx)
	case "create":
		return keys.create(ctx)
	case "activate":
		return keys.activate(ctx, fs.Arg(1))
	case "revoke":
		return keys.revoke(ctx, fs.Arg(1))
	default:
		return keys.rotate(ctx)
	}
}

type authKeyCommand struct {
	client *api.Client
	app    api.AppResponse
	scope  string
}

// authKeyInventory joins the sealed configuration with what the running app
// reports. running is nil when the app could not be reached.
type authKeyInventory struct {
	created    map[string]string // key ID -> secret creation time
	signers    map[string]bool   // key IDs with an AUTH_JWT_SIGNER_<ID> marker
	running    *authPublicSettings
	runningErr error
}

func (k authKeyCommand) inventory(ctx context.Context) (authKeyInventory, error) {
	list, err := k.client.ListSecretsWithScope(ctx, k.app.Slug, k.scope)
	if err != nil {
		return authKeyInventory{}, err
	}
	inv := authKeyInventory{created: map[string]string{}, signers: map[string]bool{}}
	for _, secret := range list.Secrets {
		if secret.Scope != "" && secret.Scope != k.scope {
			continue
		}
		switch {
		case authSigningKeyName.MatchString(secret.Key):
			inv.created[strings.TrimPrefix(secret.Key, authSigningKeyPrefix)] = secret.CreatedAt
		case authSignerName.MatchString(secret.Key):
			inv.signers[strings.TrimPrefix(secret.Key, authSignerPrefix)] = true
		}
	}
	settings, err := fetchAuthSettings(ctx, k.app)
	if err != nil {
		inv.runningErr = err
	} else {
		inv.running = &settings
	}
	return inv, nil
}

type authKeyRow struct {
	ID        string `json:"id"`
	KID       string `json:"kid"`
	State     string `json:"state"`
	CreatedAt string `json:"created_at,omitempty"`
}

// rows reports each key's state: signing or verifying in the running app;
// activating or pending while a restart loads a change; removing when the
// running app still has a revoked key; unknown when the app is unreachable.
func (inv authKeyInventory) rows() []authKeyRow {
	ids := map[string]bool{}
	for id := range inv.created {
		ids[id] = true
	}
	if inv.running != nil {
		ids[inv.running.Keys.Signing] = true
		for _, id := range inv.running.Keys.Verifying {
			ids[id] = true
		}
	}
	rows := []authKeyRow{}
	for id := range ids {
		row := authKeyRow{ID: id, KID: authKeyKID(id), CreatedAt: inv.created[id]}
		_, sealed := inv.created[id]
		switch {
		case inv.running == nil:
			row.State = "unknown"
		case !sealed:
			row.State = "removing"
		case inv.running.Keys.Signing == id:
			row.State = "signing"
		case !slices.Contains(inv.running.Keys.Verifying, id):
			row.State = "pending"
		case inv.signers[id]:
			row.State = "activating"
		default:
			row.State = "verifying"
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt+rows[i].ID < rows[j].CreatedAt+rows[j].ID })
	return rows
}

func (k authKeyCommand) list(ctx context.Context) int {
	inv, err := k.inventory(ctx)
	if err != nil {
		return printErr("Could not list signing keys", err)
	}
	rows := inv.rows()
	if inv.runningErr != nil {
		PrintWarn(osStderr, "could not read the running Auth app (%v); key states are unknown", inv.runningErr)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"keys": rows}))
	}
	for _, row := range rows {
		_, _ = fmt.Fprintf(osStdout, "%s  %-10s  kid %s  created %s\n", row.ID, row.State, row.KID, row.CreatedAt)
	}
	return 0
}

// create adds a standby key: published for verification so every verifier
// can learn it before it signs.
func (k authKeyCommand) create(ctx context.Context) int {
	id, err := k.addStandby(ctx)
	if err != nil {
		return printErr("Could not add a signing key", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(authKeyRow{ID: id, KID: authKeyKID(id), State: "pending"}))
	}
	PrintOK(osStdout, "Added standby key %s to %s; it verifies tokens but does not sign yet", id, k.app.Slug)
	_, _ = fmt.Fprintf(osStdout, "When `gregale auth keys list %s` shows it as verifying, make it sign:\n  gregale auth keys activate %s %s\n", k.app.Slug, k.app.Slug, id)
	return 0
}

func (k authKeyCommand) addStandby(ctx context.Context) (string, error) {
	inv, err := k.inventory(ctx)
	if err != nil {
		return "", err
	}
	if len(inv.created) >= api.AuthMaxSigningKeys {
		return "", fmt.Errorf("%s already has %d signing keys, the most allowed; revoke a previous key first", k.app.Slug, len(inv.created))
	}
	id, jwk, err := newAuthSigningKey()
	if err != nil {
		return "", err
	}
	if err := k.client.SetSecretWithScope(ctx, k.app.Slug, authSigningKeyPrefix+id, jwk, k.scope); err != nil {
		return "", err
	}
	restartAuthApp(ctx, k.client, k.app.Slug)
	return id, nil
}

func (k authKeyCommand) activate(ctx context.Context, id string) int {
	previous, err := k.makeSigner(ctx, id)
	if err != nil {
		return printErr("Could not activate the signing key", err)
	}
	return k.reportActivated(id, previous)
}

// makeSigner makes a published key sign and returns the previous signer. It
// writes the new marker before deleting the old ones, so a restart at any
// point finds a signer, and waits until the running app signs with the key.
func (k authKeyCommand) makeSigner(ctx context.Context, id string) (string, error) {
	inv, err := k.inventory(ctx)
	if err != nil {
		return "", err
	}
	if _, ok := inv.created[id]; !ok {
		return "", fmt.Errorf("%s has no signing key %s", k.app.Slug, id)
	}
	if inv.running == nil {
		return "", fmt.Errorf("cannot confirm the running Auth app publishes key %s: %w", id, inv.runningErr)
	}
	previous := inv.running.Keys.Signing
	if previous == id && len(inv.signers) == 1 && inv.signers[id] {
		return id, nil
	}
	if previous != id && !slices.Contains(inv.running.Keys.Verifying, id) {
		return "", fmt.Errorf("the running Auth app does not publish key %s yet; wait for its restart (`gregale auth keys list %s`)", id, k.app.Slug)
	}
	if err := k.client.SetSecretWithScope(ctx, k.app.Slug, authSignerPrefix+id, strconv.FormatInt(time.Now().Unix(), 10), k.scope); err != nil {
		return "", err
	}
	for other := range inv.signers {
		if other == id {
			continue
		}
		if err := k.client.UnsetSecretWithScope(ctx, k.app.Slug, authSignerPrefix+other, k.scope); err != nil {
			return "", fmt.Errorf("key %s is the newest signer, but removing the old marker for %s failed (run the command again): %w", id, other, err)
		}
	}
	restartAuthApp(ctx, k.client, k.app.Slug)
	if err := k.waitFor(ctx, func(s authPublicSettings) bool { return s.Keys.Signing == id }); err != nil {
		return "", err
	}
	return previous, nil
}

func (k authKeyCommand) reportActivated(id, previous string) int {
	revokeAfter := time.Now().Add(api.AuthAccessTokenTTLSeconds * time.Second).UTC()
	if jsonOutput {
		out := map[string]any{"id": id, "kid": authKeyKID(id), "state": "signing"}
		if previous != id {
			out["previous"] = previous
			out["revoke_after"] = revokeAfter.Format(time.RFC3339)
		}
		return jsonOut(writeJSON(out))
	}
	PrintOK(osStdout, "Key %s now signs tokens for %s", id, k.app.Slug)
	if previous != id {
		_, _ = fmt.Fprintf(osStdout, "Key %s still verifies the tokens it signed until they expire. After %s, revoke it:\n  gregale auth keys revoke %s %s\n", previous, revokeAfter.Format("15:04 UTC"), k.app.Slug, previous)
	}
	return 0
}

// revoke removes a key that no longer signs. Its unexpired tokens stop
// verifying, so revoking early is how a leaked key is cut off.
func (k authKeyCommand) revoke(ctx context.Context, id string) int {
	inv, err := k.inventory(ctx)
	if err != nil {
		return printErr("Could not revoke the signing key", err)
	}
	if _, ok := inv.created[id]; !ok {
		return printErr("Could not revoke the signing key", fmt.Errorf("%s has no signing key %s", k.app.Slug, id))
	}
	if inv.signers[id] || (inv.running != nil && inv.running.Keys.Signing == id) {
		return printErr("Key still signs", fmt.Errorf("key %s signs, or is being made the signer; activate another key first", id))
	}
	if inv.running == nil {
		return printErr("Could not revoke the signing key", fmt.Errorf("cannot confirm which key the running Auth app signs with: %w", inv.runningErr))
	}
	if err := k.client.UnsetSecretWithScope(ctx, k.app.Slug, authSigningKeyPrefix+id, k.scope); err != nil {
		return printErr("Could not revoke the signing key", err)
	}
	restartAuthApp(ctx, k.client, k.app.Slug)
	if jsonOutput {
		return jsonOut(writeJSON(authKeyRow{ID: id, KID: authKeyKID(id), State: "removing"}))
	}
	PrintOK(osStdout, "Revoked key %s for %s", id, k.app.Slug)
	_, _ = fmt.Fprintf(osStdout, "Tokens it signed stop verifying once %s restarts, and in Gregale's gateway, Data API and realtime within 5 minutes.\nSigned-in users keep their sessions: their next refresh returns tokens from the current key.\n", k.app.Slug)
	return 0
}

// rotate adds a standby key, waits until the running app publishes it, then
// makes it sign. The previous key keeps verifying until it is revoked.
func (k authKeyCommand) rotate(ctx context.Context) int {
	id, err := k.addStandby(ctx)
	if err != nil {
		return printErr("Could not rotate the signing key", err)
	}
	if !jsonOutput {
		_, _ = fmt.Fprintf(osStdout, "Added standby key %s; waiting for %s to publish it...\n", id, k.app.Slug)
	}
	if err := k.waitFor(ctx, func(s authPublicSettings) bool { return slices.Contains(s.Keys.Verifying, id) }); err != nil {
		return printErr("Could not rotate the signing key", fmt.Errorf("standby key %s was added but not yet published; finish with `gregale auth keys activate %s %s`: %w", id, k.app.Slug, id, err))
	}
	previous, err := k.makeSigner(ctx, id)
	if err != nil {
		return printErr("Could not rotate the signing key", fmt.Errorf("standby key %s is published but does not sign yet; finish with `gregale auth keys activate %s %s`: %w", id, k.app.Slug, id, err))
	}
	return k.reportActivated(id, previous)
}

func (k authKeyCommand) waitFor(ctx context.Context, done func(authPublicSettings) bool) error {
	ctx, cancel := context.WithTimeout(ctx, api.AuthKeyChangeWait)
	defer cancel()
	for {
		if settings, err := fetchAuthSettings(ctx, k.app); err == nil && done(settings) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the restarted Auth app did not report the change within %s", api.AuthKeyChangeWait)
		case <-time.After(authKeyChangePoll):
		}
	}
}

// authPublicSettings is a running Auth app's /settings/v1.
type authPublicSettings struct {
	App          string   `json:"app"`
	SiteURL      string   `json:"site_url"`
	RedirectURLs []string `json:"redirect_urls"`
	Signup       struct {
		Enabled     bool `json:"enabled"`
		Autoconfirm bool `json:"autoconfirm"`
	} `json:"signup"`
	Providers []string `json:"providers"`
	Password  struct {
		MinLength           int      `json:"min_length"`
		Required            []string `json:"required"`
		LeakedPasswordCheck bool     `json:"leaked_password_check"`
	} `json:"password"`
	MFA struct {
		TOTP          bool `json:"totp"`
		Phone         bool `json:"phone"`
		RecoveryCodes bool `json:"recovery_codes"`
	} `json:"mfa"`
	Email struct {
		Configured bool   `json:"configured"`
		Provider   string `json:"provider"`
		From       string `json:"from"`
	} `json:"email"`
	Events struct {
		Lifecycle bool `json:"lifecycle"`
	} `json:"events"`
	Passkeys struct {
		Enabled bool     `json:"enabled"`
		RPID    string   `json:"rp_id"`
		Origins []string `json:"origins"`
	} `json:"passkeys"`
	Branding struct {
		Name    string  `json:"name"`
		Color   *string `json:"color"`
		LogoURL *string `json:"logo_url"`
	} `json:"branding"`
	Captcha *struct {
		Provider string `json:"provider"`
		SiteKey  string `json:"site_key"`
	} `json:"captcha"`
	Activity struct {
		RetentionDays *int `json:"retention_days"`
	} `json:"activity"`
	Phone struct {
		Enabled   bool     `json:"enabled"`
		Provider  string   `json:"provider"`
		Countries []string `json:"countries"`
	} `json:"phone"`
	SSO struct {
		Enabled     bool   `json:"enabled"`
		EntityID    string `json:"entity_id"`
		ACSURL      string `json:"acs_url"`
		SelfService bool   `json:"self_service"`
	} `json:"sso"`
	OAuth struct {
		Enabled             bool   `json:"enabled"`
		DynamicRegistration bool   `json:"dynamic_registration"`
		Issuer              string `json:"issuer"`
		MetadataURL         string `json:"metadata_url"`
		DefaultAudience     string `json:"default_audience"`
	} `json:"oauth"`
	Anonymous struct {
		Enabled       bool `json:"enabled"`
		RetentionDays *int `json:"retention_days"`
	} `json:"anonymous"`
	IdentityLinking    bool `json:"identity_linking"`
	SelfDeletion       bool `json:"self_deletion"`
	OrganizationClaims bool `json:"organization_claims"`
	Web3               struct {
		Ethereum bool `json:"ethereum"`
		Solana   bool `json:"solana"`
	} `json:"web3"`
	Keys struct {
		Signing   string   `json:"signing"`
		Verifying []string `json:"verifying"`
	} `json:"keys"`
}

// fetchAuthSettings reads the running app's public settings without
// credentials or redirects; a parked app wakes to answer.
func fetchAuthSettings(ctx context.Context, app api.AppResponse) (authPublicSettings, error) {
	external, err := authExternalOrigin(app)
	if err != nil {
		return authPublicSettings{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, external+"/settings/v1", nil)
	if err != nil {
		return authPublicSettings{}, err
	}
	response, err := authAdminHTTPClient.Do(request)
	if err != nil {
		return authPublicSettings{}, fmt.Errorf("reach the Auth app: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return authPublicSettings{}, fmt.Errorf("read the Auth app settings: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return authPublicSettings{}, fmt.Errorf("the Auth app answered %d", response.StatusCode)
	}
	var settings authPublicSettings
	if err := json.Unmarshal(raw, &settings); err != nil || settings.Keys.Signing == "" {
		return authPublicSettings{}, errors.New("the Auth app returned malformed settings; redeploy it with this CLI's template")
	}
	return settings, nil
}
