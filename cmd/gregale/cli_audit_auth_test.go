package main_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// CLI audit 2026-10-31: auth and credential handling.

// authAuditAPI records every request and answers the account, apps, keys
// and key-delete routes the auth paths use.
type authAuditAPI struct {
	mu       sync.Mutex
	requests []string
	auth     map[string]string // "METHOD path" -> Authorization
	keys     string
}

func newAuthAuditAPI(t *testing.T) (*authAuditAPI, *httptest.Server) {
	t.Helper()
	a := &authAuditAPI{auth: map[string]string{}, keys: `[]`}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		route := r.Method + " " + r.URL.Path
		a.requests = append(a.requests, route)
		a.auth[route] = r.Header.Get("Authorization")
		keys := a.keys
		a.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/account":
			_ = json.NewEncoder(w).Encode(api.AccountResponse{ID: "acct-1", Email: "person@example.test", Plan: "free", Status: "active"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/keys":
			_, _ = w.Write([]byte(keys))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/keys/"):
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return a, srv
}

func (a *authAuditAPI) saw(route string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	authz, ok := a.auth[route]
	return authz, ok
}

func (a *authAuditAPI) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.requests)
}

// interactiveTerminal pins the TTY seam on: start needs a terminal, and the
// saved-login check reads the keychain only from one.
func interactiveTerminal(t *testing.T) {
	t.Helper()
	previous := *main.TestOnlyTTY
	on := true
	*main.TestOnlyTTY = &on
	t.Cleanup(func() { *main.TestOnlyTTY = previous })
}

func runAuthAudit(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	stdout, stderr, restore := main.SwapIO(t)
	defer restore()
	code := main.Run(args)
	return code, stdout.String(), stderr()
}

func TestInsecureAPIBaseIsRefusedBeforeAnyRequest(t *testing.T) {
	main.SetupCLIRegression(t)
	t.Setenv("GREGALE_ALLOW_INSECURE_API", "")
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
	t.Setenv("FAAS_API", "http://gregale.internal:8080")

	code, stdout, stderr := runAuthAudit(t, "whoami")
	if code != 1 || stdout != "" {
		t.Fatalf("whoami exit=%d stdout=%q", code, stdout)
	}
	for _, want := range []string{"plain HTTP", "GREGALE_ALLOW_INSECURE_API=1"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}

	// Requests made outside the preflight (capability discovery, logout's
	// saved session base, completion helpers) fail in the client without
	// dialing.
	_, err := main.NewClient("http://gregale.internal:8080", main.TestAPIKey('a')).Whoami(context.Background())
	if err == nil || !strings.Contains(err.Error(), "plain HTTP") {
		t.Fatalf("guarded client error = %v", err)
	}
}

func TestInsecureAPIBaseLoopbackAndOptIn(t *testing.T) {
	main.SetupCLIRegression(t)
	t.Setenv("GREGALE_ALLOW_INSECURE_API", "")
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
	recorder, srv := newAuthAuditAPI(t)

	// Loopback HTTP (httptest listens on 127.0.0.1) needs no opt-in.
	t.Setenv("FAAS_API", srv.URL)
	if code, _, stderr := runAuthAudit(t, "whoami"); code != 0 || strings.Contains(stderr, "plain HTTP") {
		t.Fatalf("loopback whoami exit=%d stderr=%s", code, stderr)
	}
	if _, ok := recorder.saw("GET /v1/account"); !ok {
		t.Fatal("loopback whoami did not reach the API")
	}

	// The opt-in passes the preflight with one warning. "whoami extra" stops
	// at its usage check right after, so nothing is dialed.
	t.Setenv("FAAS_API", "http://gregale.internal:8080")
	t.Setenv("GREGALE_ALLOW_INSECURE_API", "1")
	code, _, stderr := runAuthAudit(t, "whoami", "extra")
	if code != 1 || strings.Count(stderr, "uses plain HTTP") != 1 || strings.Contains(stderr, "Insecure API connection") {
		t.Fatalf("opted-in whoami exit=%d stderr=%s", code, stderr)
	}
}

func TestSavingAnInsecureAPIBaseIsRefused(t *testing.T) {
	main.SetupCLIRegression(t)
	t.Setenv("GREGALE_ALLOW_INSECURE_API", "")
	for _, args := range [][]string{
		{"config", "set", "api-base", "http://gregale.internal"},
		{"config", "profile", "add", "lan", "http://10.0.0.5:8080"},
	} {
		if code, _, stderr := runAuthAudit(t, args...); code == 0 || !strings.Contains(stderr, "plain HTTP") {
			t.Fatalf("%v exit=%d stderr=%s", args, code, stderr)
		}
	}
	for _, args := range [][]string{
		{"config", "set", "api-base", "http://127.0.0.1:8080"},
		{"config", "profile", "add", "local", "http://localhost:8080"},
		{"config", "profile", "add", "tls", "https://gregale.internal"},
	} {
		if code, _, stderr := runAuthAudit(t, args...); code != 0 {
			t.Fatalf("%v exit=%d stderr=%s", args, code, stderr)
		}
	}
	t.Setenv("GREGALE_ALLOW_INSECURE_API", "true")
	code, _, stderr := runAuthAudit(t, "config", "profile", "add", "lan", "http://10.0.0.5:8080")
	if code != 0 || !strings.Contains(stderr, "uses plain HTTP") {
		t.Fatalf("opted-in profile add exit=%d stderr=%s", code, stderr)
	}

	// A base an older release saved still loads, so it can be replaced, but
	// commands that contact the API refuse it.
	t.Setenv("GREGALE_ALLOW_INSECURE_API", "")
	if err := main.SaveCLIConfig(main.CliConfig{APIBase: "http://gregale.internal"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
	if code, _, stderr := runAuthAudit(t, "whoami"); code != 1 || !strings.Contains(stderr, "plain HTTP") {
		t.Fatalf("saved insecure base whoami exit=%d stderr=%s", code, stderr)
	}
	if code, _, stderr := runAuthAudit(t, "config", "set", "api-base", "https://gregale.internal"); code != 0 {
		t.Fatalf("replacing the saved base exit=%d stderr=%s", code, stderr)
	}
}

func TestLoginAndStartRefuseWhileFAASTokenIsSet(t *testing.T) {
	main.SetupCLIRegression(t)
	interactiveTerminal(t)
	keychain := main.SetFakeKeyring(t)
	recorder, srv := newAuthAuditAPI(t)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))

	for _, args := range [][]string{
		{"login", "--token", main.TestAPIKey('b')},
		{"login"},
		{"start"},
	} {
		code, stdout, stderr := runAuthAudit(t, args...)
		if code != 1 || !strings.Contains(stderr, "unset FAAS_TOKEN") || strings.Contains(stdout, "Logged in") {
			t.Fatalf("%v exit=%d stdout=%q stderr=%s", args, code, stdout, stderr)
		}
	}
	if n := recorder.count(); n != 0 {
		t.Fatalf("refused commands made %d API requests", n)
	}
	if got, err := keychain.Get(main.KeyringService, main.KeyringAccount); err == nil {
		t.Fatalf("refused login saved a credential: %q", got)
	}

	// `login --token "$FAAS_TOKEN"` saves the credential later commands use
	// anyway, so the documented CI form keeps working.
	if code, stdout, stderr := runAuthAudit(t, "login", "--token", main.TestAPIKey('a')); code != 0 || !strings.Contains(stdout, "Logged in") {
		t.Fatalf("login with FAAS_TOKEN's value exit=%d stderr=%s", code, stderr)
	}
}

func TestFAASTokenOverrideWarnsOnStderrOnly(t *testing.T) {
	main.SetupCLIRegression(t)
	interactiveTerminal(t)
	recorder, srv := newAuthAuditAPI(t)
	t.Setenv("FAAS_API", srv.URL)
	env := main.TestAPIKey('a')
	t.Setenv("FAAS_TOKEN", env)

	// An explicit --profile whose credential FAAS_TOKEN replaces.
	if code, _, stderr := runAuthAudit(t, "--profile", "staging", "whoami"); code != 0 || !strings.Contains(stderr, "overrides --profile staging") {
		t.Fatalf("--profile whoami exit=%d stderr=%s", code, stderr)
	}
	if authz, _ := recorder.saw("GET /v1/account"); authz != "Bearer "+env {
		t.Fatalf("authorization = %q", authz)
	}
	// JSON mode keeps stderr for Problem documents and stdout for the result.
	code, stdout, stderr := runAuthAudit(t, "--profile", "staging", "--json", "whoami")
	if code != 0 || strings.Contains(stderr, "FAAS_TOKEN") {
		t.Fatalf("--json --profile whoami exit=%d stderr=%s", code, stderr)
	}
	var account api.AccountResponse
	if err := json.Unmarshal([]byte(stdout), &account); err != nil || account.ID != "acct-1" {
		t.Fatalf("--json stdout is not one account: %q (%v)", stdout, err)
	}

	// A saved login that FAAS_TOKEN shadows.
	main.SetFakeKeyring(t, main.WithEntry(main.KeyringService, main.KeyringAccount, main.TestAPIKey('b')))
	if code, _, stderr := runAuthAudit(t, "whoami"); code != 0 || !strings.Contains(stderr, "overrides your saved login") {
		t.Fatalf("saved-login whoami exit=%d stderr=%s", code, stderr)
	}

	// No warning when FAAS_TOKEN is the saved login, or is unset.
	main.SetFakeKeyring(t, main.WithEntry(main.KeyringService, main.KeyringAccount, env))
	if _, _, stderr := runAuthAudit(t, "whoami"); strings.Contains(stderr, "FAAS_TOKEN") {
		t.Fatalf("same-credential whoami warned: %s", stderr)
	}
	t.Setenv("FAAS_TOKEN", "")
	if _, _, stderr := runAuthAudit(t, "whoami"); strings.Contains(stderr, "FAAS_TOKEN") {
		t.Fatalf("whoami without FAAS_TOKEN warned: %s", stderr)
	}
}

func TestReplacedOrRemovedLoginsRevokeTheirCLIKey(t *testing.T) {
	main.SetupCLIRegression(t)
	recorder, srv := newAuthAuditAPI(t)
	t.Setenv("FAAS_API", srv.URL)
	account := api.AccountResponse{Email: "person@example.test", Plan: string(api.PlanFree)}
	finalize := func(token, keyID string) {
		t.Helper()
		_, _, restore := main.SwapIO(t)
		defer restore()
		if code := main.FinalizeLogin(context.Background(), main.NewClient(srv.URL, ""), token, keyID, account); code != 0 {
			t.Fatalf("finalizeLogin(%s) = %d", keyID, code)
		}
	}

	// Re-login revokes the replaced login's key with that login's token.
	finalize(main.TestAPIKey('1'), "cli-key-1")
	finalize(main.TestAPIKey('2'), "cli-key-2")
	if authz, ok := recorder.saw("DELETE /v1/keys/cli-key-1"); !ok || authz != "Bearer "+main.TestAPIKey('1') {
		t.Fatalf("re-login revoke: seen=%t authorization=%q", ok, authz)
	}
	if _, ok := recorder.saw("DELETE /v1/keys/cli-key-2"); ok {
		t.Fatal("re-login revoked the new key")
	}

	// Logout under FAAS_TOKEN revokes the saved login, not FAAS_TOKEN, and
	// says FAAS_TOKEN still authenticates.
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('9'))
	stdout, stderr, restore := main.SwapIO(t)
	code := main.CmdLogout()
	restore()
	if code != 0 || strings.Contains(stdout.String(), "Logged out") || !strings.Contains(stderr(), "FAAS_TOKEN is still set") {
		t.Fatalf("logout exit=%d stdout=%q stderr=%s", code, stdout, stderr())
	}
	if authz, ok := recorder.saw("DELETE /v1/keys/cli-key-2"); !ok || authz != "Bearer "+main.TestAPIKey('2') {
		t.Fatalf("logout revoke: seen=%t authorization=%q", ok, authz)
	}
	t.Setenv("FAAS_TOKEN", "")

	// Removing a profile revokes the login saved under it.
	*main.ProfileOverride = "staging"
	finalize(main.TestAPIKey('3'), "cli-key-3")
	*main.ProfileOverride = ""
	_, removeStderr, restoreRemove := main.SwapIO(t)
	code = main.CmdProfile([]string{"remove", "staging"})
	restoreRemove()
	if code != 0 {
		t.Fatalf("profile remove = %d: %s", code, removeStderr())
	}
	if authz, ok := recorder.saw("DELETE /v1/keys/cli-key-3"); !ok || authz != "Bearer "+main.TestAPIKey('3') {
		t.Fatalf("profile remove revoke: seen=%t authorization=%q", ok, authz)
	}
}

func TestKeyListShowsIDCreatedAndExpires(t *testing.T) {
	main.SetupCLIRegression(t)
	recorder, srv := newAuthAuditAPI(t)
	recorder.keys = `[{"id":"key-live-1","label":"laptop","prefix":"fp_live_aaaaaaaa","status":"active","created_at":"2026-10-01T12:00:00Z","expires_at":"2026-10-31T12:00:00Z"},` +
		`{"id":"key-old-2","label":"old ci","prefix":"fp_live_bbbbbbbb","status":"revoked","created_at":"2026-01-01T00:00:00Z"}]`
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))

	code, stdout, stderr := runAuthAudit(t, "key", "list")
	if code != 0 {
		t.Fatalf("key list exit=%d stderr=%s", code, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("key list output = %q", stdout)
	}
	for _, want := range []string{"ID", "LABEL", "PREFIX", "STATUS", "CREATED", "EXPIRES"} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("header %q lacks %s", lines[0], want)
		}
	}
	if fields := strings.Fields(lines[1]); fields[0] != "key-live-1" || !strings.Contains(lines[1], "2026-10-") || !strings.Contains(lines[1], "active") {
		t.Fatalf("live row = %q", lines[1])
	}
	if fields := strings.Fields(lines[2]); fields[0] != "key-old-2" || !strings.Contains(lines[2], "revoked") || !strings.HasSuffix(lines[2], "-") {
		t.Fatalf("revoked row = %q", lines[2])
	}
}

func TestConfigAndContextOutputDropFAASAPICredentials(t *testing.T) {
	main.SetupCLIRegression(t)
	main.WithCwd(t, t.TempDir())
	t.Setenv("FAAS_API", "https://deploy-user:s3cret-pass@api.example.test/")
	for _, args := range [][]string{
		{"config", "list"},
		{"--json", "config", "list"},
		{"config", "get", "api-base"},
		{"config", "context", "get"},
		{"--json", "config", "context", "get"},
	} {
		code, stdout, stderr := runAuthAudit(t, args...)
		if code != 0 {
			t.Fatalf("%v exit=%d stderr=%s", args, code, stderr)
		}
		if strings.Contains(stdout+stderr, "s3cret-pass") || strings.Contains(stdout+stderr, "deploy-user") {
			t.Fatalf("%v printed URL credentials:\n%s%s", args, stdout, stderr)
		}
		if !strings.Contains(stdout, "https://api.example.test") {
			t.Fatalf("%v lost the API host:\n%s", args, stdout)
		}
	}
}
