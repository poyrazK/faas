package conformance

import (
	"crypto/rand"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func randomTokenHash(t *testing.T) []byte {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return sum[:]
}

// testAPIKeyRequiresScopes pins the api_keys.scopes NOT NULL + cardinality
// CHECK in both stores. MemStore accepted a nil slice, which hid that
// programmatic signup minted every key with nil scopes and 500'd on
// Postgres.
func testAPIKeyRequiresScopes(t *testing.T, fx *Fixture) {
	if _, err := fx.Store.CreateAPIKey(fx.Ctx, fx.Account.ID, randomTokenHash(t), "no-scopes", nil); err == nil {
		t.Fatal("CreateAPIKey with nil scopes succeeded; Postgres rejects it")
	}
	if _, err := fx.Store.CreateAPIKeyWithExpiryAndProvenance(fx.Ctx, fx.Account.ID, randomTokenHash(t), "no-scopes",
		[]string{}, nil, "203.0.113.1", "ua", nil); err == nil {
		t.Fatal("CreateAPIKeyWithExpiryAndProvenance with empty scopes succeeded; Postgres rejects it")
	}
	key, err := fx.Store.CreateAPIKey(fx.Ctx, fx.Account.ID, randomTokenHash(t), "admin", api.ScopesAdminOnly)
	if err != nil || len(key.Scopes) != 1 || key.Scopes[0] != api.ScopeAdmin {
		t.Fatalf("CreateAPIKey(admin) = %+v, %v", key, err)
	}
}

// testLoginTokenSingleUseAndExpiry pins the magic-link / password-reset
// token contract: a token signs in exactly once and never after expiry.
func testLoginTokenSingleUseAndExpiry(t *testing.T, fx *Fixture) {
	live := randomTokenHash(t)
	if err := fx.Store.IssueLoginToken(fx.Ctx, live, fx.Account.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("IssueLoginToken: %v", err)
	}
	if got, err := fx.Store.ConsumeLoginToken(fx.Ctx, live); err != nil || got != fx.Account.ID {
		t.Fatalf("first ConsumeLoginToken = %q, %v; want the account", got, err)
	}
	if got, err := fx.Store.ConsumeLoginToken(fx.Ctx, live); err == nil || got != "" {
		t.Fatalf("second ConsumeLoginToken = %q, %v; want an error", got, err)
	}
	expired := randomTokenHash(t)
	if err := fx.Store.IssueLoginToken(fx.Ctx, expired, fx.Account.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("IssueLoginToken(expired): %v", err)
	}
	if got, err := fx.Store.ConsumeLoginToken(fx.Ctx, expired); err == nil || got != "" {
		t.Fatalf("ConsumeLoginToken(expired) = %q, %v; want an error", got, err)
	}
	if got, err := fx.Store.ConsumeLoginToken(fx.Ctx, randomTokenHash(t)); err == nil || got != "" {
		t.Fatalf("ConsumeLoginToken(unknown) = %q, %v; want an error", got, err)
	}
}

// testEmailVerificationTokenSingleUseAndExpiry mirrors the login-token
// contract for address-verification tokens.
func testEmailVerificationTokenSingleUseAndExpiry(t *testing.T, fx *Fixture) {
	live := randomTokenHash(t)
	if err := fx.Store.IssueEmailVerificationToken(fx.Ctx, live, fx.Account.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("IssueEmailVerificationToken: %v", err)
	}
	if got, err := fx.Store.ConsumeEmailVerificationToken(fx.Ctx, live); err != nil || got != fx.Account.ID {
		t.Fatalf("first ConsumeEmailVerificationToken = %q, %v; want the account", got, err)
	}
	if got, err := fx.Store.ConsumeEmailVerificationToken(fx.Ctx, live); err == nil || got != "" {
		t.Fatalf("second ConsumeEmailVerificationToken = %q, %v; want an error", got, err)
	}
	expired := randomTokenHash(t)
	if err := fx.Store.IssueEmailVerificationToken(fx.Ctx, expired, fx.Account.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("IssueEmailVerificationToken(expired): %v", err)
	}
	if got, err := fx.Store.ConsumeEmailVerificationToken(fx.Ctx, expired); err == nil || got != "" {
		t.Fatalf("ConsumeEmailVerificationToken(expired) = %q, %v; want an error", got, err)
	}
}

// testSessionRevocationIsAccountScoped pins the session inventory the
// dashboard's "sign out other devices" and the password-change revocation
// rely on, including the cross-account guard on single revocation.
func testSessionRevocationIsAccountScoped(t *testing.T, fx *Fixture) {
	keep, drop := uuid.NewString(), uuid.NewString()
	for _, id := range []string{keep, drop} {
		if _, err := fx.Store.CreateSession(fx.Ctx, id, fx.Account.ID, "192.0.2.1", "conformance"); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
	}
	other, err := fx.Store.CreateAccount(fx.Ctx, "sessions-other-"+uuid.NewString()[:8]+"@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := fx.Store.RevokeSession(fx.Ctx, keep, other.ID); err != nil || ok {
		t.Fatalf("RevokeSession from another account = %v, %v; want false", ok, err)
	}
	n, err := fx.Store.RevokeAllSessions(fx.Ctx, fx.Account.ID, keep)
	if err != nil || n != 1 {
		t.Fatalf("RevokeAllSessions(except keep) = %d, %v; want 1", n, err)
	}
	sessions, err := fx.Store.ListSessions(fx.Ctx, fx.Account.ID)
	if err != nil || len(sessions) != 1 || sessions[0].ID != keep {
		t.Fatalf("ListSessions after revoke-all = %+v, %v; want only the kept session", sessions, err)
	}
	if n, err := fx.Store.RevokeAllSessions(fx.Ctx, fx.Account.ID, uuid.Nil.String()); err != nil || n != 1 {
		t.Fatalf("RevokeAllSessions(no exception) = %d, %v; want 1", n, err)
	}
	if ok, err := fx.Store.RevokeSession(fx.Ctx, drop, fx.Account.ID); err != nil || ok {
		t.Fatalf("RevokeSession of an already revoked session = %v, %v; want false", ok, err)
	}
}

// testDeleteAPIKeyIsAccountScoped pins that one account cannot delete or
// authenticate with another account's key after deletion.
func testDeleteAPIKeyIsAccountScoped(t *testing.T, fx *Fixture) {
	hash := randomTokenHash(t)
	key, err := fx.Store.CreateAPIKey(fx.Ctx, fx.Account.ID, hash, "scoped", api.ScopesAdminOnly)
	if err != nil {
		t.Fatal(err)
	}
	other, err := fx.Store.CreateAccount(fx.Ctx, "keys-other-"+uuid.NewString()[:8]+"@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.DeleteAPIKey(fx.Ctx, other.ID, key.ID); err == nil {
		t.Fatal("DeleteAPIKey from another account succeeded")
	}
	if _, _, err := fx.Store.AuthenticateKey(fx.Ctx, hash); err != nil {
		t.Fatalf("AuthenticateKey after a refused cross-account delete: %v", err)
	}
	if err := fx.Store.DeleteAPIKey(fx.Ctx, fx.Account.ID, key.ID); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}
	if _, _, err := fx.Store.AuthenticateKey(fx.Ctx, hash); err == nil {
		t.Fatal("AuthenticateKey succeeded after the key was deleted")
	}
}

// testMFADisableRequestSingleUse pins the emailed "disable MFA" link: it
// works once, and issuing a newer request invalidates the older link.
func testMFADisableRequestSingleUse(t *testing.T, fx *Fixture) {
	first, second := randomTokenHash(t), randomTokenHash(t)
	if err := fx.Store.IssueMFADisableRequest(fx.Ctx, first, fx.Account.ID, time.Now()); err != nil {
		t.Fatalf("IssueMFADisableRequest: %v", err)
	}
	if err := fx.Store.IssueMFADisableRequest(fx.Ctx, second, fx.Account.ID, time.Now()); err != nil {
		t.Fatalf("IssueMFADisableRequest(second): %v", err)
	}
	if got, err := fx.Store.ConsumeMFADisableRequest(fx.Ctx, first); err == nil || got != "" {
		t.Fatalf("ConsumeMFADisableRequest(superseded) = %q, %v; want an error", got, err)
	}
	if got, err := fx.Store.ConsumeMFADisableRequest(fx.Ctx, second); err != nil || got != fx.Account.ID {
		t.Fatalf("ConsumeMFADisableRequest = %q, %v; want the account", got, err)
	}
	if got, err := fx.Store.ConsumeMFADisableRequest(fx.Ctx, second); err == nil || got != "" {
		t.Fatalf("second ConsumeMFADisableRequest = %q, %v; want an error", got, err)
	}
}

// testDeployTokenAuthentication pins the per-app deploy token: it
// authenticates as a deploy:write principal bound to its app, and stops
// authenticating once revoked.
func testDeployTokenAuthentication(t *testing.T, fx *Fixture) {
	tokens, ok := fx.Store.(state.DeployTokenStore)
	if !ok {
		t.Skip("store has no deploy tokens")
	}
	hash := randomTokenHash(t)
	token, err := tokens.CreateDeployToken(fx.Ctx, fx.Account.ID, fx.App.ID, hash, "ci", nil, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("CreateDeployToken: %v", err)
	}
	acct, key, err := tokens.AuthenticateDeployToken(fx.Ctx, hash)
	if err != nil || acct.ID != fx.Account.ID || len(key.Scopes) != 1 || key.Scopes[0] != api.ScopeDeployWrite {
		t.Fatalf("AuthenticateDeployToken = %s, %+v, %v; want the account with deploy:write", acct.ID, key, err)
	}
	if _, err := tokens.RevokeDeployToken(fx.Ctx, fx.Account.ID, fx.App.ID, token.ID); err != nil {
		t.Fatalf("RevokeDeployToken: %v", err)
	}
	if _, _, err := tokens.AuthenticateDeployToken(fx.Ctx, hash); err == nil {
		t.Fatal("AuthenticateDeployToken succeeded after revocation")
	}
	other, err := fx.Store.CreateAccount(fx.Ctx, "deploy-token-other-"+uuid.NewString()[:8]+"@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tokens.CreateDeployToken(fx.Ctx, other.ID, fx.App.ID, randomTokenHash(t), "cross", nil, time.Now().Add(time.Hour)); err == nil {
		t.Fatal("CreateDeployToken for another account's app succeeded")
	}
}
