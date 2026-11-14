package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/zalando/go-keyring"
)

// CLI configuration: the API base URL and the auth token.
//
// The token lives in three places, in priority order:
//
//  1. $FAAS_TOKEN (CI / scripts).
//  2. The OS keychain (macOS Keychain / Linux libsecret via D-Bus /
//     Windows wincred, UX §2.2). The canonical store — gated by the
//     OS user unlock, never backed up by default.
//  3. This plaintext file (~/.config/gregale/token at 0o600) — a
//     fallback for headless hosts with no D-Bus session (CI runners,
//     SSH-only servers, Docker images), a keyring the user declined to
//     unlock (WSL, where nothing unlocks GNOME Keyring at login), or an
//     explicit opt-out (`login --insecure-storage`, GREGALE_KEYRING=off).
//     A WARN line is printed when the keychain refuses the key so the
//     operator sees the degraded mode.
//
// Reads check the token file before the keychain: the file exists only
// when the keychain did not take the key (a successful keychain save
// removes it), so a user whose keyring is locked is not asked to unlock
// it on every command.
//
// saveToken migrates the legacy plaintext file to the keychain
// one-shot: on the first successful keychain Set, the old file is
// removed if present. Issue #293 closes gap G5 (impl spec §17).
//
// Pre-rename (PR #439, 3bc9796c) the keychain service was "faas-cli"
// and the file path was ~/.config/faas/token. saveToken / loadToken
// also look up the legacy names first and migrate forward on the
// first successful Set — otherwise every existing user gets logged
// out at the upgrade.

const defaultAPIBase = "https://api.gregale.dev"

// keyringService is the OS-portable service identifier written to the
// platform keychain. Lowercase + hyphens per platform conventions;
// stable across CLI versions so re-installs overwrite the same entry
// rather than orphaning the old one.
const (
	keyringService = "gregale-cli"
	keyringAccount = "default"

	// legacyKeyringService is the pre-#439 keychain service. We
	// consult it on loadToken (a one-shot fallback) and remove it
	// on saveToken (forward migration). The legacy KeychainAccount
	// is the same `default` — only the service identifier changed.
	legacyKeyringService = "faas-cli"
)

// keyringStub is the OS-keychain surface we need. The production
// implementation (productionKeyring) wraps github.com/zalando/go-keyring
// so macOS / Linux (libsecret via D-Bus) / Windows (wincred) are all
// covered without a build-tag switch on our side. Tests inject a
// map-backed fake via installKeyringStub so the read/write/migration
// paths can be exercised on any host.
type keyringStub interface {
	Get(service, account string) (string, error)
	Set(service, account, value string) error
	Delete(service, account string) error
}

// keyringBackend is the package-private test seam. nil in production
// (effectiveKeyring returns productionKeyring then). Mirrors the
// testOnlyTTY pattern in output.go. Named `keyringBackend` rather
// than `keyring` to avoid shadowing the imported
// github.com/zalando/go-keyring package.
var keyringBackend keyringStub

// installKeyringStub swaps the test seam and restores the previous
// value via t.Cleanup. Use one install per test so cleanup is
// deterministic; the t parameter is required so tests cannot
// silently leak a stub into a sibling test.
func installKeyringStub(t *testing.T, stub keyringStub) {
	t.Helper()
	prev := keyringBackend
	keyringBackend = stub
	t.Cleanup(func() { keyringBackend = prev })
}

// productionKeyring wraps github.com/zalando/go-keyring so the rest
// of the package depends on a small interface, not the library's
// concrete types. Keeps the test seam honest and makes a future
// alternative implementation (e.g. an in-process HSM) a one-file
// change.
type productionKeyring struct{}

func (productionKeyring) Get(service, account string) (string, error) {
	return keyring.Get(service, account)
}

func (productionKeyring) Set(service, account, value string) error {
	return keyring.Set(service, account, value)
}

func (productionKeyring) Delete(service, account string) error {
	return keyring.Delete(service, account)
}

// keyringModeEnv turns the OS keychain off when set to off/0/false/no:
// the API key then lives only in the token file. For hosts where the
// keyring is locked or cannot show its unlock dialog (WSL, containers,
// SSH sessions).
const keyringModeEnv = "GREGALE_KEYRING"

// loginInsecureStorage is set by `gregale login --insecure-storage` for
// the duration of that login.
var loginInsecureStorage bool

// keyringDisabled reports whether the user opted out of the OS keychain.
func keyringDisabled() bool {
	if loginInsecureStorage {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(keyringModeEnv))) {
	case "off", "0", "false", "no", "disabled":
		return true
	}
	return false
}

// errKeyringDisabled is what a save into a disabled keychain returns.
var errKeyringDisabled = errors.New("OS keychain disabled")

// disabledKeyring stands in for the keychain when the user opted out:
// reads and deletes find nothing (silently), saves are refused so
// saveToken writes the token file.
type disabledKeyring struct{}

func (disabledKeyring) Get(string, string) (string, error) { return "", keyring.ErrNotFound }
func (disabledKeyring) Set(string, string, string) error   { return errKeyringDisabled }
func (disabledKeyring) Delete(string, string) error        { return keyring.ErrNotFound }

// errKeyringTimeout is returned when the keychain does not answer in time.
var errKeyringTimeout = errors.New("OS keychain did not respond")

// keyringWait bounds one keychain call. go-keyring waits on an unlock
// prompt with no deadline, so a dialog that can never be shown (WSL
// without WSLg, SSH, containers) would hang the CLI. A person at a
// terminal gets time to type the keyring password; scripts do not wait
// long. A variable so tests can shrink it.
var keyringWait = func() time.Duration {
	if nonInteractive || !stdinIsTTY() {
		return 5 * time.Second
	}
	return 60 * time.Second
}

// timeoutKeyring bounds every call on inner by keyringWait. A call that
// times out keeps running in its goroutine; the CLI moves on to the
// token file.
type timeoutKeyring struct{ inner keyringStub }

func (k timeoutKeyring) Get(service, account string) (string, error) {
	return withKeyringTimeout(func() (string, error) { return k.inner.Get(service, account) })
}

func (k timeoutKeyring) Set(service, account, value string) error {
	_, err := withKeyringTimeout(func() (string, error) { return "", k.inner.Set(service, account, value) })
	return err
}

func (k timeoutKeyring) Delete(service, account string) error {
	_, err := withKeyringTimeout(func() (string, error) { return "", k.inner.Delete(service, account) })
	return err
}

func withKeyringTimeout(call func() (string, error)) (string, error) {
	type result struct {
		value string
		err   error
	}
	wait := keyringWait()
	done := make(chan result, 1)
	go func() {
		v, err := call()
		done <- result{v, err}
	}()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case r := <-done:
		return r.value, r.err
	case <-timer.C:
		return "", fmt.Errorf("%w within %s", errKeyringTimeout, wait)
	}
}

// effectiveKeyring returns the keychain every token path uses: a no-op
// when the user opted out, else the test seam if installed, else the
// production adapter — both bounded by keyringWait. Never nil.
func effectiveKeyring() keyringStub {
	if keyringDisabled() {
		return disabledKeyring{}
	}
	if keyringBackend != nil {
		return timeoutKeyring{inner: keyringBackend}
	}
	return timeoutKeyring{inner: productionKeyring{}}
}

// describeKeyringErr turns a keychain failure into words a user can act
// on. go-keyring reports a cancelled or failed unlock dialog as
// "failed to unlock correct collection '/org/freedesktop/...'".
func describeKeyringErr(err error) string {
	switch {
	case errors.Is(err, errKeyringTimeout):
		return err.Error()
	case strings.Contains(err.Error(), "unlock"):
		return "the keyring is locked (the unlock prompt was cancelled or failed)"
	default:
		return err.Error()
	}
}

// keyringOptOutHint tells a user how to stop the CLI asking the keychain.
const keyringOptOutHint = "To keep the API key in a file instead, run 'gregale login --insecure-storage' or set GREGALE_KEYRING=off."

// apiBase returns the effective API base URL. Explicit $FAAS_API remains the
// highest-priority override for local development and CI; the persistent
// non-secret CLI config is consulted only when the environment is unset.
func apiBase() string {
	if raw := strings.TrimSpace(os.Getenv("FAAS_API")); raw != "" {
		return normalizeAPIBase(raw)
	}
	if cfg, err := loadCLIConfig(); err == nil {
		if name := selectedProfile(cfg); name != "default" {
			return cfg.Profiles[name].APIBase
		}
		if cfg.APIBase != "" {
			return normalizeAPIBase(cfg.APIBase)
		}
	}
	return defaultAPIBase
}

// tokenPath is where the CLI persists the auth token (legacy file
// fallback only — see package comment).
func tokenPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	if name := currentProfile(); name != "default" {
		return filepath.Join(dir, "gregale", "profiles", name, "token"), nil
	}
	return filepath.Join(dir, "gregale", "token"), nil
}

// legacyTokenPath returns the pre-#439 plaintext-file fallback path:
// the same config dir as tokenPath, but the "faas" subdirectory
// instead of "gregale". Used by loadToken and saveToken for the
// one-shot migration of pre-rename installs.
func legacyTokenPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "faas", "token"), nil
}

// loadToken returns the token from $FAAS_TOKEN, the OS keychain, or
// the plaintext-file fallback — in that priority order. A non-empty
// env var always wins (CI) and is intentionally not format-validated:
// callers may use an explicitly supplied short-lived bearer there.
// Persisted stores are different: only the current long-lived API-key
// format is accepted. An invalid keychain entry therefore cannot
// shadow a valid file fallback (the reported stale-Keychain failure).
// When the keychain returns ErrNotFound we silently fall through to
// the file (a fresh install with no entry yet is indistinguishable
// from "no user logged in"). Any other keychain error, or an invalid
// persisted value, triggers a WARN and the next fallback is tried.
//
// Pre-#439 legacy lookup is silently chained AFTER the new keychain
// + new file: a pre-#439 install never wrote to the new keychain
// service, so the new lookup is always ErrNotFound and the legacy
// probe runs cheaply. The new saveToken migrates the legacy entry
// forward on the first successful Set — see saveToken below.
func loadToken() string {
	var diagnostics io.Writer = os.Stderr
	if jsonOutput {
		diagnostics = io.Discard
	}
	return loadTokenWithDiagnostics(diagnostics)
}

func loadTokenWithDiagnostics(diagnostics io.Writer) string {
	if v := os.Getenv("FAAS_TOKEN"); v != "" {
		return strings.TrimSpace(v)
	}
	return loadStoredToken(diagnostics)
}

// envToken returns the FAAS_TOKEN override, trimmed like loadToken does.
func envToken() string { return strings.TrimSpace(os.Getenv("FAAS_TOKEN")) }

// loadStoredToken is loadToken without the FAAS_TOKEN override: the
// credential saved for the selected profile. Logout and re-login revoke the
// key behind this credential, never the environment's.
func loadStoredToken(diagnostics io.Writer) string {
	// The token file first: it exists only when the keychain did not
	// take the key, and reading it never raises a keyring unlock dialog.
	invalidFile := false
	if p, err := tokenPath(); err == nil {
		if b, err := os.ReadFile(p); err == nil {
			v := strings.TrimSpace(string(b))
			if validPersistedToken(v) {
				return v
			}
			invalidFile = true
		}
	}
	keychainReachable := true
	v, err := effectiveKeyring().Get(keyringService, profileKeyringAccount())
	switch {
	case err == nil:
		v = strings.TrimSpace(v)
		if validPersistedToken(v) {
			return v
		}
		PrintWarn(diagnostics, "OS keychain contains an invalid Gregale API key; ignoring it.")
	case errors.Is(err, keyring.ErrNotFound):
		// not saved in the keychain — not an error worth a WARN
	default:
		keychainReachable = false
		PrintWarn(diagnostics, "Could not read the API key from the OS keychain: %s. %s", describeKeyringErr(err), keyringOptOutHint)
	}
	if invalidFile {
		PrintWarn(diagnostics, "Token file contains an invalid Gregale API key; trying legacy stores.")
	}
	// A pre-#439 install has neither the new keychain entry nor the new
	// file, so the legacy stores are the load path for everyone who
	// hasn't re-logged-in after the rename.
	return loadLegacyToken(diagnostics, keychainReachable)
}

// loadLegacyToken probes the pre-#439 keychain service and the
// pre-#439 plaintext file in turn. Silent on ErrNotFound (the
// expected case for a fresh install); other keychain errors get
// a WARN. askKeychain is false when the keychain just refused a read
// (locked, unlock cancelled, timed out): asking again would show the
// user a second unlock dialog for the same command.
func loadLegacyToken(diagnostics io.Writer, askKeychain bool) string {
	if currentProfile() != "default" {
		return ""
	}
	if askKeychain {
		kr := effectiveKeyring()
		v, err := kr.Get(legacyKeyringService, keyringAccount)
		switch {
		case err == nil:
			v = strings.TrimSpace(v)
			if validPersistedToken(v) {
				return v
			}
			PrintWarn(diagnostics, "Legacy OS keychain contains an invalid Gregale API key; trying the legacy token file.")
		case errors.Is(err, keyring.ErrNotFound):
			// fall through to legacy file
		default:
			PrintWarn(diagnostics, "Legacy keychain lookup failed (%v).", err)
		}
	}
	if p, err := legacyTokenPath(); err == nil {
		if b, err := os.ReadFile(p); err == nil {
			v := strings.TrimSpace(string(b))
			if validPersistedToken(v) {
				return v
			}
			PrintWarn(diagnostics, "Legacy token file contains an invalid Gregale API key; ignoring it.")
		}
	}
	return ""
}

// warnEnvTokenOverride prints one stderr line when FAAS_TOKEN replaces a
// credential the user selected: an explicit --profile, or a login saved for
// the selected profile. JSON mode prints nothing: there stderr carries only
// Problem documents, which scripts parse.
func warnEnvTokenOverride() {
	env := envToken()
	switch {
	case env == "" || jsonOutput:
	case profileOverride != "":
		PrintWarn(osStderr, "FAAS_TOKEN is set and overrides --profile %s's saved login; this command sends FAAS_TOKEN to %s. Unset FAAS_TOKEN to use the profile's login.", profileOverride, displayAPIBase(apiBase()))
	case savedLoginShadowedBy(env):
		PrintWarn(osStderr, "FAAS_TOKEN is set and overrides your saved login; unset FAAS_TOKEN to use it.")
	}
}

// savedLoginShadowedBy reports whether a credential saved for the selected
// profile differs from env. The OS keychain is read only from an interactive
// terminal: automation that exports FAAS_TOKEN never touched it before, and a
// keychain probe on a headless host can start a D-Bus session.
func savedLoginShadowedBy(env string) bool {
	if meta, err := loadManagedSession(); err == nil && meta.Managed && meta.KeyID != "" && meta.TokenFingerprint != "" && !meta.matches(env) {
		return true
	}
	if p, err := tokenPath(); err == nil {
		if b, err := os.ReadFile(p); err == nil {
			if v := strings.TrimSpace(string(b)); validPersistedToken(v) && v != env {
				return true
			}
		}
	}
	if !stdinIsTTY() {
		return false
	}
	v, err := effectiveKeyring().Get(keyringService, profileKeyringAccount())
	v = strings.TrimSpace(v)
	return err == nil && validPersistedToken(v) && v != env
}

// validPersistedToken keeps the on-disk/keychain stores aligned with the
// server's long-lived API-key contract. FAAS_TOKEN is handled separately in
// loadToken because explicit environment overrides are also used for
// short-lived bearer credentials in automation.
func validPersistedToken(token string) bool {
	return api.ValidAPIKeyFormat(token)
}

// saveToken persists a current API key to the OS keychain, falling
// back to the plaintext file at 0o600 if the keychain is unavailable.
// Invalid values are rejected before either store is touched, so a
// malformed auth response can never become the next stale credential.
// On the first successful keychain Set the legacy plaintext file (if
// any) is removed — a one-shot migration so customers do not keep a
// redundant plaintext copy on disk after the upgrade. Issue #293.
//
// Additionally, when the new keychain Set succeeds the legacy
// keychain service entry (pre-#439 "faas-cli") and the legacy
// plaintext file (pre-#439 ~/.config/faas/token) are removed — see
// PR #439. The new store is the source of truth; the legacy store
// is erased, not republished.
func saveToken(token string) error {
	_, err := saveTokenStore(token)
	return err
}

// savedTokenStore says where saveTokenStore put the API key: the OS
// keychain (Path empty) or the token file at Path.
type savedTokenStore struct {
	Path string
}

// describe is the "Credentials:" line printed after a login.
func (s savedTokenStore) describe() string {
	if s.Path == "" {
		return "OS keychain"
	}
	return s.Path + " (plaintext, readable only by you)"
}

// saveTokenStore is saveToken, also reporting which store took the key.
func saveTokenStore(token string) (savedTokenStore, error) {
	token = strings.TrimSpace(token)
	if !validPersistedToken(token) {
		return savedTokenStore{}, errors.New("gregale: refusing to persist an invalid API key")
	}

	kr := effectiveKeyring()
	if err := kr.Set(keyringService, profileKeyringAccount(), token); err == nil {
		// One-shot migration: if the legacy plaintext file exists
		// from a pre-#293 install, remove it now that the secret
		// lives in the keychain. ErrNotExist is silent; any other
		// remove failure is a WARN (the save still succeeded — the
		// keychain is the source of truth).
		if p, perr := tokenPath(); perr == nil {
			switch rerr := os.Remove(p); {
			case rerr == nil:
				PrintProgress(os.Stdout, "Migrated token from plaintext file to OS keychain.")
			case errors.Is(rerr, os.ErrNotExist):
				// nothing to migrate
			default:
				PrintWarn(os.Stderr, "Could not remove legacy plaintext token file: %v", rerr)
			}
		}
		// One-shot migration: pre-#439 legacy keychain service
		// entry. Delete is best-effort — ErrNotFound is silent,
		// any other failure is a WARN (the new save already
		// succeeded). Print a separate progress line so a customer
		// who only had the plaintext file (no keychain entry) sees
		// an accurate "what just happened" rather than a misleading
		// combined message.
		if err := deleteLegacyKeyring(kr); err == nil {
			PrintProgress(os.Stdout, "Removed legacy keychain entry (\"faas-cli\").")
		} else if !errors.Is(err, keyring.ErrNotFound) {
			PrintWarn(os.Stderr, "Could not remove legacy keychain entry: %v", err)
		}
		// One-shot migration: pre-#439 legacy plaintext file.
		if lp, lperr := legacyTokenPath(); lperr == nil && currentProfile() == "default" {
			switch lerr := os.Remove(lp); {
			case lerr == nil:
				PrintProgress(os.Stdout, "Removed legacy plaintext token file.")
			case errors.Is(lerr, os.ErrNotExist):
				// nothing to migrate
			default:
				PrintWarn(os.Stderr, "Could not remove legacy plaintext token file: %v", lerr)
			}
		}
		clearCompletionCaches()
		return savedTokenStore{}, nil
	} else if !errors.Is(err, errKeyringDisabled) {
		// Keychain unreachable (no D-Bus on headless Linux, a locked
		// keyring whose unlock prompt was cancelled, a timeout). Say
		// so, and how to make the choice stick; the file fallback keeps
		// the CLI working either way. An explicit opt-out is not a
		// degradation and gets no warning.
		PrintWarn(os.Stderr, "Could not save the API key to the OS keychain: %s. Saved it to a plaintext file readable only by you instead. To use the keychain, unlock it and run 'gregale login' again. %s", describeKeyringErr(err), keyringOptOutHint)
	}

	p, err := tokenPath()
	if err != nil {
		return savedTokenStore{}, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return savedTokenStore{}, err
	}
	if err := os.WriteFile(p, []byte(token+"\n"), 0o600); err != nil {
		return savedTokenStore{}, err
	}
	clearCompletionCaches()
	return savedTokenStore{Path: p}, nil
}

// deleteToken clears the token from both stores (keychain + legacy
// file). Best-effort: a stuck keychain must not block logout, and a
// missing file is not an error. Used by `gregale logout`.
//
// Also clears the pre-#439 legacy keychain service and the
// pre-#439 legacy plaintext file so a logout is symmetric: it
// removes both the new and the legacy copies regardless of which
// store the user upgraded from.
func deleteToken() {
	if err := effectiveKeyring().Delete(keyringService, profileKeyringAccount()); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		PrintWarn(os.Stderr, "Could not remove token from OS keychain: %v", err)
	}
	if currentProfile() == "default" {
		_ = effectiveKeyring().Delete(legacyKeyringService, keyringAccount)
	}
	if p, err := tokenPath(); err == nil {
		_ = os.Remove(p)
	}
	if lp, err := legacyTokenPath(); err == nil && currentProfile() == "default" {
		_ = os.Remove(lp)
	}
	clearCompletionCaches()
}
