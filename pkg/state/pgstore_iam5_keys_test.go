package state_test

// PgStore coverage gap tests for the IAM-5 (issue #189) key surface.
//
// Methods under test:
//
//   CreateAPIKeyWithExpiry  — new key with explicit expires_at (nullable).
//   CountAPIKeys            — quota lookup, status IN ('active','grace').
//   MarkAPIKeyRevoked       — soft-delete (status='revoked', revoked_at stamped).
//   RotateAPIKey            — atomic new + old-key demote + grace window.
//   GetAccountKeyGraceWindow — per-account override read.
//   SetAccountKeyGraceWindow — per-account override write.
//
// Pattern: mirrors pgstore_auth_lifecycle_test.go (helpers pgStore, createAccount,
// pgTestEmail). Each test exercises the load-bearing branches: happy path,
// ErrNotFound for unknown rows / cross-account, idempotency where the spec
// guarantees it. The auth gate (status='revoked' → ErrAPIKeyRevoked,
// expires_at < now() → ErrAPIKeyExpired) lives in AuthenticateKey and has its
// own coverage in pkg/auth/middleware_test.go; the store tests below prove
// the SQL projections and the rotation transaction wiring.

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// --- CreateAPIKeyWithExpiry -------------------------------------------------

func TestPg_CreateAPIKeyWithExpiry_NilExpiryPersistsNull(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	hash := []byte{0xCA, 0xFE, 0xBA, 0xBE}
	k, err := s.CreateAPIKeyWithExpiry(ctx, acctID, hash, "admin", []string{"admin"}, nil)
	if err != nil {
		t.Fatalf("CreateAPIKeyWithExpiry: %v", err)
	}
	if k.ExpiresAt != nil {
		t.Errorf("ExpiresAt = %v, want nil (admin keys never expire)", *k.ExpiresAt)
	}
	if string(k.Status) != "active" {
		t.Errorf("Status = %q, want %q", k.Status, "active")
	}
	if k.RotatedFromID != nil {
		t.Errorf("RotatedFromID = %v, want nil on fresh key", *k.RotatedFromID)
	}
}

func TestPg_CreateAPIKeyWithExpiry_FutureExpiryPersistsValue(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	hash := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	exp := time.Now().Add(24 * time.Hour).Truncate(time.Microsecond)
	k, err := s.CreateAPIKeyWithExpiry(ctx, acctID, hash, "scoped", []string{"apps:read"}, &exp)
	if err != nil {
		t.Fatalf("CreateAPIKeyWithExpiry: %v", err)
	}
	if k.ExpiresAt == nil {
		t.Fatalf("ExpiresAt = nil, want ~24h from now")
	}
	if !k.ExpiresAt.Equal(exp) {
		t.Errorf("ExpiresAt = %v, want %v", *k.ExpiresAt, exp)
	}
}

func TestPg_APIKeyLastUsedPreservesNullAndTimestamp(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	k, err := s.CreateAPIKeyWithExpiry(ctx, acctID, []byte{0xA1, 0xB2}, "never-used", []string{"apps:read"}, nil)
	if err != nil {
		t.Fatalf("CreateAPIKeyWithExpiry: %v", err)
	}
	if k.LastUsedAt != nil {
		t.Fatalf("fresh key LastUsedAt = %v, want nil", k.LastUsedAt)
	}
	listed, err := s.ListAPIKeys(ctx, acctID)
	if err != nil || len(listed) != 1 {
		t.Fatalf("ListAPIKeys = (%v, %v)", listed, err)
	}
	if listed[0].LastUsedAt != nil {
		t.Fatalf("listed fresh key LastUsedAt = %v, want nil", listed[0].LastUsedAt)
	}
	if err := s.TouchKeyLastUsed(ctx, k.ID); err != nil {
		t.Fatalf("TouchKeyLastUsed: %v", err)
	}
	used, err := s.GetAPIKey(ctx, acctID, k.ID)
	if err != nil {
		t.Fatalf("GetAPIKey: %v", err)
	}
	if used.LastUsedAt == nil || used.LastUsedAt.Before(time.Now().Add(-time.Minute)) {
		t.Fatalf("used key LastUsedAt = %v, want recent timestamp", used.LastUsedAt)
	}
}

// --- CountAPIKeys ------------------------------------------------------------

func TestPg_CountAPIKeys_ExcludesRevoked(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	// Seed three keys: two active, one revoked.
	for i, hash := range [][]byte{{0x01}, {0x02}, {0x03}} {
		_, err := s.CreateAPIKeyWithExpiry(ctx, acctID, hash, "k"+string(rune('a'+i)), []string{"apps:read"}, nil)
		if err != nil {
			t.Fatalf("CreateAPIKeyWithExpiry[%d]: %v", i, err)
		}
	}
	keys, err := s.ListAPIKeys(ctx, acctID)
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if _, err := s.MarkAPIKeyRevoked(ctx, acctID, keys[2].ID); err != nil {
		t.Fatalf("MarkAPIKeyRevoked: %v", err)
	}
	n, err := s.CountAPIKeys(ctx, acctID)
	if err != nil {
		t.Fatalf("CountAPIKeys: %v", err)
	}
	if n != 2 {
		t.Errorf("CountAPIKeys = %d, want 2 (revoked excluded)", n)
	}
}

func TestPg_CountAPIKeys_EmptyAccountIsZero(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	n, err := s.CountAPIKeys(ctx, acctID)
	if err != nil {
		t.Fatalf("CountAPIKeys: %v", err)
	}
	if n != 0 {
		t.Errorf("CountAPIKeys = %d, want 0 for empty account", n)
	}
}

// --- MarkAPIKeyRevoked -------------------------------------------------------

func TestPg_MarkAPIKeyRevoked_FlipsStatusAndStampsRevokedAt(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	k, err := s.CreateAPIKeyWithExpiry(ctx, acctID, []byte{0xAA}, "to-revoke", []string{"apps:read"}, nil)
	if err != nil {
		t.Fatalf("CreateAPIKeyWithExpiry: %v", err)
	}
	got, err := s.MarkAPIKeyRevoked(ctx, acctID, k.ID)
	if err != nil {
		t.Fatalf("MarkAPIKeyRevoked: %v", err)
	}
	if string(got.Status) != "revoked" {
		t.Errorf("Status = %q, want %q", got.Status, "revoked")
	}
	if got.RevokedAt == nil {
		t.Errorf("RevokedAt = nil, want stamped")
	}
}

func TestPg_MarkAPIKeyRevoked_IdempotentReturnsRow(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	k, err := s.CreateAPIKeyWithExpiry(ctx, acctID, []byte{0xBB}, "to-revoke", []string{"apps:read"}, nil)
	if err != nil {
		t.Fatalf("CreateAPIKeyWithExpiry: %v", err)
	}
	first, err := s.MarkAPIKeyRevoked(ctx, acctID, k.ID)
	if err != nil {
		t.Fatalf("first MarkAPIKeyRevoked: %v", err)
	}
	second, err := s.MarkAPIKeyRevoked(ctx, acctID, k.ID)
	if err != nil {
		t.Fatalf("second MarkAPIKeyRevoked: %v", err)
	}
	if string(second.Status) != "revoked" {
		t.Errorf("Status = %q, want revoked on retry", second.Status)
	}
	if first.RevokedAt == nil || second.RevokedAt == nil || !second.RevokedAt.Equal(*first.RevokedAt) {
		t.Errorf("RevokedAt drifted across retries: first=%v second=%v (coalesce must hold)", first.RevokedAt, second.RevokedAt)
	}
}

func TestPg_MarkAPIKeyRevoked_UnknownKeyReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	if _, err := s.MarkAPIKeyRevoked(ctx, acctID, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("MarkAPIKeyRevoked(unknown) = %v, want ErrNotFound", err)
	}
}

func TestPg_MarkAPIKeyRevoked_CrossAccountReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	acctA := createAccount(t, s, ctx, pgTestEmail(t)+"-a")
	acctB := createAccount(t, s, ctx, pgTestEmail(t)+"-b")
	k, err := s.CreateAPIKeyWithExpiry(ctx, acctA, []byte{0xCC}, "cross", []string{"apps:read"}, nil)
	if err != nil {
		t.Fatalf("CreateAPIKeyWithExpiry: %v", err)
	}
	if _, err := s.MarkAPIKeyRevoked(ctx, acctB, k.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("MarkAPIKeyRevoked(cross-account) = %v, want ErrNotFound", err)
	}
}

// --- RotateAPIKey ------------------------------------------------------------

func TestPg_RotateAPIKey_GraceSetsOldKeyToGrace(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	old, err := s.CreateAPIKeyWithExpiry(ctx, acctID, []byte{0x01}, "rotated", []string{"admin"}, nil)
	if err != nil {
		t.Fatalf("CreateAPIKeyWithExpiry: %v", err)
	}
	newHash := []byte{0x02}
	newKey, oldKey, err := s.RotateAPIKey(ctx, acctID, old.ID, newHash, "rotated", 7*24*time.Hour)
	if err != nil {
		t.Fatalf("RotateAPIKey: %v", err)
	}
	if string(newKey.Status) != "active" {
		t.Errorf("newKey.Status = %q, want active", newKey.Status)
	}
	if string(oldKey.Status) != "grace" {
		t.Errorf("oldKey.Status = %q, want grace", oldKey.Status)
	}
	if oldKey.RotatedFromID != nil {
		t.Errorf("old key rotated_from_id = %v, want nil (old is the predecessor)", *oldKey.RotatedFromID)
	}
	if newKey.RotatedFromID == nil || *newKey.RotatedFromID != old.ID {
		t.Errorf("newKey.RotatedFromID = %v, want %s", newKey.RotatedFromID, old.ID)
	}
	if oldKey.ExpiresAt == nil {
		t.Errorf("oldKey.ExpiresAt = nil, want grace deadline stamped")
	}
	if oldKey.RevokedAt != nil {
		t.Errorf("oldKey.RevokedAt = %v, want nil (grace, not revoked)", *oldKey.RevokedAt)
	}
}

func TestPg_RotateAPIKey_AtomicRotationFlipsOldKeyToRevoked(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	old, err := s.CreateAPIKeyWithExpiry(ctx, acctID, []byte{0x10}, "atomic", []string{"admin"}, nil)
	if err != nil {
		t.Fatalf("CreateAPIKeyWithExpiry: %v", err)
	}
	_, oldKey, err := s.RotateAPIKey(ctx, acctID, old.ID, []byte{0x11}, "atomic", 0)
	if err != nil {
		t.Fatalf("RotateAPIKey(0): %v", err)
	}
	if string(oldKey.Status) != "revoked" {
		t.Errorf("oldKey.Status = %q, want revoked (atomic rotation)", oldKey.Status)
	}
	if oldKey.RevokedAt == nil {
		t.Errorf("oldKey.RevokedAt = nil, want stamped on atomic rotation")
	}
}

func TestPg_RotateAPIKey_OnRevokedKeyReturnsErrAPIKeyRevoked(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	k, err := s.CreateAPIKeyWithExpiry(ctx, acctID, []byte{0x20}, "already-gone", []string{"admin"}, nil)
	if err != nil {
		t.Fatalf("CreateAPIKeyWithExpiry: %v", err)
	}
	if _, err := s.MarkAPIKeyRevoked(ctx, acctID, k.ID); err != nil {
		t.Fatalf("MarkAPIKeyRevoked: %v", err)
	}
	if _, _, err := s.RotateAPIKey(ctx, acctID, k.ID, []byte{0x21}, "again", 7*24*time.Hour); !errors.Is(err, state.ErrAPIKeyRevoked) {
		t.Errorf("RotateAPIKey(revoked) = %v, want ErrAPIKeyRevoked", err)
	}
}

func TestPg_RotateAPIKey_UnknownKeyReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	if _, _, err := s.RotateAPIKey(ctx, acctID, "00000000-0000-0000-0000-000000000000", []byte{0x30}, "x", 7*24*time.Hour); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("RotateAPIKey(unknown) = %v, want ErrNotFound", err)
	}
}

func TestPg_RotateAPIKey_CrossAccountReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	acctA := createAccount(t, s, ctx, pgTestEmail(t)+"-a")
	acctB := createAccount(t, s, ctx, pgTestEmail(t)+"-b")
	k, err := s.CreateAPIKeyWithExpiry(ctx, acctA, []byte{0x40}, "victim", []string{"admin"}, nil)
	if err != nil {
		t.Fatalf("CreateAPIKeyWithExpiry: %v", err)
	}
	if _, _, err := s.RotateAPIKey(ctx, acctB, k.ID, []byte{0x41}, "x", 7*24*time.Hour); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("RotateAPIKey(cross-account) = %v, want ErrNotFound", err)
	}
}

// --- GetAccountKeyGraceWindow / SetAccountKeyGraceWindow --------------------

func TestPg_GetAccountKeyGraceWindow_NilWhenUnset(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	got, err := s.GetAccountKeyGraceWindow(ctx, acctID)
	if err != nil {
		t.Fatalf("GetAccountKeyGraceWindow: %v", err)
	}
	if got != nil {
		t.Errorf("GetAccountKeyGraceWindow = %d, want nil for unset", *got)
	}
}

func TestPg_GetAccountKeyGraceWindow_UnknownAccountReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	if _, err := s.GetAccountKeyGraceWindow(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("GetAccountKeyGraceWindow(unknown) = %v, want ErrNotFound", err)
	}
}

func TestPg_SetAccountKeyGraceWindow_StoresValue(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	days := 14
	if err := s.SetAccountKeyGraceWindow(ctx, acctID, &days); err != nil {
		t.Fatalf("SetAccountKeyGraceWindow: %v", err)
	}
	got, err := s.GetAccountKeyGraceWindow(ctx, acctID)
	if err != nil {
		t.Fatalf("GetAccountKeyGraceWindow: %v", err)
	}
	if got == nil || *got != 14 {
		t.Errorf("GetAccountKeyGraceWindow = %v, want 14", got)
	}
}

func TestPg_SetAccountKeyGraceWindow_NilClearsOverride(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	days := 7
	if err := s.SetAccountKeyGraceWindow(ctx, acctID, &days); err != nil {
		t.Fatalf("SetAccountKeyGraceWindow: %v", err)
	}
	if err := s.SetAccountKeyGraceWindow(ctx, acctID, nil); err != nil {
		t.Fatalf("SetAccountKeyGraceWindow(nil): %v", err)
	}
	got, err := s.GetAccountKeyGraceWindow(ctx, acctID)
	if err != nil {
		t.Fatalf("GetAccountKeyGraceWindow: %v", err)
	}
	if got != nil {
		t.Errorf("GetAccountKeyGraceWindow after clear = %d, want nil", *got)
	}
}

func TestPg_SetAccountKeyGraceWindow_UnknownAccountIsNoop(t *testing.T) {
	s, ctx := pgStore(t)
	days := 7
	// Unknown account — UPDATE matches zero rows but the call still
	// returns nil. The handler relies on this so a wrong actor doesn't
	// surface a 500 to the customer; authn check is upstream.
	if err := s.SetAccountKeyGraceWindow(ctx, "00000000-0000-0000-0000-000000000000", &days); err != nil {
		t.Errorf("SetAccountKeyGraceWindow(unknown) = %v, want nil", err)
	}
}
