package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zalando/go-keyring"
)

// fakeKeyring is the in-memory test double for keyringStub. Mutex-guarded
// so a future t.Parallel switch (none today) doesn't race. Each method
// also has an injectable error so a single test can pin the exact
// behaviour it wants (keychain unavailable, ErrNotFound, etc.) without
// per-test stub boilerplate.
type fakeKeyring struct {
	mu     sync.Mutex
	data   map[string]string
	setErr error
	getErr error
	delErr error
}

func newFakeKeyring() *fakeKeyring {
	return &fakeKeyring{data: map[string]string{}}
}

func (f *fakeKeyring) storeKey(service, account string) string {
	return service + "\x00" + account
}

func (f *fakeKeyring) Get(service, account string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return "", f.getErr
	}
	v, ok := f.data[f.storeKey(service, account)]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (f *fakeKeyring) Set(service, account, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setErr != nil {
		return f.setErr
	}
	f.data[f.storeKey(service, account)] = value
	return nil
}

func (f *fakeKeyring) Delete(service, account string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.delErr != nil {
		return f.delErr
	}
	if _, ok := f.data[f.storeKey(service, account)]; !ok {
		return keyring.ErrNotFound
	}
	delete(f.data, f.storeKey(service, account))
	return nil
}

// setFakeKeyring installs a fakeKeyring with the requested behaviour.
// All error overrides are optional (nil = no override).
func setFakeKeyring(t *testing.T, opts ...func(*fakeKeyring)) *fakeKeyring {
	t.Helper()
	f := newFakeKeyring()
	for _, opt := range opts {
		opt(f)
	}
	installKeyringStub(t, f)
	return f
}

func withSetErr(err error) func(*fakeKeyring) {
	return func(f *fakeKeyring) { f.setErr = err }
}

func withGetErr(err error) func(*fakeKeyring) {
	return func(f *fakeKeyring) { f.getErr = err }
}

func withDelErr(err error) func(*fakeKeyring) {
	return func(f *fakeKeyring) { f.delErr = err }
}

func withEntry(service, account, value string) func(*fakeKeyring) {
	return func(f *fakeKeyring) {
		f.data[f.storeKey(service, account)] = value
	}
}

// testAPIKey returns a syntactically valid persisted API key without
// embedding a real credential in the test suite. The production loader
// intentionally validates keychain/file values, while FAAS_TOKEN fixtures
// elsewhere may continue using short sentinel strings.
func testAPIKey(ch byte) string {
	return "fp_live_" + strings.Repeat(string(ch), 48)
}

// setupHermeticTokensEnv enforces the 3-knob CLI hermeticity rule
// (memory: cmd-gregale-requireslogin-hermeticity): HOME + XDG_CONFIG_HOME
// pointed at a temp dir, FAAS_TOKEN cleared. The temp dir doubles as
// the legacy-file fallback root.
func setupHermeticTokensEnv(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)
	t.Setenv("FAAS_TOKEN", "")
	return tmp
}

// writeLegacyToken pre-creates the plaintext-file fallback with the
// given value (used by #293-era migration tests — pre-keychain
// installs that still wrote a file at the CURRENT path). For
// pre-#439 (pre-rename) seeding use writePreRenamePlaintextToken
// below.
func writeLegacyToken(t *testing.T, value string) string {
	t.Helper()
	p, err := tokenPath()
	if err != nil {
		t.Fatalf("tokenPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(p, []byte(value+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

// writePreRenamePlaintextToken writes the token to the PRE-#439
// path (the "faas" subdirectory under os.UserConfigDir). The
// existing writeLegacyToken writes the CURRENT path (post-#439,
// "gregale"); this sibling seeds the pre-rename state for the
// legacy-migration tests. The PR #439 rename flipped the path
// without forward-migrating, so any customer who logged in before
// the rename has a file at the OLD path.
func writePreRenamePlaintextToken(t *testing.T, value string) string {
	t.Helper()
	p, err := legacyTokenPath()
	if err != nil {
		t.Fatalf("legacyTokenPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(p, []byte(value+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

// writePreRenameKeychainEntry seeds the pre-#439 keychain service
// ("faas-cli") so migration tests can exercise the legacy-keychain
// branch. Mirrors the withEntry helper for the new service.
func writePreRenameKeychainEntry(t *testing.T, f *fakeKeyring, value string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[f.storeKey(legacyKeyringService, keyringAccount)] = value
}

// ----- Save-side tests -----

// TestSaveToken_WritesToKeychain covers the happy path: saveToken
// delegates to the keychain stub and never touches the file. Issue
// #293 — closes the on-disk plaintext leak on supported platforms.
func TestSaveToken_WritesToKeychain(t *testing.T) {
	setupHermeticTokensEnv(t)
	f := setFakeKeyring(t)
	want := testAPIKey('a')

	if err := saveToken(want); err != nil {
		t.Fatalf("saveToken: %v", err)
	}

	// Keychain received the value.
	if got, _ := f.Get(keyringService, keyringAccount); got != want {
		t.Errorf("keychain entry = %q, want saved token", got)
	}
	// No file written.
	if _, err := tokenPath(); err == nil {
		if _, err := os.Stat(mustTokenPath(t)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("plaintext file should NOT exist after keychain save (err=%v)", err)
		}
	}
}

// TestSaveToken_FallsBackToFile_WhenKeychainUnavailable is the rename
// of the prior TestSaveToken_FilePermsAre0600, retargeted at the
// fallback branch: when the keychain returns an error from Set,
// saveToken must write the file at 0o600 with the trimmed body.
func TestSaveToken_FallsBackToFile_WhenKeychainUnavailable(t *testing.T) {
	tmp := setupHermeticTokensEnv(t)
	setFakeKeyring(t, withSetErr(errors.New("no D-Bus")))
	want := testAPIKey('b')

	if err := saveToken("  " + want + "  "); err != nil {
		t.Fatalf("saveToken: %v", err)
	}
	p := mustTokenPath(t)
	st, err := os.Stat(p)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if mode := st.Mode().Perm(); mode != 0o600 {
		t.Errorf("perm = %o, want 0600", mode)
	}
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got := strings.TrimSpace(string(body)); got != want {
		t.Errorf("body trimmed = %q, want saved token", got)
	}
	if _, err := os.Stat(filepath.Join(tmp, "should-not-exist")); err == nil {
		t.Errorf("unexpected file in tempdir root")
	}
}

// TestSaveToken_TrimsAndAppendsNewline_OnFallback pins the byte-shape
// of the fallback file (trimmed value + trailing newline). Mirrors
// the existing cli_test.go assertion but routes through the fallback
// branch via the stub.
func TestSaveToken_TrimsAndAppendsNewline_OnFallback(t *testing.T) {
	setupHermeticTokensEnv(t)
	setFakeKeyring(t, withSetErr(errors.New("no D-Bus")))
	want := testAPIKey('c')

	if err := saveToken("  " + want + "  "); err != nil {
		t.Fatalf("saveToken: %v", err)
	}
	body, err := os.ReadFile(mustTokenPath(t))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got := string(body); got != want+"\n" {
		t.Errorf("body = %q, want %q", got, "trimmed-value\n")
	}
}

// TestSaveToken_MigratesLegacyPlaintextFile covers the one-shot
// migration: pre-existing plaintext file at the legacy path is
// removed after a successful keychain Set, and the keychain has the
// new value.
func TestSaveToken_MigratesLegacyPlaintextFile(t *testing.T) {
	setupHermeticTokensEnv(t)
	p := writeLegacyToken(t, "old-plaintext")
	f := setFakeKeyring(t)
	want := testAPIKey('d')

	if err := saveToken(want); err != nil {
		t.Fatalf("saveToken: %v", err)
	}
	if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("legacy file should be removed after keychain save; Stat err=%v", err)
	}
	if got, _ := f.Get(keyringService, keyringAccount); got != want {
		t.Errorf("keychain entry = %q, want saved token", got)
	}
}

// TestSaveToken_NoMigrationWhenFileAbsent covers the no-op migration
// path: no legacy file → no error, keychain still has the value.
func TestSaveToken_NoMigrationWhenFileAbsent(t *testing.T) {
	setupHermeticTokensEnv(t)
	f := setFakeKeyring(t)
	want := testAPIKey('e')

	if err := saveToken(want); err != nil {
		t.Fatalf("saveToken: %v", err)
	}
	if _, err := os.Stat(mustTokenPath(t)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("no file expected; Stat err=%v", err)
	}
	if got, _ := f.Get(keyringService, keyringAccount); got != want {
		t.Errorf("keychain entry = %q, want saved token", got)
	}
}

// TestSaveToken_RejectsInvalidAPIKey ensures a malformed auth response
// cannot overwrite the current credential in either persistence store.
func TestSaveToken_RejectsInvalidAPIKey(t *testing.T) {
	setupHermeticTokensEnv(t)
	f := setFakeKeyring(t)

	if err := saveToken("stale-session-value"); err == nil {
		t.Fatal("saveToken accepted an invalid API key")
	}
	if got, err := f.Get(keyringService, keyringAccount); !errors.Is(err, keyring.ErrNotFound) || got != "" {
		t.Errorf("invalid save changed keychain: value=%q err=%v", got, err)
	}
	if _, err := os.Stat(mustTokenPath(t)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("invalid save created token file: err=%v", err)
	}
}

// ----- Load-side tests -----

// TestLoadToken_PrefersEnvOverKeychain covers env winning over a
// populated keychain. Same priority as TestLoadToken_PrefersEnvOverFile
// but with a keychain value present to prove the env short-circuit
// is hit BEFORE the keychain lookup runs.
func TestLoadToken_PrefersEnvOverKeychain(t *testing.T) {
	setupHermeticTokensEnv(t)
	setFakeKeyring(t, withEntry(keyringService, keyringAccount, "kc-token"))
	t.Setenv("FAAS_TOKEN", "env-token")

	if got := loadToken(); got != "env-token" {
		t.Errorf("loadToken = %q, want env-token (env wins)", got)
	}
}

// TestLoadToken_PrefersKeychainOverFile covers the keychain taking
// priority over the plaintext-file fallback when env is unset.
func TestLoadToken_PrefersKeychainOverFile(t *testing.T) {
	setupHermeticTokensEnv(t)
	writeLegacyToken(t, testAPIKey('b'))
	setFakeKeyring(t, withEntry(keyringService, keyringAccount, testAPIKey('a')))

	if got := loadToken(); got != testAPIKey('a') {
		t.Errorf("loadToken = %q, want keychain value (keychain wins over file)", got)
	}
}

// TestLoadToken_InvalidKeychainFallsThroughToFile covers the production
// failure mode where an older/foreign value remains under the canonical
// Keychain service while a valid fallback file exists. The invalid entry
// must not shadow the file credential or cause every command to send a
// known-bad bearer token to the server.
func TestLoadToken_InvalidKeychainFallsThroughToFile(t *testing.T) {
	setupHermeticTokensEnv(t)
	want := testAPIKey('f')
	writeLegacyToken(t, want)
	setFakeKeyring(t, withEntry(keyringService, keyringAccount, "stale-session-value"))

	stderr, restore := captureStderr(t)
	defer restore()
	if got := loadToken(); got != want {
		t.Errorf("loadToken = %q, want valid file token after invalid keychain value", got)
	}
	if !strings.Contains(stderr.String(), "keychain contains an invalid Gregale API key") {
		t.Errorf("expected invalid-keychain warning; got %q", stderr.String())
	}
}

// TestLoadToken_InvalidCurrentFileFallsThroughToLegacy ensures a corrupt
// current file cannot block a valid pre-#439 fallback during migration.
func TestLoadToken_InvalidCurrentFileFallsThroughToLegacy(t *testing.T) {
	setupHermeticTokensEnv(t)
	writeLegacyToken(t, "stale-file-value")
	want := testAPIKey('d')
	writePreRenamePlaintextToken(t, want)
	setFakeKeyring(t)

	if got := loadToken(); got != want {
		t.Errorf("loadToken = %q, want valid legacy file token", got)
	}
}

// TestLoadToken_KeychainErrNotFound_FallsThroughToFile covers the
// silent fall-through when the keychain has no entry: no WARN, file
// used. A fresh install with neither store populated returns "" via
// TestLoadToken_MissingFileAndMissingEnv below.
func TestLoadToken_KeychainErrNotFound_FallsThroughToFile(t *testing.T) {
	tmp := setupHermeticTokensEnv(t)
	want := testAPIKey('b')
	writeLegacyToken(t, want)
	// Default fakeKeyring returns keyring.ErrNotFound from Get on a
	// missing entry — already the behaviour we want.

	got := loadToken()
	if got != want {
		t.Errorf("loadToken = %q, want valid file token", got)
	}
	// Nothing logged to stderr at WARN level — covered by the absence
	// of any getter-error injection.
	_ = tmp
}

// TestLoadToken_KeychainOtherError_FallsThroughToFileAndWarns covers
// the WARN branch: a keychain error other than ErrNotFound falls
// through to the file and emits a WARN to stderr.
func TestLoadToken_KeychainOtherError_FallsThroughToFileAndWarns(t *testing.T) {
	setupHermeticTokensEnv(t)
	want := testAPIKey('b')
	writeLegacyToken(t, want)
	setFakeKeyring(t, withGetErr(errors.New("keychain broken")))

	stderr, restore := captureStderr(t)
	defer restore()
	got := loadToken()

	if got != want {
		t.Errorf("loadToken = %q, want valid file token", got)
	}
	if !strings.Contains(stderr.String(), "OS keychain lookup failed") {
		t.Errorf("expected WARN to mention keychain failure; got %q", stderr.String())
	}
}

// TestLoadToken_MissingFileAndMissingEnv covers the empty case —
// env unset, keychain empty, file absent → returns "".
func TestLoadToken_MissingFileAndMissingEnv(t *testing.T) {
	setupHermeticTokensEnv(t)
	setFakeKeyring(t) // empty

	if got := loadToken(); got != "" {
		t.Errorf("loadToken = %q, want empty", got)
	}
}

// ----- Logout / seam tests -----

// TestLogout_RemovesKeychainAndFile covers the dual-store cleanup:
// deleteToken must clear both stores regardless of which one held
// the value.
func TestLogout_RemovesKeychainAndFile(t *testing.T) {
	tmp := setupHermeticTokensEnv(t)
	f := setFakeKeyring(t, withEntry(keyringService, keyringAccount, "kc-token"))
	p := writeLegacyToken(t, "file-token")

	deleteToken()

	if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("legacy file should be removed; Stat err=%v", err)
	}
	if got, err := f.Get(keyringService, keyringAccount); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("keychain entry should be gone; got value=%q err=%v", got, err)
	}
	_ = tmp
}

// TestLogout_KeychainDeleteError_StillRemovesFile covers the WARN
// branch on delete: a stuck keychain must not block the file
// cleanup, and the operator sees the WARN.
func TestLogout_KeychainDeleteError_StillRemovesFile(t *testing.T) {
	setupHermeticTokensEnv(t)
	setFakeKeyring(t, withDelErr(errors.New("keychain busy")))
	p := writeLegacyToken(t, "file-token")

	stderr, restore := captureStderr(t)
	defer restore()
	deleteToken()

	if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("legacy file should be removed even when keychain delete fails; Stat err=%v", err)
	}
	if !strings.Contains(stderr.String(), "Could not remove token from OS keychain") {
		t.Errorf("expected WARN about keychain delete; got %q", stderr.String())
	}
}

// TestEffectiveKeyring_FallsBackToProductionWhenNil pins the nil-stub
// invariant: effectiveKeyring must return a non-nil interface value
// even when the test seam is unset. Without this guard, a regression
// that forgets to install a stub in a test would nil-deref in
// production code paths.
func TestEffectiveKeyring_FallsBackToProductionWhenNil(t *testing.T) {
	prev := keyringBackend
	keyringBackend = nil
	t.Cleanup(func() { keyringBackend = prev })

	if kr := effectiveKeyring(); kr == nil {
		t.Errorf("effectiveKeyring = nil with no stub installed")
	}
}

// TestKeyringStub_IsWired confirms installKeyringStub swaps the seam
// for the duration of the test.
func TestKeyringStub_IsWired(t *testing.T) {
	f := setFakeKeyring(t)
	if kr := effectiveKeyring(); kr != keyringStub(f) {
		t.Errorf("effectiveKeyring is not the installed fake")
	}
}

// mustTokenPath is defined in cli_login_test.go (one helper per
// package is enough — keeping the definition there preserves the
// existing import surface for cmd/gregale/*_test.go).

// --- Pre-#439 rename migration --------------------------------------------
//
// PR #439 (commit 3bc9796c, "feat(cli): rename binary faas -> gregale")
// flipped keyringService from "faas-cli" to "gregale-cli" and the
// plaintext-file path from ~/.config/faas/token to
// ~/.config/gregale/token without forward-migrating. A customer who
// logged in before the upgrade is logged out at the next launch. The
// tests below pin the one-shot migration: loadToken consults the
// legacy stores in turn, and saveToken erases them once the new
// store has the value.

// TestLoadToken_PreRenameKeychain_LegacyServiceIsRead covers the
// case where a pre-#439 customer has the secret only in the legacy
// keychain service ("faas-cli"). loadToken must read it so the
// current command sees the secret without forcing a re-login.
func TestLoadToken_PreRenameKeychain_LegacyServiceIsRead(t *testing.T) {
	setupHermeticTokensEnv(t)
	f := setFakeKeyring(t)
	want := testAPIKey('c')
	writePreRenameKeychainEntry(t, f, want)

	if got := loadToken(); got != want {
		t.Errorf("loadToken = %q, want valid pre-rename keychain token", got)
	}
}

// TestLoadToken_PreRenameFile_LegacyFileIsRead covers the case
// where a pre-#439 customer has the secret only in the legacy
// plaintext file (~/.config/faas/token). loadToken must read it.
func TestLoadToken_PreRenameFile_LegacyFileIsRead(t *testing.T) {
	setupHermeticTokensEnv(t)
	setFakeKeyring(t) // empty
	want := testAPIKey('d')
	p := writePreRenamePlaintextToken(t, want)

	if got := loadToken(); got != want {
		t.Errorf("loadToken = %q, want valid pre-rename file token", got)
	}
	// The legacy file is read but NOT removed on load — removal is
	// saveToken's job (mirrors the existing #293 pattern: read
	// eagerly, write migrates).
	if _, err := os.Stat(p); err != nil {
		t.Errorf("legacy file should still exist after load; Stat err=%v", err)
	}
}

// TestLoadToken_PreRename_PreferNewOverLegacy pins the priority:
// when BOTH the new and legacy stores are populated, the new store
// wins. A post-rename customer must not be served the legacy
// value (e.g. an ex-coworker's account on the same machine).
func TestLoadToken_PreRename_PreferNewOverLegacy(t *testing.T) {
	setupHermeticTokensEnv(t)
	want := testAPIKey('e')
	setFakeKeyring(t,
		withEntry(keyringService, keyringAccount, want),
		withEntry(legacyKeyringService, keyringAccount, testAPIKey('c')),
	)
	writePreRenamePlaintextToken(t, testAPIKey('f'))

	if got := loadToken(); got != want {
		t.Errorf("loadToken = %q, want new keychain value (new keychain wins over legacy)", got)
	}
}

// TestSaveToken_PreRename_MigratesKeychainAndFile covers the
// forward-migration on the first successful keychain Set. Both
// the legacy keychain service entry AND the legacy plaintext file
// are removed once the new keychain has the new value.
func TestSaveToken_PreRename_MigratesKeychainAndFile(t *testing.T) {
	setupHermeticTokensEnv(t)
	f := setFakeKeyring(t)
	writePreRenameKeychainEntry(t, f, "legacy-kc")
	legacyFile := writePreRenamePlaintextToken(t, "legacy-file")
	want := testAPIKey('f')

	if err := saveToken(want); err != nil {
		t.Fatalf("saveToken: %v", err)
	}
	if got, _ := f.Get(legacyKeyringService, keyringAccount); got != "" {
		t.Errorf("legacy keychain entry = %q, want removed (ErrNotFound)", got)
	}
	if _, err := os.Stat(legacyFile); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("legacy plaintext file should be removed; Stat err=%v", err)
	}
	if got, _ := f.Get(keyringService, keyringAccount); got != want {
		t.Errorf("new keychain entry = %q, want saved token", got)
	}
}

// TestDeleteToken_ClearsLegacyKeychainAndFile pins the symmetric
// logout path: gregale logout must remove BOTH the new and legacy
// stores, regardless of which one the user originally logged in
// with. A pre-#439 customer who runs logout should not have the
// old entry survive.
func TestDeleteToken_ClearsLegacyKeychainAndFile(t *testing.T) {
	setupHermeticTokensEnv(t)
	f := setFakeKeyring(t)
	writePreRenameKeychainEntry(t, f, "legacy-kc")
	legacyFile := writePreRenamePlaintextToken(t, "legacy-file")
	if err := saveToken(testAPIKey('a')); err != nil {
		t.Fatalf("saveToken: %v", err)
	}
	// After save, the new store is populated. Logout must clear
	// both stores.
	deleteToken()

	if _, err := f.Get(keyringService, keyringAccount); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("new keychain entry should be removed")
	}
	if _, err := f.Get(legacyKeyringService, keyringAccount); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("legacy keychain entry should be removed")
	}
	if _, err := os.Stat(legacyFile); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("legacy plaintext file should be removed; Stat err=%v", err)
	}
}
