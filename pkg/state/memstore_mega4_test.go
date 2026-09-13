// memstore_mega4_test.go — Coverage Mega-PR #4 cluster 1:
// fill *MemStore method coverage on the load-bearing writers
// + read-back methods that the existing memstore_test.go leaves
// at low percentage.
//
// Targets (baseline 43.2% on pkg/state at branch time):
//   - Account: AccountByProviderCustomerID, ListAllAccounts,
//     UpdateAccountStatus, UpdateAccountPlan,
//     UpdateAccountProviderCustomerID, UpdateAccountStripeSubscriptionItem
//   - APIKey:  CreateAPIKey, ListAPIKeys, DeleteAPIKeyReturning,
//     MarkAPIKeyRevoked, TouchKeyLastUsed, CountAPIKeys, GetAPIKey
//   - MFA:     ReadMFASecret, SetMFASecret, MarkMFAEnrolled,
//     ClearMFA, SetMFARequired, ConsumeRecoveryCode, MatchRecoveryCode
//
// Whitebox `package state`. Same NewMemStore() construction used in
// the existing memstore_test.go (pkg/state/memstore.go:582).

package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// seedAccount inserts an Account with the given ID + plan into m.
// MemStore requires CreatedAt to be set on the struct (it's part of
// the wire shape pkg/state exposes for memstore↔pg parity tests).
func seedAccount(m *MemStore, id string, plan api.Plan) Account {
	a := Account{
		ID:        id,
		Email:     id + "@example.com",
		Plan:      plan,
		Status:    AccountActive,
		CreatedAt: time.Now().UTC(),
	}
	m.accounts[id] = a
	return a
}

func seedAPIKey_Mega4(m *MemStore, accountID, label string, scopes []string) APIKey {
	hash := sha256Bytes_Mega4([]byte(label + ":" + accountID))
	k := APIKey{
		ID:        newID(),
		AccountID: accountID,
		Hash:      hash,
		Label:     label,
		Scopes:    scopes,
		CreatedAt: time.Now(),
		Status:    string(APIKeyStatusActive),
	}
	m.keys[k.ID] = k
	m.keyByHash[hexEncode_Mega4(hash)] = k
	return k
}

func sha256Bytes_Mega4(in []byte) []byte {
	h := sha256.Sum256(in)
	return h[:]
}

// hexEncode_Mega4 keeps this file self-contained: memstore.go's hex
// path uses the stdlib hex package, but re-declaring the helper here
// avoids touching the memstore internals.
func hexEncode_Mega4(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hexdigits[c>>4]
		out[i*2+1] = hexdigits[c&0x0f]
	}
	return string(out)
}

// --- Account -----------------------------------------------------

func TestUpdateAccountPlan_Mega4(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	seedAccount(m, "acc-1", api.PlanFree)

	for _, p := range []api.Plan{api.PlanHobby, api.PlanPro, api.PlanScale, api.PlanFree} {
		if err := m.UpdateAccountPlan(context.Background(), "acc-1", p); err != nil {
			t.Fatalf("UpdateAccountPlan(%v): %v", p, err)
		}
		got, err := m.AccountByID(context.Background(), "acc-1")
		if err != nil {
			t.Fatalf("AccountByID: %v", err)
		}
		if got.Plan != p {
			t.Errorf("Plan = %v, want %v", got.Plan, p)
		}
	}

	if err := m.UpdateAccountPlan(context.Background(), "missing", api.PlanPro); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateAccountPlan(missing): err = %v, want ErrNotFound", err)
	}
}

func TestUpdateAccountStatus_Mega4(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	seedAccount(m, "acc-1", api.PlanPro)

	cases := []AccountStatus{
		AccountActive, AccountSuspended, AccountDeletedPending,
	}
	for _, s := range cases {
		if err := m.UpdateAccountStatus(context.Background(), "acc-1", s); err != nil {
			t.Fatalf("UpdateAccountStatus(%v): %v", s, err)
		}
		got, _ := m.AccountByID(context.Background(), "acc-1")
		if got.Status != s {
			t.Errorf("Status = %v, want %v", got.Status, s)
		}
	}

	if err := m.UpdateAccountStatus(context.Background(), "missing", AccountSuspended); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing account: got %v, want ErrNotFound", err)
	}
}

func TestUpdateAccountProviderCustomerID_Mega4(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	seedAccount(m, "acc-1", api.PlanPro)

	if err := m.UpdateAccountProviderCustomerID(context.Background(), "acc-1", "cus_abc"); err != nil {
		t.Fatalf("UpdateAccountProviderCustomerID: %v", err)
	}
	a, _ := m.AccountByID(context.Background(), "acc-1")
	if a.ProviderCustomerID != "cus_abc" {
		t.Errorf("ProviderCustomerID = %q, want cus_abc", a.ProviderCustomerID)
	}

	// AccountByProviderCustomerID round-trip.
	got, err := m.AccountByProviderCustomerID(context.Background(), "cus_abc")
	if err != nil {
		t.Fatalf("AccountByProviderCustomerID: %v", err)
	}
	if got.ID != "acc-1" {
		t.Errorf("AccountByProviderCustomerID.ID = %q, want acc-1", got.ID)
	}

	if _, err := m.AccountByProviderCustomerID(context.Background(), "cus_missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("AccountByProviderCustomerID(missing): err = %v, want ErrNotFound", err)
	}

	if err := m.UpdateAccountProviderCustomerID(context.Background(), "missing", "cus_x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateAccountProviderCustomerID(missing): err = %v, want ErrNotFound", err)
	}
}

func TestUpdateAccountStripeSubscriptionItem_Mega4(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	seedAccount(m, "acc-1", api.PlanPro)

	if err := m.UpdateAccountStripeSubscriptionItem(context.Background(), "acc-1", "si_xyz"); err != nil {
		t.Fatalf("UpdateAccountStripeSubscriptionItem: %v", err)
	}
	a, _ := m.AccountByID(context.Background(), "acc-1")
	if a.StripeSubscriptionItem != "si_xyz" {
		t.Errorf("StripeSubscriptionItem = %q, want si_xyz", a.StripeSubscriptionItem)
	}

	if err := m.UpdateAccountStripeSubscriptionItem(context.Background(), "missing", "si_x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: got %v, want ErrNotFound", err)
	}
}

func TestListAllAccounts_Mega4(t *testing.T) {
	t.Parallel()
	m := NewMemStore()

	// Empty: returns (nil/empty, nil), no panic.
	if got, err := m.ListAllAccounts(context.Background()); err != nil {
		t.Errorf("ListAllAccounts(empty): err = %v, want nil", err)
	} else if len(got) != 0 {
		t.Errorf("ListAllAccounts(empty): len = %d, want 0", len(got))
	}

	seedAccount(m, "acc-a", api.PlanFree)
	seedAccount(m, "acc-b", api.PlanPro)
	seedAccount(m, "acc-c", api.PlanScale)

	got, err := m.ListAllAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListAllAccounts: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("ListAllAccounts: len = %d, want 3", len(got))
	}
}

// --- APIKey ------------------------------------------------------

func TestCreateAndListAPIKey_Mega4(t *testing.T) {
	t.Parallel()
	m := NewMemStore()

	k, err := m.CreateAPIKey(context.Background(), "acc-1", sha256Bytes_Mega4([]byte("secret-1")), "label-1", []string{"read", "write"})
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if k.ID == "" {
		t.Error("CreateAPIKey: empty ID")
	}
	if k.AccountID != "acc-1" || k.Label != "label-1" {
		t.Errorf("CreateAPIKey: %+v", k)
	}

	keys, err := m.ListAPIKeys(context.Background(), "acc-1")
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if len(keys) != 1 || keys[0].ID != k.ID {
		t.Errorf("ListAPIKeys = %+v, want one matching key", keys)
	}

	// Cross-account isolation: other account sees nothing.
	if keys, err := m.ListAPIKeys(context.Background(), "acc-other"); err != nil || len(keys) != 0 {
		t.Errorf("ListAPIKeys(other) = (%v, %v), want (empty, nil)", keys, err)
	}
}

func TestGetAPIKey_Mega4(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	k := seedAPIKey_Mega4(m, "acc-1", "label", []string{"read"})

	// Happy path
	got, err := m.GetAPIKey(context.Background(), "acc-1", k.ID)
	if err != nil {
		t.Fatalf("GetAPIKey: %v", err)
	}
	if got.ID != k.ID {
		t.Errorf("GetAPIKey.ID = %q, want %q", got.ID, k.ID)
	}

	// Cross-account: IDOR-safe collapse to ErrNotFound.
	if _, err := m.GetAPIKey(context.Background(), "acc-other", k.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetAPIKey(cross-account): err = %v, want ErrNotFound", err)
	}

	// Missing.
	if _, err := m.GetAPIKey(context.Background(), "acc-1", "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetAPIKey(missing): err = %v, want ErrNotFound", err)
	}
}

func TestDeleteAPIKeyReturning_Mega4(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	k := seedAPIKey_Mega4(m, "acc-1", "label", []string{"read"})

	got, err := m.DeleteAPIKeyReturning(context.Background(), "acc-1", k.ID)
	if err != nil {
		t.Fatalf("DeleteAPIKeyReturning: %v", err)
	}
	if got.ID != k.ID {
		t.Errorf("DeleteAPIKeyReturning.ID = %q, want %q", got.ID, k.ID)
	}

	// After delete: ListAPIKeys returns empty; keyByHash also cleared.
	keys, _ := m.ListAPIKeys(context.Background(), "acc-1")
	if len(keys) != 0 {
		t.Errorf("after delete: ListAPIKeys len = %d, want 0", len(keys))
	}
	if _, ok := m.keyByHash[hexEncode_Mega4(k.Hash)]; ok {
		t.Error("after delete: keyByHash still has the entry")
	}

	// Re-delete returns ErrNotFound.
	if _, err := m.DeleteAPIKeyReturning(context.Background(), "acc-1", k.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("re-delete: err = %v, want ErrNotFound", err)
	}
}

func TestMarkAPIKeyRevoked_Mega4(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	k := seedAPIKey_Mega4(m, "acc-1", "label", []string{"read"})

	revoked, err := m.MarkAPIKeyRevoked(context.Background(), "acc-1", k.ID)
	if err != nil {
		t.Fatalf("MarkAPIKeyRevoked: %v", err)
	}
	if revoked.Status != string(APIKeyStatusRevoked) {
		t.Errorf("Status = %q, want %q", revoked.Status, APIKeyStatusRevoked)
	}
	if revoked.RevokedAt == nil {
		t.Error("RevokedAt: nil, want stamped")
	}

	// Idempotent re-revoke: same row, no double-stamp.
	revoked2, _ := m.MarkAPIKeyRevoked(context.Background(), "acc-1", k.ID)
	if !revoked2.RevokedAt.Equal(*revoked.RevokedAt) {
		t.Errorf("RevokedAt changed on re-revoke: %v → %v", revoked.RevokedAt, revoked2.RevokedAt)
	}

	// Cross-account IDOR.
	if _, err := m.MarkAPIKeyRevoked(context.Background(), "acc-other", k.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-account revoke: err = %v, want ErrNotFound", err)
	}
}

func TestTouchKeyLastUsed_Mega4(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	k := seedAPIKey_Mega4(m, "acc-1", "label", []string{"read"})

	before, _ := m.GetAPIKey(context.Background(), "acc-1", k.ID)
	time.Sleep(2 * time.Millisecond)

	if err := m.TouchKeyLastUsed(context.Background(), k.ID); err != nil {
		t.Fatalf("TouchKeyLastUsed: %v", err)
	}
	after, _ := m.GetAPIKey(context.Background(), "acc-1", k.ID)

	if before.LastUsedAt != nil {
		t.Fatalf("fresh key LastUsedAt = %v, want nil", before.LastUsedAt)
	}
	if after.LastUsedAt == nil {
		t.Fatal("touched key LastUsedAt = nil")
	}

	// Missing.
	if err := m.TouchKeyLastUsed(context.Background(), "missing-id"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: err = %v, want ErrNotFound", err)
	}
}

func TestCountAPIKeys_Mega4(t *testing.T) {
	t.Parallel()
	m := NewMemStore()

	seedAPIKey_Mega4(m, "acc-1", "a", []string{"read"})
	seedAPIKey_Mega4(m, "acc-1", "b", []string{"read"})
	seedAPIKey_Mega4(m, "acc-1", "c", []string{"read"})
	seedAPIKey_Mega4(m, "acc-2", "d", []string{"read"})

	n1, err := m.CountAPIKeys(context.Background(), "acc-1")
	if err != nil {
		t.Fatalf("CountAPIKeys: %v", err)
	}
	if n1 != 3 {
		t.Errorf("CountAPIKeys(acc-1) = %d, want 3", n1)
	}

	// Revoke one — count drops to 2.
	keys, _ := m.ListAPIKeys(context.Background(), "acc-1")
	_, _ = m.MarkAPIKeyRevoked(context.Background(), "acc-1", keys[0].ID)
	if n, _ := m.CountAPIKeys(context.Background(), "acc-1"); n != 2 {
		t.Errorf("after revoke: CountAPIKeys = %d, want 2", n)
	}

	// Other account still sees its own.
	if n, _ := m.CountAPIKeys(context.Background(), "acc-2"); n != 1 {
		t.Errorf("CountAPIKeys(acc-2) = %d, want 1", n)
	}
}

// --- MFA ---------------------------------------------------------

func TestSetAndReadMFASecret_Mega4(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	seedAccount(m, "acc-1", api.PlanPro)

	secret := []byte("encrypted-secret-bytes")
	hashes := [][]byte{sha256Bytes_Mega4([]byte("recovery-1")), sha256Bytes_Mega4([]byte("recovery-2"))}

	if err := m.SetMFASecret(context.Background(), "acc-1", secret, hashes); err != nil {
		t.Fatalf("SetMFASecret: %v", err)
	}

	got, err := m.ReadMFASecret(context.Background(), "acc-1")
	if err != nil {
		t.Fatalf("ReadMFASecret: %v", err)
	}
	if !bytes.Equal(got, secret) {
		t.Errorf("ReadMFASecret: %q, want %q", got, secret)
	}

	// Defensive copy: mutating returned slice must not mutate store.
	got[0] = 0xFF
	again, _ := m.ReadMFASecret(context.Background(), "acc-1")
	if again[0] == 0xFF {
		t.Error("ReadMFASecret: returned slice shares backing store (defensive copy missing)")
	}

	// SetMFASecret with no recovery codes is allowed.
	if err := m.SetMFASecret(context.Background(), "acc-1", []byte("s2"), nil); err != nil {
		t.Errorf("SetMFASecret(nil recovery): %v", err)
	}

	// Missing account.
	if err := m.SetMFASecret(context.Background(), "missing", secret, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetMFASecret(missing): err = %v, want ErrNotFound", err)
	}
}

func TestReadMFASecret_EmptyAndMissing_Mega4(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	seedAccount(m, "acc-1", api.PlanPro)

	// Never set: ErrNotFound (matches the pg behavior).
	if _, err := m.ReadMFASecret(context.Background(), "acc-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ReadMFASecret(never set): err = %v, want ErrNotFound", err)
	}

	// Missing account: ErrNotFound.
	if _, err := m.ReadMFASecret(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ReadMFASecret(missing acct): err = %v, want ErrNotFound", err)
	}
}

func TestMarkMFAEnrolled_AndClearMFA_Mega4(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	seedAccount(m, "acc-1", api.PlanPro)

	// Mark enrolled stamps mfa_enrolled_at + clears MFARequired.
	_ = m.SetMFASecret(context.Background(), "acc-1", []byte("s"), nil)
	if err := m.MarkMFAEnrolled(context.Background(), "acc-1"); err != nil {
		t.Fatalf("MarkMFAEnrolled: %v", err)
	}
	a, _ := m.AccountByID(context.Background(), "acc-1")
	if a.MFAEnrolledAt == nil {
		t.Error("after MarkMFAEnrolled: MFAEnrolledAt nil")
	}
	if a.MFARequired {
		t.Error("after MarkMFAEnrolled: MFARequired still true")
	}

	// ClearMFA nulls secret + recovery + enrolled_at; MFARequired untouched.
	_, _ = m.SetMFARequired(context.Background(), "acc-1", true)
	if err := m.ClearMFA(context.Background(), "acc-1"); err != nil {
		t.Fatalf("ClearMFA: %v", err)
	}
	a2, _ := m.AccountByID(context.Background(), "acc-1")
	if a2.MFASecretEncrypted != nil {
		t.Error("after ClearMFA: MFASecretEncrypted not nil")
	}
	if a2.MFARecoveryCodesHash != nil {
		t.Error("after ClearMFA: MFARecoveryCodesHash not nil")
	}
	if a2.MFAEnrolledAt != nil {
		t.Error("after ClearMFA: MFAEnrolledAt not nil")
	}
	if !a2.MFARequired {
		t.Error("after ClearMFA: MFARequired cleared (must NOT be touched)")
	}

	// Missing.
	if err := m.MarkMFAEnrolled(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("MarkMFAEnrolled(missing): err = %v, want ErrNotFound", err)
	}
	if err := m.ClearMFA(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ClearMFA(missing): err = %v, want ErrNotFound", err)
	}
}

func TestSetMFARequired_ChangedFlag_Mega4(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	seedAccount(m, "acc-1", api.PlanPro)

	// First call: false→true changes.
	c1, _ := m.SetMFARequired(context.Background(), "acc-1", true)
	if !c1 {
		t.Error("SetMFARequired(false→true): changed = false, want true")
	}

	// Second call (already true): no change.
	c2, _ := m.SetMFARequired(context.Background(), "acc-1", true)
	if c2 {
		t.Error("SetMFARequired(true→true): changed = true, want false (idempotent)")
	}

	// Flip back: true→false changes.
	c3, _ := m.SetMFARequired(context.Background(), "acc-1", false)
	if !c3 {
		t.Error("SetMFARequired(true→false): changed = false, want true")
	}

	if _, err := m.SetMFARequired(context.Background(), "missing", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetMFARequired(missing): err = %v, want ErrNotFound", err)
	}
}

func TestConsumeAndMatchRecoveryCode_Mega4(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	seedAccount(m, "acc-1", api.PlanPro)

	h1 := sha256Bytes_Mega4([]byte("recovery-1"))
	h2 := sha256Bytes_Mega4([]byte("recovery-2"))
	h3 := sha256Bytes_Mega4([]byte("recovery-3"))
	_ = m.SetMFASecret(context.Background(), "acc-1", []byte("s"), [][]byte{h1, h2, h3})

	// Match (read-only): h2 matches, lastCode=false (3 codes total).
	matched, last, err := m.MatchRecoveryCode(context.Background(), "acc-1", h2)
	if err != nil {
		t.Fatalf("MatchRecoveryCode: %v", err)
	}
	if !matched {
		t.Error("MatchRecoveryCode: matched = false, want true")
	}
	if last {
		t.Error("MatchRecoveryCode(h2 with 3 codes): last = true, want false")
	}

	// No match.
	matched2, _, _ := m.MatchRecoveryCode(context.Background(), "acc-1", sha256Bytes_Mega4([]byte("bogus")))
	if matched2 {
		t.Error("MatchRecoveryCode(bogus): matched = true, want false")
	}

	// Consume h2 → matched=true, lastCode=false, remaining=2.
	cMatched, cLast, cRem, err := m.ConsumeRecoveryCode(context.Background(), "acc-1", h2)
	if err != nil {
		t.Fatalf("ConsumeRecoveryCode: %v", err)
	}
	if !cMatched || cLast || cRem != 2 {
		t.Errorf("Consume h2: matched=%v last=%v rem=%d, want true/false/2", cMatched, cLast, cRem)
	}

	// Consume h1 → matched=true, lastCode=false, remaining=1.
	cMatched2, cLast2, cRem2, _ := m.ConsumeRecoveryCode(context.Background(), "acc-1", h1)
	if !cMatched2 || cLast2 || cRem2 != 1 {
		t.Errorf("Consume h1: matched=%v last=%v rem=%d, want true/false/1", cMatched2, cLast2, cRem2)
	}

	// Consume h3 → matched=true, lastCode=TRUE, remaining=0.
	cMatched3, cLast3, cRem3, _ := m.ConsumeRecoveryCode(context.Background(), "acc-1", h3)
	if !cMatched3 || !cLast3 || cRem3 != 0 {
		t.Errorf("Consume h3: matched=%v last=%v rem=%d, want true/true/0", cMatched3, cLast3, cRem3)
	}

	// Now no codes left: any consume returns matched=false.
	if matchedN, _, _, _ := m.ConsumeRecoveryCode(context.Background(), "acc-1", h1); matchedN {
		t.Error("Consume(h1 post-empty): matched = true, want false")
	}

	// Consume a bogus hash: matched=false, no mutation.
	before, _ := m.AccountByID(context.Background(), "acc-1")
	if matchedB, _, _, _ := m.ConsumeRecoveryCode(context.Background(), "acc-1", sha256Bytes_Mega4([]byte("nope"))); matchedB {
		t.Error("Consume(bogus): matched = true, want false")
	}
	after, _ := m.AccountByID(context.Background(), "acc-1")
	if len(before.MFARecoveryCodesHash) != len(after.MFARecoveryCodesHash) {
		t.Error("Consume(bogus) mutated the recovery-code slice")
	}

	// Missing account.
	if _, _, _, err := m.ConsumeRecoveryCode(context.Background(), "missing", h1); !errors.Is(err, ErrNotFound) {
		t.Errorf("Consume(missing): err = %v, want ErrNotFound", err)
	}
	if _, _, err := m.MatchRecoveryCode(context.Background(), "missing", h1); !errors.Is(err, ErrNotFound) {
		t.Errorf("Match(missing): err = %v, want ErrNotFound", err)
	}
}
