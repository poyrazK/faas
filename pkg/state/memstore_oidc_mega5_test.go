// memstore_oidc_mega5_test.go — Coverage Mega-PR #5 cluster 4:
// pin the OIDC surface on *MemStore. Targets:
//
//   - AuthenticateOIDCBearer (memstore.go:938) — bearer-token → Account+APIKey
//     resolution with lazy-expiry past TTL and account-deleted fallback
//   - AccountByOIDCSubject (memstore.go:978) — issuer + subject_pattern →
//     Account scan with regex-match + empty-pattern fallback
//   - InsertOIDCExchangedToken (memstore.go:1092) — store row, mint id,
//     auto-fill CreatedAt
//   - GetOIDCExchangedTokenByHash (memstore.go:1109, 80%) — fill the
//     expired-lazy-delete branch the existing test didn't reach
//
// Whitebox `package state`. No Postgres dependency.

package state

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// --- AuthenticateOIDCBearer --------------------------------------

func TestAuthenticateOIDCBearer_Happy_Mega5(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	seedAccount(m, "acc-1", api.PlanPro)
	tok := &OIDCExchangedToken{
		ID:        "row-1",
		AccountID: "acc-1",
		TokenHash: []byte("hash-1"),
		ExpiresAt: time.Now().Add(time.Hour),
		IssuerURL: "https://issuer.example",
		Subject:   "user-1",
	}
	if _, err := m.InsertOIDCExchangedToken(t.Context(), tok); err != nil {
		t.Fatalf("seed: %v", err)
	}

	acct, key, err := m.AuthenticateOIDCBearer(t.Context(), []byte("hash-1"))
	if err != nil {
		t.Fatalf("AuthenticateOIDCBearer: %v", err)
	}
	if acct.ID != "acc-1" {
		t.Errorf("acct.ID = %q, want acc-1", acct.ID)
	}
	if key.AccountID != "acc-1" {
		t.Errorf("key.AccountID = %q, want acc-1", key.AccountID)
	}
	if key.Status != string(APIKeyStatusActive) {
		t.Errorf("key.Status = %q, want active", key.Status)
	}
}

func TestAuthenticateOIDCBearer_NotFoundHash_Mega5(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	seedAccount(m, "acc-1", api.PlanPro)
	if _, _, err := m.AuthenticateOIDCBearer(t.Context(), []byte("missing")); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestAuthenticateOIDCBearer_AccountDeleted_Mega5(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	// Seed the exchanged token WITHOUT a backing account. InsertOIDCExchangedToken
	// doesn't check that the account exists; AuthenticateOIDCBearer must return
	// ErrNotFound when the lookup hits the gap.
	tok := &OIDCExchangedToken{
		ID:        "row-1",
		AccountID: "ghost",
		TokenHash: []byte("hash-ghost"),
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if _, err := m.InsertOIDCExchangedToken(t.Context(), tok); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, _, err := m.AuthenticateOIDCBearer(t.Context(), []byte("hash-ghost")); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound (account missing)", err)
	}
}

func TestAuthenticateOIDCBearer_ExpiredLazyDelete_Mega5(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	seedAccount(m, "acc-1", api.PlanPro)
	tok := &OIDCExchangedToken{
		ID:        "row-2",
		AccountID: "acc-1",
		TokenHash: []byte("hash-expired"),
		ExpiresAt: time.Now().Add(-time.Minute), // past TTL
	}
	if _, err := m.InsertOIDCExchangedToken(t.Context(), tok); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// First call: lazy-deletes the row, returns ErrNotFound.
	if _, _, err := m.AuthenticateOIDCBearer(t.Context(), []byte("hash-expired")); !errors.Is(err, ErrNotFound) {
		t.Errorf("first call: err = %v, want ErrNotFound", err)
	}
	// Second call: row is gone — confirms lazy-delete happened.
	if _, _, err := m.AuthenticateOIDCBearer(t.Context(), []byte("hash-expired")); !errors.Is(err, ErrNotFound) {
		t.Errorf("second call: err = %v, want ErrNotFound (row should be deleted)", err)
	}
}

// --- AccountByOIDCSubject ----------------------------------------

func seedOIDCTrustPolicy_Mega5(m *MemStore, accountID, issuer, pattern string) {
	m.oidcTrustPolicies[accountID+"\x00"+issuer] = OIDCTrustPolicy{
		AccountID:      accountID,
		IssuerURL:      issuer,
		SubjectPattern: pattern,
	}
}

func TestAccountByOIDCSubject_NotFound_Mega5(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	if _, err := m.AccountByOIDCSubject(t.Context(), "https://noissuer", "any"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestAccountByOIDCSubject_EmptyPattern_Mega5(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	seedAccount(m, "acc-1", api.PlanPro)
	seedOIDCTrustPolicy_Mega5(m, "acc-1", "https://issuer-1", "")

	// An empty pattern binds nothing (legacy permissive policies let any
	// workflow resolve to the account).
	if acct, err := m.AccountByOIDCSubject(t.Context(), "https://issuer-1", "anything"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AccountByOIDCSubject(empty pattern) = %q, %v; want ErrNotFound", acct.ID, err)
	}
}

func TestAccountByOIDCSubject_RegexMatch_Mega5(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	seedAccount(m, "acc-1", api.PlanPro)
	seedOIDCTrustPolicy_Mega5(m, "acc-1", "https://issuer-1", `^user-[0-9]+$`)

	acct, err := m.AccountByOIDCSubject(t.Context(), "https://issuer-1", "user-42")
	if err != nil {
		t.Fatalf("err = %v, want nil (subject matches regex)", err)
	}
	if acct.ID != "acc-1" {
		t.Errorf("acct.ID = %q, want acc-1", acct.ID)
	}
}

func TestAccountByOIDCSubject_RegexNoMatch_Mega5(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	seedAccount(m, "acc-1", api.PlanPro)
	seedOIDCTrustPolicy_Mega5(m, "acc-1", "https://issuer-1", `^user-[0-9]+$`)

	if _, err := m.AccountByOIDCSubject(t.Context(), "https://issuer-1", "admin-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound (regex no match)", err)
	}
}

func TestAccountByOIDCSubject_WrongIssuer_Mega5(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	seedAccount(m, "acc-1", api.PlanPro)
	// Policy is bound to issuer-1; query for issuer-2 must miss.
	seedOIDCTrustPolicy_Mega5(m, "acc-1", "https://issuer-1", "")

	if _, err := m.AccountByOIDCSubject(t.Context(), "https://issuer-2", "anything"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound (wrong issuer)", err)
	}
}

func TestAccountByOIDCSubject_AccountMissingForPolicy_Mega5(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	// Trust policy points at a non-existent account; the scan should
	// skip the policy and return ErrNotFound (rather than panicking).
	seedOIDCTrustPolicy_Mega5(m, "ghost", "https://issuer-1", "")

	if _, err := m.AccountByOIDCSubject(t.Context(), "https://issuer-1", "anything"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound (account for policy is missing)", err)
	}
}

// --- InsertOIDCExchangedToken ------------------------------------

func TestInsertOIDCExchangedToken_AutoMintsID_Mega5(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	tok := &OIDCExchangedToken{
		AccountID: "acc-1",
		TokenHash: []byte("hash-a"),
		ExpiresAt: time.Now().Add(time.Hour),
	}
	id, err := m.InsertOIDCExchangedToken(t.Context(), tok)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if id == "" {
		t.Fatal("id = empty, want auto-minted")
	}
	if tok.ID != id {
		t.Errorf("tok.ID = %q, want %q", tok.ID, id)
	}
}

func TestInsertOIDCExchangedToken_PreservesExplicitID_Mega5(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	tok := &OIDCExchangedToken{
		ID:        "row-explicit",
		AccountID: "acc-1",
		TokenHash: []byte("hash-b"),
		ExpiresAt: time.Now().Add(time.Hour),
	}
	id, err := m.InsertOIDCExchangedToken(t.Context(), tok)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if id != "row-explicit" {
		t.Errorf("id = %q, want row-explicit", id)
	}
}

func TestInsertOIDCExchangedToken_FillsCreatedAt_Mega5(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	tok := &OIDCExchangedToken{
		ID:        "row-c",
		AccountID: "acc-1",
		TokenHash: []byte("hash-c"),
		ExpiresAt: time.Now().Add(time.Hour),
		// CreatedAt deliberately zero → service must fill it.
	}
	before := time.Now()
	if _, err := m.InsertOIDCExchangedToken(t.Context(), tok); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if tok.CreatedAt.Before(before) {
		t.Errorf("CreatedAt = %v, want >= %v", tok.CreatedAt, before)
	}
}

// --- GetOIDCExchangedTokenByHash (80% → 100%) --------------------

func TestGetOIDCExchangedTokenByHash_ExpiredLazyDelete_Mega5(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	expired := &OIDCExchangedToken{
		ID:        "row-exp",
		AccountID: "acc-1",
		TokenHash: []byte("hash-exp"),
		ExpiresAt: time.Now().Add(-time.Minute),
	}
	if _, err := m.InsertOIDCExchangedToken(t.Context(), expired); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// First call: lazy-deletes, returns ErrNotFound.
	if _, err := m.GetOIDCExchangedTokenByHash(t.Context(), []byte("hash-exp")); !errors.Is(err, ErrNotFound) {
		t.Errorf("first call: err = %v, want ErrNotFound", err)
	}
	// Second call: row gone — confirms the lazy-delete branch ran.
	if _, err := m.GetOIDCExchangedTokenByHash(t.Context(), []byte("hash-exp")); !errors.Is(err, ErrNotFound) {
		t.Errorf("second call: err = %v, want ErrNotFound (row should be deleted)", err)
	}
}

func TestGetOIDCExchangedTokenByHash_NotFound_Mega5(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	if _, err := m.GetOIDCExchangedTokenByHash(t.Context(), []byte("never-inserted")); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
