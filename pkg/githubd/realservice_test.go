// RealService tests (slice 8 + PR-C, ADR-012).
//
// PR-C adds the install-state rehydrate path:
//   - ExchangeOAuthCode now hits the real GitHub OAuth API
//     (user-to-server token exchange → /user/installations →
//     install-token mint → age seal → StoreInstalls.Upsert).
//   - ensureInstallToken rehydrates the install token from the
//     sealed durable row on TokenCache miss (process restart).
//
// Tests below cover: the rejection paths (empty code/state),
// the reject-empty-accountID path, the bad-verification-code path,
// the real-API success path (httptest impersonating api.github.com),
// the seal-magic-prefix tripwire, the upsert-on-conflict path,
// the cold-start rehydrate happy path, the cold-start rotation
// path (token expired → re-mint → re-seal → re-persist), and the
// cold-start rehydrate of GetInstallState + BindAppRepo (the
// audit-gap closure: the dashboard bind picker no longer 502's
// after githubd restart).
package githubd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// newTestRealService builds a RealService with the in-memory
// BindingsStore + StoreInstalls + a fresh age keypair.
//
// Pass a non-empty accountID to also seed a durable install row
// (so the bind path exercises the happy path); pass empty string
// to exercise the "no installation for account" 502 branch that
// PR-B's no-install tests rely on.
//
// Pre-PR-C, the install state was a direct in-memory assignment
// (s.installs[accountID] = ...) that the BindAppRepo test
// exposed via newTestRealService. PR-C promotes that assignment
// to a StoreInstalls.Upsert, but the test surface looks the same
// from the helper's perspective.
func newTestRealService(t *testing.T, accountID string) *RealService {
	t.Helper()
	ident, recipient := newTestAgeKeypair(t)
	istore := newMemStoreInstalls()
	if accountID != "" {
		sealed, err := sealForTest(t, recipient, "ghs_test_token")
		if err != nil {
			t.Fatalf("newTestRealService: seed seal: %v", err)
		}
		if err := istore.Upsert(context.Background(), state.GitHubInstall{
			AccountID:        accountID,
			InstallationID:   1,
			DefaultBranch:    "main",
			SealedToken:      sealed,
			TokenExpiresAt:   time.Now().Add(time.Hour),
			AuditGithubLogin: "test",
		}); err != nil {
			t.Fatalf("newTestRealService: seed install: %v", err)
		}
	}
	return &RealService{
		Auth:          nil,
		Tokens:        nil,
		Checks:        nil,
		Store:         newMemBindingsStore(),
		Installs:      istore,
		Recipient:     recipient,
		Identity:      ident,
		Audit:         func(string, string, map[string]any) {},
		bindingsCache: map[string]map[string]state.GitHubBinding{},
		installs:      map[string]installState{},
	}
}

// memStoreInstalls is the in-memory StoreInstalls test double.
// Mirrors MemStore's multi-install behavior so unit
// tests don't need a Postgres round-trip. Used by
// ensureInstallToken's rehydrate tests.
type memStoreInstalls struct {
	mu    sync.Mutex
	items map[string]state.GitHubInstall
}

func newMemStoreInstalls() *memStoreInstalls {
	return &memStoreInstalls{items: map[string]state.GitHubInstall{}}
}

func (m *memStoreInstalls) Upsert(_ context.Context, inst state.GitHubInstall) error {
	if inst.AccountID == "" {
		return state.ErrNotFound
	}
	if inst.AuditGithubLogin == "" {
		return state.ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	inst.SealedAt = time.Now()
	m.items[inst.AccountID+"\x00"+strconv.FormatInt(inst.InstallationID, 10)] = inst
	return nil
}

func (m *memStoreInstalls) ForAccount(_ context.Context, accountID string) (state.GitHubInstall, error) {
	if accountID == "" {
		return state.GitHubInstall{}, state.ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var newest state.GitHubInstall
	for _, inst := range m.items {
		if inst.AccountID == accountID && (newest.AccountID == "" || inst.SealedAt.After(newest.SealedAt)) {
			newest = inst
		}
	}
	if newest.AccountID == "" {
		return state.GitHubInstall{}, state.ErrNotFound
	}
	return newest, nil
}

func (m *memStoreInstalls) ForAccountInstallation(_ context.Context, accountID string, installationID int64) (state.GitHubInstall, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.items[accountID+"\x00"+strconv.FormatInt(installationID, 10)]
	if !ok {
		return state.GitHubInstall{}, state.ErrNotFound
	}
	return inst, nil
}

// ----- Bind-path tests (PR-B contract, unchanged) ---------------

func TestRealService_BindAndLookup(t *testing.T) {
	svc := newTestRealService(t, "acct-1")
	id, err := svc.BindAppRepo("app-1", "acct-1", 1, "octo/api", "main")
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty binding id")
	}
	b, err := svc.GetAppBinding("app-1", "acct-1")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if b.RepoFullName != "octo/api" {
		t.Errorf("repo = %q, want octo/api", b.RepoFullName)
	}
	if b.ProductionBranch != "main" {
		t.Errorf("branch = %q, want main", b.ProductionBranch)
	}
	if b.BindingID != id {
		t.Errorf("binding id mismatch: got %q, want %q", b.BindingID, id)
	}
}

func TestRealService_BindDefaultsToMain(t *testing.T) {
	svc := newTestRealService(t, "acct-1")
	if _, err := svc.BindAppRepo("app-2", "acct-1", 1, "octo/api", ""); err != nil {
		t.Fatal(err)
	}
	b, _ := svc.GetAppBinding("app-2", "acct-1")
	if b.ProductionBranch != "main" {
		t.Errorf("default branch = %q, want main", b.ProductionBranch)
	}
}

func TestRealService_UnbindRemovesBinding(t *testing.T) {
	svc := newTestRealService(t, "acct-1")
	if _, err := svc.BindAppRepo("app-3", "acct-1", 1, "octo/api", "main"); err != nil {
		t.Fatal(err)
	}
	if err := svc.UnbindAppRepo("app-3", "acct-1"); err != nil {
		t.Fatal(err)
	}
	b, _ := svc.GetAppBinding("app-3", "acct-1")
	if b.BindingID != "" {
		t.Errorf("after unbind, binding = %+v, want empty", b)
	}
	// Idempotent: second unbind is a no-op.
	if err := svc.UnbindAppRepo("app-3", "acct-1"); err != nil {
		t.Errorf("second unbind: %v", err)
	}
}

// TestRealService_InstallStateDefaults asserts the cache miss +
// StoreInstalls miss path: a never-installed account returns
// UNSPECIFIED with no error.
func TestRealService_InstallStateDefaults(t *testing.T) {
	svc := newTestRealService(t, "")
	state, instID, branch, err := svc.GetInstallState("acct-none")
	if err != nil {
		t.Fatal(err)
	}
	if state != githubdgrpc.InstallStateUnspecified {
		t.Errorf("state = %v, want Unspecified", state)
	}
	if instID != "" || branch != "" {
		t.Errorf("got non-empty install id/branch: %q/%q", instID, branch)
	}
}

// ----- PR-C ExchangeOAuthCode: rejection + happy path -----------

func TestRealService_ExchangeOAuthRejectsEmpty(t *testing.T) {
	svc := newTestRealService(t, "")
	if _, _, err := svc.ExchangeOAuthCode("", "code-1", "state-1"); err == nil {
		t.Error("empty accountID should error")
	}
	if _, _, err := svc.ExchangeOAuthCode("acct", "", "state-1"); err == nil {
		t.Error("empty code should error")
	}
	if _, _, err := svc.ExchangeOAuthCode("acct", "code-1", ""); err == nil {
		t.Error("empty state should error (CSRF defense-in-depth)")
	}
}

// TestRealService_ExchangeOAuthCode_MakesRealAPICall pins the full
// real-flow happy path: user-to-server token exchange →
// /user/installations → install-token mint → seal → store upsert.
// Uses an httptest.Server impersonating api.github.com.
func TestRealService_ExchangeOAuthCode_MakesRealAPICall(t *testing.T) {
	ident, recipient := newTestAgeKeypair(t)
	fake := newFakeGithubServer(t, fakeGithubOpts{
		accessToken:  "user-token-abc",
		installs:     []map[string]any{{"id": float64(4242), "account": map[string]any{"login": "octocat"}}},
		installToken: "ghs_install_xyz",
		expiresAt:    time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		appID:        "100",
	})
	defer fake.Close()

	auth := newTestAppAuth(t, "100", fake.URL)
	tokens := NewTokenCache(auth, 5*time.Minute)
	istore := newMemStoreInstalls()
	audit := newRecordingAuditFn()
	svc := NewRealService(auth, tokens, nil, newMemBindingsStore(), istore, recipient, ident, audit.AuditEvent)

	instID, branch, err := svc.ExchangeOAuthCode("acct-1", "code-xyz", "state-abc")
	if err != nil {
		t.Fatalf("ExchangeOAuthCode: %v", err)
	}
	if instID != "4242" {
		t.Errorf("installation_id = %q, want 4242", instID)
	}
	if branch == "" {
		t.Error("default_branch empty, want non-empty")
	}

	// Audit fired with the right event.
	audit.mu.Lock()
	defer audit.mu.Unlock()
	var sawSeal bool
	for _, e := range audit.events {
		if e.event == "auth.install.token_sealed" && e.accountID == "acct-1" {
			sawSeal = true
		}
	}
	if !sawSeal {
		t.Errorf("audit event auth.install.token_sealed missing; got %+v", audit.events)
	}

	// Store row exists with sealed token.
	inst, err := istore.ForAccount(context.Background(), "acct-1")
	if err != nil {
		t.Fatalf("store read: %v", err)
	}
	if inst.InstallationID != 4242 {
		t.Errorf("stored installation_id = %d, want 4242", inst.InstallationID)
	}
	if !strings.HasPrefix(string(inst.SealedToken), "age-encryption.org/v1") {
		t.Errorf("SealedToken prefix = %q, want age-encryption.org/v1", inst.SealedToken[:32])
	}
	// Unseal round-trip — a regression that wrote a hand-stamped
	// plain-text blob starting with the magic prefix would pass the
	// HasPrefix check above but fail here. Pinned against the
	// githubd installTokenSealKey so a future envelope-key rename
	// trips this test instead of a silent production downgrade.
	env, oerr := secretbox.Open(ident, inst.SealedToken)
	if oerr != nil {
		t.Fatalf("unseal SealedToken: %v", oerr)
	}
	if got, ok := env[installTokenSealKey]; !ok || got != "ghs_install_xyz" {
		t.Errorf("unsealed value = %q, want %q", got, "ghs_install_xyz")
	}
	if inst.AuditGithubLogin != "octocat" {
		t.Errorf("AuditGithubLogin = %q, want octocat", inst.AuditGithubLogin)
	}
}

// TestRealService_ExchangeOAuthCode_RejectsExpiredCode pins the
// "user OAuth code rejected" path: a 200 + {"error":"bad_verification_code"}
// from /login/oauth/access_token must surface as a non-nil err and
// leave the store untouched.
func TestRealService_ExchangeOAuthCode_RejectsExpiredCode(t *testing.T) {
	ident, recipient := newTestAgeKeypair(t)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "bad_verification_code"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer fake.Close()

	auth := newTestAppAuth(t, "100", fake.URL)
	istore := newMemStoreInstalls()
	svc := NewRealService(auth, NewTokenCache(auth, 5*time.Minute), nil, newMemBindingsStore(), istore, recipient, ident, nil)

	if _, _, err := svc.ExchangeOAuthCode("acct-1", "stale-code", "state"); err == nil {
		t.Fatal("expected error for bad_verification_code")
	}
	// Store untouched.
	if _, err := istore.ForAccount(context.Background(), "acct-1"); err == nil {
		t.Error("store should be untouched on rejected user code")
	}
}

// TestRealService_ExchangeOAuthCode_TransitFailureReturnsErr pins
// the transport-failure path: a 502 to /login/oauth/access_token
// must surface as a non-nil err.
func TestRealService_ExchangeOAuthCode_TransitFailureReturnsErr(t *testing.T) {
	ident, recipient := newTestAgeKeypair(t)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer fake.Close()

	auth := newTestAppAuth(t, "100", fake.URL)
	svc := NewRealService(auth, NewTokenCache(auth, 5*time.Minute), nil, newMemBindingsStore(), newMemStoreInstalls(), recipient, ident, nil)

	if _, _, err := svc.ExchangeOAuthCode("acct-1", "code", "state"); err == nil {
		t.Fatal("expected error on 502 response")
	}
}

// TestRealService_ExchangeOAuthCode_AlreadyInstalledUpserts pins
// the multi-install path: a second call with a different selected
// installation_id preserves both rows and makes the newer one current.
func TestRealService_ExchangeOAuthCode_AlreadyInstalledUpserts(t *testing.T) {
	ident, recipient := newTestAgeKeypair(t)
	var hitCount atomic.Int64
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "user-tok"})
		case "/user/installations":
			// atomic counter is incremented on every request so the
			// handler's read races safely with the test goroutine's
			// future writes. After the first call, /user/installations
			// returns install id 2 instead of 1.
			id := float64(1)
			if hitCount.Add(1) > 1 {
				id = 2
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"installations": []map[string]any{{"id": id, "account": map[string]any{"login": "octocat"}}},
			})
		case "/app/installations/1/access_tokens", "/app/installations/2/access_tokens":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"token":      "ghs_x",
				"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			})
		default:
			if strings.HasPrefix(r.URL.Path, "/app/installations/") {
				// /app/installations/{id} (verify) — echo the id so the
				// test can assert the round-trip (the production code
				// trusts the verify endpoint's id, so a stub that
				// always returns id=1 would mask a regression).
				idStr := strings.TrimPrefix(r.URL.Path, "/app/installations/")
				_, _ = w.Write([]byte(`{"id":` + idStr + `,"account":{"login":"octocat"},"default_branch":"main"}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer fake.Close()

	auth := newTestAppAuth(t, "100", fake.URL)
	istore := newMemStoreInstalls()
	svc := NewRealService(auth, NewTokenCache(auth, 5*time.Minute), nil, newMemBindingsStore(), istore, recipient, ident, nil)

	if _, _, err := svc.ExchangeOAuthCode("acct-1", "code-1", "state.1"); err != nil {
		t.Fatalf("first exchange: %v", err)
	}
	if _, _, err := svc.ExchangeOAuthCode("acct-1", "code-2", "state.2"); err != nil {
		t.Fatalf("second exchange: %v", err)
	}
	got, err := istore.ForAccount(context.Background(), "acct-1")
	if err != nil {
		t.Fatalf("store read: %v", err)
	}
	if got.InstallationID != 2 {
		t.Errorf("installation_id = %d, want 2 (newest install)", got.InstallationID)
	}
	if _, err := istore.ForAccountInstallation(context.Background(), "acct-1", 1); err != nil {
		t.Errorf("first installation was not preserved: %v", err)
	}
}

// ----- PR-C ensureInstallToken: cold-start rehydrate -------------

// TestRealService_EnsureInstallToken_ColdStart pins the audit-gap
// closure: with a TokenCache miss but a fresh durable row,
// ensureInstallToken unseals the sealed blob (no fresh mint) and
// returns the install token.
func TestRealService_EnsureInstallToken_ColdStart(t *testing.T) {
	ident, recipient := newTestAgeKeypair(t)
	rawToken := "ghs_cold_start_token"

	// Pre-seal a row.
	sealed, err := sealForTest(t, recipient, rawToken)
	if err != nil {
		t.Fatalf("sealForTest: %v", err)
	}
	istore := newMemStoreInstalls()
	if err := istore.Upsert(context.Background(), state.GitHubInstall{
		AccountID:        "acct-1",
		InstallationID:   9999,
		DefaultBranch:    "main",
		SealedToken:      sealed,
		TokenExpiresAt:   time.Now().Add(time.Hour),
		AuditGithubLogin: "octocat",
	}); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	fake := newFakeGithubServer(t, fakeGithubOpts{
		accessToken:  "user-tok",
		installs:     []map[string]any{{"id": float64(9999), "account": map[string]any{"login": "octocat"}}},
		installToken: rawToken,
		expiresAt:    time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		appID:        "100",
		// Track whether /app/installations/{id}/access_tokens gets
		// hit (it should NOT on the cold path — sealed token is
		// still valid).
		trackInstallTokenCalls: true,
	})
	defer fake.Close()

	auth := newTestAppAuth(t, "100", fake.URL)
	tokens := NewTokenCache(auth, 5*time.Minute)
	svc := NewRealService(auth, tokens, nil, newMemBindingsStore(), istore, recipient, ident, nil)

	// Empty in-memory install cache → falls through to durable.
	_, token, err := svc.ensureInstallToken(context.Background(), "acct-1")
	if err != nil {
		t.Fatalf("ensureInstallToken: %v", err)
	}
	if token != rawToken {
		t.Errorf("token = %q, want %q (cold-start rehydrate should return the unsealed value)", token, rawToken)
	}
	if fake.installTokenCallsCount() > 0 {
		t.Errorf("expected 0 install-token mints on cold-start (sealed token still valid); got %d", fake.installTokenCallsCount())
	}
}

// TestRealService_EnsureInstallToken_RotatesExpired pins the
// rotation path: a sealed row whose TokenExpiresAt is in the past
// must trigger a fresh ExchangeInstallationToken + re-seal +
// re-persist.
func TestRealService_EnsureInstallToken_RotatesExpired(t *testing.T) {
	ident, recipient := newTestAgeKeypair(t)

	sealed, err := sealForTest(t, recipient, "ghs_old_token")
	if err != nil {
		t.Fatalf("sealForTest: %v", err)
	}
	istore := newMemStoreInstalls()
	if err := istore.Upsert(context.Background(), state.GitHubInstall{
		AccountID:        "acct-1",
		InstallationID:   9999,
		DefaultBranch:    "main",
		SealedToken:      sealed,
		TokenExpiresAt:   time.Now().Add(-time.Minute), // expired
		AuditGithubLogin: "octocat",
	}); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	fake := newFakeGithubServer(t, fakeGithubOpts{
		appID:                  "100",
		installToken:           "ghs_rotated_token",
		expiresAt:              time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		trackInstallTokenCalls: true,
	})
	defer fake.Close()

	auth := newTestAppAuth(t, "100", fake.URL)
	tokens := NewTokenCache(auth, 5*time.Minute)
	audit := newRecordingAuditFn()
	svc := NewRealService(auth, tokens, nil, newMemBindingsStore(), istore, recipient, ident, audit.AuditEvent)

	_, token, err := svc.ensureInstallToken(context.Background(), "acct-1")
	if err != nil {
		t.Fatalf("ensureInstallToken: %v", err)
	}
	if token != "ghs_rotated_token" {
		t.Errorf("token = %q, want ghs_rotated_token", token)
	}
	if fake.installTokenCallsCount() != 1 {
		t.Errorf("install-token mint count = %d, want 1 (rotation)", fake.installTokenCallsCount())
	}
	// Store row was updated.
	got, err := istore.ForAccount(context.Background(), "acct-1")
	if err != nil {
		t.Fatalf("store read: %v", err)
	}
	if got.TokenExpiresAt.Before(time.Now()) {
		t.Errorf("TokenExpiresAt = %v, want refreshed", got.TokenExpiresAt)
	}
	// Audit fired for the rotation.
	audit.mu.Lock()
	defer audit.mu.Unlock()
	var sawRotation bool
	for _, e := range audit.events {
		if e.event == "auth.install.token_sealed" {
			if rot, ok := e.payload["rotated"].(bool); ok && rot {
				sawRotation = true
			}
		}
	}
	if !sawRotation {
		t.Errorf("rotation audit event missing; got %+v", audit.events)
	}
}

// TestRealService_GetInstallState_ColdStart pins the audit-gap
// closure for the GetInstallState path: a fresh process (empty
// in-memory install cache) with a durable row returns the right
// (state, installation_id, default_branch).
func TestRealService_GetInstallState_ColdStart(t *testing.T) {
	ident, recipient := newTestAgeKeypair(t)
	sealed, err := sealForTest(t, recipient, "ghs_token")
	if err != nil {
		t.Fatalf("sealForTest: %v", err)
	}
	istore := newMemStoreInstalls()
	if err := istore.Upsert(context.Background(), state.GitHubInstall{
		AccountID:        "acct-1",
		InstallationID:   4242,
		DefaultBranch:    "develop",
		SealedToken:      sealed,
		TokenExpiresAt:   time.Now().Add(time.Hour),
		AuditGithubLogin: "octocat",
	}); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	auth := newTestAppAuth(t, "100", "http://unused")
	svc := NewRealService(auth, NewTokenCache(auth, 5*time.Minute), nil, newMemBindingsStore(), istore, recipient, ident, nil)

	st, instID, branch, err := svc.GetInstallState("acct-1")
	if err != nil {
		t.Fatalf("GetInstallState: %v", err)
	}
	if st != githubdgrpc.InstallStateInstalled {
		t.Errorf("state = %v, want Installed", st)
	}
	if instID != "4242" {
		t.Errorf("installation_id = %q, want 4242", instID)
	}
	if branch != "develop" {
		t.Errorf("default_branch = %q, want develop", branch)
	}
}

// TestRealService_BindAppRepo_ColdStart pins the audit-gap closure
// for the bind path: with empty in-memory install cache but a
// durable row, BindAppRepo succeeds (no "no installation for
// account" 502). This is the bug PR-C ships to fix.
func TestRealService_BindAppRepo_ColdStart(t *testing.T) {
	ident, recipient := newTestAgeKeypair(t)
	sealed, err := sealForTest(t, recipient, "ghs_token")
	if err != nil {
		t.Fatalf("sealForTest: %v", err)
	}
	istore := newMemStoreInstalls()
	if err := istore.Upsert(context.Background(), state.GitHubInstall{
		AccountID:        "acct-1",
		InstallationID:   7777,
		DefaultBranch:    "main",
		SealedToken:      sealed,
		TokenExpiresAt:   time.Now().Add(time.Hour),
		AuditGithubLogin: "octocat",
	}); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	auth := newTestAppAuth(t, "100", "http://unused")
	svc := NewRealService(auth, NewTokenCache(auth, 5*time.Minute), nil, newMemBindingsStore(), istore, recipient, ident, nil)

	bid, err := svc.BindAppRepo("app-1", "acct-1", 7777, "octo/api", "main")
	if err != nil {
		t.Fatalf("BindAppRepo cold-start: %v", err)
	}
	if bid == "" {
		t.Error("expected non-empty binding id")
	}
	// Lookup round-trip.
	b, err := svc.GetAppBinding("app-1", "acct-1")
	if err != nil {
		t.Fatalf("GetAppBinding: %v", err)
	}
	if b.BindingID != bid {
		t.Errorf("binding id = %q, want %q", b.BindingID, bid)
	}
	if b.RepoFullName != "octo/api" {
		t.Errorf("repo = %q, want octo/api", b.RepoFullName)
	}
}

// ----- other surface (PR-B contract, unchanged) -----------------

func TestRealService_WriteCheckRequiresConfig(t *testing.T) {
	svc := newTestRealService(t, "")
	err := svc.WriteCheck("octo/api", "abc", githubdgrpc.CheckPhaseQueued, "", "queued")
	if err == nil {
		t.Error("nil Checks writer should error")
	}
}

func TestRealService_ListInstallableReposRequiresAuth(t *testing.T) {
	svc := newTestRealService(t, "")
	_, err := svc.ListInstallableRepos("acct-1", 42)
	if err == nil {
		t.Error("nil Auth should error")
	}
}

func TestRealService_CreateDeploymentFromPushIsHTTPPath(t *testing.T) {
	svc := newTestRealService(t, "")
	_, _, err := svc.CreateDeploymentFromPush("octo/api", "refs/heads/main", "abc", "alice")
	if err == nil {
		t.Error("gRPC CreateDeploymentFromPush should error (webhook path is HTTP)")
	}
}

// _ pins context import so a future refactor that drops the only
// user doesn't drop the import.
var _ = context.Background

// TestRealService_VerifyInstallation_RequiresAuth asserts the
// §11 fail-closed behavior: a RealService built without OAuth
// credentials must refuse VerifyInstallation rather than silently
// returning verified=false (which the dashboard would treat as a
// "forged" callback and could confuse with a transient GitHub
// outage).
func TestRealService_VerifyInstallation_RequiresAuth(t *testing.T) {
	svc := newTestRealService(t, "")
	verified, _, _, err := svc.VerifyInstallation(1, "")
	if err == nil {
		t.Fatal("expected error when Auth is nil, got nil")
	}
	if verified {
		t.Errorf("verified = true, want false when Auth is nil")
	}
}

// TestRealService_VerifyInstallation_ForgedIsNotAnError asserts the
// reviewed contract: a forged installation_id returns
// (false, "", "", nil) — verified=false with err=nil — so the
// dashboard renders the "forged callback" banner rather than a 5xx
// page. A non-nil err is reserved for transport failures
// (api.github.com unreachable, App JWT rejected).
//
// We exercise this with an httptest.Server that returns 404 for
// every /app/installations/{id} request, mirroring GitHub's
// response to an unknown install_id.
func TestRealService_VerifyInstallation_ForgedIsNotAnError(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/app/installations/") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer fake.Close()

	auth := &AppAuth{AppID: "1", PrivateKey: newTestKey(t), HTTPClient: &singleHostClient{base: fake.Client(), api: fake.URL}}
	svc := NewRealServiceLegacy(auth, nil, nil, newMemBindingsStore())
	verified, _, branch, err := svc.VerifyInstallation(9999999, "")
	if err != nil {
		t.Fatalf("err = %v, want nil for forged install_id", err)
	}
	if verified {
		t.Errorf("verified = true, want false for forged install_id")
	}
	if branch != "" {
		t.Errorf("branch = %q, want empty for forged install_id", branch)
	}
}

// TestRealService_VerifyInstallation_TransportErrorIsErr asserts the
// inverse: a 5xx from api.github.com (anything not 200/404) comes
// back as a non-nil err so the dashboard can render a "couldn't
// reach GitHub" banner instead of a "forged callback" banner.
func TestRealService_VerifyInstallation_TransportErrorIsErr(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
	}))
	defer fake.Close()

	auth := &AppAuth{AppID: "1", PrivateKey: newTestKey(t), HTTPClient: &singleHostClient{base: fake.Client(), api: fake.URL}}
	svc := NewRealServiceLegacy(auth, nil, nil, newMemBindingsStore())
	verified, _, _, err := svc.VerifyInstallation(1, "")
	if err == nil {
		t.Fatal("err = nil, want non-nil for 5xx response")
	}
	if verified {
		t.Errorf("verified = true, want false when err is non-nil")
	}
}

// TestRealService_VerifyInstallation_AccountLoginMismatchForged asserts
// the §11 ownership check (PR-B): a real install whose account.login
// does NOT match expectedLogin returns verified=false, err=nil —
// indistinguishable from a 404 to a forged caller. The dashboard
// distinguishes them by the AccountLogin field the apid-side
// comparison path consumes (it gets the install payload, not just
// the bool).
func TestRealService_VerifyInstallation_AccountLoginMismatchForged(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/app/installations/") {
			// The install is REAL — its account is "alice".
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":42,"account":{"login":"alice"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer fake.Close()

	auth := &AppAuth{AppID: "1", PrivateKey: newTestKey(t), HTTPClient: &singleHostClient{base: fake.Client(), api: fake.URL}}
	svc := NewRealServiceLegacy(auth, nil, nil, newMemBindingsStore())
	verified, _, _, err := svc.VerifyInstallation(42, "bob")
	if err != nil {
		t.Fatalf("err = %v, want nil for §11 mismatch (caller should treat as forged)", err)
	}
	if verified {
		t.Errorf("verified = true, want false for login mismatch")
	}
}

// TestRealService_VerifyInstallation_AccountLoginMatchAccepted asserts
// the §11 ownership check happy path: real install with matching
// account.login returns verified=true with the install's account
// login surfaced (so the apid handler can log it for the audit).
func TestRealService_VerifyInstallation_AccountLoginMatchAccepted(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/app/installations/") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":42,"account":{"login":"alice"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer fake.Close()

	auth := &AppAuth{AppID: "1", PrivateKey: newTestKey(t), HTTPClient: &singleHostClient{base: fake.Client(), api: fake.URL}}
	svc := NewRealServiceLegacy(auth, nil, nil, newMemBindingsStore())
	verified, login, _, err := svc.VerifyInstallation(42, "alice")
	if err != nil {
		t.Fatalf("err = %v, want nil for matching login", err)
	}
	if !verified {
		t.Errorf("verified = false, want true for matching login")
	}
	if login != "alice" {
		t.Errorf("account login = %q, want alice", login)
	}
}

// ----- Test helpers (httptest github server + audit recorder) ---

// fakeGithubOpts configures the httptest impersonator.
type fakeGithubOpts struct {
	accessToken            string
	installs               []map[string]any
	installToken           string
	expiresAt              string
	appID                  string
	trackInstallTokenCalls bool
	// verifyLogin is the account.login the /app/installations/{id}
	// verify endpoint echoes back. Defaults to "octocat" so the
	// PR-B §11 happy path holds; tests that want to exercise the
	// mismatch branch set it explicitly.
	verifyLogin string
	// verifyBranch is the default_branch the verify endpoint
	// echoes back. Defaults to defaultProductionBranch ("main").
	verifyBranch string
}

type fakeGithubServer struct {
	*httptest.Server
	installTokenCalls  atomic.Int64
	trackInstallTokens bool
}

// newFakeGithubServer stands up an httptest.Server impersonating
// api.github.com. Routes handled:
//
//   - POST /login/oauth/access_token → {access_token}
//   - GET  /user/installations        → {installations:[{id,account}]}
//   - GET  /app/installations/{id}    → {id,account.login}
//   - POST /app/installations/{id}/access_tokens → {token,expires_at}
func newFakeGithubServer(t *testing.T, opts fakeGithubOpts) *fakeGithubServer {
	t.Helper()
	if opts.verifyLogin == "" {
		opts.verifyLogin = "octocat"
	}
	if opts.verifyBranch == "" {
		opts.verifyBranch = defaultProductionBranch
	}
	srv := &fakeGithubServer{trackInstallTokens: opts.trackInstallTokenCalls}
	srv.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/login/oauth/access_token" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": opts.accessToken})
		case r.URL.Path == "/user/installations" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"installations": opts.installs})
		case strings.HasPrefix(r.URL.Path, "/app/installations/") && strings.HasSuffix(r.URL.Path, "/access_tokens") && r.Method == http.MethodPost:
			if srv.trackInstallTokens {
				srv.installTokenCalls.Add(1)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{
				"token":      opts.installToken,
				"expires_at": opts.expiresAt,
			})
		case strings.HasPrefix(r.URL.Path, "/app/installations/") && r.Method == http.MethodGet:
			// /app/installations/{id} verify. Echo the requested id
			// back so tests that compare IDs see the round-trip; pin
			// the login + default_branch via opts so the §11 mismatch
			// branch can be modelled.
			idStr := strings.TrimPrefix(r.URL.Path, "/app/installations/")
			id, perr := strconv.ParseInt(idStr, 10, 64)
			if perr != nil {
				id = 0
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             id,
				"account":        map[string]any{"login": opts.verifyLogin},
				"default_branch": opts.verifyBranch,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return srv
}

// installTokenCallsCount reads the atomic counter; safe to call
// after the test's last network call without coordinating with
// the deferred Close() (the counter is atomic so the read is
// race-free even if a stale handler is still incrementing).
func (s *fakeGithubServer) installTokenCallsCount() int64 {
	return s.installTokenCalls.Load()
}

// newTestAppAuth builds an AppAuth wired to a httptest.Server. The
// appID parameter sets the App ID (any non-empty string works for
// the tests; the actual JWT mint doesn't depend on it being a real
// GitHub App ID).
func newTestAppAuth(t *testing.T, appID, apiURL string) *AppAuth {
	t.Helper()
	return &AppAuth{
		AppID:        appID,
		ClientID:     "client-id-test",
		ClientSecret: "client-secret-test",
		PrivateKey:   newTestKey(t),
		HTTPClient:   &singleHostClient{base: http.DefaultClient, api: apiURL},
	}
}

// recordingAuditFn is a tiny in-memory audit collector for tests.
type recordingAuditFn struct {
	mu     sync.Mutex
	events []recordedEvent
}

type recordedEvent struct {
	event     string
	accountID string
	payload   map[string]any
}

func (r *recordingAuditFn) AuditEvent(event string, accountID string, payload map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Copy payload so a caller mutation later doesn't trip the test.
	cp := make(map[string]any, len(payload))
	for k, v := range payload {
		cp[k] = v
	}
	r.events = append(r.events, recordedEvent{event: event, accountID: accountID, payload: cp})
}

func newRecordingAuditFn() *recordingAuditFn {
	r := &recordingAuditFn{}
	return r
}

// _ pins the closure shape: AuditEvent is func(string, string, map[string]any).
var _ AuditEvent = (&recordingAuditFn{}).AuditEvent
