package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
//     SSH-only servers, Docker images). A WARN line is printed when
//     the keychain is unavailable so the operator sees the degraded
//     mode.
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

// effectiveKeyring returns the test seam if installed, otherwise the
// production adapter. Centralises the nil-check so every call site
// stays a one-liner.
func effectiveKeyring() keyringStub {
	if keyringBackend != nil {
		return keyringBackend
	}
	return productionKeyring{}
}

// apiBase returns the API base URL, overridable via $FAAS_API for local/dev.
func apiBase() string {
	return normalizeAPIBase(os.Getenv("FAAS_API"))
}

// tokenPath is where the CLI persists the auth token (legacy file
// fallback only — see package comment).
func tokenPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
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
// env var always wins (CI). When the keychain returns ErrNotFound
// we silently fall through to the file (a fresh install with no
// entry yet is indistinguishable from "no user logged in"). Any
// other keychain error triggers a one-shot WARN and the same file
// fallback.
//
// Pre-#439 legacy lookup is silently chained AFTER the new keychain
// + new file: a pre-#439 install never wrote to the new keychain
// service, so the new lookup is always ErrNotFound and the legacy
// probe runs cheaply. The new saveToken migrates the legacy entry
// forward on the first successful Set — see saveToken below.
func loadToken() string {
	if v := os.Getenv("FAAS_TOKEN"); v != "" {
		return strings.TrimSpace(v)
	}
	if kr := effectiveKeyring(); kr != nil {
		v, err := kr.Get(keyringService, keyringAccount)
		switch {
		case err == nil:
			return strings.TrimSpace(v)
		case errors.Is(err, keyring.ErrNotFound):
			// fall through to file — not an error worth a WARN
		default:
			PrintWarn(os.Stderr, "OS keychain lookup failed (%v); falling back to plaintext file.", err)
		}
	}
	p, err := tokenPath()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		// No new file — fall through to the legacy keychain / file
		// paths before giving up. A pre-#439 install has neither the
		// new keychain service nor the new file, so this loop is the
		// load path for everyone who hasn't re-logged-in after the
		// rename.
		if v := loadLegacyToken(); v != "" {
			return v
		}
		return ""
	}
	return strings.TrimSpace(string(b))
}

// loadLegacyToken probes the pre-#439 keychain service and the
// pre-#439 plaintext file in turn. Silent on ErrNotFound (the
// expected case for a fresh install); other keychain errors get
// a WARN.
func loadLegacyToken() string {
	if kr := effectiveKeyring(); kr != nil {
		v, err := kr.Get(legacyKeyringService, keyringAccount)
		switch {
		case err == nil:
			return strings.TrimSpace(v)
		case errors.Is(err, keyring.ErrNotFound):
			// fall through to legacy file
		default:
			PrintWarn(os.Stderr, "Legacy keychain lookup failed (%v).", err)
		}
	}
	if p, err := legacyTokenPath(); err == nil {
		if b, err := os.ReadFile(p); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return ""
}

// saveToken persists the token to the OS keychain, falling back to
// the plaintext file at 0o600 if the keychain is unavailable. On
// the first successful keychain Set the legacy plaintext file (if
// any) is removed — a one-shot migration so customers do not keep a
// redundant plaintext copy on disk after the upgrade. Issue #293.
//
// Additionally, when the new keychain Set succeeds the legacy
// keychain service entry (pre-#439 "faas-cli") and the legacy
// plaintext file (pre-#439 ~/.config/faas/token) are removed — see
// PR #439. The new store is the source of truth; the legacy store
// is erased, not republished.
func saveToken(token string) error {
	token = strings.TrimSpace(token)

	kr := effectiveKeyring()
	if err := kr.Set(keyringService, keyringAccount, token); err == nil {
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
		if err := kr.Delete(legacyKeyringService, keyringAccount); err == nil {
			PrintProgress(os.Stdout, "Removed legacy keychain entry (\"faas-cli\").")
		} else if !errors.Is(err, keyring.ErrNotFound) {
			PrintWarn(os.Stderr, "Could not remove legacy keychain entry: %v", err)
		}
		// One-shot migration: pre-#439 legacy plaintext file.
		if lp, lperr := legacyTokenPath(); lperr == nil {
			switch lerr := os.Remove(lp); {
			case lerr == nil:
				PrintProgress(os.Stdout, "Removed legacy plaintext token file.")
			case errors.Is(lerr, os.ErrNotExist):
				// nothing to migrate
			default:
				PrintWarn(os.Stderr, "Could not remove legacy plaintext token file: %v", lerr)
			}
		}
		return nil
	} else {
		// Keychain unreachable (no D-Bus on headless Linux, locked
		// Keychain, etc.). Surface the degradation loudly so an
		// operator on a shared host sees the warning; the file
		// fallback keeps the CLI functional.
		PrintWarn(os.Stderr, "OS keychain unavailable; falling back to plaintext token file. Install gnome-keyring (Linux) for safer storage.")
	}

	p, err := tokenPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(token+"\n"), 0o600)
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
	if err := effectiveKeyring().Delete(keyringService, keyringAccount); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		PrintWarn(os.Stderr, "Could not remove token from OS keychain: %v", err)
	}
	_ = effectiveKeyring().Delete(legacyKeyringService, keyringAccount)
	if p, err := tokenPath(); err == nil {
		_ = os.Remove(p)
	}
	if lp, err := legacyTokenPath(); err == nil {
		_ = os.Remove(lp)
	}
}
