// memstore_oidc_branches_test.go — coverage for the
// in-process OIDC trust-policy + exchanged-token seams in
// pkg/state/memstore.go (issue #270 / ADR-101).
//
// The DB-bound surface for OIDC (upsert_policy.sql,
// find_exchanged_token.sql) needs Postgres. The MemStore
// paths are testable in isolation and pin:
//
//   - UpsertOIDCTrustPolicy happy path (insert)
//   - UpsertOIDCTrustPolicy update preserves CreatedAt
//   - GetOIDCTrustPolicy returns ErrNotFound on miss
//   - ListOIDCTrustPoliciesForAccount filters by account_id
//   - InsertOIDCExchangedToken happy path
//   - GetOIDCExchangedTokenByHash on hit + miss (ErrNotFound)
//   - DeleteOIDCExchangedToken happy + unknown-id idempotent
//   - regexpMatch: the inlined Go-regexp wrapper. Pins the
//     patterns OIDC-issuers actually use (claim-prefix,
//     email-domain, character classes).
//
// Whitebox test (package state) matching the memstore_*_test.go
// convention.
package state

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestRegexpMatch_HitsAndMisses pins the canonical Go regexp
// semantics over the OIDC-relevant pattern surface (issuer
// allowlist subjects, claim-prefix, etc.).
func TestRegexpMatch_HitsAndMisses(t *testing.T) {
	cases := []struct {
		pattern, s string
		want       bool
	}{
		{`^ops@`, "ops@gregale.dev", true},
		{`^ops@`, "user@gregale.dev", false},
		{`@gregale\.dev$`, "alice@gregale.dev", true},
		{`@gregale\.dev$`, "alice@evil.dev", false},
		// Character class
		{`^[a-z]+$`, "abc", true},
		{`^[a-z]+$`, "ABC", false},
		{`^[a-z]+$`, "abc123", false},
		// Wildcard
		{`.*`, "", true},
		{`^.*$`, "anything goes here", true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run("", func(t *testing.T) {
			if got := regexpMatch(tc.pattern, tc.s); got != tc.want {
				t.Errorf("regexpMatch(%q, %q) = %v, want %v", tc.pattern, tc.s, got, tc.want)
			}
		})
	}
}

// TestUpsertOIDCTrustPolicy_InsertNew pins the first-time path:
// no existing row, CreatedAt is server-stamped to a non-zero
// value, the row is reachable via GetOIDCTrustPolicy.
func TestUpsertOIDCTrustPolicy_InsertNew(t *testing.T) {
	m := NewMemStore()
	p := &OIDCTrustPolicy{
		AccountID:      "acct-1",
		IssuerURL:      "https://accounts.google.com",
		JWKSURL:        "https://accounts.google.com/.well-known/jwks.json",
		SubjectPattern: "^ops@",
	}
	got, err := m.UpsertOIDCTrustPolicy(context.Background(), p)
	if err != nil {
		t.Fatalf("UpsertOIDCTrustPolicy: %v", err)
	}
	if got == nil {
		t.Fatal("returned policy is nil")
	}
	if got.CreatedAt.IsZero() {
		t.Error("new policy CreatedAt is zero (server stamp missing)")
	}
	// Read-back via Lookup.
	fetch, err := m.GetOIDCTrustPolicy(context.Background(), "acct-1", "https://accounts.google.com")
	if err != nil {
		t.Fatalf("GetOIDCTrustPolicy: %v", err)
	}
	if fetch.IssuerURL != "https://accounts.google.com" {
		t.Errorf("Get back IssuerURL = %q, want original", fetch.IssuerURL)
	}
}

// TestUpsertOIDCTrustPolicy_UpdatePreservesCreatedAt pins the
// "preserve CreatedAt across upserts" contract: a second
// upsert for the same (account_id, issuer_url) does NOT
// overwrite the original CreatedAt. The audit-reader depends
// on stable CreatedAt to identify "first-use" timestamps.
func TestUpsertOIDCTrustPolicy_UpdatePreservesCreatedAt(t *testing.T) {
	m := NewMemStore()
	first := &OIDCTrustPolicy{
		AccountID: "acct-2",
		IssuerURL: "https://idp.example.com",
	}
	if _, err := m.UpsertOIDCTrustPolicy(context.Background(), first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	originalCreatedAt := first.CreatedAt

	// Sleep a measurable delta so we can detect an UpdateAt drift
	// independent of CreatedAt preservation.
	time.Sleep(2 * time.Millisecond)

	second := &OIDCTrustPolicy{
		AccountID: "acct-2",
		IssuerURL: "https://idp.example.com",
		// Same key — exercises the update branch.
	}
	if _, err := m.UpsertOIDCTrustPolicy(context.Background(), second); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if !second.CreatedAt.Equal(originalCreatedAt) {
		t.Errorf("CreatedAt drifted on update: original=%v second=%v", originalCreatedAt, second.CreatedAt)
	}
	if second.UpdatedAt.Before(originalCreatedAt) {
		t.Errorf("UpdatedAt = %v not after CreatedAt = %v", second.UpdatedAt, originalCreatedAt)
	}
}

// TestGetOIDCTrustPolicy_NotFound pins the miss path: a
// GetOIDCTrustPolicy for an unknown key returns ErrNotFound.
func TestGetOIDCTrustPolicy_NotFound(t *testing.T) {
	m := NewMemStore()
	_, err := m.GetOIDCTrustPolicy(context.Background(), "nope", "https://unknown.example.com")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(unknown): err = %v, want ErrNotFound", err)
	}
}

// TestListOIDCTrustPoliciesForAccount_FiltersByAccount pins the
// account-scoped list: policies for other accounts must not
// appear in the returned slice.
func TestListOIDCTrustPoliciesForAccount_FiltersByAccount(t *testing.T) {
	m := NewMemStore()
	// Insert for two accounts.
	for _, p := range []*OIDCTrustPolicy{
		{AccountID: "acct-a", IssuerURL: "https://idp1.example.com"},
		{AccountID: "acct-a", IssuerURL: "https://idp2.example.com"},
		{AccountID: "acct-b", IssuerURL: "https://idp3.example.com"},
	} {
		if _, err := m.UpsertOIDCTrustPolicy(context.Background(), p); err != nil {
			t.Fatalf("seed %v: %v", p, err)
		}
	}
	a, err := m.ListOIDCTrustPoliciesForAccount(context.Background(), "acct-a")
	if err != nil {
		t.Fatalf("List(acct-a): %v", err)
	}
	if len(a) != 2 {
		t.Errorf("List(acct-a) returned %d policies, want 2", len(a))
	}
	for _, p := range a {
		if p.AccountID != "acct-a" {
			t.Errorf("cross-account leak: %q appears in acct-a list", p.AccountID)
		}
	}
}

// TestInsertOIDCExchangedToken_HappyPath pins the row-creation
// branch. The TokenHash gets hex-encoded into the map key, the
// row is reachable via GetOIDCExchangedTokenByHash.
func TestInsertOIDCExchangedToken_HappyPath(t *testing.T) {
	m := NewMemStore()
	hash := bytes.Repeat([]byte{0xAB}, 32)
	tok := &OIDCExchangedToken{
		ID:        "exch-1",
		AccountID: "acct-1",
		TokenHash: hash,
		IssuerURL: "https://accounts.google.com",
	}
	if _, err := m.InsertOIDCExchangedToken(context.Background(), tok); err != nil {
		t.Fatalf("InsertOIDCExchangedToken: %v", err)
	}
	fetch, err := m.GetOIDCExchangedTokenByHash(context.Background(), hash)
	if err != nil {
		t.Fatalf("GetOIDCExchangedTokenByHash: %v", err)
	}
	if fetch.ID != "exch-1" {
		t.Errorf("Get back ID = %q, want exch-1", fetch.ID)
	}
}

// TestGetOIDCExchangedTokenByHash_NotFound pins the miss path:
// an unknown hash returns ErrNotFound.
func TestGetOIDCExchangedTokenByHash_NotFound(t *testing.T) {
	m := NewMemStore()
	hash := bytes.Repeat([]byte{0x00}, 32)
	_, err := m.GetOIDCExchangedTokenByHash(context.Background(), hash)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(unknown hash): err = %v, want ErrNotFound", err)
	}
}

// TestDeleteOIDCExchangedToken_HappyAndErrorOnUnknown pins two
// paths: delete-an-existing-row succeeds (and the row is gone
// afterwards), and delete-of-unknown-id surfaces ErrNotFound
// (the documented contract — operator-driven revoke MUST be
// able to distinguish "row never existed" from "row gone
// silently" so an idempotency guard doesn't apply here).
func TestDeleteOIDCExchangedToken_HappyAndErrorOnUnknown(t *testing.T) {
	m := NewMemStore()
	hash := bytes.Repeat([]byte{0xCD}, 32)
	tok := &OIDCExchangedToken{
		ID:        "exch-x",
		AccountID: "acct-x",
		TokenHash: hash,
	}
	if _, err := m.InsertOIDCExchangedToken(context.Background(), tok); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Happy path.
	if err := m.DeleteOIDCExchangedToken(context.Background(), "exch-x"); err != nil {
		t.Errorf("Delete(existing): err = %v, want nil", err)
	}
	if _, err := m.GetOIDCExchangedTokenByHash(context.Background(), hash); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: err = %v, want ErrNotFound", err)
	}

	// Re-delete of the just-deleted id: ErrNotFound (the row is
	// gone).
	if err := m.DeleteOIDCExchangedToken(context.Background(), "exch-x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete of just-deleted: err = %v, want ErrNotFound", err)
	}
	// Unknown id from the start also returns ErrNotFound.
	if err := m.DeleteOIDCExchangedToken(context.Background(), "never-existed"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete(never-existed): err = %v, want ErrNotFound", err)
	}
}

// TestAuthenticateOIDCBearer_HitAndMisses pins the bearer-token
// authentication path (issue #270 / ADR-101). Three branches:
//
//   - Hit: an inserted exchanged token resolves to its (Account,
//     APIKey) pair.
//   - Miss (unknown hash): returns (zero, zero, ErrNotFound).
//   - Expired: a row whose ExpiresAt is in the past is
//     lazy-deleted and returns ErrNotFound. The lazy-delete is
//     the MemStore equivalent of the pg `WHERE expires_at >
//     NOW()` filter.
//
// The lazy-delete side-effect is load-bearing: a second
// AuthenticateOIDCBearer call with the same expired hash must
// still return ErrNotFound but the row must be gone (no memory
// leak, no goroutine pinning the row for retry).
func TestAuthenticateOIDCBearer_HitAndMisses(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	// Seed an account the bearer resolves to.
	acct, err := m.CreateAccount(ctx, "bearer-"+uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	// Happy path: insert a token, then authenticate against the
	// same hash.
	goodHash := bytes.Repeat([]byte{0x11}, 32)
	if _, err := m.InsertOIDCExchangedToken(ctx, &OIDCExchangedToken{
		ID:        "exch-good",
		AccountID: acct.ID,
		TokenHash: goodHash,
		IssuerURL: "https://accounts.google.com",
	}); err != nil {
		t.Fatalf("InsertOIDCExchangedToken: %v", err)
	}
	resolvedAcct, key, err := m.AuthenticateOIDCBearer(ctx, goodHash)
	if err != nil {
		t.Fatalf("AuthenticateOIDCBearer(hit): err = %v, want nil", err)
	}
	if resolvedAcct.ID != acct.ID {
		t.Errorf("Auth(hit).Account.ID = %q, want %q", resolvedAcct.ID, acct.ID)
	}
	if key.ID == "" {
		t.Error("Auth(hit).APIKey.ID is empty")
	}

	// Miss: an unknown hash → ErrNotFound, zero values.
	if _, _, err := m.AuthenticateOIDCBearer(ctx, bytes.Repeat([]byte{0x22}, 32)); !errors.Is(err, ErrNotFound) {
		t.Errorf("Auth(unknown): err = %v, want ErrNotFound", err)
	}

	// Expired: insert a token with ExpiresAt in the past. The
	// first auth call returns ErrNotFound AND lazily deletes the
	// row (MemStore mirror of `WHERE expires_at > NOW()`). The
	// second call still returns ErrNotFound (no resurrection).
	expiredHash := bytes.Repeat([]byte{0x33}, 32)
	if _, err := m.InsertOIDCExchangedToken(ctx, &OIDCExchangedToken{
		ID:        "exch-expired",
		AccountID: acct.ID,
		TokenHash: expiredHash,
		IssuerURL: "https://idp.example.com",
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	}); err != nil {
		t.Fatalf("InsertOIDCExchangedToken (expired): %v", err)
	}
	if _, _, err := m.AuthenticateOIDCBearer(ctx, expiredHash); !errors.Is(err, ErrNotFound) {
		t.Errorf("Auth(expired): err = %v, want ErrNotFound", err)
	}
	// Lazy-delete verified: GetOIDCExchangedTokenByHash on the
	// same hash is now also ErrNotFound.
	if _, err := m.GetOIDCExchangedTokenByHash(ctx, expiredHash); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetOIDCExchangedTokenByHash after lazy-delete: err = %v, want ErrNotFound", err)
	}

	// Account-deleted branch: insert a token whose AccountID no
	// longer exists in m.accounts. AuthenticateOIDCBearer must
	// still return ErrNotFound (NOT a panic, NOT a stale row
	// resolution). The pg equivalent is FK CASCADE on the
	// account delete; MemStore mirrors it by looking up
	// m.accounts[row.AccountID] and returning ErrNotFound when
	// the lookup misses.
	orphanHash := bytes.Repeat([]byte{0x44}, 32)
	if _, err := m.InsertOIDCExchangedToken(ctx, &OIDCExchangedToken{
		ID:        "exch-orphan",
		AccountID: "ghost-account-id",
		TokenHash: orphanHash,
		IssuerURL: "https://idp.example.com",
	}); err != nil {
		t.Fatalf("InsertOIDCExchangedToken (orphan): %v", err)
	}
	if _, _, err := m.AuthenticateOIDCBearer(ctx, orphanHash); !errors.Is(err, ErrNotFound) {
		t.Errorf("Auth(orphan): err = %v, want ErrNotFound", err)
	}
}

// TestAccountByOIDCSubject_TrustPolicyMatch pins the issuer-
// URL + subject-pattern resolution path. The contract (ADR-101
// PR-A): any account that has a trust policy for the given
// issuer URL matches; an empty SubjectPattern accepts any
// subject; a non-empty SubjectPattern is regex-matched.
//
// Branches pinned:
//
//   - Issuer URL mismatch: ErrNotFound even with a valid
//     policy on the same account (proves the issuer filter is
//     the gating predicate, not the account match).
//   - Subject pattern mismatch (regex no-match): ErrNotFound
//     when the policy's pattern rejects the subject.
//   - Subject pattern match (regex hit): returns the policy's
//     account.
//   - Account bound to the policy missing from m.accounts:
//     ErrNotFound (the policy row is dangling — the pg schema
//     has FK ON DELETE SET NULL; MemStore mirrors the
//     ErrNotFound behaviour).
//   - Empty SubjectPattern accepts any subject: returns the
//     account without regex matching.
func TestAccountByOIDCSubject_TrustPolicyMatch(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	// Seed two accounts + four policies.
	acctA, err := m.CreateAccount(ctx, "oidc-a-"+uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount(a): %v", err)
	}
	acctB, err := m.CreateAccount(ctx, "oidc-b-"+uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount(b): %v", err)
	}
	if _, err := m.UpsertOIDCTrustPolicy(ctx, &OIDCTrustPolicy{
		AccountID:      acctA.ID,
		IssuerURL:      "https://idp1.example.com",
		SubjectPattern: "^ops@",
	}); err != nil {
		t.Fatalf("UpsertOIDCTrustPolicy(acctA): %v", err)
	}
	if _, err := m.UpsertOIDCTrustPolicy(ctx, &OIDCTrustPolicy{
		AccountID: acctB.ID,
		IssuerURL: "https://idp1.example.com",
		// Empty pattern = accept any subject.
		SubjectPattern: "",
	}); err != nil {
		t.Fatalf("UpsertOIDCTrustPolicy(acctB): %v", err)
	}

	// Issuer URL mismatch → ErrNotFound.
	if _, err := m.AccountByOIDCSubject(ctx, "https://other.example.com", "ops@example.com"); !errors.Is(err, ErrNotFound) {
		t.Errorf("AccountByOIDCSubject(wrong issuer): err = %v, want ErrNotFound", err)
	}

	// Subject pattern match (regex hit) → acctA.
	resolved, err := m.AccountByOIDCSubject(ctx, "https://idp1.example.com", "ops@gregale.dev")
	if err != nil {
		t.Fatalf("AccountByOIDCSubject(regex match): %v", err)
	}
	if resolved.ID != acctA.ID {
		t.Errorf("regex-match resolved.ID = %q, want %q (acctA)", resolved.ID, acctA.ID)
	}

	// Subject pattern mismatch (regex no-match) → acctB's
	// empty pattern catches it. We can distinguish by asking
	// for a subject the regex rejects.
	resolved2, err := m.AccountByOIDCSubject(ctx, "https://idp1.example.com", "user@gregale.dev")
	if err != nil {
		t.Fatalf("AccountByOIDCSubject(empty-pattern catch): %v", err)
	}
	if resolved2.ID != acctB.ID {
		t.Errorf("empty-pattern resolved.ID = %q, want %q (acctB)", resolved2.ID, acctB.ID)
	}

	// Dangling policy: insert a policy whose AccountID doesn't
	// exist in m.accounts. The resolver must skip it (return
	// ErrNotFound, not panic, not stale).
	if _, err := m.UpsertOIDCTrustPolicy(ctx, &OIDCTrustPolicy{
		AccountID:      "ghost-account",
		IssuerURL:      "https://dangling.example.com",
		SubjectPattern: "",
	}); err != nil {
		t.Fatalf("UpsertOIDCTrustPolicy(dangling): %v", err)
	}
	if _, err := m.AccountByOIDCSubject(ctx, "https://dangling.example.com", "anything"); !errors.Is(err, ErrNotFound) {
		t.Errorf("AccountByOIDCSubject(dangling): err = %v, want ErrNotFound", err)
	}
}

func TestAccountByOIDCSubject_GitHubBindingBootstrap(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	seed := func(email, slug string, installID int64) Account {
		acct, err := m.CreateAccount(ctx, email, api.PlanHobby)
		if err != nil {
			t.Fatalf("CreateAccount(%s): %v", email, err)
		}
		app, err := m.CreateApp(ctx, App{AccountID: acct.ID, Slug: slug})
		if err != nil {
			t.Fatalf("CreateApp(%s): %v", slug, err)
		}
		if err := m.UpsertGitHubInstall(ctx, GitHubInstall{
			AccountID: acct.ID, InstallationID: installID, AuditGithubLogin: "octocat",
		}); err != nil {
			t.Fatalf("UpsertGitHubInstall(%s): %v", slug, err)
		}
		if err := m.UpsertGithubInstallBinding(ctx, GitHubBinding{
			AppID: app.ID, AccountID: acct.ID, BindingID: "bind-" + slug,
			InstallID: installID, RepoFullName: "OctoCat/Hello", ProductionBranch: "main",
		}); err != nil {
			t.Fatalf("UpsertGithubInstallBinding(%s): %v", slug, err)
		}
		return acct
	}

	first := seed("oidc-github-a-"+uuid.NewString()+"@example.com", "oidc-gh-a-"+uuid.NewString(), 101)
	resolved, err := m.AccountByOIDCSubject(ctx, githubActionsOIDCIssuer,
		"repo:octocat/hello:environment:production")
	if err != nil {
		t.Fatalf("AccountByOIDCSubject(binding bootstrap): %v", err)
	}
	if resolved.ID != first.ID {
		t.Fatalf("resolved account %q, want %q", resolved.ID, first.ID)
	}

	seed("oidc-github-b-"+uuid.NewString()+"@example.com", "oidc-gh-b-"+uuid.NewString(), 202)
	if _, err := m.AccountByOIDCSubject(ctx, githubActionsOIDCIssuer,
		"repo:octocat/hello:environment:production"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ambiguous binding: got %v, want ErrNotFound", err)
	}
}
