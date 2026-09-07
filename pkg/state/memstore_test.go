package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authcode"
)

// TestMain installs a deterministic recovery-HMAC secret so the
// authcode.NewRecoveryCodes + authcode.HashRecoveryCode calls in
// this suite have a key to compute HMAC-SHA256 against. The test
// path mirrors what pkg/authcode/recovery_test.go's TestMain
// does in the unit-test surface; the integration surface here
// uses a fixed 32-byte pattern because the recovery-code tests
// only need consistency, not a per-run random key. A real
// operational key is generated + persisted by
// cmd/apid/main.go::loadOrGenerateRecoveryHMACKey.
func TestMain(m *testing.M) {
	// 32 bytes of 0x33 — deterministic across runs; any consistent
	// non-zero pattern works.
	secret := bytes.Repeat([]byte{0x33}, 32)
	dup := bytes.Clone(secret)
	// Zero the input slice after the copy (SetHMACSecret mutates
	// the input on success — defence-in-depth with the contract).
	for i := range secret {
		secret[i] = 0
	}
	if err := authcode.SetHMACSecret(dup); err != nil {
		fmt.Fprintln(os.Stderr, "pkg/state TestMain: SetHMACSecret:", err)
		os.Exit(2)
	}
	os.Exit(m.Run())
}

// --- Account / Account.Active ------------------------------------------------

func TestAccountActive(t *testing.T) {
	cases := []struct {
		name   string
		status AccountStatus
		want   bool
	}{
		{"active is active", AccountActive, true},
		{"past_due is still active", AccountPastDue, true},
		{"suspended is not active", AccountSuspended, false},
		{"deleted_pending is not active", AccountDeletedPending, false},
		{"empty is not active", AccountStatus(""), false},
		{"bogus is not active", AccountStatus("zzz"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := Account{Status: tc.status}
			if got := a.Active(); got != tc.want {
				t.Errorf("Account{Status:%q}.Active() = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

// --- NewMemStore -------------------------------------------------------------

func TestNewMemStoreIsEmpty(t *testing.T) {
	m := NewMemStore()
	if m == nil {
		t.Fatal("NewMemStore returned nil")
	}
	ctx := context.Background()
	if _, err := m.AccountByEmail(ctx, "anyone@example.com"); !errors.Is(err, ErrNotFound) {
		t.Errorf("fresh store AccountByEmail: want ErrNotFound, got %v", err)
	}
	if _, err := m.AppBySlug(ctx, "anything"); !errors.Is(err, ErrNotFound) {
		t.Errorf("fresh store AppBySlug: want ErrNotFound, got %v", err)
	}
	if _, err := m.LatestDeployment(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("fresh store LatestDeployment: want ErrNotFound, got %v", err)
	}
	if _, _, err := m.GetIdempotent(ctx, "acc", "k"); !errors.Is(err, ErrNotFound) {
		t.Errorf("fresh store GetIdempotent: want ErrNotFound, got %v", err)
	}
}

// --- Accounts ----------------------------------------------------------------

func TestCreateAndLookupAccount(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	a, err := m.CreateAccount(ctx, "alice@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if a.ID == "" {
		t.Error("ID must be assigned")
	}
	if a.Email != "alice@example.com" {
		t.Errorf("Email = %q, want alice@example.com", a.Email)
	}
	if a.Plan != api.PlanHobby {
		t.Errorf("Plan = %q, want hobby", a.Plan)
	}
	if a.Status != AccountActive {
		t.Errorf("Status = %q, want active", a.Status)
	}
	if a.CreatedAt.IsZero() {
		t.Error("CreatedAt must be set")
	}

	got, err := m.AccountByEmail(ctx, "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	if got.ID != a.ID {
		t.Errorf("AccountByEmail.ID = %q, want %q", got.ID, a.ID)
	}
}

func TestCreateAccountDuplicateEmail(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	if _, err := m.CreateAccount(ctx, "dup@example.com", api.PlanFree); err != nil {
		t.Fatalf("first CreateAccount: %v", err)
	}
	_, err := m.CreateAccount(ctx, "dup@example.com", api.PlanPro)
	if err == nil {
		t.Fatal("duplicate email must error")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("exists")) {
		t.Errorf("error %q should mention 'exists'", err.Error())
	}
}

// TestMemStoreAccountsByIDs exercises the batch helper that
// closes the N+1 fan-out in the dashboard org-detail render
// (PR-9 §1). Mirrors the per-row AccountByID contract: missing
// IDs are absent from the returned map (NOT errors), and the
// empty-slice short-circuit returns an empty map without
// touching the map.
func TestMemStoreAccountsByIDs(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	// Empty input → empty map, no error.
	got, err := m.AccountsByIDs(ctx, nil)
	if err != nil {
		t.Fatalf("AccountsByIDs(nil) err = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("len(AccountsByIDs(nil)) = %d, want 0", len(got))
	}
	got, err = m.AccountsByIDs(ctx, []string{})
	if err != nil {
		t.Fatalf("AccountsByIDs([]) err = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("len(AccountsByIDs([])) = %d, want 0", len(got))
	}

	// Seed three accounts.
	a1, err := m.CreateAccount(ctx, "alice@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount alice: %v", err)
	}
	a2, err := m.CreateAccount(ctx, "bob@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount bob: %v", err)
	}
	a3, err := m.CreateAccount(ctx, "carol@example.com", api.PlanScale)
	if err != nil {
		t.Fatalf("CreateAccount carol: %v", err)
	}

	// Two present + one absent → map has 2 entries keyed by ID.
	missing := uuid.NewString()
	got, err = m.AccountsByIDs(ctx, []string{a1.ID, a2.ID, missing})
	if err != nil {
		t.Fatalf("AccountsByIDs: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len(got) = %d, want 2", len(got))
	}
	if g1, ok := got[a1.ID]; !ok || g1.Email != a1.Email {
		t.Errorf("got[a1] = %+v, ok=%v, want Email=%q", g1, ok, a1.Email)
	}
	if g2, ok := got[a2.ID]; !ok || g2.Email != a2.Email {
		t.Errorf("got[a2] = %+v, ok=%v, want Email=%q", g2, ok, a2.Email)
	}
	if _, ok := got[missing]; ok {
		t.Errorf("missing ID %q should not appear in map", missing)
	}
	if _, ok := got[a3.ID]; ok {
		t.Errorf("a3.ID should not appear (not requested)")
	}

	// Duplicate IDs in the request → only one entry per unique ID.
	got, err = m.AccountsByIDs(ctx, []string{a1.ID, a1.ID, a2.ID})
	if err != nil {
		t.Fatalf("AccountsByIDs dup: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len(got) = %d, want 2 (unique IDs)", len(got))
	}
}

func TestAccountByKeyHash(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, err := m.CreateAccount(ctx, "kb@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	hash := []byte("0123456789abcdef")
	if _, err := m.CreateAPIKey(ctx, acc.ID, hash, "laptop", []string{"admin"}); err != nil {
		t.Fatal(err)
	}

	got, err := m.AccountByKeyHash(ctx, hash)
	if err != nil {
		t.Fatalf("AccountByKeyHash: %v", err)
	}
	if got.ID != acc.ID {
		t.Errorf("AccountByKeyHash.ID = %q, want %q", got.ID, acc.ID)
	}

	if _, err := m.AccountByKeyHash(ctx, []byte("nope")); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown hash: want ErrNotFound, got %v", err)
	}
}

// --- API keys ----------------------------------------------------------------

func TestCreateAndDeleteAPIKey(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "k@example.com", api.PlanHobby)

	hash := []byte("deadbeef")
	k, err := m.CreateAPIKey(ctx, acc.ID, hash, "ci", []string{"admin"})
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if k.ID == "" || k.AccountID != acc.ID || !bytes.Equal(k.Hash, hash) || k.Label != "ci" {
		t.Errorf("key fields wrong: %+v", k)
	}
	if k.CreatedAt.IsZero() {
		t.Error("CreatedAt must be set")
	}

	if err := m.DeleteAPIKey(ctx, acc.ID, k.ID); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}
	if _, err := m.AccountByKeyHash(ctx, hash); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete, hash lookup should be ErrNotFound, got %v", err)
	}
}

func TestCreateAPIKeyDuplicateHash(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	a1, _ := m.CreateAccount(ctx, "a1@x.com", api.PlanFree)
	a2, _ := m.CreateAccount(ctx, "a2@x.com", api.PlanFree)

	hash := []byte("samehash")
	if _, err := m.CreateAPIKey(ctx, a1.ID, hash, "first", []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateAPIKey(ctx, a2.ID, hash, "second", []string{"admin"}); err == nil {
		t.Fatal("duplicate hash must error")
	}
}

func TestDeleteAPIKeyNotFoundAndCrossAccount(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	a1, _ := m.CreateAccount(ctx, "a1@x.com", api.PlanFree)
	a2, _ := m.CreateAccount(ctx, "a2@x.com", api.PlanFree)

	// missing key
	if err := m.DeleteAPIKey(ctx, a1.ID, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing key: want ErrNotFound, got %v", err)
	}

	// cross-account: key belongs to a1, a2 asks to delete
	k, _ := m.CreateAPIKey(ctx, a1.ID, []byte("h"), "lbl", []string{"admin"})
	if err := m.DeleteAPIKey(ctx, a2.ID, k.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-account delete: want ErrNotFound, got %v", err)
	}

	// owner can still delete after the failed cross-account attempt
	if err := m.DeleteAPIKey(ctx, a1.ID, k.ID); err != nil {
		t.Errorf("owner delete after cross-account attempt: %v", err)
	}
}

// --- IAM-5: API-key expiry + rotation (issue #189) -------------------------

// TestAuthenticateKey_RejectsRevoked pins that a key in
// status='revoked' cannot authenticate. The auth middleware
// surfaces this as a 401 with api_key_revoked; the store just
// returns the sentinel.
func TestAuthenticateKey_RejectsRevoked(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	a, _ := m.CreateAccount(ctx, "a@x.com", api.PlanFree)
	k, _ := m.CreateAPIKey(ctx, a.ID, []byte("hash1"), "lbl", []string{"admin"})

	// Sanity: not revoked yet.
	if _, _, err := m.AuthenticateKey(ctx, []byte("hash1")); err != nil {
		t.Fatalf("pre-revoke auth: %v", err)
	}

	// Mark revoked.
	if _, err := m.MarkAPIKeyRevoked(ctx, a.ID, k.ID); err != nil {
		t.Fatalf("MarkAPIKeyRevoked: %v", err)
	}
	_, _, err := m.AuthenticateKey(ctx, []byte("hash1"))
	if !errors.Is(err, ErrAPIKeyRevoked) {
		t.Errorf("post-revoke auth: want ErrAPIKeyRevoked, got %v", err)
	}
}

// TestAuthenticateKey_LazyExpirySetsRevoked pins the auth-time
// lazy-expiry gate. expires_at in the past causes the auth call
// to (a) flip status='revoked' + stamp revoked_at, (b) return
// ErrAPIKeyExpired. A second auth call after that returns
// ErrAPIKeyRevoked.
func TestAuthenticateKey_LazyExpirySetsRevoked(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	a, _ := m.CreateAccount(ctx, "a@x.com", api.PlanFree)
	expired := time.Now().Add(-1 * time.Hour)
	k, _ := m.CreateAPIKeyWithExpiry(ctx, a.ID, []byte("hash1"), "lbl", []string{"apps:read"}, &expired)
	if k.Status != "active" {
		t.Fatalf("pre-auth status: got %q, want active", k.Status)
	}
	_, _, err := m.AuthenticateKey(ctx, []byte("hash1"))
	if !errors.Is(err, ErrAPIKeyExpired) {
		t.Errorf("lazy expiry: want ErrAPIKeyExpired, got %v", err)
	}
	// Status now flipped to revoked.
	row, _ := m.APIKeyByHash(ctx, []byte("hash1"))
	if row.Status != "revoked" {
		t.Errorf("post-expiry status: got %q, want revoked", row.Status)
	}
	if row.RevokedAt == nil {
		t.Errorf("post-expiry revoked_at: got nil, want timestamp")
	}
	// Subsequent auth returns ErrAPIKeyRevoked (not ErrAPIKeyExpired again).
	_, _, err = m.AuthenticateKey(ctx, []byte("hash1"))
	if !errors.Is(err, ErrAPIKeyRevoked) {
		t.Errorf("post-expiry second auth: want ErrAPIKeyRevoked, got %v", err)
	}
}

// TestMarkAPIKeyRevoked_Idempotent pins the contract that
// repeated revoke calls are no-ops (not errors). Audit seam
// relies on this — the key.revoked audit is emitted in the
// handler, so the store must not surface a duplicate error.
func TestMarkAPIKeyRevoked_Idempotent(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	a, _ := m.CreateAccount(ctx, "a@x.com", api.PlanFree)
	k, _ := m.CreateAPIKey(ctx, a.ID, []byte("hash1"), "lbl", []string{"admin"})

	first, err := m.MarkAPIKeyRevoked(ctx, a.ID, k.ID)
	if err != nil {
		t.Fatalf("first revoke: %v", err)
	}
	if first.Status != "revoked" {
		t.Errorf("first status: got %q, want revoked", first.Status)
	}
	second, err := m.MarkAPIKeyRevoked(ctx, a.ID, k.ID)
	if err != nil {
		t.Fatalf("second revoke: %v", err)
	}
	if second.Status != "revoked" {
		t.Errorf("second status: got %q, want revoked", second.Status)
	}
	// revoked_at unchanged.
	if !first.RevokedAt.Equal(*second.RevokedAt) {
		t.Errorf("revoked_at changed: first=%v second=%v", first.RevokedAt, second.RevokedAt)
	}
}

// TestCountAPIKeys_RespectsStatusFilter pins that the per-account
// cap check excludes status='revoked' rows. A customer who has
// 3 active + 5 revoked has 3 keys, not 8 — so the IAM-5 cap
// check (Plan.KeysMax) lets them mint more.
func TestCountAPIKeys_RespectsStatusFilter(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	a, _ := m.CreateAccount(ctx, "a@x.com", api.PlanFree)
	// 3 active
	for i := 0; i < 3; i++ {
		_, err := m.CreateAPIKey(ctx, a.ID, []byte("h"+string(rune('a'+i))), "k", []string{"admin"})
		if err != nil {
			t.Fatalf("seed active: %v", err)
		}
	}
	// 5 revoked
	for i := 0; i < 5; i++ {
		k, err := m.CreateAPIKey(ctx, a.ID, []byte("r"+string(rune('a'+i))), "k", []string{"admin"})
		if err != nil {
			t.Fatalf("seed revoked: %v", err)
		}
		_, _ = m.MarkAPIKeyRevoked(ctx, a.ID, k.ID)
	}
	n, err := m.CountAPIKeys(ctx, a.ID)
	if err != nil {
		t.Fatalf("CountAPIKeys: %v", err)
	}
	if n != 3 {
		t.Errorf("CountAPIKeys: got %d, want 3 (revoked rows excluded)", n)
	}
}

// TestRotateAPIKey_GraceStatus pins the rotation primitive's
// grace branch. A 7-day grace window sets the old key's
// status='grace' and overwrites expires_at to now+7d. The new
// key inherits the old key's label + scopes and has
// status='active' + rotated_from_id set.
func TestRotateAPIKey_GraceStatus(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	a, _ := m.CreateAccount(ctx, "a@x.com", api.PlanHobby)
	oldKey, _ := m.CreateAPIKey(ctx, a.ID, []byte("old"), "ci-deploy", []string{"apps:read", "deploy:write"})

	grace := 7 * 24 * time.Hour
	before := time.Now()
	newKey, oldKeyAfter, err := m.RotateAPIKey(ctx, a.ID, oldKey.ID, []byte("new"), "", grace)
	if err != nil {
		t.Fatalf("RotateAPIKey: %v", err)
	}
	// New key shape.
	if newKey.Status != "active" {
		t.Errorf("new status: got %q, want active", newKey.Status)
	}
	if newKey.RotatedFromID == nil || *newKey.RotatedFromID != oldKey.ID {
		t.Errorf("new rotated_from_id: got %v, want %s", newKey.RotatedFromID, oldKey.ID)
	}
	if newKey.Label != "ci-deploy" {
		t.Errorf("new label: got %q, want ci-deploy (inherited)", newKey.Label)
	}
	// Old key shape.
	if oldKeyAfter.Status != "grace" {
		t.Errorf("old status: got %q, want grace", oldKeyAfter.Status)
	}
	if oldKeyAfter.ExpiresAt == nil {
		t.Fatal("old expires_at: got nil, want grace deadline")
	}
	wantDeadline := before.Add(grace)
	if oldKeyAfter.ExpiresAt.Before(wantDeadline.Add(-1*time.Second)) ||
		oldKeyAfter.ExpiresAt.After(wantDeadline.Add(1*time.Second)) {
		t.Errorf("old expires_at: got %v, want ~%v", oldKeyAfter.ExpiresAt, wantDeadline)
	}
}

// TestRotateAPIKey_Atomic pins the rotation primitive's atomic
// branch. graceWindow=0 flips status='revoked' + sets
// expires_at=now + revoked_at=now.
func TestRotateAPIKey_Atomic(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	a, _ := m.CreateAccount(ctx, "a@x.com", api.PlanHobby)
	oldKey, _ := m.CreateAPIKey(ctx, a.ID, []byte("old"), "ci", []string{"admin"})

	before := time.Now()
	_, oldKeyAfter, err := m.RotateAPIKey(ctx, a.ID, oldKey.ID, []byte("new"), "", 0)
	if err != nil {
		t.Fatalf("RotateAPIKey: %v", err)
	}
	if oldKeyAfter.Status != "revoked" {
		t.Errorf("old status: got %q, want revoked", oldKeyAfter.Status)
	}
	if oldKeyAfter.RevokedAt == nil {
		t.Fatal("old revoked_at: got nil, want now")
	}
	if oldKeyAfter.RevokedAt.Before(before) {
		t.Errorf("old revoked_at: got %v, want >= %v", oldKeyAfter.RevokedAt, before)
	}
}

// TestRotateAPIKey_OldKeyExpiresAtOverwritten pins the issue's
// explicit contract: rotation OVERWRITES the old key's
// expires_at to the grace deadline, regardless of any prior
// value. A pre-rotated key with expires_at=now+1y gets
// rewritten to now+7d.
func TestRotateAPIKey_OldKeyExpiresAtOverwritten(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	a, _ := m.CreateAccount(ctx, "a@x.com", api.PlanHobby)
	originalExp := time.Now().Add(365 * 24 * time.Hour)
	oldKey, _ := m.CreateAPIKeyWithExpiry(ctx, a.ID, []byte("old"), "ci", []string{"admin"}, &originalExp)
	if oldKey.ExpiresAt == nil {
		t.Fatal("setup: old key has no expires_at")
	}

	grace := 7 * 24 * time.Hour
	_, oldKeyAfter, err := m.RotateAPIKey(ctx, a.ID, oldKey.ID, []byte("new"), "", grace)
	if err != nil {
		t.Fatalf("RotateAPIKey: %v", err)
	}
	if oldKeyAfter.ExpiresAt == nil {
		t.Fatal("post-rotation expires_at: got nil")
	}
	// Original was ~365d out; rotation must have rewritten to ~7d.
	delta := time.Until(*oldKeyAfter.ExpiresAt)
	if delta > 8*24*time.Hour || delta < 6*24*time.Hour {
		t.Errorf("post-rotation expires_at delta: got %v, want ~7d", delta)
	}
}

// TestRotateAPIKey_RejectsAlreadyRevoked pins the early-return
// when the old key is already revoked — the handler surfaces
// 404 "key already revoked" so a customer cannot rotate a dead
// key (the rotation primitive would have no successor to
// demote).
func TestRotateAPIKey_RejectsAlreadyRevoked(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	a, _ := m.CreateAccount(ctx, "a@x.com", api.PlanHobby)
	oldKey, _ := m.CreateAPIKey(ctx, a.ID, []byte("old"), "ci", []string{"admin"})
	_, _ = m.MarkAPIKeyRevoked(ctx, a.ID, oldKey.ID)

	_, _, err := m.RotateAPIKey(ctx, a.ID, oldKey.ID, []byte("new"), "", 7*24*time.Hour)
	if !errors.Is(err, ErrAPIKeyRevoked) {
		t.Errorf("rotate revoked key: want ErrAPIKeyRevoked, got %v", err)
	}
}

// TestSetAccountKeyGraceWindow pins the per-account override
// round-trip. Nil clears the column, a positive value stamps
// it, a negative value is rejected by the SQL CHECK.
func TestSetAccountKeyGraceWindow(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	a, _ := m.CreateAccount(ctx, "a@x.com", api.PlanHobby)

	// nil → no override.
	if err := m.SetAccountKeyGraceWindow(ctx, a.ID, nil); err != nil {
		t.Fatalf("set nil: %v", err)
	}
	got, err := m.GetAccountKeyGraceWindow(ctx, a.ID)
	if err != nil {
		t.Fatalf("get after nil: %v", err)
	}
	if got != nil {
		t.Errorf("get after nil: got %v, want nil", got)
	}

	// 14 → override.
	d14 := 14
	if err := m.SetAccountKeyGraceWindow(ctx, a.ID, &d14); err != nil {
		t.Fatalf("set 14: %v", err)
	}
	got, err = m.GetAccountKeyGraceWindow(ctx, a.ID)
	if err != nil {
		t.Fatalf("get after 14: %v", err)
	}
	if got == nil || *got != 14 {
		t.Errorf("get after 14: got %v, want 14", got)
	}

	// nil again → cleared.
	if err := m.SetAccountKeyGraceWindow(ctx, a.ID, nil); err != nil {
		t.Fatalf("set nil again: %v", err)
	}
	got, _ = m.GetAccountKeyGraceWindow(ctx, a.ID)
	if got != nil {
		t.Errorf("get after second nil: got %v, want nil", got)
	}
}

// --- Apps --------------------------------------------------------------------

func TestCreateAndLookupApp(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "appowner@x.com", api.PlanHobby)

	app, err := m.CreateApp(ctx, App{
		AccountID: acc.ID, Slug: "my-app", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
		Status: AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if app.ID == "" {
		t.Error("ID must be assigned")
	}
	if app.Status != AppActive {
		t.Errorf("Status = %q, want active", app.Status)
	}
	if app.CreatedAt.IsZero() {
		t.Error("CreatedAt must be set")
	}

	got, err := m.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if got.Slug != "my-app" {
		t.Errorf("AppByID.Slug = %q, want my-app", got.Slug)
	}

	got, err = m.AppBySlug(ctx, "my-app")
	if err != nil {
		t.Fatalf("AppBySlug: %v", err)
	}
	if got.ID != app.ID {
		t.Errorf("AppBySlug.ID = %q, want %q", got.ID, app.ID)
	}
}

func TestCreateAppDuplicateSlug(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "dup@x.com", api.PlanFree)

	if _, err := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "same"}); err != nil {
		t.Fatal(err)
	}
	_, err := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "same"})
	if err == nil {
		t.Fatal("duplicate slug must error")
	}
}

func TestCreateAppPreservesCallerIDAndCreatedAt(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "p@x.com", api.PlanFree)
	set := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	app, err := m.CreateApp(ctx, App{
		AccountID: acc.ID, Slug: "carryover",
		ID: "client-supplied-id", CreatedAt: set,
	})
	if err != nil {
		t.Fatal(err)
	}
	if app.ID != "client-supplied-id" {
		t.Errorf("ID = %q, want client-supplied-id", app.ID)
	}
	if !app.CreatedAt.Equal(set) {
		t.Errorf("CreatedAt = %v, want %v", app.CreatedAt, set)
	}
}

func TestAppBySlugAndListIgnoreDeleted(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "del@x.com", api.PlanHobby)

	a1, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "alive"})
	a2, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "doomed"})

	if err := m.DeleteApp(ctx, a2.ID); err != nil {
		t.Fatalf("DeleteApp: %v", err)
	}

	// AppBySlug must not see deleted apps.
	if _, err := m.AppBySlug(ctx, "doomed"); !errors.Is(err, ErrNotFound) {
		t.Errorf("AppBySlug(deleted): want ErrNotFound, got %v", err)
	}

	// ListApps must not include deleted apps.
	list, err := m.ListApps(ctx, acc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != a1.ID {
		t.Errorf("ListApps = %+v, want only the alive app", list)
	}

	// Recreating the same slug after deletion must succeed (soft delete frees the slug).
	if _, err := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "doomed"}); err != nil {
		t.Errorf("re-create after delete: %v", err)
	}

	// Different account — verify isolation.
	other, _ := m.CreateAccount(ctx, "other@x.com", api.PlanFree)
	otherList, err := m.ListApps(ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(otherList) != 0 {
		t.Errorf("other account ListApps = %+v, want []", otherList)
	}
}

func TestAppByIDNotFound(t *testing.T) {
	m := NewMemStore()
	if _, err := m.AppByID(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestDeleteAppNotFound(t *testing.T) {
	m := NewMemStore()
	if err := m.DeleteApp(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

// --- Quota: CountDeployedApps ------------------------------------------------

func TestCountDeployedApps(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "q@x.com", api.PlanPro)

	// 0 deployed apps initially.
	if n, err := m.CountDeployedApps(ctx, acc.ID); err != nil || n != 0 {
		t.Fatalf("initial CountDeployedApps = (%d, %v), want (0, nil)", n, err)
	}

	a1, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "a1", Status: AppActive})
	a2, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "a2", Status: AppActive})
	a3, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "a3", Status: AppActive})

	if n, _ := m.CountDeployedApps(ctx, acc.ID); n != 3 {
		t.Errorf("three active apps: CountDeployedApps = %d, want 3", n)
	}

	// evicted_cold also occupies a slot (spec §4.2).
	a2.Status = AppEvictedCold
	m.apps[a2.ID] = a2
	if n, _ := m.CountDeployedApps(ctx, acc.ID); n != 3 {
		t.Errorf("with evicted_cold: CountDeployedApps = %d, want 3", n)
	}

	// deleted does NOT occupy a slot.
	_ = m.DeleteApp(ctx, a3.ID)
	if n, _ := m.CountDeployedApps(ctx, acc.ID); n != 2 {
		t.Errorf("after delete: CountDeployedApps = %d, want 2", n)
	}

	// Other account — isolation.
	other, _ := m.CreateAccount(ctx, "o@x.com", api.PlanFree)
	if n, _ := m.CountDeployedApps(ctx, other.ID); n != 0 {
		t.Errorf("other account: CountDeployedApps = %d, want 0", n)
	}

	_ = a1
}

// TestCountAppsWithEvictionPriority (issue #475) pins the per-account
// reserved-tier cap reader. The Hobby+ plans gate the reader: Hobby
// caps reserved at 1, Pro at 2, Scale at 4. The reader counts APPS
// (not instances) and excludes soft-deleted apps so a recently
// deleted reserved app doesn't leak into the cap and reject a
// subsequent recreate. Mirrors TestCountDeployedApps above.
func TestCountAppsWithEvictionPriority(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "cap-pro@x.com", api.PlanPro)
	reserved := string(api.EvictionPriorityReserved)

	// 0 reserved initially — fresh accounts always read 0.
	if n, err := m.CountAppsWithEvictionPriority(ctx, acc.ID, reserved); err != nil || n != 0 {
		t.Fatalf("initial CountAppsWithEvictionPriority = (%d, %v), want (0, nil)", n, err)
	}

	// Seed 3 apps; flip 2 to reserved via UpdateApp. The reader
	// must return 2 — Pro caps reserved at 2 (the apid handler is
	// the load-bearing gate, but the reader is what the gate
	// consults).
	a1, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "a1", Status: AppActive})
	a2, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "a2", Status: AppActive})
	_, _ = m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "a3", Status: AppActive})

	if _, err := m.UpdateApp(ctx, a1.ID, UpdateAppParams{EvictionPriority: &reserved, SetEvictionPriority: true}); err != nil {
		t.Fatalf("update a1: %v", err)
	}
	if _, err := m.UpdateApp(ctx, a2.ID, UpdateAppParams{EvictionPriority: &reserved, SetEvictionPriority: true}); err != nil {
		t.Fatalf("update a2: %v", err)
	}

	if n, _ := m.CountAppsWithEvictionPriority(ctx, acc.ID, reserved); n != 2 {
		t.Errorf("two apps at reserved: CountAppsWithEvictionPriority = %d, want 2 (Pro cap)", n)
	}

	// best_effort apps don't count toward the reserved cap; the
	// fresh a3 (and any pre-#475 row) snap-to-default lands as
	// best_effort via CreateApp. The reader lets operators query
	// either tier independently.
	if n, _ := m.CountAppsWithEvictionPriority(ctx, acc.ID, string(api.EvictionPriorityBestEffort)); n != 1 {
		t.Errorf("best_effort reader = %d, want 1 (only a3 is best_effort)", n)
	}

	// Soft-delete a reserved app — the cap reader must drop the
	// count to 1 so a future recreate isn't rejected by a stale
	// count.
	a2.Status = AppDeleted
	m.apps[a2.ID] = a2
	if n, _ := m.CountAppsWithEvictionPriority(ctx, acc.ID, reserved); n != 1 {
		t.Errorf("after soft-delete: CountAppsWithEvictionPriority = %d, want 1 (deleted apps excluded)", n)
	}

	// Other account — isolation. A Scale account with 4 reserved
	// apps must not leak into the Pro account's reader.
	scale, _ := m.CreateAccount(ctx, "cap-scale@x.com", api.PlanScale)
	for i := 0; i < 4; i++ {
		slug := fmt.Sprintf("scale-app-%d", i)
		created, err := m.CreateApp(ctx, App{AccountID: scale.ID, Slug: slug, Status: AppActive})
		if err != nil {
			t.Fatalf("seed scale app %d: %v", i, err)
		}
		if _, err := m.UpdateApp(ctx, created.ID, UpdateAppParams{EvictionPriority: &reserved, SetEvictionPriority: true}); err != nil {
			t.Fatalf("flip scale app %d: %v", i, err)
		}
	}
	if n, _ := m.CountAppsWithEvictionPriority(ctx, scale.ID, reserved); n != 4 {
		t.Errorf("Scale at cap: CountAppsWithEvictionPriority = %d, want 4", n)
	}
	if n, _ := m.CountAppsWithEvictionPriority(ctx, acc.ID, reserved); n != 1 {
		t.Errorf("Pro account cross-leak: CountAppsWithEvictionPriority = %d, want 1 (only a1 reserved)", n)
	}
}

// --- Deployments -------------------------------------------------------------

func TestCreateAndLatestDeployment(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "d@x.com", api.PlanHobby)
	app, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "dep-app"})

	// Unknown app must fail.
	if _, err := m.CreateDeployment(ctx, Deployment{AppID: "no-such-app"}); err == nil {
		t.Error("CreateDeployment for unknown app must error")
	}

	d1, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:1"})
	if err != nil {
		t.Fatalf("CreateDeployment d1: %v", err)
	}
	if d1.ID == "" || d1.CreatedAt.IsZero() {
		t.Errorf("d1 fields not initialized: %+v", d1)
	}
	// Force a later CreatedAt on d2 so Latest is unambiguous.
	d2, err := m.CreateDeployment(ctx, Deployment{
		AppID: app.ID, ImageDigest: "sha256:2",
		CreatedAt: time.Now().Add(time.Second),
	})
	if err != nil {
		t.Fatalf("CreateDeployment d2: %v", err)
	}

	latest, err := m.LatestDeployment(ctx, app.ID)
	if err != nil {
		t.Fatalf("LatestDeployment: %v", err)
	}
	if latest.ID != d2.ID {
		t.Errorf("LatestDeployment.ID = %q, want %q", latest.ID, d2.ID)
	}
}

func TestCreateDeploymentPreservesCallerFields(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "p2@x.com", api.PlanFree)
	app, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "preserve"})
	set := time.Date(2025, 6, 7, 8, 9, 10, 0, time.UTC)

	d, err := m.CreateDeployment(ctx, Deployment{
		AppID: app.ID, ImageDigest: "sha256:x",
		ID: "client-id", CreatedAt: set,
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != "client-id" {
		t.Errorf("ID = %q, want client-id", d.ID)
	}
	if !d.CreatedAt.Equal(set) {
		t.Errorf("CreatedAt = %v, want %v", d.CreatedAt, set)
	}
}

// TestMemStore_SetDeploymentParked_Idempotent (issue #554 /
// ADR-079 / AC #3) is the memstore mirror of
// TestPg_SetDeploymentParked_Idempotent. The memstore mutex hides
// the schedd-restart race that motivates the idempotency
// contract, but the explicit "second call leaves parked_at
// unchanged" assertion is the load-bearing contract — a
// regression would surface as the apid
// GET /v1/apps/{slug}.parked_deployment timestamp drifting on
// every schedd restart.
func TestMemStore_SetDeploymentParked_Idempotent(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "park-mem@x.com", api.PlanHobby)
	app, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "park-mem"})
	dep, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:p"})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	first := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	if err := m.SetDeploymentParked(ctx, dep.ID, "liveness_exhausted", first); err != nil {
		t.Fatalf("SetDeploymentParked (first): %v", err)
	}

	// Second call 1h later with a different reason — reason stays
	// pinned to the first park (the closed-set audit reason is set
	// once and never repainted), parked_at must NOT drift.
	if err := m.SetDeploymentParked(ctx, dep.ID, "lifecycle_park", first.Add(time.Hour)); err != nil {
		t.Fatalf("SetDeploymentParked (second): %v", err)
	}
	got, err := m.LatestParkedDeploymentForApp(ctx, app.ID)
	if err != nil {
		t.Fatalf("LatestParkedDeploymentForApp: %v", err)
	}
	if got.ParkedReason != "liveness_exhausted" {
		t.Errorf("parked_reason = %q, want %q (second park leaked reason)", got.ParkedReason, "liveness_exhausted")
	}
	if got.ParkedAt == nil || !got.ParkedAt.Equal(first) {
		t.Errorf("parked_at = %v, want %v (second park leaked timestamp)", got.ParkedAt, first)
	}
}

// TestMemStore_LatestParkedDeploymentForApp_NoParkReturnsErrNotFound
// is the load-bearing "app is healthy" branch on the apid
// surface. The handler maps ErrNotFound → nil ParkedDeploymentRef
// (no field on the wire).

// TestMemStore_SetDeploymentFailedEx_PersistsExplanationFields locks
// the MemStore parity surface for the error-explanations cluster
// (spec §6.4 amendment 1, migrations/00290). The PgStore equivalent
// runs against a live schema; this test locks the in-process store
// so the unit-test suite that exercises MemStore stays aligned with
// the persistence shape. The contract being locked:
//
//   - status is pinned to 'failed' regardless of prior status
//   - error_code carries the RFC 7807 code
//   - error_hint / error_why / error_fix carry the customer-facing prose
//   - error_relevant_logs carries the per-line log excerpts
//
// A regression here means the wire-side DTO would diverge from the
// in-process store — pkg/api.DeploymentResponse.ErrorHint is
// populated from d.ErrorHint at serialise time and the dashboard
// would render an empty block.
func TestMemStore_SetDeploymentFailedEx_PersistsExplanationFields(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "expl-mem@x.com", api.PlanHobby)
	app, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "expl-mem"})
	dep, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:e"})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	logs := []api.LogExcerpt{
		{Timestamp: "2026-08-18T19:00:00Z", Level: "error", Source: "vm-init", Message: "dial :8080: connection refused"},
		{Timestamp: "2026-08-18T19:00:01Z", Level: "error", Source: "app", Message: "panic: listen tcp 127.0.0.1:8080: bind: address already in use"},
	}
	got, err := m.SetDeploymentFailedEx(ctx, dep.ID,
		api.CodeAppNotListening,
		"wake readiness probe failed",
		"your app isn't accepting traffic on the port we expect",
		"the readiness probe dialed :8080 and got no listener",
		"• bind to 0.0.0.0\n• check `app.listen(process.env.PORT)`",
		logs,
	)
	if err != nil {
		t.Fatalf("SetDeploymentFailedEx: %v", err)
	}
	if got.Status != DeployFailed {
		t.Errorf("status = %s, want failed", got.Status)
	}
	if got.ErrorCode != api.CodeAppNotListening {
		t.Errorf("error_code = %q, want %q", got.ErrorCode, api.CodeAppNotListening)
	}
	if got.ErrorHint != "your app isn't accepting traffic on the port we expect" {
		t.Errorf("error_hint = %q, want the customer-facing hint", got.ErrorHint)
	}
	if got.ErrorWhy != "the readiness probe dialed :8080 and got no listener" {
		t.Errorf("error_why = %q, want the templated why", got.ErrorWhy)
	}
	if got.ErrorFix != "• bind to 0.0.0.0\n• check `app.listen(process.env.PORT)`" {
		t.Errorf("error_fix = %q, want the multi-line fix", got.ErrorFix)
	}
	if len(got.ErrorRelevantLogs) != 2 {
		t.Fatalf("error_relevant_logs len = %d, want 2", len(got.ErrorRelevantLogs))
	}
	if got.ErrorRelevantLogs[0].Source != "vm-init" {
		t.Errorf("error_relevant_logs[0].source = %q, want vm-init", got.ErrorRelevantLogs[0].Source)
	}
	if got.ErrorRelevantLogs[1].Message != "panic: listen tcp 127.0.0.1:8080: bind: address already in use" {
		t.Errorf("error_relevant_logs[1].message = %q, want the panic line", got.ErrorRelevantLogs[1].Message)
	}
}

// TestMemStore_SetDeploymentFailedEx_EmptyFieldsPersistAsEmpty locks
// the empty-input fallthrough path: a non-sentinel failure (no
// catalog row) leaves all four prose fields empty strings + nil
// logs slice. The wire DTO's omitempty tags suppress them so
// customers on a non-catalog failure see the legacy 3-line shape
// without an empty hint/why/fix block.
func TestMemStore_SetDeploymentFailedEx_EmptyFieldsPersistAsEmpty(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "expl-empty-mem@x.com", api.PlanHobby)
	app, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "expl-empty-mem"})
	dep, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:em"})

	got, err := m.SetDeploymentFailedEx(ctx, dep.ID, api.CodeAppStartupTimeout, "no boot", "", "", "", nil)
	if err != nil {
		t.Fatalf("SetDeploymentFailedEx: %v", err)
	}
	if got.ErrorHint != "" || got.ErrorWhy != "" || got.ErrorFix != "" {
		t.Errorf("prose fields should be empty: hint=%q why=%q fix=%q", got.ErrorHint, got.ErrorWhy, got.ErrorFix)
	}
	if got.ErrorRelevantLogs != nil {
		t.Errorf("error_relevant_logs should be nil, got %v", got.ErrorRelevantLogs)
	}
}

// TestMemStore_SetDeploymentFailedEx_UnknownReturnsErrNotFound locks
// the not-found branch that callers depend on for the post-deploy
// failure path (a race where the deployment was deleted between
// CreateDeployment and SetDeploymentFailedEx must surface as
// ErrNotFound rather than panic on a nil map entry).
func TestMemStore_SetDeploymentFailedEx_UnknownReturnsErrNotFound(t *testing.T) {
	m := NewMemStore()
	_, err := m.SetDeploymentFailedEx(context.Background(), "missing-id", api.CodeAppStartupTimeout, "msg", "hint", "why", "fix", nil)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("SetDeploymentFailedEx on missing id: got err=%v, want ErrNotFound", err)
	}
}
func TestMemStore_LatestParkedDeploymentForApp_NoParkReturnsErrNotFound(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "nopark-mem@x.com", api.PlanHobby)
	app, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "nopark-mem"})
	_, err := m.LatestParkedDeploymentForApp(ctx, app.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("no park err = %v, want ErrNotFound", err)
	}
}

// TestMemStore_LatestParkedDeploymentForApp_SupersededKeepsParking
// mirrors the pgstore-side TestPg_LatestParkedDeploymentForApp_SupersededKeepsParking.
// A parked + superseded deployment row stays parked (parked_reason
// / parked_at are NOT cleared on supersede) so the apid surface
// surfaces "this deployment was parked" even after a customer
// redeploys. See pkg/state/pgstore.go::LatestParkedDeploymentForApp
// docstring for the rationale.
func TestMemStore_LatestParkedDeploymentForApp_SupersededKeepsParking(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "supersede-mem@x.com", api.PlanHobby)
	app, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "supersede-mem"})

	// First deployment: live, then parked.
	depParked, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:parked", Status: DeployPending})
	if err != nil {
		t.Fatalf("CreateDeployment(parked): %v", err)
	}
	if err := m.MarkDeploymentLive(ctx, depParked.ID); err != nil {
		t.Fatalf("MarkDeploymentLive(parked): %v", err)
	}
	parkStamp := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	if err := m.SetDeploymentParked(ctx, depParked.ID, "liveness_exhausted", parkStamp); err != nil {
		t.Fatalf("SetDeploymentParked: %v", err)
	}

	// Second deployment: supersedes depParked, never parked.
	depNewer, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:newer", Status: DeployPending})
	if err != nil {
		t.Fatalf("CreateDeployment(newer): %v", err)
	}
	if err := m.MarkDeploymentLive(ctx, depNewer.ID); err != nil {
		t.Fatalf("MarkDeploymentLive(newer): %v", err)
	}

	got, err := m.LatestParkedDeploymentForApp(ctx, app.ID)
	if err != nil {
		t.Fatalf("LatestParkedDeploymentForApp: %v", err)
	}
	if got.ID != depParked.ID {
		t.Errorf("latest.ID = %s, want %s (parked + superseded, not the newer live)", got.ID, depParked.ID)
	}
	if got.ParkedReason != "liveness_exhausted" {
		t.Errorf("latest.ParkedReason = %q, want liveness_exhausted", got.ParkedReason)
	}
	if got.ParkedAt == nil || !got.ParkedAt.Equal(parkStamp) {
		t.Errorf("latest.ParkedAt = %v, want %v", got.ParkedAt, parkStamp)
	}
}

func TestLatestDeploymentNotFound(t *testing.T) {
	m := NewMemStore()
	if _, err := m.LatestDeployment(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

// --- Idempotency -------------------------------------------------------------

func TestIdempotencyPutGetRoundTrip(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	body := []byte(`{"ok":true}`)

	if err := m.PutIdempotent(ctx, "acc", "k", 201, body); err != nil {
		t.Fatalf("PutIdempotent: %v", err)
	}
	status, got, err := m.GetIdempotent(ctx, "acc", "k")
	if err != nil {
		t.Fatalf("GetIdempotent: %v", err)
	}
	if status != 201 {
		t.Errorf("status = %d, want 201", status)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("body = %q, want %q", got, body)
	}
}

func TestIdempotencyGetMisses(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	if _, _, err := m.GetIdempotent(ctx, "acc", "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing key: want ErrNotFound, got %v", err)
	}

	// Different account, same key — must be isolated.
	if err := m.PutIdempotent(ctx, "acc1", "k", 200, []byte("a")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.GetIdempotent(ctx, "acc2", "k"); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-account: want ErrNotFound, got %v", err)
	}
}

func TestIdempotencyPutDefensivelyCopiesBody(t *testing.T) {
	// Spec invariant: PutIdempotent must not alias the caller's slice.
	m := NewMemStore()
	ctx := context.Background()
	body := []byte("original")
	if err := m.PutIdempotent(ctx, "acc", "k", 200, body); err != nil {
		t.Fatal(err)
	}
	body[0] = 'X' // mutate after Put
	_, got, err := m.GetIdempotent(ctx, "acc", "k")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Errorf("stored body = %q, want %q (PutIdempotent must defensively copy)", got, "original")
	}
}

func TestIdempotencyOverwrite(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	if err := m.PutIdempotent(ctx, "acc", "k", 200, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := m.PutIdempotent(ctx, "acc", "k", 500, []byte("second")); err != nil {
		t.Fatal(err)
	}
	status, body, err := m.GetIdempotent(ctx, "acc", "k")
	if err != nil {
		t.Fatal(err)
	}
	if status != 500 || string(body) != "second" {
		t.Errorf("overwrite: got (%d, %q), want (500, %q)", status, body, "second")
	}
}

// TestDeploymentLogsAppendAndPage is the M7.5 slice 5 contract:
// every insert returns a monotonic seq, ListDeploymentLogs returns
// the rows DESC by seq, paging by `seq < before` works, and hasMore
// is true iff an older row sits behind the page.
func TestDeploymentLogsAppendAndPage(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	for i := 0; i < 200; i++ {
		seq, err := m.AppendDeploymentLog(ctx, "dep-1", "stdout", lineN(i))
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		if seq != int64(i+1) {
			t.Errorf("append %d seq = %d, want %d", i, seq, i+1)
		}
	}

	// First page: newest first.
	page, hasMore, err := m.ListDeploymentLogs(ctx, "dep-1", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 50 {
		t.Fatalf("first page size = %d, want 50", len(page))
	}
	if !hasMore {
		t.Errorf("first page hasMore = false, want true (200 rows > 50)")
	}
	if page[0].Seq != 200 || page[49].Seq != 151 {
		t.Errorf("page seq range = [%d, %d], want [200, 151]", page[0].Seq, page[49].Seq)
	}

	// Page 2: before the first row's seq boundary.
	page2, hasMore2, err := m.ListDeploymentLogs(ctx, "dep-1", page[49].Seq, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 50 || page2[0].Seq != 150 || page2[49].Seq != 101 {
		t.Errorf("page2 seq range = [%d, %d], want [150, 101]", page2[0].Seq, page2[49].Seq)
	}
	if !hasMore2 {
		t.Errorf("page2 hasMore = false, want true")
	}

	// Last page: rows 100..51 returned, hasMore=true (rows 50..1 remain).
	page3, hasMore3, err := m.ListDeploymentLogs(ctx, "dep-1", page2[49].Seq, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page3) != 50 || !hasMore3 {
		t.Errorf("page3 len=%d hasMore=%v, want 50/true", len(page3), hasMore3)
	}
	// Past the second-to-last page: rows 50..1.
	page4, hasMore4, err := m.ListDeploymentLogs(ctx, "dep-1", page3[49].Seq, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page4) != 50 || hasMore4 {
		t.Errorf("page4 len=%d hasMore=%v, want 50/false (no rows behind seq=1)", len(page4), hasMore4)
	}
	// Past the oldest row: empty.
	page5, hasMore5, err := m.ListDeploymentLogs(ctx, "dep-1", 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page5) != 0 || hasMore5 {
		t.Errorf("page5 len=%d hasMore=%v, want 0/false", len(page5), hasMore5)
	}
}

// TestDeploymentLogsUnknownDeployment covers the empty-row path —
// the SSE handler always opens with a page, even when nothing has
// been logged yet.
func TestDeploymentLogsUnknownDeployment(t *testing.T) {
	m := NewMemStore()
	page, hasMore, err := m.ListDeploymentLogs(context.Background(), "missing", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 0 || hasMore {
		t.Errorf("unknown dep page = (%d, hasMore=%v), want (0, false)", len(page), hasMore)
	}
}

// TestDeploymentLogsLimitClamp asserts the safe-by-default guard
// against caller-supplied limit values (CodeQL go/allocation-size).
// A hostile caller that forgets to clamp `limit` must not be able
// to trigger an oversized slice allocation.
func TestDeploymentLogsLimitClamp(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	dep := "dep-clamp"
	for i := 0; i < MaxDeploymentLogPage*2; i++ {
		if _, err := m.AppendDeploymentLog(ctx, dep, "stdout", lineN(i)); err != nil {
			t.Fatal(err)
		}
	}
	// Caller requests 1_000_000 rows → must clamp to MaxDeploymentLogPage.
	page, hasMore, err := m.ListDeploymentLogs(ctx, dep, 0, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != MaxDeploymentLogPage {
		t.Errorf("clamped page len = %d, want %d", len(page), MaxDeploymentLogPage)
	}
	if !hasMore {
		t.Errorf("hasMore = false; expected true (rows remain past the clamp)")
	}
}

func lineN(i int) string {
	return "line" + itoaSmall(i)
}

func itoaSmall(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [4]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}

// TestMemStore_GitHubBinding_RoundTrip exercises the binding
// persistence + reverse-lookup added for review finding #1+#2
// closure (migration 00007). Asserts:
//
//   - RecordGitHubBinding persists across GetGitHubBindingForApp
//   - InstallationIDForRepo (the new reverse lookup) returns the
//     right id for a bound repo
//   - ErrNotFound for an unbound repo (this is the §11 fail-closed
//     path: checks.go must NOT fall back to install_id=1 when no
//     app is bound)
func TestMemStore_GitHubBinding_RoundTrip(t *testing.T) {
	store := NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "alice@example.com", "free")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(context.Background(), App{AccountID: acct.ID, Slug: "api"})
	if err != nil {
		t.Fatal(err)
	}

	if err := store.RecordGitHubBinding(context.Background(), app.ID, 4242, "octo/api", "main"); err != nil {
		t.Fatalf("RecordGitHubBinding: %v", err)
	}

	b, err := store.GitHubBindingForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("GitHubBindingForApp: %v", err)
	}
	if b.InstallID != 4242 {
		t.Errorf("InstallID = %d, want 4242", b.InstallID)
	}
	if b.RepoFullName != "octo/api" {
		t.Errorf("RepoFullName = %q, want octo/api", b.RepoFullName)
	}

	id, err := store.InstallationIDForRepo(context.Background(), "octo/api")
	if err != nil {
		t.Fatalf("InstallationIDForRepo: %v", err)
	}
	if id != 4242 {
		t.Errorf("install id for octo/api = %d, want 4242", id)
	}

	// Unbound repo → ErrNotFound (NOT a hardcoded id=1).
	_, err = store.InstallationIDForRepo(context.Background(), "octo/unbound")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound for unbound repo", err)
	}

	// Empty repo → ErrNotFound (defensive).
	_, err = store.InstallationIDForRepo(context.Background(), "")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound for empty repo", err)
	}
}

// TestMemStore_GitHubBinding_RejectsConflict mirrors the migration's
// apps_github_install_repo_uniq partial index: two apps cannot claim
// the same (install_id, repo) pair. The §11 least-privilege audit
// invariant lives on this constraint.
func TestMemStore_GitHubBinding_RejectsConflict(t *testing.T) {
	store := NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "alice@example.com", "free")
	if err != nil {
		t.Fatal(err)
	}
	app1, err := store.CreateApp(context.Background(), App{AccountID: acct.ID, Slug: "api1"})
	if err != nil {
		t.Fatal(err)
	}
	app2, err := store.CreateApp(context.Background(), App{AccountID: acct.ID, Slug: "api2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordGitHubBinding(context.Background(), app1.ID, 1, "octo/api", "main"); err != nil {
		t.Fatalf("first binding: %v", err)
	}
	err = store.RecordGitHubBinding(context.Background(), app2.ID, 1, "octo/api", "main")
	if err == nil {
		t.Fatal("expected conflict error when second app tries to bind same (install_id, repo)")
	}
}

// --- Account passwords + OAuth links (issue #165 / ADR-032 PR #2) --------

// TestMemStore_AccountPassword_RoundTrip pins the upsert / read /
// delete cycle on the Argon2id PHC store. The postLogin handler
// depends on this exact shape:
//   - SetAccountPassword persists the PHC verbatim (the package
//     parses the embedded m/t/p at verify time).
//   - AccountPasswordByAccountID returns ErrNotFound for the
//     no-row case so the handler can branch into the
//     anti-enumeration Argon2id pad (pkg/auth.DummyPHC).
//   - DeleteAccountPassword is idempotent.
func TestMemStore_AccountPassword_RoundTrip(t *testing.T) {
	store := NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "alice@example.com", "free")
	if err != nil {
		t.Fatal(err)
	}

	// No row yet → ErrNotFound (the postLogin no-row branch).
	if _, err := store.AccountPasswordByAccountID(context.Background(), acct.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AccountPasswordByAccountID on missing row err = %v, want ErrNotFound", err)
	}

	// Set the hash.
	const phc = "$argon2id$v=19$m=65536,t=1,p=2$AAAA$BBBB"
	if err := store.SetAccountPassword(context.Background(), acct.ID, phc); err != nil {
		t.Fatalf("SetAccountPassword: %v", err)
	}

	// Read back.
	got, err := store.AccountPasswordByAccountID(context.Background(), acct.ID)
	if err != nil {
		t.Fatalf("AccountPasswordByAccountID: %v", err)
	}
	if got != phc {
		t.Errorf("AccountPasswordByAccountID = %q, want %q (round-trip broken)", got, phc)
	}

	// Upsert: SetAccountPassword overwrites in place.
	const phc2 = "$argon2id$v=19$m=65536,t=1,p=2$CCCC$DDDD"
	if err := store.SetAccountPassword(context.Background(), acct.ID, phc2); err != nil {
		t.Fatalf("SetAccountPassword (upsert): %v", err)
	}
	got, err = store.AccountPasswordByAccountID(context.Background(), acct.ID)
	if err != nil {
		t.Fatalf("AccountPasswordByAccountID (post-upsert): %v", err)
	}
	if got != phc2 {
		t.Errorf("AccountPasswordByAccountID after upsert = %q, want %q", got, phc2)
	}

	// Delete is idempotent.
	if err := store.DeleteAccountPassword(context.Background(), acct.ID); err != nil {
		t.Errorf("DeleteAccountPassword: %v", err)
	}
	if err := store.DeleteAccountPassword(context.Background(), acct.ID); err != nil {
		t.Errorf("DeleteAccountPassword second call should be idempotent: %v", err)
	}
	if _, err := store.AccountPasswordByAccountID(context.Background(), acct.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("post-delete AccountPasswordByAccountID err = %v, want ErrNotFound", err)
	}

	// Empty arguments → ErrInvalidArgument (the caller bug surface).
	if err := store.SetAccountPassword(context.Background(), "", phc); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("SetAccountPassword with empty account_id err = %v, want ErrInvalidArgument", err)
	}
	if err := store.SetAccountPassword(context.Background(), acct.ID, ""); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("SetAccountPassword with empty hash err = %v, want ErrInvalidArgument", err)
	}
}

// TestMemStore_OAuthLink_RoundTrip pins the (provider, subject) →
// account_id lookup. The §11 anti-takeover invariant is the
// composite PK on the table; the MemStore mirrors it via the
// (provider + "\x00" + subject) map key. A second UpsertOAuthLink
// with a DIFFERENT account_id but the SAME (provider, sub) returns
// ErrConflict — that's the in-memory equivalent of the database PK
// rejection.
func TestMemStore_OAuthLink_RoundTrip(t *testing.T) {
	store := NewMemStore()
	acct1, err := store.CreateAccount(context.Background(), "alice@example.com", "free")
	if err != nil {
		t.Fatal(err)
	}
	acct2, err := store.CreateAccount(context.Background(), "bob@example.com", "free")
	if err != nil {
		t.Fatal(err)
	}

	// Bind alice to (google, sub-1).
	if err := store.UpsertOAuthLink(context.Background(), acct1.ID, "google", "sub-1", "alice@example.com", true); err != nil {
		t.Fatalf("UpsertOAuthLink (alice): %v", err)
	}

	// Read back.
	got, err := store.OAuthLinkByProviderSubject(context.Background(), "google", "sub-1")
	if err != nil {
		t.Fatalf("OAuthLinkByProviderSubject: %v", err)
	}
	if got.AccountID != acct1.ID {
		t.Errorf("AccountID = %q, want %q", got.AccountID, acct1.ID)
	}
	if !got.EmailVerified {
		t.Errorf("EmailVerified = false, want true (was set on insert)")
	}
	if got.Email != "alice@example.com" {
		t.Errorf("Email = %q, want alice@example.com", got.Email)
	}

	// Same account re-binds with a refreshed email → overwrites
	// in place (CreatedAt preserved, Email + EmailVerified updated).
	originalCreated := got.CreatedAt
	if err := store.UpsertOAuthLink(context.Background(), acct1.ID, "google", "sub-1", "alice-renamed@example.com", true); err != nil {
		t.Fatalf("UpsertOAuthLink (re-bind by same account): %v", err)
	}
	got, err = store.OAuthLinkByProviderSubject(context.Background(), "google", "sub-1")
	if err != nil {
		t.Fatalf("OAuthLinkByProviderSubject (post re-bind): %v", err)
	}
	if got.Email != "alice-renamed@example.com" {
		t.Errorf("Email after re-bind = %q, want alice-renamed@example.com", got.Email)
	}
	if !got.CreatedAt.Equal(originalCreated) {
		t.Errorf("CreatedAt = %v, want %v (re-bind should preserve CreatedAt)", got.CreatedAt, originalCreated)
	}

	// DIFFERENT account claiming (google, sub-1) → ErrConflict.
	err = store.UpsertOAuthLink(context.Background(), acct2.ID, "google", "sub-1", "bob@example.com", true)
	if !errors.Is(err, ErrConflict) {
		t.Errorf("UpsertOAuthLink (bob stealing alice's sub) err = %v, want ErrConflict (§11 anti-takeover closure)", err)
	}

	// DIFFERENT provider with the same subject string → no conflict
	// (the PK is composite, not on sub alone).
	if err := store.UpsertOAuthLink(context.Background(), acct1.ID, "github", "sub-1", "alice@example.com", true); err != nil {
		t.Errorf("UpsertOAuthLink (alice github/sub-1) err = %v, want nil (different provider, no collision)", err)
	}

	// ErrNotFound for unbound subject.
	if _, err := store.OAuthLinkByProviderSubject(context.Background(), "google", "sub-unbound"); !errors.Is(err, ErrNotFound) {
		t.Errorf("OAuthLinkByProviderSubject on unbound err = %v, want ErrNotFound", err)
	}

	// Empty arguments → ErrInvalidArgument.
	if err := store.UpsertOAuthLink(context.Background(), "", "google", "sub-x", "x@example.com", true); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("UpsertOAuthLink with empty account_id err = %v, want ErrInvalidArgument", err)
	}
}

// --- Customer secrets (spec §11/G2) ----------------------------------------

// TestAppSecretUpsertListDelete exercises the four-method CRUD through
// MemStore. Ciphertext is opaque bytes here — the MemStore's job is to
// model the (account_id, app_id, key) row shape; pgstore mirrors the same
// SQL semantics. Encryption is pkg/secretbox's responsibility.
func TestAppSecretUpsertListDelete(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	const acctA, acctB = "acct-A", "acct-B"
	const appA, appB = "app-A", "app-B"

	// Initial state: nothing.
	got, err := m.ListAppSecrets(ctx, acctA, appA)
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty list: got %d, want 0", len(got))
	}

	// Upsert two keys under (acctA, appA).
	if err := m.UpsertAppSecret(ctx, acctA, appA, "STRIPE_KEY", []byte("cipher-1")); err != nil {
		t.Fatalf("upsert STRIPE_KEY: %v", err)
	}
	if err := m.UpsertAppSecret(ctx, acctA, appA, "API_TOKEN", []byte("cipher-2")); err != nil {
		t.Fatalf("upsert API_TOKEN: %v", err)
	}

	// Count is 2.
	n, err := m.CountAppSecrets(ctx, acctA, appA)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("count: got %d, want 2", n)
	}

	// List returns both, sorted by key (API_TOKEN before STRIPE_KEY).
	got, err = m.ListAppSecrets(ctx, acctA, appA)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("list: got %d, want 2", len(got))
	}
	if got[0].Key != "API_TOKEN" || got[1].Key != "STRIPE_KEY" {
		t.Errorf("order: got %q/%q, want API_TOKEN/STRIPE_KEY", got[0].Key, got[1].Key)
	}

	// Upsert replaces ciphertext on conflict (same key).
	if err := m.UpsertAppSecret(ctx, acctA, appA, "API_TOKEN", []byte("cipher-2-rotated")); err != nil {
		t.Fatalf("upsert rotate: %v", err)
	}
	got, _ = m.ListAppSecrets(ctx, acctA, appA)
	if string(got[0].Ciphertext) != "cipher-2-rotated" {
		t.Errorf("rotate: got %q, want cipher-2-rotated", string(got[0].Ciphertext))
	}

	// Cross-account isolation: acctB sees nothing on appA.
	if n, _ := m.CountAppSecrets(ctx, acctB, appA); n != 0 {
		t.Errorf("cross-acct count: got %d, want 0", n)
	}
	if got, _ := m.ListAppSecrets(ctx, acctB, appA); len(got) != 0 {
		t.Errorf("cross-acct list: got %d, want 0", len(got))
	}

	// Cross-app isolation: same account, different app.
	if err := m.UpsertAppSecret(ctx, acctA, appB, "DB_URL", []byte("cipher-3")); err != nil {
		t.Fatalf("upsert appB: %v", err)
	}
	if n, _ := m.CountAppSecrets(ctx, acctA, appB); n != 1 {
		t.Errorf("appB count: got %d, want 1", n)
	}
	if n, _ := m.CountAppSecrets(ctx, acctA, appA); n != 2 {
		t.Errorf("appA count after appB upsert: got %d, want 2", n)
	}

	// Delete scoped to (acctA, appA, STRIPE_KEY).
	if err := m.DeleteAppSecret(ctx, acctA, appA, "STRIPE_KEY"); err != nil {
		t.Fatalf("delete STRIPE_KEY: %v", err)
	}
	if n, _ := m.CountAppSecrets(ctx, acctA, appA); n != 1 {
		t.Errorf("post-delete count: got %d, want 1", n)
	}

	// Delete on cross-account returns ErrNotFound (renders 400 CodeSecretNotFound).
	if err := m.DeleteAppSecret(ctx, acctB, appA, "API_TOKEN"); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-acct delete: got %v, want ErrNotFound", err)
	}

	// Delete on unknown key returns ErrNotFound.
	if err := m.DeleteAppSecret(ctx, acctA, appA, "MISSING"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing delete: got %v, want ErrNotFound", err)
	}
}

// TestAppSecretOwnershipOnUpsert asserts the (account_id, app_id, key) is
// the unique row identifier: a different account's upsert against the same
// (app_id, key) returns ErrNotFound (treated as "row not yours" by the
// handler). This matches the SQL semantics where the PRIMARY KEY is on
// (app_id, key) but the ownership check happens via account_id at the
// query layer.
func TestAppSecretOwnershipOnUpsert(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	// acctA owns the row.
	if err := m.UpsertAppSecret(ctx, "acct-A", "app-1", "K", []byte("c1")); err != nil {
		t.Fatalf("upsert A: %v", err)
	}
	// acctB tries to overwrite — gets ErrNotFound (handler renders 400).
	if err := m.UpsertAppSecret(ctx, "acct-B", "app-1", "K", []byte("c2")); !errors.Is(err, ErrNotFound) {
		t.Errorf("acctB upsert: got %v, want ErrNotFound", err)
	}
	// Original row untouched.
	got, _ := m.ListAppSecrets(ctx, "acct-A", "app-1")
	if len(got) != 1 || string(got[0].Ciphertext) != "c1" {
		t.Errorf("row integrity: got %+v, want c1", got)
	}
}

// --- Customer env vars (spec §11, issue #395 / ADR-045) ----------------------
//
// Mirror of the customer-secrets surface minus the ciphertext column.
// Plaintext TEXT values: the MemStore's role is to model the
// (account_id, app_id, key) row shape + the ownership checks so unit
// tests can verify quota / list / delete logic without touching
// Postgres. Encryption does not apply to env vars by design.

// TestAppEnvUpsertListDelete exercises the four-method CRUD through
// MemStore. Mirrors TestAppSecretUpsertListDelete so the table-driven
// shapes of the two surfaces agree.
func TestAppEnvUpsertListDelete(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	const acctA, acctB = "acct-A", "acct-B"
	const appA, appB = "app-A", "app-B"

	// Initial state: nothing.
	got, err := m.ListAppEnv(ctx, acctA, appA)
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty list: got %d, want 0", len(got))
	}

	// Upsert two keys under (acctA, appA).
	if err := m.UpsertAppEnv(ctx, acctA, appA, "LOG_LEVEL", "debug"); err != nil {
		t.Fatalf("upsert LOG_LEVEL: %v", err)
	}
	if err := m.UpsertAppEnv(ctx, acctA, appA, "FEATURE_X", "on"); err != nil {
		t.Fatalf("upsert FEATURE_X: %v", err)
	}

	// Count is 2.
	n, err := m.CountAppEnv(ctx, acctA, appA)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("count: got %d, want 2", n)
	}

	// List returns both, sorted by key (FEATURE_X before LOG_LEVEL).
	got, err = m.ListAppEnv(ctx, acctA, appA)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("list: got %d, want 2", len(got))
	}
	if got[0].Key != "FEATURE_X" || got[1].Key != "LOG_LEVEL" {
		t.Errorf("order: got %q/%q, want FEATURE_X/LOG_LEVEL", got[0].Key, got[1].Key)
	}

	// Upsert replaces value on conflict (same key).
	if err := m.UpsertAppEnv(ctx, acctA, appA, "LOG_LEVEL", "info"); err != nil {
		t.Fatalf("upsert rotate: %v", err)
	}
	got, _ = m.ListAppEnv(ctx, acctA, appA)
	if got[1].Value != "info" {
		t.Errorf("rotate: got %q, want info", got[1].Value)
	}

	// Cross-account isolation: acctB sees nothing on appA.
	if n, _ := m.CountAppEnv(ctx, acctB, appA); n != 0 {
		t.Errorf("cross-acct count: got %d, want 0", n)
	}
	if got, _ := m.ListAppEnv(ctx, acctB, appA); len(got) != 0 {
		t.Errorf("cross-acct list: got %d, want 0", len(got))
	}

	// Cross-app isolation: same account, different app.
	if err := m.UpsertAppEnv(ctx, acctA, appB, "PORT", "8080"); err != nil {
		t.Fatalf("upsert appB: %v", err)
	}
	if n, _ := m.CountAppEnv(ctx, acctA, appB); n != 1 {
		t.Errorf("appB count: got %d, want 1", n)
	}
	if n, _ := m.CountAppEnv(ctx, acctA, appA); n != 2 {
		t.Errorf("appA count after appB upsert: got %d, want 2", n)
	}

	// Delete scoped to (acctA, appA, LOG_LEVEL).
	if err := m.DeleteAppEnv(ctx, acctA, appA, "LOG_LEVEL"); err != nil {
		t.Fatalf("delete LOG_LEVEL: %v", err)
	}
	if n, _ := m.CountAppEnv(ctx, acctA, appA); n != 1 {
		t.Errorf("post-delete count: got %d, want 1", n)
	}

	// Delete on cross-account returns ErrNotFound (renders 400 CodeEnvVarNotFound).
	if err := m.DeleteAppEnv(ctx, acctB, appA, "FEATURE_X"); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-acct delete: got %v, want ErrNotFound", err)
	}

	// Delete on unknown key returns ErrNotFound.
	if err := m.DeleteAppEnv(ctx, acctA, appA, "MISSING"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing delete: got %v, want ErrNotFound", err)
	}
}

// TestAppEnvOwnershipOnUpsert asserts the same ownership semantics as the
// secrets surface — a different account's upsert against the same
// (app_id, key) returns ErrNotFound and leaves the original row
// untouched. Code path is structured so a future refactor that drops the
// ownership check from one surface but not the other is caught here.
func TestAppEnvOwnershipOnUpsert(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	if err := m.UpsertAppEnv(ctx, "acct-A", "app-1", "LOG_LEVEL", "debug"); err != nil {
		t.Fatalf("upsert A: %v", err)
	}
	if err := m.UpsertAppEnv(ctx, "acct-B", "app-1", "LOG_LEVEL", "info"); !errors.Is(err, ErrNotFound) {
		t.Errorf("acctB upsert: got %v, want ErrNotFound", err)
	}
	got, _ := m.ListAppEnv(ctx, "acct-A", "app-1")
	if len(got) != 1 || got[0].Value != "debug" {
		t.Errorf("row integrity: got %+v, want debug", got)
	}
}

// TestMem_DeleteAccount_CascadesAppEnv asserts the G6 GDPR cascade covers
// the app_envs surface. Mirrors TestMem_DeleteAccount_CascadesEvents —
// the right-to-erasure contract requires that no env row survives an
// account deletion, since env rows hold plaintext values that are
// indistinguishable from customer-readable config.
func TestMem_DeleteAccount_CascadesAppEnv(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "envcasc@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{AccountID: acct.ID, Slug: "env-casc-app"})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if err := m.UpsertAppEnv(ctx, acct.ID, app.ID, "FEATURE_A", "on"); err != nil {
		t.Fatalf("upsert env: %v", err)
	}
	if err := m.UpsertAppEnv(ctx, acct.ID, app.ID, "LOG_LEVEL", "debug"); err != nil {
		t.Fatalf("upsert env: %v", err)
	}
	if n, _ := m.CountAppEnv(ctx, acct.ID, app.ID); n != 2 {
		t.Fatalf("pre-cascade count: got %d, want 2", n)
	}
	// Pending is the prerequisite for the cascade path.
	if err := m.MarkAccountDeletionPending(ctx, acct.ID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	if err := m.DeleteAccount(ctx, acct.ID); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	// Re-seed a fresh account + app under the same ids to probe the map
	// (MemStore's id-keyed map means stale rows would surface as
	// false-positive counts in a fresh account lookup).
	fresh, err := m.CreateAccount(ctx, "envcasc-fresh@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount fresh: %v", err)
	}
	if n, _ := m.CountAppEnv(ctx, fresh.ID, app.ID); n != 0 {
		t.Errorf("post-cascade cross-acct count: got %d, want 0", n)
	}
}

// --- G6 GDPR self-service regressions ----------------------------------------

// TestMem_DeleteAccount_CascadesEvents is the MemStore half of the G6
// right-to-erasure regression (spec §17 G6, ADR-021). Audit events
// whose subject is the account id, or whose payload account_id
// matches, must not survive DeleteAccount.
func TestMem_DeleteAccount_CascadesEvents(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "events@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	// DeleteAccount is conditional on status='deleted_pending'; mark
	// pending first so the cascade actually runs (mirrors the grace
	// timer's pre-condition).
	if err := m.MarkAccountDeletionPending(ctx, acct.ID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	// Subject-keyed event: subject == acct.ID.
	subject := acct.ID
	if err := m.AppendEvent(ctx, "test", "export", &subject, []byte(`{}`)); err != nil {
		t.Fatalf("AppendEvent subject: %v", err)
	}
	// Data-keyed event: data.account_id == acct.ID.
	payload := []byte(`{"account_id":"` + acct.ID + `"}`)
	if err := m.AppendEvent(ctx, "test", "export", nil, payload); err != nil {
		t.Fatalf("AppendEvent data: %v", err)
	}
	// Surviving event (different account) — must NOT be touched.
	other := "00000000-0000-0000-0000-000000000099"
	if err := m.AppendEvent(ctx, "test", "export", nil,
		[]byte(`{"account_id":"`+other+`"}`)); err != nil {
		t.Fatalf("AppendEvent other: %v", err)
	}

	if err := m.DeleteAccount(ctx, acct.ID); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	// ListEvents returns m.events; nothing here filters by subject so
	// we walk the slice directly to assert both erasure predicates ran.
	idUUID := uuid.MustParse(acct.ID)
	for _, e := range m.events {
		if e.Subject != nil && *e.Subject == idUUID {
			t.Errorf("subject-keyed event survived DeleteAccount: %+v", e)
		}
		if len(e.Data) > 0 {
			var got map[string]string
			if jerr := json.Unmarshal(e.Data, &got); jerr == nil &&
				got["account_id"] == acct.ID {
				t.Errorf("data-keyed event survived DeleteAccount: %+v", e)
			}
		}
	}
	// Surviving-event sanity: the other account's audit row is still
	// in the slice.
	var sawOther bool
	for _, e := range m.events {
		if len(e.Data) == 0 {
			continue
		}
		var got map[string]string
		if json.Unmarshal(e.Data, &got) == nil && got["account_id"] == other {
			sawOther = true
		}
	}
	if !sawOther {
		t.Errorf("unrelated event was collateral damage")
	}
}

// TestMem_DeleteAccount_OnActiveRowReturnsErrNotFound is the
// MemStore half of the conditional-DELETE sentinel regression (review
// of #46). Before the patch, DeleteAccount ran an unconditional
// `delete from accounts` and then a probe — so a redelivered tick on
// an already-restored row reported success and the grace timer's
// `errors.Is(err, ErrNotFound)` branch was dead code. The new
// conditional matches the PG SQL and returns ErrNotFound.
func TestMem_DeleteAccount_OnActiveRowReturnsErrNotFound(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "active@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	err = m.DeleteAccount(ctx, acct.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteAccount on active row = %v, want ErrNotFound", err)
	}
	if _, err := m.AccountByID(ctx, acct.ID); err != nil {
		t.Errorf("AccountByID after no-op delete = %v, want nil", err)
	}
}

// TestMem_DeleteAccount_TwiceIsErrNotFound covers the idempotent
// retry path: the second call must report ErrNotFound, not silently
// succeed.
func TestMem_DeleteAccount_TwiceIsErrNotFound(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "twice@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := m.MarkAccountDeletionPending(ctx, acct.ID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	if err := m.DeleteAccount(ctx, acct.ID); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	err = m.DeleteAccount(ctx, acct.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v, want ErrNotFound", err)
	}
}

// --- RenameApp (issue #63) --------------------------------------------------
//
// These tests lock down the MemStore contract that the apid handler relies
// on (handlers_ext.go:247 errors.Is(err, state.ErrConflict)) and that the
// PgStore must mirror via mapErr → unique-violation SQLSTATE. They run as
// pure in-memory tests, so they're part of `make test` (no KVM, no real
// Postgres). The PgStore equivalents live in pgstore_test.go behind
// pgtest.Open — the two together pin the error contract.

func TestMem_RenameApp_HappyPath(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "rename@x.com", api.PlanHobby)
	app, err := m.CreateApp(ctx, App{
		AccountID: acc.ID, Slug: "old-name", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60, Status: AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	got, err := m.RenameApp(ctx, acc.ID, "old-name", "new-name")
	if err != nil {
		t.Fatalf("RenameApp: %v", err)
	}
	if got.Slug != "new-name" {
		t.Errorf("Slug = %q, want new-name", got.Slug)
	}
	if got.ID != app.ID {
		t.Errorf("ID = %q, want %q (same row, mutated in place)", got.ID, app.ID)
	}

	// Old slug must be gone from the lookup table.
	if _, err := m.AppBySlug(ctx, "old-name"); !errors.Is(err, ErrNotFound) {
		t.Errorf("AppBySlug(old-name) = %v, want ErrNotFound", err)
	}
	// New slug must resolve.
	if back, err := m.AppBySlug(ctx, "new-name"); err != nil {
		t.Errorf("AppBySlug(new-name): %v", err)
	} else if back.ID != app.ID {
		t.Errorf("AppBySlug(new-name).ID = %q, want %q", back.ID, app.ID)
	}
}

func TestMem_RenameApp_SlugTakenReturnsErrConflict(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "take@x.com", api.PlanHobby)
	if _, err := m.CreateApp(ctx, App{
		AccountID: acc.ID, Slug: "victim", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, Status: AppActive,
	}); err != nil {
		t.Fatalf("CreateApp victim: %v", err)
	}
	if _, err := m.CreateApp(ctx, App{
		AccountID: acc.ID, Slug: "blocker", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, Status: AppActive,
	}); err != nil {
		t.Fatalf("CreateApp blocker: %v", err)
	}

	_, err := m.RenameApp(ctx, acc.ID, "victim", "blocker")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("RenameApp onto existing slug = %v, want ErrConflict", err)
	}

	// The losing rename must not have moved the victim.
	if _, err := m.AppBySlug(ctx, "victim"); err != nil {
		t.Errorf("victim disappeared after failed rename: %v", err)
	}
	if _, err := m.AppBySlug(ctx, "blocker"); err != nil {
		t.Errorf("blocker disappeared after failed rename: %v", err)
	}
}

func TestMem_RenameApp_UnknownSlugReturnsErrNotFound(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "ghost@x.com", api.PlanHobby)
	if _, err := m.CreateApp(ctx, App{
		AccountID: acc.ID, Slug: "real", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, Status: AppActive,
	}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	_, err := m.RenameApp(ctx, acc.ID, "ghost", "anything")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("RenameApp on missing slug = %v, want ErrNotFound", err)
	}
}

// TestMem_RenameApp_CrossAccountIsolation pins the (account_id, slug)
// pair in the source lookup: account A must not be able to mutate an
// app that belongs to account B. Attempting to rename B's slug from
// A's context looks the same as "no such slug in this account" →
// ErrNotFound. Without the accountID scope in the WHERE clause, this
// would be a horizontal-priv-esc.
//
// The collision direction (A trying to rename alpha → beta where B owns
// beta) is a SEPARATE concern — slug namespacing is global by design
// (apps.slug is a unique constraint, same as CreateApp). Probing for
// foreign slugs via rename collisions is a known enumeration surface
// that mirrors CreateApp's; not in scope for this test.
func TestMem_RenameApp_CrossAccountIsolation(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	accA, _ := m.CreateAccount(ctx, "a@x.com", api.PlanHobby)
	accB, _ := m.CreateAccount(ctx, "b@x.com", api.PlanHobby)

	if _, err := m.CreateApp(ctx, App{
		AccountID: accA.ID, Slug: "alpha", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, Status: AppActive,
	}); err != nil {
		t.Fatalf("CreateApp A: %v", err)
	}
	if _, err := m.CreateApp(ctx, App{
		AccountID: accB.ID, Slug: "beta", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, Status: AppActive,
	}); err != nil {
		t.Fatalf("CreateApp B: %v", err)
	}

	// A cannot rename B's slug — must look like ErrNotFound, not
	// ErrConflict (which would leak existence info about B's app).
	if _, err := m.RenameApp(ctx, accA.ID, "beta", "stolen"); !errors.Is(err, ErrNotFound) {
		t.Errorf("A renaming B's slug = %v, want ErrNotFound (account scope on source lookup)", err)
	}
	// Symmetric: A also cannot touch B's slug when asking for an
	// unrelated rename target.
	if _, err := m.RenameApp(ctx, accA.ID, "beta", "renamed-beta"); !errors.Is(err, ErrNotFound) {
		t.Errorf("A renaming B's slug (any target) = %v, want ErrNotFound", err)
	}

	// B's app must still exist under the original slug.
	if got, err := m.AppBySlug(ctx, "beta"); err != nil {
		t.Errorf("B's beta vanished after failed cross-account rename: %v", err)
	} else if got.AccountID != accB.ID {
		t.Errorf("B's beta reassigned to %q after cross-account rename attempt", got.AccountID)
	}
	// A's app must not have been touched either.
	if got, err := m.AppBySlug(ctx, "alpha"); err != nil {
		t.Errorf("A's alpha vanished after cross-account attempt: %v", err)
	} else if got.AccountID != accA.ID {
		t.Errorf("A's alpha reassigned: %v", err)
	}
}

// --- snapshot GC tests -----------------------------------------------------

func TestMemStore_ListSnapshotsForGC(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, _ := m.CreateAccount(ctx, "u@example.com", "pro")
	app, _ := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "snap", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	depA, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:a"})
	depB, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:b"})
	if _, err := m.CreateSnapshot(ctx, Snapshot{
		DeploymentID: depA.ID, MemBytes: 100, DiskBytes: 100,
		FCVersion:  "1.8.0",
		StorageKey: SnapMemKey(depA.ID),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateSnapshot(ctx, Snapshot{
		DeploymentID: depB.ID, MemBytes: 200, DiskBytes: 200,
		FCVersion:  "1.8.0",
		StorageKey: SnapMemKey(depB.ID),
	}); err != nil {
		t.Fatal(err)
	}

	rows, err := m.ListSnapshotsForGC(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Errorf("ListSnapshotsForGC returned %d, want 2", len(rows))
	}
	for _, r := range rows {
		if r.AppID != app.ID {
			t.Errorf("row AppID = %q, want %q", r.AppID, app.ID)
		}
		if r.AccountID != acct.ID {
			t.Errorf("row AccountID = %q, want %q", r.AccountID, acct.ID)
		}
	}
}

func TestMemStore_ListSnapshotsForGC_IncludesDeletedAppForCleanup(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, _ := m.CreateAccount(ctx, "u@example.com", "pro")
	app, _ := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "del-app", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	dep, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:a"})
	if _, err := m.CreateSnapshot(ctx, Snapshot{
		DeploymentID: dep.ID, MemBytes: 100, DiskBytes: 100,
		FCVersion:  "1.8.0",
		StorageKey: SnapMemKey(dep.ID),
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteApp(ctx, app.ID); err != nil {
		t.Fatal(err)
	}

	rows, _ := m.ListSnapshotsForGC(ctx)
	if len(rows) != 1 {
		t.Fatalf("deleted app's snapshot missing from GC cleanup: %d rows", len(rows))
	}
	if rows[0].AppStatus != AppDeleted {
		t.Errorf("AppStatus = %q, want %q", rows[0].AppStatus, AppDeleted)
	}
}

func TestMemStore_ListSnapshotDeploymentIDsIncludesStaleAndDeduplicates(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, _ := m.CreateAccount(ctx, "ids@example.com", "pro")
	app, _ := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "snapshot-ids", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	depA, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:a"})
	depB, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:b"})
	snapA, _ := m.CreateSnapshot(ctx, Snapshot{DeploymentID: depA.ID, FCVersion: "1.8.0", StorageKey: SnapMemKey(depA.ID)})
	_, _ = m.CreateSnapshot(ctx, Snapshot{DeploymentID: depA.ID, FCVersion: "1.8.0", StorageKey: SnapshotCaptureMemKey(depA.ID, SnapshotTierInit, "capture-a")})
	_, _ = m.CreateSnapshot(ctx, Snapshot{DeploymentID: depB.ID, FCVersion: "1.8.0", StorageKey: SnapMemKey(depB.ID)})
	if err := m.MarkSnapshotStale(ctx, snapA.ID); err != nil {
		t.Fatal(err)
	}

	got, err := m.ListSnapshotDeploymentIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{depA.ID, depB.ID}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("deployment IDs = %v, want %v", got, want)
	}
}

func TestMemStore_DeleteSnapshotsByID_BulkAndIdempotent(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, _ := m.CreateAccount(ctx, "u@example.com", "pro")
	app, _ := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "del-snap", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	depA, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:a"})
	depB, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:b"})
	snapA, _ := m.CreateSnapshot(ctx, Snapshot{
		DeploymentID: depA.ID, MemBytes: 100, DiskBytes: 100, FCVersion: "1.8.0",
		StorageKey: SnapMemKey(depA.ID),
	})
	snapB, _ := m.CreateSnapshot(ctx, Snapshot{
		DeploymentID: depB.ID, MemBytes: 100, DiskBytes: 100, FCVersion: "1.8.0",
		StorageKey: SnapMemKey(depB.ID),
	})

	n, err := m.DeleteSnapshotsByID(ctx, []string{snapA.ID, snapB.ID})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("first delete = %d, want 2", n)
	}
	n2, err := m.DeleteSnapshotsByID(ctx, []string{snapA.ID, snapB.ID})
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Errorf("second delete = %d, want 0 (idempotent)", n2)
	}
}

func TestMemStore_MarkAllSnapshotsStaleByFCVersion(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, _ := m.CreateAccount(ctx, "u@example.com", "pro")
	app, _ := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "fc-sweep", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	insert := func(v string) string {
		dep, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:" + v})
		snap, _ := m.CreateSnapshot(ctx, Snapshot{
			DeploymentID: dep.ID, MemBytes: 100, DiskBytes: 100,
			FCVersion:  v,
			StorageKey: SnapMemKey(dep.ID),
		})
		return snap.ID
	}
	insert("1.7.0")
	insert("1.8.0")
	insert("1.9.0")

	n, err := m.MarkAllSnapshotsStaleByFCVersion(ctx, "1.8.0")
	if err != nil {
		t.Fatal(err)
	}
	// Both 1.7 and 1.9 are NOT 1.8 → marked stale. Only the current
	// FC version's snapshots stay live.
	if n != 2 {
		t.Errorf("marked %d stale, want 2", n)
	}
	// Idempotent: a second call finds no non-stale rows to flip.
	n2, _ := m.MarkAllSnapshotsStaleByFCVersion(ctx, "1.8.0")
	if n2 != 0 {
		t.Errorf("second sweep marked %d, want 0", n2)
	}
}

func TestMemStore_MarkOldSnapshotsStale(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, _ := m.CreateAccount(ctx, "u@example.com", "pro")
	app, _ := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "mark-old", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	depA, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:a"})
	depB, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:b"})
	snapA, _ := m.CreateSnapshot(ctx, Snapshot{
		DeploymentID: depA.ID, MemBytes: 100, DiskBytes: 100, FCVersion: "1.8.0",
		StorageKey: SnapMemKey(depA.ID),
	})
	_, _ = m.CreateSnapshot(ctx, Snapshot{
		DeploymentID: depB.ID, MemBytes: 100, DiskBytes: 100, FCVersion: "1.8.0",
		StorageKey: SnapMemKey(depB.ID),
	})

	n, err := m.MarkOldSnapshotsStale(ctx, []string{snapA.ID})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("marked %d, want 1", n)
	}
	// LatestSnapshot filters stale out; inspect the row directly.
	foundA, foundB := false, false
	for _, s := range m.snapshots {
		if s.ID == snapA.ID {
			foundA = true
			if !s.Stale {
				t.Errorf("snapA not marked stale")
			}
		}
		if s.ID != snapA.ID && s.DeploymentID == depB.ID {
			foundB = true
			if s.Stale {
				t.Errorf("snapB marked stale by an unrelated call")
			}
		}
	}
	if !foundA || !foundB {
		t.Errorf("seed snapshots missing from store: A=%v B=%v", foundA, foundB)
	}
}

// TestMemStore_SnapshotStorageKey_RoundTrip pins the #96 / ADR-025
// axis 2 storage_key field: CreateSnapshot stores the value the
// caller passes, LatestSnapshot reads it back unchanged, and
// ListSnapshotsForGC exposes it on SnapshotForGC so the imaged GC
// can Storage.Delete under the canonical key.
// --- Compute nodes (issue #97 / ADR-025 axis 3) -----------------------------
//
// The MemStore seeds a synthetic 'default-local' node on NewMemStore()
// (memstore.go:seedDefaultLocalNodeLocked) so any caller that needs the
// canonical default-local id can fetch it via ComputeNodeByName without
// seeding first. Tests below rely on that — they do NOT call seedDefault.

func TestMem_ComputeNodes_DefaultLocalSeededOnNewStore(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	// The seeded node is the synthetic default-local: active=true,
	// target URL the legacy unix socket, admission ceiling the legacy
	// 47,600 MB. The id is non-empty and the name matches
	// DefaultLocalNodeName (the canonical name callers resolve against).
	// PR scale-out readiness #4: the AdmissionCeilingMB assertion
	// compares against api.DefaultComputeNodeCeilingMB() so a future
	// helper change surfaces here with a targeted message instead of
	// a hard-coded drift between the seed and the platform baseline.
	got, err := m.ComputeNodeByName(ctx, DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("ComputeNodeByName(default-local): %v", err)
	}
	if got.Name != DefaultLocalNodeName {
		t.Errorf("Name=%q, want %q", got.Name, DefaultLocalNodeName)
	}
	if !got.Active {
		t.Errorf("seeded default-local should be active, got %v", got.Active)
	}
	if got.AdmissionCeilingMB != api.DefaultComputeNodeCeilingMB() {
		t.Errorf("AdmissionCeilingMB=%d, want %d (api.DefaultComputeNodeCeilingMB())",
			got.AdmissionCeilingMB, api.DefaultComputeNodeCeilingMB())
	}
	// PR scale-out readiness #4: independent literal pin. The helper
	// check above catches drift between the seed and the helper; this
	// pin catches drift between the helper and the platform baseline
	// (47_600 MB), so a future contributor who changes both at once
	// still gets a targeted failure here. Mirrors the value-pinning
	// assertion in TestDefaultComputeNodeCeilingMB.
	if got.AdmissionCeilingMB != 47_600 {
		t.Errorf("AdmissionCeilingMB=%d, want 47_600 (platform baseline pin)",
			got.AdmissionCeilingMB)
	}
	if got.TargetURL != "unix:///run/faas/vmmd.sock" {
		t.Errorf("TargetURL=%q, want %q", got.TargetURL, "unix:///run/faas/vmmd.sock")
	}
	if got.LastHeartbeatAt.IsZero() {
		t.Errorf("seeded LastHeartbeatAt should be stamped at creation")
	}
	// PR #429: region/zone backfill mirrors migrations/00069 so a
	// single-box deploy has a deterministic ("local","local") tie-break
	// ordering without needing the migration to have run on the
	// memstore. Pin the seed here so a future contributor who changes
	// memstore.seedDefaultLocalNodeLocked (or the migration) sees a
	// targeted failure if the two drift apart.
	if got.Region == nil || *got.Region != DefaultLocalityLabel {
		t.Errorf("Region = %v, want pointer to %q", got.Region, DefaultLocalityLabel)
	}
	if got.Zone == nil || *got.Zone != DefaultLocalityLabel {
		t.Errorf("Zone = %v, want pointer to %q", got.Zone, DefaultLocalityLabel)
	}
}

func TestMem_ComputeNodes_NewMemStoreSeedsDefaultLocal(t *testing.T) {
	// Pin the seeding invariant: NewMemStore() must place the synthetic
	// default-local row in ActiveComputeNodes immediately, NOT on first
	// read. schedd's startup path depends on this — cmd/schedd/main.go's
	// runHeartbeat calls ComputeNodeByName once at boot and treats a
	// non-row result as a loud failure. A future refactor that lazily
	// seeds on first read would silently break that contract; this test
	// surfaces the regression before it lands.
	m := NewMemStore()
	nodes, err := m.ActiveComputeNodes(context.Background())
	if err != nil {
		t.Fatalf("ActiveComputeNodes: %v", err)
	}
	found := false
	for _, n := range nodes {
		if n.Name == DefaultLocalNodeName {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, 0, len(nodes))
		for _, n := range nodes {
			names = append(names, n.Name)
		}
		t.Errorf("default-local missing from ActiveComputeNodes after NewMemStore (got %v)", names)
	}
}

func TestMem_ComputeNodes_ActiveComputeNodes_OnlyReturnsActive(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	// Create one active and one drained node.
	active := state_computeNodeFixture("active-node", true)
	drained := state_computeNodeFixture("drained-node", false)
	if _, err := m.CreateComputeNode(ctx, active); err != nil {
		t.Fatalf("CreateComputeNode(active): %v", err)
	}
	if _, err := m.CreateComputeNode(ctx, drained); err != nil {
		t.Fatalf("CreateComputeNode(drained): %v", err)
	}

	// ActiveComputeNodes should return both seeded default-local AND
	// the new active node, but skip drained. Result is sorted by name.
	nodes, err := m.ActiveComputeNodes(ctx)
	if err != nil {
		t.Fatalf("ActiveComputeNodes: %v", err)
	}
	gotNames := make([]string, 0, len(nodes))
	for _, n := range nodes {
		gotNames = append(gotNames, n.Name)
	}
	wantNames := []string{"active-node", DefaultLocalNodeName} // alphabetical
	if len(gotNames) != len(wantNames) {
		t.Fatalf("ActiveComputeNodes returned %d nodes, want %d (got=%v)", len(gotNames), len(wantNames), gotNames)
	}
	for i := range wantNames {
		if gotNames[i] != wantNames[i] {
			t.Errorf("ActiveComputeNodes[%d]=%q, want %q", i, gotNames[i], wantNames[i])
		}
	}
}

func TestMem_ComputeNodes_ComputeNodeByID_NotFound(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	if _, err := m.ComputeNodeByID(ctx, "no-such-id"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ComputeNodeByID(unknown): want ErrNotFound, got %v", err)
	}
}

func TestMem_ComputeNodes_ComputeNodeByName_NotFound(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	if _, err := m.ComputeNodeByName(ctx, "no-such-name"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ComputeNodeByName(unknown): want ErrNotFound, got %v", err)
	}
}

func TestMem_ComputeNodes_Heartbeat_BumpsAndUnknownReturnsNotFound(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	// Heartbeat an unknown id → ErrNotFound.
	if err := m.HeartbeatComputeNode(ctx, "no-such-id"); !errors.Is(err, ErrNotFound) {
		t.Errorf("HeartbeatComputeNode(unknown): want ErrNotFound, got %v", err)
	}

	// Create a node, capture original heartbeat, sleep briefly, heartbeat
	// again, and assert last_heartbeat_at moved forward.
	//
	// Flake guard: time.Now() is monotonic-resolution on most platforms
	// but a 2 ms sleep + a same-microsecond stamp on the goroutine can
	// still collapse on a busy CI runner. Retry once with a longer
	// sleep before failing. Same pattern as the PgStore test.
	node := state_computeNodeFixture("hb-node", true)
	created, err := m.CreateComputeNode(ctx, node)
	if err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	if !assertMemHeartbeatAdvanced(t, m, ctx, created.ID, created.LastHeartbeatAt, 2*time.Millisecond) {
		if !assertMemHeartbeatAdvanced(t, m, ctx, created.ID, created.LastHeartbeatAt, 10*time.Millisecond) {
			t.Errorf("HeartbeatComputeNode did not bump LastHeartbeatAt after 2 retries")
		}
	}
}

func assertMemHeartbeatAdvanced(t *testing.T, m *MemStore, ctx context.Context, id string, before time.Time, sleep time.Duration) bool {
	t.Helper()
	time.Sleep(sleep)
	if err := m.HeartbeatComputeNode(ctx, id); err != nil {
		t.Fatalf("HeartbeatComputeNode: %v", err)
		return false
	}
	after, err := m.ComputeNodeByID(ctx, id)
	if err != nil {
		t.Fatalf("ComputeNodeByID: %v", err)
		return false
	}
	return after.LastHeartbeatAt.After(before)
}

func TestMem_ComputeNodes_AppendComputeNodeHeartbeat_RoundTrip(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	node := state_computeNodeFixture("hb-history-node", true)
	created, err := m.CreateComputeNode(ctx, node)
	if err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	// Append five rows in known order; assert ListComputeNodeHeartbeats
	// returns them newest-first.
	base := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		receivedAt := base.Add(time.Duration(i) * 30 * time.Second)
		if err := m.AppendComputeNodeHeartbeat(ctx, created.ID, receivedAt, receivedAt, "heartbeat_tick"); err != nil {
			t.Fatalf("AppendComputeNodeHeartbeat #%d: %v", i, err)
		}
	}
	rows, err := m.ListComputeNodeHeartbeats(ctx, created.ID, time.Time{}, 100)
	if err != nil {
		t.Fatalf("ListComputeNodeHeartbeats: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("got %d rows, want 5", len(rows))
	}
	// Newest first.
	for i := 0; i < len(rows)-1; i++ {
		if !rows[i].ReceivedAt.After(rows[i+1].ReceivedAt) {
			t.Errorf("rows not newest-first: row[%d].ReceivedAt = %v, row[%d].ReceivedAt = %v",
				i, rows[i].ReceivedAt, i+1, rows[i+1].ReceivedAt)
		}
	}
}

func TestMem_ComputeNodes_AppendComputeNodeHeartbeat_DuplicateIsConflict(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	node := state_computeNodeFixture("hb-dup-node", true)
	created, err := m.CreateComputeNode(ctx, node)
	if err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	if err := m.AppendComputeNodeHeartbeat(ctx, created.ID, at, at, "heartbeat_tick"); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	err = m.AppendComputeNodeHeartbeat(ctx, created.ID, at, at, "heartbeat_tick")
	if err == nil {
		t.Fatalf("duplicate (node_id, received_at) must surface as ErrConflict")
	}
	if !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate = %v, want ErrConflict", err)
	}
}

func TestMem_ComputeNodes_AppendComputeNodeHeartbeat_UnknownNodeIsNotFound(t *testing.T) {
	m := NewMemStore()
	err := m.AppendComputeNodeHeartbeat(context.Background(), "no-such-id",
		time.Now(), time.Now(), "heartbeat_tick")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("AppendComputeNodeHeartbeat(unknown) = %v, want ErrNotFound", err)
	}
}

func TestMem_ComputeNodes_ListComputeNodeHeartbeats_FiltersSince(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	node := state_computeNodeFixture("hb-since-node", true)
	created, err := m.CreateComputeNode(ctx, node)
	if err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	base := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 6; i++ {
		receivedAt := base.Add(time.Duration(i) * 30 * time.Second)
		if err := m.AppendComputeNodeHeartbeat(ctx, created.ID, receivedAt, receivedAt, "heartbeat_tick"); err != nil {
			t.Fatalf("append #%d: %v", i, err)
		}
	}
	since := base.Add(60 * time.Second)
	rows, err := m.ListComputeNodeHeartbeats(ctx, created.ID, since, 100)
	if err != nil {
		t.Fatalf("ListComputeNodeHeartbeats: %v", err)
	}
	// ReceivedAt >= base+60s means rows 2..5 inclusive = 4 rows.
	if len(rows) != 4 {
		t.Errorf("since-filter row count = %d, want 4", len(rows))
	}
	for _, r := range rows {
		if r.ReceivedAt.Before(since) {
			t.Errorf("row ReceivedAt=%v below since=%v", r.ReceivedAt, since)
		}
	}
}

func TestMem_ComputeNodes_ListComputeNodeHeartbeats_LimitZeroDefaults200(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	node := state_computeNodeFixture("hb-limit-node", true)
	created, err := m.CreateComputeNode(ctx, node)
	if err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	// Append 250 rows; assert limit=0 returns ≤ 200 (the documented default).
	base := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 250; i++ {
		receivedAt := base.Add(time.Duration(i) * time.Second)
		if err := m.AppendComputeNodeHeartbeat(ctx, created.ID, receivedAt, receivedAt, "heartbeat_tick"); err != nil {
			t.Fatalf("append #%d: %v", i, err)
		}
	}
	rows, err := m.ListComputeNodeHeartbeats(ctx, created.ID, time.Time{}, 0)
	if err != nil {
		t.Fatalf("ListComputeNodeHeartbeats: %v", err)
	}
	if len(rows) != 200 {
		t.Errorf("limit=0 row count = %d, want 200 (default)", len(rows))
	}
}

func TestMem_ComputeNodes_DeleteComputeNode_CascadesHeartbeats(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	node := state_computeNodeFixture("hb-cascade-node", true)
	created, err := m.CreateComputeNode(ctx, node)
	if err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	if err := m.AppendComputeNodeHeartbeat(ctx, created.ID,
		time.Now(), time.Now(), "heartbeat_tick"); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := m.DeleteComputeNode(ctx, created.ID); err != nil {
		t.Fatalf("DeleteComputeNode: %v", err)
	}
	rows, err := m.ListComputeNodeHeartbeats(ctx, created.ID, time.Time{}, 100)
	if err != nil {
		t.Fatalf("ListComputeNodeHeartbeats: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("after cascade, row count = %d, want 0", len(rows))
	}
}

func TestMem_ComputeNodes_CreateComputeNode_AutoFillsIDAndTimestamps(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	// Caller omits ID, CreatedAt, LastHeartbeatAt — MemStore fills them.
	in := state_computeNodeFixture("autofill", true)
	in.ID = ""
	in.CreatedAt = time.Time{}
	in.LastHeartbeatAt = time.Time{}

	got, err := m.CreateComputeNode(ctx, in)
	if err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	if got.ID == "" {
		t.Errorf("MemStore should auto-fill ID")
	}
	if got.CreatedAt.IsZero() {
		t.Errorf("MemStore should stamp CreatedAt")
	}
	if got.LastHeartbeatAt.IsZero() {
		t.Errorf("MemStore should stamp LastHeartbeatAt (= CreatedAt when unset)")
	}
}

func TestMem_ComputeNodes_CreateComputeNode_DuplicateNameIsConflict(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	// 'default-local' is seeded on NewMemStore(); a second row with the
	// same name must ErrConflict, not overwrite the seeded row.
	dup := state_computeNodeFixture(DefaultLocalNodeName, true)
	if _, err := m.CreateComputeNode(ctx, dup); !errors.Is(err, ErrConflict) {
		t.Errorf("CreateComputeNode(duplicate name): want ErrConflict, got %v", err)
	}
}

func TestMem_ComputeNodes_UsedMB_SumsLiveInstancesOnly(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	// Create app + deployment to anchor instance rows. MemStore does
	// NOT enforce FK to compute_nodes (see memstore.go:1089) so we can
	// create instances on any nodeID — useful for negative-coverage tests.
	acct, err := m.CreateAccount(ctx, "u@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "node-mb", RAMMB: 256, MaxConcurrency: 4, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := m.CreateDeployment(ctx, Deployment{
		AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:k",
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	nodeA := "node-A"
	nodeB := "node-B"

	// nodeA: 2 waking, 1 cold_booting, 1 running, 1 stopped (not counted),
	// 1 snapshotted (not counted). Total live = 4 × (256 + 8) = 1056 MB.
	for _, st := range []string{"waking", "cold_booting", "running"} {
		if _, err := m.CreateInstance(ctx, app.ID, dep.ID, st, 256, nodeA, ""); err != nil {
			t.Fatalf("CreateInstance(%s): %v", st, err)
		}
	}
	if _, err := m.CreateInstance(ctx, app.ID, dep.ID, "running", 256, nodeA, ""); err != nil {
		t.Fatalf("CreateInstance(running-2): %v", err)
	}
	if _, err := m.CreateInstance(ctx, app.ID, dep.ID, "stopped", 256, nodeA, ""); err != nil {
		t.Fatalf("CreateInstance(stopped): %v", err)
	}
	if _, err := m.CreateInstance(ctx, app.ID, dep.ID, "snapshotted", 256, nodeA, ""); err != nil {
		t.Fatalf("CreateInstance(snapshotted): %v", err)
	}

	// nodeB: 1 running 512 MB → 520 MB total.
	if _, err := m.CreateInstance(ctx, app.ID, dep.ID, "running", 512, nodeB, ""); err != nil {
		t.Fatalf("CreateInstance(nodeB): %v", err)
	}

	gotA, err := m.ComputeNodeUsedMB(ctx, nodeA)
	if err != nil {
		t.Fatalf("ComputeNodeUsedMB(nodeA): %v", err)
	}
	wantA := int64(4 * (256 + api.PerVMOverheadMB))
	if gotA != wantA {
		t.Errorf("ComputeNodeUsedMB(nodeA)=%d, want %d (4 live × (256+8))", gotA, wantA)
	}

	gotB, err := m.ComputeNodeUsedMB(ctx, nodeB)
	if err != nil {
		t.Fatalf("ComputeNodeUsedMB(nodeB): %v", err)
	}
	wantB := int64(512 + api.PerVMOverheadMB)
	if gotB != wantB {
		t.Errorf("ComputeNodeUsedMB(nodeB)=%d, want %d", gotB, wantB)
	}

	// Unknown node → 0 (no error).
	gotU, err := m.ComputeNodeUsedMB(ctx, "no-such-node")
	if err != nil {
		t.Fatalf("ComputeNodeUsedMB(unknown): %v", err)
	}
	if gotU != 0 {
		t.Errorf("ComputeNodeUsedMB(unknown)=%d, want 0", gotU)
	}
}

// state_computeNodeFixture builds a valid ComputeNode for tests — a
// fresh node with a unique name and the production-shape field set.
// All non-name fields use the same values the production default-local
// row carries, so positive tests assert against a known shape.
func state_computeNodeFixture(name string, active bool) ComputeNode {
	return ComputeNode{
		Name:               name,
		TargetURL:          "unix:///run/faas/vmmd.sock",
		VPCPUs:             160,
		MemMB:              56000,
		MaxConcurrency:     200,
		AdmissionCeilingMB: 47600,
		Active:             active,
	}
}

func TestMemStore_SnapshotStorageKey_RoundTrip(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, _ := m.CreateAccount(ctx, "u@example.com", "pro")
	app, _ := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "snap-key", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 1,
	})
	dep, _ := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:k"})

	// (1) Caller-supplied storage_key round-trips through CreateSnapshot → LatestSnapshot.
	want := "snap/" + dep.ID + "/mem"
	snap, err := m.CreateSnapshot(ctx, Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.8.0", MemBytes: 100, DiskBytes: 100,
		StorageKey: want,
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if snap.StorageKey != want {
		t.Errorf("CreateSnapshot returned StorageKey=%q, want %q", snap.StorageKey, want)
	}
	got, err := m.LatestSnapshot(ctx, dep.ID)
	if err != nil {
		t.Fatalf("LatestSnapshot: %v", err)
	}
	if got.StorageKey != want {
		t.Errorf("LatestSnapshot returned StorageKey=%q, want %q", got.StorageKey, want)
	}

	// (2) ListSnapshotsForGC exposes the same value on SnapshotForGC
	// so imaged's GC loop can Storage.Delete under the canonical key.
	rows, err := m.ListSnapshotsForGC(ctx)
	if err != nil {
		t.Fatalf("ListSnapshotsForGC: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListSnapshotsForGC returned %d rows, want 1", len(rows))
	}
	if rows[0].StorageKey != want {
		t.Errorf("SnapshotForGC.StorageKey = %q, want %q", rows[0].StorageKey, want)
	}
}

// TestMemStore_ClaimQueuedBuild pins the atomic queued → running CAS

// TestMemStore_ClaimQueuedBuild pins the atomic queued → running CAS
// that closes the apid/reaper double-emit race (PR-A review). First
// claim wins; subsequent claims return ErrNotFound. Mirrors
// TestPg_ClaimQueuedBuild so the two backends stay in lock-step.
func TestMemStore_ClaimQueuedBuild(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "claim@example.com", "pro")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "claim-app", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := m.CreateDeployment(ctx, Deployment{
		AppID: app.ID, Kind: DeploymentKindTarball, Status: DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	b, err := m.CreateBuild(ctx, dep.ID, DeploymentKindTarball, 100, "")
	if err != nil {
		t.Fatalf("CreateBuild: %v", err)
	}

	// First claim wins.
	won, err := m.ClaimQueuedBuild(ctx, b.ID)
	if err != nil {
		t.Fatalf("first ClaimQueuedBuild: %v", err)
	}
	if won.Status != BuildRunning {
		t.Errorf("first claim status = %q, want running", won.Status)
	}
	if won.StartedAt.IsZero() {
		t.Errorf("first claim started_at is zero")
	}

	// Second claim loses — row is no longer queued.
	_, err = m.ClaimQueuedBuild(ctx, b.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("second claim err = %v, want ErrNotFound", err)
	}

	// Unknown id loses the same way.
	_, err = m.ClaimQueuedBuild(ctx, "deadbeef")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id err = %v, want ErrNotFound", err)
	}
}

// TestMemStore_CreateDeployment_SupersedesPriorLive mirrors the
// PgStore supersede happy-path. Two pending-style deployments go
// through; the second must observe the prior as superseded in the
// store map (CreateDeployment's 2-return shape carries the new row
// only; the prior is read back via DeploymentByID to assert).
func TestMemStore_CreateDeployment_SupersedesPriorLive(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "sup@x.com", api.PlanPro)
	app, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "sup-app"})

	d1, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:1"})
	if err != nil {
		t.Fatalf("d1: %v", err)
	}
	d2, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:2"})
	if err != nil {
		t.Fatalf("d2: %v", err)
	}
	// Map must agree: d1 (the prior) is now DeploySuperseded.
	if m.deployments[d1.ID].Status != DeploySuperseded {
		t.Errorf("m.deployments[%s].Status = %q, want superseded", d1.ID, m.deployments[d1.ID].Status)
	}
	// And d2 is the new pending row.
	if d2.Status != DeployPending {
		t.Errorf("d2.Status = %q, want pending", d2.Status)
	}
}

// TestMemStore_CreateDeployment_LeavesBuildingRowAlone pins the
// M-1 invariant in the MemStore: a building row is NOT superseded by
// a subsequent deploy. Mirrors pgstore_test.go's same-named test.
func TestMemStore_CreateDeployment_LeavesBuildingRowAlone(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, _ := m.CreateAccount(ctx, "bld@x.com", api.PlanPro)
	app, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "bld-app"})

	d1, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:1"})
	if err != nil {
		t.Fatalf("d1: %v", err)
	}
	// Flip d1 to building (simulating builderd mid-pipeline).
	d1.Status = DeployBuilding
	m.deployments[d1.ID] = d1

	d2, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:2"})
	if err != nil {
		t.Fatalf("d2: %v", err)
	}
	if d2.Status != DeployPending {
		t.Errorf("d2.Status = %q, want pending", d2.Status)
	}
	if m.deployments[d1.ID].Status != DeployBuilding {
		t.Errorf("d1.Status = %q, want building (untouched)", m.deployments[d1.ID].Status)
	}
}

// TestMemStore_ListLatestInstancesForApp_BoundedLimit asserts the
// dashboard's "Recent wakes" path returns at most `limit` rows and
// that limit ≤ 0 fails closed (empty slice, not unbounded). Added
// alongside the bounded SQL path for the dashboard per gaps analysis
// 2026-07-23 review finding #5 — the previous Go-side sort on
// ListInstancesForApp was unbounded for long-lived apps.
func TestMemStore_ListLatestInstancesForApp_BoundedLimit(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "listlim@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: "listlim", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := m.CreateDeployment(ctx, Deployment{
		AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:abc", Status: DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := m.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}

	// Seed 5 instances for this app — they all share the same
	// started_at second so the sort order between them is stable
	// (sorted by StartedAt after write); the bounded path must
	// still return exactly `limit` rows.
	for i := 0; i < 5; i++ {
		_, err := m.CreateInstance(ctx, app.ID, dep.ID, "parked", 256, DefaultLocalNodeName, "")
		if err != nil {
			t.Fatalf("CreateInstance %d: %v", i, err)
		}
	}

	// limit=3 returns exactly 3 rows.
	rows, err := m.ListLatestInstancesForApp(ctx, app.ID, 3)
	if err != nil {
		t.Fatalf("ListLatestInstancesForApp(3): %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("rows = %d, want 3 (limit bound)", len(rows))
	}

	// limit=0 and limit=-1 fail closed.
	for _, lim := range []int{0, -1} {
		rows, err := m.ListLatestInstancesForApp(ctx, app.ID, lim)
		if err != nil {
			t.Fatalf("ListLatestInstancesForApp(%d): %v", lim, err)
		}
		if len(rows) != 0 {
			t.Errorf("limit=%d returned %d rows, want 0 (fail-closed)", lim, len(rows))
		}
	}

	// limit larger than the row count returns everything, no error.
	rows, err = m.ListLatestInstancesForApp(ctx, app.ID, 50)
	if err != nil {
		t.Fatalf("ListLatestInstancesForApp(50): %v", err)
	}
	if len(rows) != 5 {
		t.Errorf("rows = %d, want 5 (all)", len(rows))
	}
}

// TestAccountByProviderCustomerID_Mirror asserts the Paddle mirror of
// AccountByProviderCustomerID is a 1-line pass-through that reads from
// the same reverse-lookup map (accounts.provider_customer_id is reused
// per ADR-025). The test exercises the full write→read round-trip:
// UpdateAccountProviderCustomerID writes ctm_xyz, AccountByProviderCustomerID
// reads it back, and AccountByProviderCustomerID returns the same account
// (the column is shared — the dedicated Paddle method name is just a
// self-documenting alias for the same body).
func TestAccountByProviderCustomerID_Mirror(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMemStore()
	acct, err := m.CreateAccount(ctx, "u-paddle@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := m.UpdateAccountProviderCustomerID(ctx, acct.ID, "ctm_test_xyz"); err != nil {
		t.Fatalf("UpdateAccountProviderCustomerID: %v", err)
	}

	got, err := m.AccountByProviderCustomerID(ctx, "ctm_test_xyz")
	if err != nil {
		t.Fatalf("AccountByProviderCustomerID: %v", err)
	}
	if got.ID != acct.ID {
		t.Errorf("ID = %q, want %q", got.ID, acct.ID)
	}
	if got.ProviderCustomerID != "ctm_test_xyz" {
		t.Errorf("ProviderCustomerID = %q, want ctm_test_xyz (column reused per ADR-025)", got.ProviderCustomerID)
	}

	// The Stripe-side mirror must return the same account — proves
	// the index is genuinely shared (the dedicated Paddle method name
	// is documentation, not a different index).
	stripeSame, err := m.AccountByProviderCustomerID(ctx, "ctm_test_xyz")
	if err != nil {
		t.Fatalf("AccountByProviderCustomerID (mirror): %v", err)
	}
	if stripeSame.ID != acct.ID {
		t.Errorf("Stripe mirror ID = %q, want %q", stripeSame.ID, acct.ID)
	}

	// Unknown ID returns ErrNotFound — pins the negative case so a
	// future refactor that aliases the bodies can't silently swallow
	// it.
	if _, err := m.AccountByProviderCustomerID(ctx, "ctm_does_not_exist"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id err = %v, want ErrNotFound", err)
	}
}

// TestMemStore_UpdateApp_AutoscaleSetBits pins the Set-bit
// semantics for the autoscale trigger targets (issue #169 / #172).
// The Set bits distinguish "unset" (don't touch the column) from
// "explicit zero" (disable). Without this guard, a PATCH with
// autoscale_target_rps omitted would silently write 0 and disable
// an existing trigger. Three cases:
//
//  1. Set + non-zero → column updated to the value.
//  2. Set + zero     → column updated to 0 (explicit disable).
//  3. Not Set (nil)  → column unchanged.
//
// The third case is the load-bearing one — the apid handler
// branches on the JSON field's presence, so a future refactor that
// drops the Set bit (or that writes 0 unconditionally when nil)
// would silently disable every customer's existing autoscale
// target. Round-trip via AppByID to catch the pgstore drift.
func TestMemStore_UpdateApp_AutoscaleSetBits(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	acc, err := m.CreateAccount(ctx, "scaleup@x.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{
		AccountID: acc.ID, Slug: "scaleup-app", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 5, IdleTimeoutS: 60,
		Status:                AppActive,
		AutoscaleTargetRPS:    50,
		AutoscaleTargetCPUPct: 70,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	// Case 1: Set + non-zero → column updated.
	updated, err := m.UpdateApp(ctx, app.ID, UpdateAppParams{
		AutoscaleTargetRPS:    ptrInt(80),
		SetAutoscaleTargetRPS: true,
	})
	if err != nil {
		t.Fatalf("UpdateApp (Set + 80): %v", err)
	}
	if updated.AutoscaleTargetRPS != 80 {
		t.Errorf("AutoscaleTargetRPS = %d, want 80", updated.AutoscaleTargetRPS)
	}
	// CPU untouched (Set=false on CPU).
	if updated.AutoscaleTargetCPUPct != 70 {
		t.Errorf("AutoscaleTargetCPUPct = %d, want 70 (untouched)", updated.AutoscaleTargetCPUPct)
	}

	// Case 2: Set + zero → explicit disable.
	updated, err = m.UpdateApp(ctx, app.ID, UpdateAppParams{
		AutoscaleTargetCPUPct:    ptrInt(0),
		SetAutoscaleTargetCPUPct: true,
	})
	if err != nil {
		t.Fatalf("UpdateApp (Set + 0): %v", err)
	}
	if updated.AutoscaleTargetCPUPct != 0 {
		t.Errorf("AutoscaleTargetCPUPct = %d, want 0 (explicit disable)", updated.AutoscaleTargetCPUPct)
	}
	// RPS untouched.
	if updated.AutoscaleTargetRPS != 80 {
		t.Errorf("AutoscaleTargetRPS = %d, want 80 (untouched)", updated.AutoscaleTargetRPS)
	}

	// Case 3: Not Set → column unchanged (the load-bearing case).
	// Patch something else entirely; verify autoscale columns survive.
	updated, err = m.UpdateApp(ctx, app.ID, UpdateAppParams{
		MaxConcurrency: ptrInt(10),
	})
	if err != nil {
		t.Fatalf("UpdateApp (no Set): %v", err)
	}
	if updated.MaxConcurrency != 10 {
		t.Errorf("MaxConcurrency = %d, want 10", updated.MaxConcurrency)
	}
	if updated.AutoscaleTargetRPS != 80 {
		t.Errorf("AutoscaleTargetRPS = %d, want 80 (survived PATCH without Set)", updated.AutoscaleTargetRPS)
	}
	if updated.AutoscaleTargetCPUPct != 0 {
		t.Errorf("AutoscaleTargetCPUPct = %d, want 0 (survived PATCH without Set)", updated.AutoscaleTargetCPUPct)
	}

	// Round-trip via AppByID — proves the same shape hydrates on
	// the read path (catches the "wrote to RETURNING but the SELECT
	// forgot to add the column" class of bug).
	loaded, err := m.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if loaded.AutoscaleTargetRPS != 80 {
		t.Errorf("loaded AutoscaleTargetRPS = %d, want 80", loaded.AutoscaleTargetRPS)
	}
	if loaded.AutoscaleTargetCPUPct != 0 {
		t.Errorf("loaded AutoscaleTargetCPUPct = %d, want 0", loaded.AutoscaleTargetCPUPct)
	}
}

// ptrInt is a tiny helper to take the address of an int literal
// (the UpdateAppParams fields are *int).
func ptrInt(v int) *int { return &v }

// --- MFA (IAM-2, issue #186) -------------------------------------------------
//
// The MemStore is the in-memory parity for PgStore; the same
// behaviour tests here are the MemStore's claim that PgStore would
// pass the equivalent pgtest run. The pgtest run is gated behind
// `make migrations-check` and isn't part of the unit loop, so the
// docs for these tests live in pkg/state/pgstore.go and the actual
// pgx-level proof is the migration-level idempotence test in
// migrations/00049_account_mfa_test.go.

// TestConsumeRecoveryCode_HappyPath drives the canonical success
// path: a freshly-enrolled account with 10 hashes; the consumer
// presents a hash that matches one of them; that hash is removed,
// the other nine remain, and the sealed TOTP secret is preserved
// (so the customer can still /verify after the burn).
func TestConsumeRecoveryCode_HappyPath(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "mfa-happy@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	plaintexts, hashes, err := authcode.NewRecoveryCodes(authcode.RecoveryCodeCount)
	if err != nil {
		t.Fatalf("NewRecoveryCodes: %v", err)
	}
	sealed := []byte("sealed-blob-test")
	if err := m.SetMFASecret(ctx, acct.ID, sealed, hashes); err != nil {
		t.Fatalf("SetMFASecret: %v", err)
	}

	presented, err := authcode.HashRecoveryCode(plaintexts[3])
	if err != nil {
		t.Fatalf("HashRecoveryCode: %v", err)
	}
	matched, lastCode, remaining, err := m.ConsumeRecoveryCode(ctx, acct.ID, presented)
	if err != nil {
		t.Fatalf("ConsumeRecoveryCode: %v", err)
	}
	if !matched {
		t.Fatalf("matched = false, want true")
	}
	if lastCode {
		t.Errorf("lastCode = true, want false (10 codes started, 1 burned, 9 remain)")
	}
	if remaining != authcode.RecoveryCodeCount-1 {
		t.Errorf("remaining = %d, want %d (issue #329: mailer needs the count)", remaining, authcode.RecoveryCodeCount-1)
	}

	after, err := m.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatalf("AccountByID: %v", err)
	}
	if len(after.MFARecoveryCodesHash) != authcode.RecoveryCodeCount-1 {
		t.Errorf("remaining hashes = %d, want %d", len(after.MFARecoveryCodesHash), authcode.RecoveryCodeCount-1)
	}
	// Sealed secret must survive the burn (UpdateMFARecoveryCodes
	// would have been the wrong call — it's preserved by the new
	// primitive).
	if string(after.MFASecretEncrypted) != string(sealed) {
		t.Errorf("MFASecretEncrypted changed across consume: got %q, want %q", after.MFASecretEncrypted, sealed)
	}
	// Burned hash must not still appear in the slice.
	for i, h := range after.MFARecoveryCodesHash {
		if Sha256Equal(h, presented) {
			t.Errorf("burned hash still present at index %d", i)
		}
	}
}

// TestConsumeRecoveryCode_NoMatch returns matched=false and leaves
// the slice unchanged. A wrong code is the most common operational
// case (typo in the customer's typing) and must not mutate state.
func TestConsumeRecoveryCode_NoMatch(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "mfa-nomatch@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	_, hashes, _ := authcode.NewRecoveryCodes(authcode.RecoveryCodeCount)
	if err := m.SetMFASecret(ctx, acct.ID, []byte("sealed"), hashes); err != nil {
		t.Fatalf("SetMFASecret: %v", err)
	}

	matched, lastCode, remaining, err := m.ConsumeRecoveryCode(ctx, acct.ID, []byte("not-a-real-hash"))
	if err != nil {
		t.Fatalf("ConsumeRecoveryCode: %v", err)
	}
	if matched {
		t.Errorf("matched = true, want false")
	}
	if lastCode {
		t.Errorf("lastCode = true, want false on a no-match")
	}
	if remaining != 0 {
		t.Errorf("remaining = %d, want 0 on a no-match (issue #329 contract)", remaining)
	}
	after, _ := m.AccountByID(ctx, acct.ID)
	if len(after.MFARecoveryCodesHash) != authcode.RecoveryCodeCount {
		t.Errorf("hash slice size = %d, want %d (no-match must not mutate)", len(after.MFARecoveryCodesHash), authcode.RecoveryCodeCount)
	}
}

// TestConsumeRecoveryCode_LastCodeDetected burns codes until 1
// remains, then asserts the next consume reports lastCode=true. The
// /recover handler uses this signal to refuse the burn and prompt
// the customer for the password fallback instead — burning the
// last code would lock the customer out.
func TestConsumeRecoveryCode_LastCodeDetected(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "mfa-lastcode@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	plaintexts, hashes, _ := authcode.NewRecoveryCodes(authcode.RecoveryCodeCount)
	if err := m.SetMFASecret(ctx, acct.ID, []byte("sealed"), hashes); err != nil {
		t.Fatalf("SetMFASecret: %v", err)
	}

	// Burn 9 of the 10 codes. Each consume reports
	// lastCode=false (because more than one remains) AND a
	// monotonically-decreasing `remaining` count (issue #329
	// wires `remaining` to the mailer tone bucket).
	for i := 0; i < authcode.RecoveryCodeCount-1; i++ {
		presented, err := authcode.HashRecoveryCode(plaintexts[i])
		if err != nil {
			t.Fatalf("HashRecoveryCode[%d]: %v", i, err)
		}
		matched, lastCode, remaining, err := m.ConsumeRecoveryCode(ctx, acct.ID, presented)
		if err != nil {
			t.Fatalf("burn %d: %v", i, err)
		}
		if !matched || lastCode {
			t.Errorf("burn %d: matched=%v lastCode=%v, want true/false", i, matched, lastCode)
		}
		if want := authcode.RecoveryCodeCount - 1 - i; remaining != want {
			t.Errorf("burn %d: remaining = %d, want %d", i, remaining, want)
		}
	}

	// The 10th burn is the last code. lastCode must be true
	// AND remaining must drop to 0 — issue #329's "NO codes
	// left" branch only fires via /disable's recovery_code path,
	// but the store primitive must still report the post-burn
	// count honestly so the handler can branch correctly.
	presented, err := authcode.HashRecoveryCode(plaintexts[authcode.RecoveryCodeCount-1])
	if err != nil {
		t.Fatalf("HashRecoveryCode: %v", err)
	}
	matched, lastCode, remaining, err := m.ConsumeRecoveryCode(ctx, acct.ID, presented)
	if err != nil {
		t.Fatalf("last burn: %v", err)
	}
	if !matched || !lastCode {
		t.Errorf("last burn: matched=%v lastCode=%v, want true/true", matched, lastCode)
	}
	if remaining != 0 {
		t.Errorf("last burn: remaining = %d, want 0", remaining)
	}

	after, _ := m.AccountByID(ctx, acct.ID)
	if len(after.MFARecoveryCodesHash) != 0 {
		t.Errorf("remaining hashes = %d, want 0", len(after.MFARecoveryCodesHash))
	}
}

// TestConsumeRecoveryCode_RaceProtectsAgainstDoubleBurn fires two
// concurrent goroutines presenting the SAME recovery code. The
// atomic store contract is: exactly one matches and burns, the
// other observes matched=false. MemStore holds m.mu over the whole
// read+compare+write so this is automatic; PgStore relies on
// SELECT … FOR UPDATE on accounts to enforce the same invariant.
func TestConsumeRecoveryCode_RaceProtectsAgainstDoubleBurn(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "mfa-race@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	plaintexts, hashes, _ := authcode.NewRecoveryCodes(authcode.RecoveryCodeCount)
	if err := m.SetMFASecret(ctx, acct.ID, []byte("sealed"), hashes); err != nil {
		t.Fatalf("SetMFASecret: %v", err)
	}
	presented, err := authcode.HashRecoveryCode(plaintexts[5])
	if err != nil {
		t.Fatalf("HashRecoveryCode: %v", err)
	}

	type result struct{ matched, lastCode bool }
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			matched, lastCode, _, err := m.ConsumeRecoveryCode(ctx, acct.ID, presented)
			if err != nil {
				t.Errorf("ConsumeRecoveryCode: %v", err)
			}
			results <- result{matched, lastCode}
		}()
	}

	var matchCount int
	for i := 0; i < 2; i++ {
		r := <-results
		if r.matched {
			matchCount++
		}
	}
	if matchCount != 1 {
		t.Errorf("matchCount = %d, want 1 (exactly one of two racing consumes must burn)", matchCount)
	}
	after, _ := m.AccountByID(ctx, acct.ID)
	if len(after.MFARecoveryCodesHash) != authcode.RecoveryCodeCount-1 {
		t.Errorf("remaining hashes = %d, want %d", len(after.MFARecoveryCodesHash), authcode.RecoveryCodeCount-1)
	}
}

// TestSha256Equal pins the constant-time compare used by the
// recovery-code consume path. The short-circuit on len-mismatch is
// NOT a timing leak (the stored hash length is a public 32 bytes),
// so the early return is safe.
func TestSha256Equal(t *testing.T) {
	a := []byte("01234567890123456789012345678901")
	if !Sha256Equal(a, a) {
		t.Errorf("Sha256Equal(a, a) = false, want true")
	}
	if Sha256Equal(a, []byte("01234567890123456789012345678900")) {
		t.Errorf("Sha256Equal accepted a 1-bit difference")
	}
	if Sha256Equal(a, a[:31]) {
		t.Errorf("Sha256Equal accepted a length mismatch")
	}
	if !Sha256Equal(nil, nil) {
		t.Errorf("Sha256Equal(nil, nil) = false, want true (both zero-length)")
	}
	if !Sha256Equal([]byte{}, []byte{}) {
		t.Errorf("Sha256Equal(empty, empty) = false, want true")
	}
}

// TestSetMFARequired_ChangedReportsRealWrites confirms the new
// (changed bool, err error) return shape differentiates a real
// write from a same-value no-op. Policy callers can use the changed
// result to avoid duplicate audit records on repeated requests.
func TestSetMFARequired_ChangedReportsRealWrites(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "mfa-req@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	// First write: false → true. Must report changed=true.
	changed, err := m.SetMFARequired(ctx, acct.ID, true)
	if err != nil {
		t.Fatalf("SetMFARequired(true): %v", err)
	}
	if !changed {
		t.Errorf("first write changed = false, want true")
	}

	// Second write: true → true. Must report changed=false so a
	// repeated policy request can suppress a duplicate audit Emit.
	changed, err = m.SetMFARequired(ctx, acct.ID, true)
	if err != nil {
		t.Fatalf("SetMFARequired(true) repeat: %v", err)
	}
	if changed {
		t.Errorf("repeat write changed = true, want false")
	}

	// Third write: true → false. Must report changed=true.
	changed, err = m.SetMFARequired(ctx, acct.ID, false)
	if err != nil {
		t.Fatalf("SetMFARequired(false): %v", err)
	}
	if !changed {
		t.Errorf("flip-back write changed = false, want true")
	}
}

// TestMatchRecoveryCode_NoMutation pins the read-only contract.
// /recover's refuse-the-burn step relies on MatchRecoveryCode to
// tell us whether the presented code matches AND whether it's
// the last remaining one — both signals must be derivable
// WITHOUT modifying the stored slice. Without this contract,
// the refuse path would still see the burn committed by the
// prior ConsumeRecoveryCode call shape (issue #186 review
// Finding #5 — the consume-then-notice-lastCode shape that
// left the customer with zero codes despite the 409 wire
// response).
func TestMatchRecoveryCode_NoMutation(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "mfa-match@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	plaintexts, hashes, _ := authcode.NewRecoveryCodes(authcode.RecoveryCodeCount)
	if err := m.SetMFASecret(ctx, acct.ID, []byte("sealed"), hashes); err != nil {
		t.Fatalf("SetMFASecret: %v", err)
	}
	// Match the first code.
	presented, err := authcode.HashRecoveryCode(plaintexts[0])
	if err != nil {
		t.Fatalf("HashRecoveryCode: %v", err)
	}
	matched, lastCode, err := m.MatchRecoveryCode(ctx, acct.ID, presented)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if !matched {
		t.Errorf("matched = false, want true")
	}
	if lastCode {
		t.Errorf("lastCode = true on N=10 store, want false")
	}

	// Crucially: the stored hashes are unchanged.
	after, _ := m.AccountByID(ctx, acct.ID)
	if got := len(after.MFARecoveryCodesHash); got != authcode.RecoveryCodeCount {
		t.Errorf("codes after Match = %d, want %d (Match must not mutate)", got, authcode.RecoveryCodeCount)
	}
	// And a follow-up Consume on the SAME code still burns
	// normally — confirms Match didn't pre-empt it.
	matched2, _, _, err := m.ConsumeRecoveryCode(ctx, acct.ID, presented)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if !matched2 {
		t.Errorf("Consume after Match: matched = false, want true")
	}
}

// TestMatchRecoveryCode_LastCodeFlag pins the (matched=true,
// lastCode=true) shape when the store has exactly one hash.
// /recover's lastCode refusal branch depends on this — a
// regression to lastCode=false would let the consume proceed
// and orphan the customer.
func TestMatchRecoveryCode_LastCodeFlag(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "mfa-matchlast@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	plaintexts, hashes, _ := authcode.NewRecoveryCodes(1)
	if err := m.SetMFASecret(ctx, acct.ID, []byte("sealed"), hashes); err != nil {
		t.Fatalf("SetMFASecret: %v", err)
	}

	presented, err := authcode.HashRecoveryCode(plaintexts[0])
	if err != nil {
		t.Fatalf("HashRecoveryCode: %v", err)
	}
	matched, lastCode, err := m.MatchRecoveryCode(ctx, acct.ID, presented)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if !matched {
		t.Errorf("matched = false, want true")
	}
	if !lastCode {
		t.Errorf("lastCode = false on N=1 store, want true")
	}

	// Confirm the array remains untouched after the refuse path.
	after, _ := m.AccountByID(ctx, acct.ID)
	if got := len(after.MFARecoveryCodesHash); got != 1 {
		t.Errorf("codes after Match = %d, want 1 (refuse must not burn)", got)
	}
}

// TestMatchRecoveryCode_NoMatch returns matched=false without
// ever reaching the lastCode branch.
func TestMatchRecoveryCode_NoMatch(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "mfa-matchmiss@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	_, hashes, _ := authcode.NewRecoveryCodes(3)
	if err := m.SetMFASecret(ctx, acct.ID, []byte("sealed"), hashes); err != nil {
		t.Fatalf("SetMFASecret: %v", err)
	}
	presented, err := authcode.HashRecoveryCode("DEFINITELY-NOT-A-STORED-CODE")
	if err != nil {
		t.Fatalf("HashRecoveryCode: %v", err)
	}
	matched, lastCode, err := m.MatchRecoveryCode(ctx, acct.ID, presented)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if matched {
		t.Errorf("matched = true on bogus code, want false")
	}
	if lastCode {
		t.Errorf("lastCode = true on miss, want false (lastCode only valid when matched)")
	}
}

// --- Cron quota errors (PR #340 follow-ups) ---------------------------------

// TestCronQuotaError_IsRoundTrip pins the contract the store.go doc
// comment promises: errors.Is(err, ErrCronQuotaExceeded) matches any
// *CronQuotaError, and errors.As(err, &qe) recovers the typed value
// with Scope / Limit / Observed intact. handlers_ext.go's createCron
// branch relies on both — losing either is a 5xx regression in the
// quota path.
func TestCronQuotaError_IsRoundTrip(t *testing.T) {
	appErr := &CronQuotaError{
		Scope:    CronQuotaScopeApp,
		Limit:    20,
		Observed: 20,
	}
	if !errors.Is(appErr, ErrCronQuotaExceeded) {
		t.Error("errors.Is(appErr, ErrCronQuotaExceeded) = false, want true")
	}
	var qe *CronQuotaError
	if !errors.As(appErr, &qe) {
		t.Fatal("errors.As failed to recover *CronQuotaError")
	}
	if qe.Scope != CronQuotaScopeApp || qe.Limit != 20 || qe.Observed != 20 {
		t.Errorf("typed value mismatch: scope=%q limit=%d observed=%d", qe.Scope, qe.Limit, qe.Observed)
	}

	// Wrapped error (via fmt.Errorf("… %w", e)) must still match the
	// sentinel — handlers may decorate before returning.
	wrapped := fmt.Errorf("decorated: %w", appErr)
	if !errors.Is(wrapped, ErrCronQuotaExceeded) {
		t.Error("errors.Is on wrapped *CronQuotaError = false, want true")
	}
	if !errors.As(wrapped, &qe) || qe.Scope != CronQuotaScopeApp {
		t.Error("errors.As on wrapped *CronQuotaError did not recover typed value")
	}
}

// TestCreateCronIfUnderQuota_PerAppArm exercises the customer-facing
// cap: at CronLimitPerApp, CreateCronIfUnderQuota returns
// *CronQuotaError{Scope: CronQuotaScopeApp} with Limit/Observed pinned.
// MemStore predicate mirrors PgStore (single critical section under
// m.mu) so the typed error's shape is the contract both backends
// honour.
func TestCreateCronIfUnderQuota_PerAppArm(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "cron-app@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{AccountID: acct.ID, Slug: "a", Type: AppTypeFunction})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	limits := api.MustLimitsFor(api.PlanPro) // Pro: 20/app, 50/acct
	for i := 0; i < limits.CronLimitPerApp; i++ {
		if _, err := m.CreateCronIfUnderQuota(ctx, app.ID, "*/5 * * * *", "/x", true, limits); err != nil {
			t.Fatalf("seed cron %d: %v", i, err)
		}
	}
	_, err = m.CreateCronIfUnderQuota(ctx, app.ID, "*/5 * * * *", "/x", true, limits)
	if err == nil {
		t.Fatal("expected *CronQuotaError at per-app cap, got nil")
	}
	var qe *CronQuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("expected *CronQuotaError, got %T: %v", err, err)
	}
	if qe.Scope != CronQuotaScopeApp {
		t.Errorf("scope = %q, want %q", qe.Scope, CronQuotaScopeApp)
	}
	if qe.Limit != limits.CronLimitPerApp {
		t.Errorf("limit = %d, want %d", qe.Limit, limits.CronLimitPerApp)
	}
	if qe.Observed != limits.CronLimitPerApp {
		t.Errorf("observed = %d, want %d", qe.Observed, limits.CronLimitPerApp)
	}
}

// TestCreateCronIfUnderQuota_PerAccountArm pins the per-account cap:
// three apps with N1+N2+N3 crons reaching CronLimitPerAccount trips
// the account arm even when the target app's per-app count is still
// under. Scope must read "account" so the handler renders "delete
// from a sibling app" copy, not "delete from THIS app".
//
// Boundary math (Pro: per-app=20, per-account=50): seed 19 on appA,
// 19 on appB, 12 on appC → total 50 (== cap). The 51st insert on
// appC trips per-account with appC's per-app count still at 12/20.
func TestCreateCronIfUnderQuota_PerAccountArm(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "cron-acct@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	limits := api.MustLimitsFor(api.PlanPro) // 20/app, 50/acct
	appA, err := m.CreateApp(ctx, App{AccountID: acct.ID, Slug: "a", Type: AppTypeFunction})
	if err != nil {
		t.Fatalf("CreateApp A: %v", err)
	}
	appB, err := m.CreateApp(ctx, App{AccountID: acct.ID, Slug: "b", Type: AppTypeFunction})
	if err != nil {
		t.Fatalf("CreateApp B: %v", err)
	}
	appC, err := m.CreateApp(ctx, App{AccountID: acct.ID, Slug: "c", Type: AppTypeFunction})
	if err != nil {
		t.Fatalf("CreateApp C: %v", err)
	}
	for i := 0; i < limits.CronLimitPerApp-1; i++ {
		if _, err := m.CreateCronIfUnderQuota(ctx, appA.ID, "*/5 * * * *", "/x", true, limits); err != nil {
			t.Fatalf("seed appA %d: %v", i, err)
		}
	}
	for i := 0; i < limits.CronLimitPerApp-1; i++ {
		if _, err := m.CreateCronIfUnderQuota(ctx, appB.ID, "*/5 * * * *", "/x", true, limits); err != nil {
			t.Fatalf("seed appB %d: %v", i, err)
		}
	}
	fillC := limits.CronLimitPerAccount - 2*(limits.CronLimitPerApp-1)
	for i := 0; i < fillC; i++ {
		if _, err := m.CreateCronIfUnderQuota(ctx, appC.ID, "*/5 * * * *", "/x", true, limits); err != nil {
			t.Fatalf("seed appC %d: %v", i, err)
		}
	}
	// Per-account is now 50 == cap. Per-app on appC is fillC/20, under.
	// Next insert on appC must trip the per-account arm.
	_, err = m.CreateCronIfUnderQuota(ctx, appC.ID, "*/5 * * * *", "/x", true, limits)
	if err == nil {
		t.Fatal("expected *CronQuotaError at per-account cap, got nil")
	}
	var qe *CronQuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("expected *CronQuotaError, got %T: %v", err, err)
	}
	if qe.Scope != CronQuotaScopeAccount {
		t.Errorf("scope = %q, want %q (per-account is at cap; per-app on appC still has room)", qe.Scope, CronQuotaScopeAccount)
	}
	if qe.Limit != limits.CronLimitPerAccount {
		t.Errorf("limit = %d, want %d", qe.Limit, limits.CronLimitPerAccount)
	}
	if qe.Observed != limits.CronLimitPerAccount {
		t.Errorf("observed = %d, want %d", qe.Observed, limits.CronLimitPerAccount)
	}
}

// IAM-3 (ADR-036, issue #187 + #244 merged) MemStore parity tests
// for the six session methods. The pgstore tests live in
// pgstore_test.go (under the same suite). These five cover the
// in-memory parity surface — the production store ships from
// pgstore but the dashboard tests + the cmd/e2e harness go
// through MemStore, so behaviour drift would surface here.

// TestMemStore_Session_RoundTripAndConflict pins CreateSession's
// (id uniqueness) + GetSession round-trip + the (issued_ip,
// issued_ua) read-back. The empty "" IP/UA path is the RemoteAddr
// unparseable + no-User-Agent surface.
func TestMemStore_Session_RoundTripAndConflict(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	acct, err := m.CreateAccount(ctx, "alice@example.com", "free")
	if err != nil {
		t.Fatal(err)
	}
	sid1 := "11111111-1111-1111-1111-111111111111"
	s1, err := m.CreateSession(ctx, sid1, acct.ID, "192.0.2.10", "Mozilla/5.0 iam3-test")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if s1.ID != sid1 || s1.AccountID != acct.ID || s1.IssuedIP != "192.0.2.10" || s1.IssuedUA != "Mozilla/5.0 iam3-test" {
		t.Errorf("returned row mismatch: %+v", s1)
	}
	if s1.RevokedAt != nil {
		t.Errorf("fresh row should not be revoked: %+v", s1)
	}
	if s1.LastSeenAt != nil {
		t.Errorf("fresh row should have nil last_seen_at: %+v", s1)
	}

	// GetSession round-trip.
	got, err := m.GetSession(ctx, sid1)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.ID != sid1 || got.AccountID != acct.ID || got.IssuedIP != "192.0.2.10" {
		t.Errorf("GetSession returned: %+v", got)
	}

	// Empty IP/UA path is the RemoteAddr-unparseable surface.
	if _, err := m.CreateSession(ctx, "22222222-2222-2222-2222-222222222222", acct.ID, "", ""); err != nil {
		t.Errorf("CreateSession with empty IP/UA: %v", err)
	}
	gotEmpty, err := m.GetSession(ctx, "22222222-2222-2222-2222-222222222222")
	if err != nil {
		t.Fatalf("GetSession on empty-ip row: %v", err)
	}
	if gotEmpty.IssuedIP != "" || gotEmpty.IssuedUA != "" {
		t.Errorf("empty ip/ua round-trip wrong: %+v", gotEmpty)
	}

	// ErrConflict on duplicate sid.
	if _, err := m.CreateSession(ctx, sid1, acct.ID, "1.1.1.1", "u"); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate sid err = %v, want ErrConflict", err)
	}

	// CreateSession against a missing account returns ErrNotFound.
	if _, err := m.CreateSession(ctx, "33333333-3333-3333-3333-333333333333", "00000000-0000-0000-0000-000000000000", "", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing-account CreateSession err = %v, want ErrNotFound", err)
	}

	// Missing sid GetSession returns ErrNotFound.
	if _, err := m.GetSession(ctx, "99999999-9999-9999-9999-999999999999"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing sid GetSession err = %v, want ErrNotFound", err)
	}
}

// TestMemStore_Session_RevokeScoping pins the IAM-3 IDOR guard:
// RevokeSession's account_id predicate. A cross-account revoke
// returns false (the handler maps to 404); a same-account revoke
// flips the row + returns true; a second revoke returns false
// (idempotence, per the coalesce(revoked_at, now()) SQL idiom).
func TestMemStore_Session_RevokeScoping(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	alice, _ := m.CreateAccount(ctx, "alice@example.com", "free")
	bob, _ := m.CreateAccount(ctx, "bob@example.com", "free")
	const sidAlice = "11111111-1111-1111-1111-111111111111"

	if _, err := m.CreateSession(ctx, sidAlice, alice.ID, "192.0.2.10", "u"); err != nil {
		t.Fatal(err)
	}

	// Cross-account revoke returns false (handler maps to 404).
	ok, err := m.RevokeSession(ctx, sidAlice, bob.ID)
	if err != nil {
		t.Fatalf("RevokeSession cross-account err = %v, want nil", err)
	}
	if ok {
		t.Errorf("cross-account revoke returned true; want false (IDOR guard)")
	}

	// Same-account revoke flips + returns true.
	ok, err = m.RevokeSession(ctx, sidAlice, alice.ID)
	if err != nil {
		t.Fatalf("RevokeSession same-account: %v", err)
	}
	if !ok {
		t.Errorf("same-account revoke returned false; want true")
	}
	got, _ := m.GetSession(ctx, sidAlice)
	if got.RevokedAt == nil {
		t.Errorf("post-revoke RevokedAt == nil")
	}

	// Revoking a never-existing sid returns false (handler 404).
	ok, _ = m.RevokeSession(ctx, "99999999-9999-9999-9999-999999999999", alice.ID)
	if ok {
		t.Errorf("missing-sid RevokeSession returned true; want false")
	}
}

// TestMemStore_Session_ListFiltersAndOrders pins ListSessions:
//   - revoked rows are excluded
//   - active rows are returned newest-first
//   - cross-account sessions are invisible
func TestMemStore_Session_ListFiltersAndOrders(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	alice, _ := m.CreateAccount(ctx, "alice@example.com", "free")
	bob, _ := m.CreateAccount(ctx, "bob@example.com", "free")

	// Three alice rows, one revoked.
	for i, sid := range []string{
		"11111111-1111-1111-1111-111111111111",
		"22222222-2222-2222-2222-222222222222",
		"33333333-3333-3333-3333-333333333333",
	} {
		if _, err := m.CreateSession(ctx, sid, alice.ID, "192.0.2.10", "u"); err != nil {
			t.Fatal(err)
		}
		// Force a distinct IssuedAt so the order assertion is
		// deterministic across the fast CI clock. Stamping in
		// reverse keeps "first inserted" = "last listed" out of
		// the test surface.
		m.mu.Lock()
		row := m.sessions[sid]
		row.IssuedAt = time.Unix(int64(1700000000+i), 0).UTC()
		m.sessions[sid] = row
		m.mu.Unlock()
	}
	// One bob row — must not leak into alice's list.
	if _, err := m.CreateSession(ctx, "44444444-4444-4444-4444-444444444444", bob.ID, "192.0.2.20", "u"); err != nil {
		t.Fatal(err)
	}
	// Revoke the second alice row (middle, would otherwise land
	// in the middle of the order assertion).
	if _, err := m.RevokeSession(ctx, "22222222-2222-2222-2222-222222222222", alice.ID); err != nil {
		t.Fatal(err)
	}

	list, err := m.ListSessions(ctx, alice.ID)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2 (revoked + cross-account excluded)", len(list))
	}
	// Newest first: row 33333333 (IssuedAt=1700000002) before
	// row 11111111 (IssuedAt=1700000000).
	if list[0].ID != "33333333-3333-3333-3333-333333333333" {
		t.Errorf("list[0].ID = %q, want 33333333...", list[0].ID)
	}
	if list[1].ID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("list[1].ID = %q, want 11111111...", list[1].ID)
	}
	for _, row := range list {
		if row.AccountID != alice.ID {
			t.Errorf("cross-account row leaked: %+v", row)
		}
		if row.RevokedAt != nil {
			t.Errorf("revoked row leaked: %+v", row)
		}
	}
}

// TestMemStore_Session_RevokeAll_ExcludesCurrent pins the "log
// out everywhere except this device" path. The calling sid is the
// exception; every other active row for the account is revoked.
// Returns the count of revoked rows.
func TestMemStore_Session_RevokeAll_ExcludesCurrent(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	alice, _ := m.CreateAccount(ctx, "alice@example.com", "free")
	const (
		current = "11111111-1111-1111-1111-111111111111"
		other1  = "22222222-2222-2222-2222-222222222222"
		other2  = "33333333-3333-3333-3333-333333333333"
	)
	for _, sid := range []string{current, other1, other2} {
		if _, err := m.CreateSession(ctx, sid, alice.ID, "192.0.2.10", "u"); err != nil {
			t.Fatal(err)
		}
	}
	n, err := m.RevokeAllSessions(ctx, alice.ID, current)
	if err != nil {
		t.Fatalf("RevokeAllSessions: %v", err)
	}
	if n != 2 {
		t.Errorf("RevokeAllSessions count = %d, want 2 (current excluded)", n)
	}
	// current stays active; others revoked.
	got, _ := m.GetSession(ctx, current)
	if got.RevokedAt != nil {
		t.Errorf("current session was revoked (should be retained)")
	}
	for _, sid := range []string{other1, other2} {
		row, _ := m.GetSession(ctx, sid)
		if row.RevokedAt == nil {
			t.Errorf("other session %s not revoked", sid)
		}
	}
	// Zero-revoke when only current remains.
	n2, err := m.RevokeAllSessions(ctx, alice.ID, current)
	if err != nil {
		t.Fatalf("RevokeAllSessions (zero): %v", err)
	}
	if n2 != 0 {
		t.Errorf("post-clean RevokeAllSessions count = %d, want 0", n2)
	}
}

// TestMemStore_Session_TouchAllowedOnRevoked pins the
// "LastSeenAt continues to update post-revoke" policy. The
// TouchSessionLastSeen no-ops on missing rows; on revoked rows
// it stamps LastSeenAt as an operational signal, not an
// authorization signal.
func TestMemStore_Session_TouchAllowedOnRevoked(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	alice, _ := m.CreateAccount(ctx, "alice@example.com", "free")
	const sid = "11111111-1111-1111-1111-111111111111"
	if _, err := m.CreateSession(ctx, sid, alice.ID, "192.0.2.10", "u"); err != nil {
		t.Fatal(err)
	}
	// Missing sid → silent no-op (the requireSessionCookie
	// caller is fire-and-forget).
	if err := m.TouchSessionLastSeen(ctx, "99999999-9999-9999-9999-999999999999"); err != nil {
		t.Errorf("TouchSessionLastSeen on missing sid err = %v, want nil", err)
	}
	// Existing sid → LastSeenAt gets stamped.
	if err := m.TouchSessionLastSeen(ctx, sid); err != nil {
		t.Fatalf("TouchSessionLastSeen: %v", err)
	}
	got, _ := m.GetSession(ctx, sid)
	if got.LastSeenAt == nil {
		t.Errorf("LastSeenAt not stamped by Touch")
	}
	// Revoke + touch → LastSeenAt updates (policy).
	if _, err := m.RevokeSession(ctx, sid, alice.ID); err != nil {
		t.Fatal(err)
	}
	touched, _ := time.Parse(time.RFC3339Nano, "2026-01-01T00:00:00Z")
	m.mu.Lock()
	row := m.sessions[sid]
	row.LastSeenAt = &touched
	m.sessions[sid] = row
	m.mu.Unlock()
	if err := m.TouchSessionLastSeen(ctx, sid); err != nil {
		t.Fatalf("TouchSessionLastSeen on revoked: %v", err)
	}
	got2, _ := m.GetSession(ctx, sid)
	if got2.LastSeenAt == nil || !got2.LastSeenAt.After(touched) {
		t.Errorf("post-revoke Touch did not bump LastSeenAt: %v", got2.LastSeenAt)
	}
}

// --- Projects (ADR-050, Phase 1) --------------------------------------------
//
// MemStore mirror of the pgstore Project tests. The contract is
// implementation-agnostic: same error sentinels, same monotonic-upgrade
// semantics, same account scoping.

func TestMem_CreateProject_UniqueSlug(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, err := m.CreateAccount(ctx, "mem-uniq@example.test", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	p, err := m.CreateProject(ctx, Project{
		AccountID: acc.ID,
		Slug:      "phase1-mem-uniq",
	})
	if err != nil {
		t.Fatalf("CreateProject happy: %v", err)
	}
	if p.ID == "" {
		t.Errorf("CreateProject ID = %q, want non-empty", p.ID)
	}
	if p.ScanSource != ProjectScanSourceUnknown {
		t.Errorf("default ScanSource = %q, want 'unknown'", p.ScanSource)
	}

	_, err = m.CreateProject(ctx, Project{
		AccountID: acc.ID,
		Slug:      "phase1-mem-uniq",
	})
	if !errors.Is(err, ErrConflict) {
		t.Errorf("CreateProject dup = %v, want ErrConflict", err)
	}
}

func TestMem_CreateProject_AccountMissing(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	_, err := m.CreateProject(ctx, Project{
		AccountID: "00000000-0000-0000-0000-000000000000",
		Slug:      "phase1-mem-orphan",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("CreateProject with missing account = %v, want ErrNotFound", err)
	}
}

func TestMem_ProjectByRepo_BackfilledHit(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, err := m.CreateAccount(ctx, "mem-backfill@example.test", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	installID := int64(90011)
	repoFull := "acme/phase1-mem-backfill"

	p, err := m.CreateProject(ctx, Project{
		AccountID:        acc.ID,
		Slug:             "phase1-mem-backfill",
		RepoFullName:     repoFull,
		ProductionBranch: "main",
		InstallID:        installID,
		ScanSource:       ProjectScanSourceCompose,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	got, err := m.ProjectByRepo(ctx, acc.ID, installID, repoFull)
	if err != nil {
		t.Fatalf("ProjectByRepo: %v", err)
	}
	if got.ID != p.ID {
		t.Errorf("ProjectByRepo.ID = %q, want %q", got.ID, p.ID)
	}
	if got.ScanSource != ProjectScanSourceCompose {
		t.Errorf("ScanSource = %q, want 'compose'", got.ScanSource)
	}
}

func TestMem_ProjectByRepo_Missing(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, err := m.CreateAccount(ctx, "mem-missing@example.test", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	_, err = m.ProjectByRepo(ctx, acc.ID, 88888, "does/not-exist")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("ProjectByRepo missing = %v, want ErrNotFound", err)
	}
}

func TestMem_AppsForProject_AccountScoped(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	accA, err := m.CreateAccount(ctx, "mem-scope-a@example.test", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount A: %v", err)
	}
	accB, err := m.CreateAccount(ctx, "mem-scope-b@example.test", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount B: %v", err)
	}

	p, err := m.CreateProject(ctx, Project{
		AccountID: accA.ID,
		Slug:      "phase1-mem-scope",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Seed an app with project_id + workload_name set on the struct.
	projStr := p.ID
	_, err = m.CreateApp(ctx, App{
		AccountID:      accA.ID,
		Slug:           "phase1-mem-scope-member",
		Type:           AppTypeApp,
		RAMMB:          256,
		MaxConcurrency: 1,
		Status:         AppActive,
		ProjectID:      projStr,
		WorkloadName:   "web",
		WorkloadClass:  WorkloadClassHTTP,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	got, err := m.AppsForProject(ctx, accA.ID, p.ID)
	if err != nil {
		t.Fatalf("AppsForProject same-account: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("AppsForProject same-account len = %d, want 1", len(got))
	}

	_, err = m.AppsForProject(ctx, accB.ID, p.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("AppsForProject cross-account = %v, want ErrNotFound", err)
	}
}

func TestMem_SetProjectScanSource_MonotonicUp(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acc, err := m.CreateAccount(ctx, "mem-mono@example.test", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	p, err := m.CreateProject(ctx, Project{
		AccountID:  acc.ID,
		Slug:       "phase1-mem-mono",
		ScanSource: ProjectScanSourceSingle,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// single → compose: ok.
	p2, err := m.SetProjectScanSource(ctx, p.ID, ProjectScanSourceCompose)
	if err != nil {
		t.Fatalf("SetProjectScanSource single→compose: %v", err)
	}
	if p2.ScanSource != ProjectScanSourceCompose {
		t.Errorf("after single→compose = %q, want 'compose'", p2.ScanSource)
	}

	// compose → single: rejected.
	_, err = m.SetProjectScanSource(ctx, p.ID, ProjectScanSourceSingle)
	if !errors.Is(err, ErrScanSourceDowngrade) {
		t.Errorf("SetProjectScanSource compose→single = %v, want ErrScanSourceDowngrade", err)
	}

	// compose → compose: same-tier no-op.
	p3, err := m.SetProjectScanSource(ctx, p.ID, ProjectScanSourceCompose)
	if err != nil {
		t.Fatalf("SetProjectScanSource compose→compose: %v", err)
	}
	if p3.ScanSource != ProjectScanSourceCompose {
		t.Errorf("same-tier write ScanSource = %q, want 'compose'", p3.ScanSource)
	}
}

// TestMem_ApplyProjectPlan_DeferredCronSkipped pins the F1 review
// finding (PR #454): ApplyProjectPlan must silently skip crons with
// empty AppID so the apply handler can resolve them after the Tx
// commits via CreateCron. The quota check ran inside the Tx (step 4
// in memstore.go:1114+) so a deferred cron cannot bypass it. The
// handler-level resolution step is what catches a cron whose
// workload name doesn't match any inserted app slug — covered in
// the cmd/gregale/commands_decompose_test.go fixtures.
//
// Pins:
//   - empty-AppID crons are skipped silently inside the Tx
//   - insertedApps is populated regardless (3 apps in, 3 apps out)
//   - insertedCrons is empty (the deferred cron re-inserts via CreateCron)
//   - the project + apps are persisted (the apply side is intact)
func TestMem_ApplyProjectPlan_DeferredCronSkipped(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	acct, err := m.CreateAccount(ctx, "alice@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	proj := Project{AccountID: acct.ID, Slug: "fixture"}
	// Pre-assign app IDs so a cron can reference one of them by ID
	// (mirrors the apply handler's post-Tx resolution: after
	// ApplyProjectPlan returns the insertedApps, the handler looks
	// up the slug → ID and calls CreateCron). The cron with an empty
	// AppID is the deferred case we pin.
	workerID := "00000000-0000-0000-0000-000000000001"
	apiID := "00000000-0000-0000-0000-000000000002"
	apps := []App{
		{ID: apiID, AccountID: acct.ID, Slug: "api", WorkloadName: "api"},
		{ID: workerID, AccountID: acct.ID, Slug: "worker", WorkloadName: "worker"},
		{AccountID: acct.ID, Slug: "web", WorkloadName: "web"},
	}
	// Two crons: one with empty AppID (must skip), one pre-resolved
	// against the worker's ID (must survive the Tx). The handler's
	// slugToID lookup happens post-commit and is not exercised here
	// — that's cmd/apid/handlers_decompose_test.go territory.
	crons := []Cron{
		{AppID: "", Schedule: "*/5 * * * *", Path: "/healthz", Enabled: true},
		{AppID: workerID, Schedule: "*/10 * * * *", Path: "/check", Enabled: true},
	}

	insertedProject, insertedApps, insertedCrons, err := m.ApplyProjectPlan(
		ctx, proj, apps, crons, api.MustLimitsFor(api.PlanHobby))
	if err != nil {
		t.Fatalf("ApplyProjectPlan: %v", err)
	}
	if insertedProject.ID == "" {
		t.Errorf("insertedProject.ID is empty")
	}
	if len(insertedApps) != 3 {
		t.Errorf("len(insertedApps) = %d, want 3", len(insertedApps))
	}
	if len(insertedCrons) != 1 {
		t.Errorf("len(insertedCrons) = %d, want 1 (only the pre-resolved cron survives the Tx)", len(insertedCrons))
	}
	if insertedCrons[0].AppID != workerID {
		t.Errorf("insertedCron[0].AppID = %q, want %q", insertedCrons[0].AppID, workerID)
	}
}

// TestMem_ApplyProjectPlan_FreePlanBlocksNewCrons pins the Free-plan
// cron guard inside the MemStore Tx (F2 parity with pgstore_test.go).
// Free plans have CronLimitPerAccount == 0, so any cron in the input
// returns QuotaError{Kind:"crons", NotAllowed:true} with zero rows
// inserted. Mirrors the pgstore step at pkg/state/pgstore.go:1120.
func TestMem_ApplyProjectPlan_FreePlanBlocksNewCrons(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	acct, err := m.CreateAccount(ctx, "alice@example.com", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	// Free plan caps DeployedApps at 1; build a single-app plan with
	// one cron and confirm the cron-not-allowed guard trips before
	// any write.
	proj := Project{AccountID: acct.ID, Slug: "fixture"}
	apps := []App{{AccountID: acct.ID, Slug: "api"}}
	crons := []Cron{{Schedule: "*/5 * * * *", Path: "/healthz", Enabled: true}}

	_, _, _, err = m.ApplyProjectPlan(ctx, proj, apps, crons, api.MustLimitsFor(api.PlanFree))
	var qe *QuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("err = %v, want *QuotaError", err)
	}
	if qe.Kind != QuotaErrorKindCrons {
		t.Errorf("qe.Kind = %q, want %q", qe.Kind, QuotaErrorKindCrons)
	}
	if !qe.NotAllowed {
		t.Errorf("qe.NotAllowed = false, want true (Free plan)")
	}

	// Confirm zero rows: the Tx rolled back, no project + no apps.
	if len(m.projects) != 0 {
		t.Errorf("len(m.projects) = %d, want 0 (rollback)", len(m.projects))
	}
	if len(m.apps) != 0 {
		t.Errorf("len(m.apps) = %d, want 0 (rollback)", len(m.apps))
	}
}

// TestMem_ApplyProjectPlan_FreePlanDowngradeKeepsExistingCrons pins
// the F2 review finding (PR #454): a Free-plan account with
// pre-existing crons (from a prior plan downgrade) must still be
// able to apply a project with len(crons)==0. The cron cap check is
// skipped entirely when len(crons) == 0 (memstore.go + pgstore.go
// ApplyProjectPlan step 3); a zero-cron apply lands cleanly even
// when observedCrons > 0 from prior history. Without this guard, a
// Free account that ever held crons would lock itself out of every
// future apply — the regression mode the review flagged.
//
// Pins:
//   - pre-existing crons survive the seed step (via CreateCron, no
//     per-account cap check, mirroring the historical path)
//   - a zero-cron apply on the same account lands the project row
//   - the cron guard does NOT trip when len(crons) == 0
//   - over-apps still trips (Free caps DeployedApps at 1)
func TestMem_ApplyProjectPlan_FreePlanDowngradeKeepsExistingCrons(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	acct, err := m.CreateAccount(ctx, "alice@example.com", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	// Seed a pre-existing app + cron to simulate the post-downgrade
	// state. Free's DeployedApps cap is 1 so we have exactly one
	// app; the cron lands via CreateCron (no per-account cap).
	existing, err := m.CreateApp(ctx, App{AccountID: acct.ID, Slug: "legacy"})
	if err != nil {
		t.Fatalf("CreateApp (legacy): %v", err)
	}
	if _, err := m.CreateCron(ctx, existing.ID, "*/15 * * * *", "/p", true); err != nil {
		t.Fatalf("CreateCron (legacy): %v", err)
	}

	// Free + 1 app already present → a one-app apply is over-quota.
	// The DeployedApps cap is 1, so observed=1 and len(apps)=1 → 2,
	// which trips the apps quota. This is the *correct* behavior: a
	// Free plan cannot add a second app, regardless of crons.
	// Confirm the over-apps guard still trips so the cron guard is
	// not shadowing it.
	proj := Project{AccountID: acct.ID, Slug: "fixture"}
	apps := []App{{AccountID: acct.ID, Slug: "newapp"}}
	_, _, _, err = m.ApplyProjectPlan(ctx, proj, apps, nil, api.MustLimitsFor(api.PlanFree))
	var qe *QuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("err = %v, want *QuotaError (apps cap trips first)", err)
	}
	if qe.Kind != QuotaErrorKindApps {
		t.Errorf("qe.Kind = %q, want %q (apps cap must trip before cron guard)", qe.Kind, QuotaErrorKindApps)
	}

	// Zero-cron apply: project only (no apps, no crons). The legacy
	// app's cron must not block this — the cron cap check is
	// skipped when len(crons) == 0. The apps cap is also fine
	// (len(apps) == 0, observed = 1, 1 + 0 <= 1). Lands cleanly.
	proj2 := Project{AccountID: acct.ID, Slug: "metadata-only"}
	inserted, _, _, err := m.ApplyProjectPlan(ctx, proj2, nil, nil, api.MustLimitsFor(api.PlanFree))
	if err != nil {
		t.Fatalf("zero-cron apply failed (F2 fix): %v — pre-existing crons must not block a cron-less apply on Free", err)
	}
	if inserted.ID == "" {
		t.Errorf("inserted.ID is empty after zero-cron apply")
	}
	if _, ok := m.projects[inserted.ID]; !ok {
		t.Errorf("project row missing after zero-cron apply")
	}

	// Sanity: the legacy cron still exists (the zero-cron apply did
	// not touch it). m.crons is package-private so this test can
	// iterate directly — a future refactor that prunes crons on
	// downgrade trips the test loudly.
	cronCount := 0
	for _, c := range m.crons {
		if c.AppID == existing.ID {
			cronCount++
		}
	}
	if cronCount != 1 {
		t.Errorf("legacy cron count = %d, want 1 (zero-cron apply must not prune)", cronCount)
	}
}

// --- SetAppWorkloadClass (ADR-051 Phase 4 PR-B parity) ----------------

// memSeedClassApp creates one Pro-plan app for the SetAppWorkloadClass
// parity tests. Mirrors the pgtest-injection style of seedAppForClass
// in pgstore_set_workload_class_test.go so the two suites can be
// read side-by-side.
func memSeedClassApp(t *testing.T, ctx context.Context, m *MemStore, email string) App {
	t.Helper()
	acc, err := m.CreateAccount(ctx, email, api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	limits := api.MustLimitsFor(api.PlanPro)
	a, err := m.CreateAppIfUnderQuota(ctx, App{
		AccountID:      acc.ID,
		Slug:           "class-mem-" + email,
		Type:           AppTypeFunction,
		Runtime:        "node22",
		RAMMB:          256,
		MaxConcurrency: 5,
		IdleTimeoutS:   60,
		Status:         AppActive,
		WorkloadClass:  WorkloadClassHTTP,
	}, limits)
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota: %v", err)
	}
	return a
}

func TestMemStore_SetAppWorkloadClass_RoundTrip(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	a := memSeedClassApp(t, ctx, m, "rt@x.test")

	cases := []WorkloadClass{
		WorkloadClassHTTP, WorkloadClassGraphQL, WorkloadClassGRPC,
		WorkloadClassJob, WorkloadClassWorker,
	}
	for _, want := range cases {
		got, err := m.SetAppWorkloadClass(ctx, a.ID, want, "scan_hint")
		if err != nil {
			t.Fatalf("SetAppWorkloadClass(%s) = %v, want nil", want, err)
		}
		if got.WorkloadClass != want {
			t.Errorf("SetAppWorkloadClass(%s) round-trip = %q, want %q",
				want, got.WorkloadClass, want)
		}
		if got.ID != a.ID {
			t.Errorf("returned ID=%q, want %q", got.ID, a.ID)
		}
	}
}

func TestMemStore_SetAppWorkloadClass_EmptyClass_FastFail(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	a := memSeedClassApp(t, ctx, m, "empty@x.test")

	_, err := m.SetAppWorkloadClass(ctx, a.ID, WorkloadClass(""), "manual")
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetAppWorkloadClass(\"\") = %v, want ErrInvalidArgument", err)
	}
	// Read-back must NOT have mutated the row.
	got, err := m.AppByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if got.WorkloadClass != a.WorkloadClass {
		t.Errorf("empty-class update mutated WorkloadClass = %q, want original %q",
			got.WorkloadClass, a.WorkloadClass)
	}
}

func TestMemStore_SetAppWorkloadClass_AppMissing(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	_, err := m.SetAppWorkloadClass(ctx,
		"00000000-0000-0000-0000-000000000000", WorkloadClassHTTP, "observed")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetAppWorkloadClass on missing app = %v, want ErrNotFound", err)
	}
}

// Parallel Sets on the same row in MemStore must converge (the PgStore
// suite proves the SQL path; this proves the mutex path). Last-writer-
// wins is fine — anything else indicates a torn write inside the lock.
func TestMemStore_SetAppWorkloadClass_ParallelSameRow(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	a := memSeedClassApp(t, ctx, m, "par@x.test")

	var wg sync.WaitGroup
	const N = 8
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			if _, err := m.SetAppWorkloadClass(ctx, a.ID, WorkloadClassWorker, "observed"); err != nil {
				t.Errorf("parallel Set %d: %v", i, err)
			}
		}()
	}
	wg.Wait()

	got, err := m.AppByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if got.WorkloadClass != WorkloadClassWorker {
		t.Errorf("final WorkloadClass = %q, want worker", got.WorkloadClass)
	}
}

// TestMemStore_StampAppScaleOut (PR-C, issue #462) pins the
// MemStore.StampAppScaleOut behavior: a fresh MemStore with a
// freshly-created app's LastScaleOutAt == nil → StampAppScaleOut
// writes a non-nil timestamp; second stamp overwrites it with a
// later timestamp. Stamp on a missing app returns ErrNotFound
// (defensive — schedd never calls this for an unknown app; tests
// pin the contract so a future refactor that silently no-ops
// trips the assertion).
func TestMemStore_StampAppScaleOut(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	acc, err := m.CreateAccount(ctx, "stamp-out@x.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{
		AccountID: acc.ID, Slug: "stamp-out-app", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 5, IdleTimeoutS: 60,
		Status: AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	// Pre: nil.
	if app.LastScaleOutAt != nil {
		t.Errorf("fresh app.LastScaleOutAt = %v, want nil", app.LastScaleOutAt)
	}
	// First stamp.
	if err := m.StampAppScaleOut(ctx, app.ID); err != nil {
		t.Fatalf("StampAppScaleOut #1: %v", err)
	}
	got, err := m.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if got.LastScaleOutAt == nil {
		t.Fatal("LastScaleOutAt nil after StampAppScaleOut, want non-nil")
	}
	first := *got.LastScaleOutAt
	// Second stamp overwrites with a later time. Sleep a millisecond
	// to guarantee monotonicity — wall-clock resolution can land two
	// stamps in the same nanosecond on fast boxes, which would
	// otherwise race the "second stamp overwrites" assertion.
	time.Sleep(time.Millisecond)
	if err := m.StampAppScaleOut(ctx, app.ID); err != nil {
		t.Fatalf("StampAppScaleOut #2: %v", err)
	}
	got2, err := m.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if !got2.LastScaleOutAt.After(first) {
		t.Errorf("second stamp %v not after first %v", got2.LastScaleOutAt, first)
	}
	// Stamp on missing app → ErrNotFound.
	if err := m.StampAppScaleOut(ctx, "missing-app-id"); !errors.Is(err, ErrNotFound) {
		t.Errorf("StampAppScaleOut(missing) = %v, want ErrNotFound", err)
	}
}

// TestMemStore_StampAppScaleIn mirrors TestMemStore_StampAppScaleOut
// for the reaper park path. Same shape — fresh → stamp → second
// stamp overwrites; missing app → ErrNotFound.
func TestMemStore_StampAppScaleIn(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	acc, err := m.CreateAccount(ctx, "stamp-in@x.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{
		AccountID: acc.ID, Slug: "stamp-in-app", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 5, IdleTimeoutS: 60,
		Status: AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if app.LastScaleInAt != nil {
		t.Errorf("fresh app.LastScaleInAt = %v, want nil", app.LastScaleInAt)
	}
	if err := m.StampAppScaleIn(ctx, app.ID); err != nil {
		t.Fatalf("StampAppScaleIn: %v", err)
	}
	got, err := m.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if got.LastScaleInAt == nil {
		t.Error("LastScaleInAt nil after StampAppScaleIn, want non-nil")
	}
	if err := m.StampAppScaleIn(ctx, "missing-app-id"); !errors.Is(err, ErrNotFound) {
		t.Errorf("StampAppScaleIn(missing) = %v, want ErrNotFound", err)
	}
}

// TestMem_CountLiveInstancesByDeployment mirrors the pgstore test
// for the in-memory Store (issue #555 PR-6). Counts every instance
// in {waking, cold_booting, running} for the given deployment_id;
// instances in PARKED / STOPPED / SNAPSHOTTING are excluded;
// unknown deployment_ids return 0.
func TestMem_CountLiveInstancesByDeployment(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	acc, err := m.CreateAccount(ctx, "count-live@x.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{
		AccountID: acc.ID, Slug: "count-live", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
		Status: AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	depA, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:a"})
	if err != nil {
		t.Fatalf("CreateDeployment depA: %v", err)
	}
	if err := m.MarkDeploymentLive(ctx, depA.ID); err != nil {
		t.Fatalf("MarkDeploymentLive depA: %v", err)
	}
	depB, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:b"})
	if err != nil {
		t.Fatalf("CreateDeployment depB: %v", err)
	}

	nodeID := "node-555"

	mustCreate := func(depID, stateStr string) {
		t.Helper()
		if _, err := m.CreateInstance(ctx, app.ID, depID, stateStr, 256, nodeID, ""); err != nil {
			t.Fatalf("CreateInstance %s/%s: %v", depID, stateStr, err)
		}
	}

	// dep-A: 1 waking + 1 cold_booting + 1 running = 3 live.
	mustCreate(depA.ID, "waking")
	mustCreate(depA.ID, "cold_booting")
	mustCreate(depA.ID, "running")
	// dep-A: 1 parked + 1 snapshotting = NOT counted.
	mustCreate(depA.ID, "parked")
	mustCreate(depA.ID, "snapshotting")
	// dep-B: 1 running = NOT counted under dep-A.
	mustCreate(depB.ID, "running")

	got, err := m.CountLiveInstancesByDeployment(ctx, depA.ID)
	if err != nil {
		t.Fatalf("CountLiveInstancesByDeployment: %v", err)
	}
	if got != 3 {
		t.Errorf("dep-A count = %d, want 3 (waking + cold_booting + running)", got)
	}

	gotUnknown, err := m.CountLiveInstancesByDeployment(ctx, "no-such-deployment")
	if err != nil {
		t.Fatalf("unknown dep: %v", err)
	}
	if gotUnknown != 0 {
		t.Errorf("unknown dep count = %d, want 0", gotUnknown)
	}

	gotEmpty, err := m.CountLiveInstancesByDeployment(ctx, "")
	if err != nil {
		t.Fatalf("empty dep: %v", err)
	}
	if gotEmpty != 0 {
		t.Errorf("empty dep count = %d, want 0", gotEmpty)
	}
}

// TestEvictionPriorityOrBestEffort (issue #475) pins the snap-to-default
// helper that all three eviction_priority call sites share. Empty Go
// zero → schema DEFAULT 'best_effort'; explicit values round-trip
// unchanged; only the two literal tier values are valid (a SQL CHECK
// backstop rejects anything else, but this test pins the helper's
// own contract — the helper is permissive, the CHECK is not).
func TestEvictionPriorityOrBestEffort(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty_zero_to_best_effort", "", string(api.EvictionPriorityBestEffort)},
		{"explicit_best_effort_roundtrip", "best_effort", "best_effort"},
		{"explicit_reserved_roundtrip", "reserved", "reserved"},
		// Defensive: the helper does not validate; the SQL CHECK is
		// the load-bearing gate. A future widening of the closed set
		// would change the helper output for new values, not these
		// pins.
		{"unknown_value_passes_through", "premium", "premium"},
	}
	for _, c := range cases {
		if got := EvictionPriorityOrBestEffort(c.in); got != c.want {
			t.Errorf("%s: EvictionPriorityOrBestEffort(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestMem_CreateApp_RequireAuthnDefault pins the per-plan default for
// apps.require_authn on the MemStore path (issue #695 / ADR-080). The
// MemStore mirrors pgstore via the same CreateApp + CreateAppIfUnderQuota
// seam; the in-memory stamp mirrors what apid's buildApp would write.
// Per-plan truth table: Free=false, Hobby=true, Pro=true, Scale=true.
//
// This is the memstore-side companion to
// TestPg_CreateAppIfUnderQuota_WritesRequireAuthnDefault — both stores
// must agree on the per-plan default so a CI matrix pin on the pgstore
// side doesn't silently regress on the memstore side (or vice versa).
func TestMem_CreateApp_RequireAuthnDefault(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	cases := []struct {
		name string
		plan api.Plan
		want bool
	}{
		{name: "FreeStaysPublic", plan: api.PlanFree, want: false},
		{name: "HobbyDefaultsToRequired", plan: api.PlanHobby, want: true},
		{name: "ProDefaultsToRequired", plan: api.PlanPro, want: true},
		{name: "ScaleDefaultsToRequired", plan: api.PlanScale, want: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			acct, err := m.CreateAccount(ctx, fmt.Sprintf("mem-auth-default-%s-%d@example.com", tc.name, time.Now().UnixNano()), tc.plan)
			if err != nil {
				t.Fatalf("CreateAccount(%s): %v", tc.plan, err)
			}
			limits := api.MustLimitsFor(acct.Plan)
			// Simulate apid's buildApp path stamping the per-plan default
			// onto the App before it hits CreateAppIfUnderQuota.
			app := App{
				AccountID:      acct.ID,
				Slug:           "mem-auth-default-" + tc.name,
				Type:           AppTypeApp,
				RAMMB:          256,
				MaxConcurrency: 1,
				IdleTimeoutS:   60,
				Status:         AppActive,
				RequireAuthn:   tc.want,
			}
			created, err := m.CreateAppIfUnderQuota(ctx, app, limits)
			if err != nil {
				t.Fatalf("CreateAppIfUnderQuota: %v", err)
			}
			if created.RequireAuthn != tc.want {
				t.Errorf("RETURNING require_authn = %v, want %v (memstore default-snap diverged)",
					created.RequireAuthn, tc.want)
			}
			fetched, err := m.AppBySlug(ctx, created.Slug)
			if err != nil {
				t.Fatalf("AppBySlug: %v", err)
			}
			if fetched.RequireAuthn != tc.want {
				t.Errorf("AppBySlug require_authn = %v, want %v (round-trip regression)",
					fetched.RequireAuthn, tc.want)
			}
		})
	}
}

// TestMem_CreateApp_PublicAuthModeDefault pins the per-plan default for
// apps.public_auth_mode on the MemStore path (issue #695 / ADR-080).
// Per-plan truth table: Free="open", Hobby="open", Pro="bearer",
// Scale="bearer". Hobby's "open" default is load-bearing (mirrors the
// pgstore test): Hobby unlocks the require_authn gate but not the
// bearer scope, so defaulting to "bearer" without a usable scope would
// strand the customer. A regression that flips Hobby's default to
// "bearer" surfaces here.
func TestMem_CreateApp_PublicAuthModeDefault(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	cases := []struct {
		name string
		plan api.Plan
		want string
	}{
		{name: "FreeStaysOpen", plan: api.PlanFree, want: api.AppPublicAuthModeOpen},
		{name: "HobbyStaysOpen", plan: api.PlanHobby, want: api.AppPublicAuthModeOpen},
		{name: "ProDefaultsToBearer", plan: api.PlanPro, want: api.AppPublicAuthModeBearer},
		{name: "ScaleDefaultsToBearer", plan: api.PlanScale, want: api.AppPublicAuthModeBearer},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			acct, err := m.CreateAccount(ctx, fmt.Sprintf("mem-public-auth-default-%s-%d@example.com", tc.name, time.Now().UnixNano()), tc.plan)
			if err != nil {
				t.Fatalf("CreateAccount(%s): %v", tc.plan, err)
			}
			limits := api.MustLimitsFor(acct.Plan)
			app := App{
				AccountID:      acct.ID,
				Slug:           "mem-public-auth-default-" + tc.name,
				Type:           AppTypeApp,
				RAMMB:          256,
				MaxConcurrency: 1,
				IdleTimeoutS:   60,
				Status:         AppActive,
				PublicAuthMode: tc.want,
			}
			created, err := m.CreateAppIfUnderQuota(ctx, app, limits)
			if err != nil {
				t.Fatalf("CreateAppIfUnderQuota: %v", err)
			}
			if created.PublicAuthMode != tc.want {
				t.Errorf("RETURNING public_auth_mode = %q, want %q (memstore default-snap diverged)",
					created.PublicAuthMode, tc.want)
			}
			fetched, err := m.AppBySlug(ctx, created.Slug)
			if err != nil {
				t.Fatalf("AppBySlug: %v", err)
			}
			if fetched.PublicAuthMode != tc.want {
				t.Errorf("AppBySlug public_auth_mode = %q, want %q (round-trip regression)",
					fetched.PublicAuthMode, tc.want)
			}
		})
	}
}

// TestMem_AppBySlug_AuthDefaultFlippedAt pins that the new
// apps.auth_default_flipped_at field lands on every App returned
// from the canonical read paths in MemStore (AppBySlug for this
// test; AppByID + ListAppsForAccount share the same fields list so
// a single test covers all three). A fresh post-flip create must
// read back as nil — only migration 00155 stamps the column. This
// is the memstore companion to
// TestPg_AppBySlug_SurfacesAuthDefaultFlippedAt.
func TestMem_AppBySlug_AuthDefaultFlippedAt(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	acct, err := m.CreateAccount(ctx, fmt.Sprintf("mem-auth-flip-readback-%d@example.com", time.Now().UnixNano()), api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	limits := api.MustLimitsFor(acct.Plan)
	app := App{
		AccountID: acct.ID, Slug: "mem-auth-flip-readback", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60, Status: AppActive,
	}
	created, err := m.CreateAppIfUnderQuota(ctx, app, limits)
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota: %v", err)
	}
	fetched, err := m.AppBySlug(ctx, created.Slug)
	if err != nil {
		t.Fatalf("AppBySlug: %v", err)
	}
	if fetched.AuthDefaultFlippedAt != nil {
		t.Errorf("post-flip create auth_default_flipped_at = %v, want nil (only migration 00155 stamps the column)", fetched.AuthDefaultFlippedAt)
	}
}

// --- PR-B (issue #679 / ADR-082) per-account extra cap store ---------------
//
// The store is the read/write boundary for the per-account
// additive budget. The validator consults the in-memory copy
// via `acct.EgressAllowlistExtra` (no extra round-trip); the
// admin endpoints hit the store directly. The tests below pin
// the three branches: default (0), round-trip, and clear-by-zero.
// The PgStore mirror is covered by the migration test
// (TestMigrations_00156_AccountsEgressAllowlistExtra) which
// seeds the column, PATCH-es via the store, and reads it back.

// TestMemStore_EgressAllowlistExtra_DefaultIsZero: a fresh
// account reads back 0 — the additive budget defaults to the
// plan cap (no override). The default must hold even before
// any admin PATCH has landed.
func TestMemStore_EgressAllowlistExtra_DefaultIsZero(t *testing.T) {
	m := NewMemStore()
	acct, err := m.CreateAccount(context.Background(), "fresh@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	got, err := m.GetAccountEgressAllowlistExtra(context.Background(), acct.ID)
	if err != nil {
		t.Fatalf("GetAccountEgressAllowlistExtra: %v", err)
	}
	if got != 0 {
		t.Errorf("default extra = %d, want 0", got)
	}
}

// TestMemStore_EgressAllowlistExtra_RoundTrip: an admin-set
// value is read back through both the direct Get helper and the
// scanned Account projection. The two paths must return the same
// value (the validator reads via the projection, the admin
// endpoint reads via the helper).
func TestMemStore_EgressAllowlistExtra_RoundTrip(t *testing.T) {
	m := NewMemStore()
	acct, err := m.CreateAccount(context.Background(), "roundtrip@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := m.SetAccountEgressAllowlistExtra(context.Background(), acct.ID, 8); err != nil {
		t.Fatalf("SetAccountEgressAllowlistExtra: %v", err)
	}
	got, err := m.GetAccountEgressAllowlistExtra(context.Background(), acct.ID)
	if err != nil {
		t.Fatalf("GetAccountEgressAllowlistExtra: %v", err)
	}
	if got != 8 {
		t.Errorf("Get extra = %d, want 8", got)
	}
	dbAcct, err := m.AccountByID(context.Background(), acct.ID)
	if err != nil {
		t.Fatalf("AccountByID: %v", err)
	}
	if dbAcct.EgressAllowlistExtra != 8 {
		t.Errorf("Account.EgressAllowlistExtra = %d, want 8 (scan projection must match)", dbAcct.EgressAllowlistExtra)
	}
}

// TestMemStore_EgressAllowlistExtra_ClearByZero: PATCH extra=0
// clears the override — the round-trip endpoint must produce
// the same 0 as the default. Otherwise a "clear" PATCH would
// leave a phantom non-zero override on the account.
func TestMemStore_EgressAllowlistExtra_ClearByZero(t *testing.T) {
	m := NewMemStore()
	acct, err := m.CreateAccount(context.Background(), "clear@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := m.SetAccountEgressAllowlistExtra(context.Background(), acct.ID, 16); err != nil {
		t.Fatalf("seed extra=16: %v", err)
	}
	if err := m.SetAccountEgressAllowlistExtra(context.Background(), acct.ID, 0); err != nil {
		t.Fatalf("clear-by-zero: %v", err)
	}
	got, _ := m.GetAccountEgressAllowlistExtra(context.Background(), acct.ID)
	if got != 0 {
		t.Errorf("after clear-by-zero, extra = %d, want 0", got)
	}
}

// TestMemStore_EgressAllowlistExtra_UnknownAccountIsNotFound: a
// store miss surfaces state.ErrNotFound so the admin handler can
// downgrade gracefully (the GET path treats ErrNotFound as
// "default 0"). The PgStore mirrors this via pgx.ErrNoRows.
func TestMemStore_EgressAllowlistExtra_UnknownAccountIsNotFound(t *testing.T) {
	m := NewMemStore()
	_, err := m.GetAccountEgressAllowlistExtra(context.Background(), "nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetAccountEgressAllowlistExtra missing acct: err = %v, want ErrNotFound", err)
	}
}

// memstoreSeedAppLive creates an account + app + one live deployment
// and returns them. Used by the four MemStore_UpdateDeploymentTraffic
// pinned tests below (issue #556 / PR-C).
func memstoreSeedAppLive(t *testing.T, m *MemStore, ctx context.Context, suffix string) (accID, appID, depID string) {
	t.Helper()
	acc, err := m.CreateAccount(ctx, suffix+"@x.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{
		AccountID: acc.ID, Slug: suffix + "-app", Type: AppTypeApp,
		RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60, Status: AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := m.CreateDeployment(ctx, Deployment{
		AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:abc",
		Status: DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := m.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	return acc.ID, app.ID, dep.ID
}

// memstoreSeedLiveSibling creates a second deployment and flips the
// superseded prior (created by CreateDeployment's auto-supersede)
// back to live at 0 traffic, AND flips the new deployment to live at
// 100, so the resulting pair is {prior:0, sibling:100}, Σ=100, both
// live. This is the precondition for every proportional-redistribution
// test in this file. CreateDeployment leaves the new row in
// DeployPending; UpdateDeploymentTraffic guards on DeployLive, so the
// sibling must be promoted to live before the test body exercises it.
//
// Returns (priorDepID, newDepID).
func memstoreSeedLiveSibling(t *testing.T, m *MemStore, ctx context.Context, priorDepID, tag string) (string, string) {
	t.Helper()
	appID := m.deployments[priorDepID].AppID
	newDep, err := m.CreateDeployment(ctx, Deployment{
		AppID: appID, Kind: DeploymentKindImage,
		ImageDigest: "sha256:" + tag, Status: DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment (%s): %v", tag, err)
	}
	// CreateDeployment auto-superseded priorDepID → traffic_percent=0,
	// status=superseded. Flip prior back to live at 0 so we have two
	// live rows summing to 100.
	if err := m.MarkDeploymentLive(ctx, priorDepID); err != nil {
		t.Fatalf("MarkDeploymentLive (restore prior): %v", err)
	}
	// Promote the new row from DeployPending to DeployLive. The
	// status guard in UpdateDeploymentTraffic requires the target to
	// be live before it can take a weight.
	if err := m.MarkDeploymentLive(ctx, newDep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive (sibling %s): %v", tag, err)
	}
	return priorDepID, newDep.ID
}

// TestMem_UpdateDeploymentTraffic_ProportionalRedistribution
// mirrors pgstore: a 100/0 split → set canary to 25 → prior drops
// to 75, Σ=100. PR-A's zero-siblings made this impossible;
// PR-C's largest-remainder (RedistributeTraffic, called from both
// stores) makes it the headline contract.
func TestMem_UpdateDeploymentTraffic_ProportionalRedistribution(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	_, _, depPrior := memstoreSeedAppLive(t, m, ctx, "prop-2way-mem")

	// Seed live sibling: after this, prior is back at 0 + live,
	// canary at 100 + live, Σ=100.
	depPrior, depCanary := memstoreSeedLiveSibling(t, m, ctx, depPrior, "canary-mem")
	prior, _ := m.DeploymentByID(ctx, depPrior)
	canary, _ := m.DeploymentByID(ctx, depCanary)
	if prior.TrafficPercent != 0 || canary.TrafficPercent != 100 {
		t.Fatalf("after seed: prior=%d canary=%d, want 0/100",
			prior.TrafficPercent, canary.TrafficPercent)
	}

	// Flip canary to 25 → prior jumps to 75.
	if _, err := m.UpdateDeploymentTraffic(ctx, depCanary, 25); err != nil {
		t.Fatalf("canary=25: %v", err)
	}
	prior, _ = m.DeploymentByID(ctx, depPrior)
	canary, _ = m.DeploymentByID(ctx, depCanary)
	if prior.TrafficPercent != 75 {
		t.Errorf("prior.TrafficPercent = %d, want 75", prior.TrafficPercent)
	}
	if canary.TrafficPercent != 25 {
		t.Errorf("canary.TrafficPercent = %d, want 25", canary.TrafficPercent)
	}
	if sum := prior.TrafficPercent + canary.TrafficPercent; sum != 100 {
		t.Errorf("Σ = %d, want 100", sum)
	}
}

// TestMem_UpdateDeploymentTraffic_ThreeWayResidual mirrors the
// pgstore three-way test in memstore: 3 live rows, multiple stamps,
// Σ must remain 100 across all transitions.
func TestMem_UpdateDeploymentTraffic_ThreeWayResidual(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	_, _, depA := memstoreSeedAppLive(t, m, ctx, "three-way-mem")
	// Seed depB live alongside depA: prior=0, B=100, Σ=100.
	depA, depB := memstoreSeedLiveSibling(t, m, ctx, depA, "B-mem")
	// Seed depC live alongside A: prior(0) gets superseded again,
	// B(100) stays live. Flip prior back: A=0, B=100, C=100? No —
	// CreateDeployment supersedes the most recent live row (B),
	// so after this B→0, C→100. Then restore A to live at 0, but
	// B is already 0 from supersede — Σ = 0+0+100 = 100 ✓.
	depB, depC := memstoreSeedLiveSibling(t, m, ctx, depB, "C-mem")
	// After this: A=superseded at 0, B=superseded at 0, C=live at 100.
	// Re-flip A and B to live at 0.
	if err := m.MarkDeploymentLive(ctx, depA); err != nil {
		t.Fatalf("MarkDeploymentLive (A): %v", err)
	}
	if err := m.MarkDeploymentLive(ctx, depB); err != nil {
		t.Fatalf("MarkDeploymentLive (B): %v", err)
	}

	// Build a 3-way table; Σ must be 100 after every stamp.
	if _, err := m.UpdateDeploymentTraffic(ctx, depB, 30); err != nil {
		t.Fatalf("B=30: %v", err)
	}
	a, _ := m.DeploymentByID(ctx, depA)
	b, _ := m.DeploymentByID(ctx, depB)
	if sum := a.TrafficPercent + b.TrafficPercent + c_deployOnly(depC, m, ctx).TrafficPercent; sum != 100 {
		t.Errorf("after B=30: Σ = %d, want 100", sum)
	}
	if _, err := m.UpdateDeploymentTraffic(ctx, depC, 20); err != nil {
		t.Fatalf("C=20: %v", err)
	}
	a, _ = m.DeploymentByID(ctx, depA)
	b, _ = m.DeploymentByID(ctx, depB)
	c, _ := m.DeploymentByID(ctx, depC)
	if sum := a.TrafficPercent + b.TrafficPercent + c.TrafficPercent; sum != 100 {
		t.Errorf("after C=20: Σ = %d, want 100", sum)
	}
	// Stamp A=25 → Σ must stay 100; A lands exactly at 25.
	if _, err := m.UpdateDeploymentTraffic(ctx, depA, 25); err != nil {
		t.Fatalf("A=25: %v", err)
	}
	a, _ = m.DeploymentByID(ctx, depA)
	b, _ = m.DeploymentByID(ctx, depB)
	c, _ = m.DeploymentByID(ctx, depC)
	if a.TrafficPercent != 25 {
		t.Errorf("A = %d, want 25", a.TrafficPercent)
	}
	if sum := a.TrafficPercent + b.TrafficPercent + c.TrafficPercent; sum != 100 {
		t.Errorf("after A=25: Σ = %d, want 100 (B=%d C=%d)", sum, b.TrafficPercent, c.TrafficPercent)
	}
}

// c_deployOnly is a small inline helper to keep the test readable;
// returns the deployment for Σ asserts.
func c_deployOnly(id string, m *MemStore, ctx context.Context) Deployment {
	d, _ := m.DeploymentByID(ctx, id)
	return d
}

// TestMem_UpdateDeploymentTraffic_SoleLiveRow mirrors the pgstore
// sole-row edge cases: target=100 no-op success; target=0 → Σ=0 →
// ErrTrafficPercentSumInvalid (the one legitimate failure mode).
func TestMem_UpdateDeploymentTraffic_SoleLiveRow(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	_, _, depID := memstoreSeedAppLive(t, m, ctx, "sole-row-mem")

	if _, err := m.UpdateDeploymentTraffic(ctx, depID, 100); err != nil {
		t.Errorf("target=100 on sole row err = %v, want nil", err)
	}
	row, _ := m.DeploymentByID(ctx, depID)
	if row.TrafficPercent != 100 {
		t.Errorf("Sole row after target=100 = %d, want 100", row.TrafficPercent)
	}
	if _, err := m.UpdateDeploymentTraffic(ctx, depID, 0); !errors.Is(err, ErrTrafficPercentSumInvalid) {
		t.Errorf("target=0 on sole row err = %v, want ErrTrafficPercentSumInvalid", err)
	}
}

// TestMem_UpdateDeploymentTraffic_TieBreakStable mirrors the
// pgstore tie-break: 2 siblings equal weight, even residual
// (no +1 needed); equal weights after restore.
func TestMem_UpdateDeploymentTraffic_TieBreakStable(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	_, _, depA := memstoreSeedAppLive(t, m, ctx, "tie-break-mem")
	depA, depB := memstoreSeedLiveSibling(t, m, ctx, depA, "B-mem")
	// Equalise to {A:50, B:50} from {A:0, B:100}: stamp A=50 → residual
	// 50 absorbed by sole sibling B.
	if _, err := m.UpdateDeploymentTraffic(ctx, depA, 50); err != nil {
		t.Fatalf("equalise A=50: %v", err)
	}
	a, _ := m.DeploymentByID(ctx, depA)
	b, _ := m.DeploymentByID(ctx, depB)
	if a.TrafficPercent != 50 || b.TrafficPercent != 50 {
		t.Errorf("equalise: A=%d B=%d, want 50/50", a.TrafficPercent, b.TrafficPercent)
	}

	// Set A=0 → residual 100 absorbed by B. Σ=100.
	if _, err := m.UpdateDeploymentTraffic(ctx, depA, 0); err != nil {
		t.Fatalf("A=0: %v", err)
	}
	a, _ = m.DeploymentByID(ctx, depA)
	b, _ = m.DeploymentByID(ctx, depB)
	if a.TrafficPercent != 0 || b.TrafficPercent != 100 {
		t.Errorf("A=0: A=%d B=%d, want 0/100", a.TrafficPercent, b.TrafficPercent)
	}

	// Restore to 50/50.
	if _, err := m.UpdateDeploymentTraffic(ctx, depA, 50); err != nil {
		t.Fatalf("restore A=50: %v", err)
	}
	a, _ = m.DeploymentByID(ctx, depA)
	b, _ = m.DeploymentByID(ctx, depB)
	if a.TrafficPercent != 50 || b.TrafficPercent != 50 {
		t.Errorf("restore: A=%d B=%d, want 50/50", a.TrafficPercent, b.TrafficPercent)
	}
	if sum := a.TrafficPercent + b.TrafficPercent; sum != 100 {
		t.Errorf("restore: Σ = %d, want 100", sum)
	}
}

// TestMemStore_ListAllEventsPaged_BadSubjectEmpty pins the
// memstore contract on unparseable subject filters (code-review
// low on PR #817, 2026-08-10). Before the fix, a non-empty but
// unparseable subject silently matched every event row — the
// code comment promised "return empty rather than silently
// matching everything" but the implementation just left
// subjectFilter = nil and the per-row check became a no-op.
//
// Mirrors the pgstore contract: the SQL
// `$3 = ” OR subject = $3::uuid` clause fails the cast on a
// non-UUID string and returns no rows (Postgres 22P02). Returning
// (nil, nil) keeps the two stores in lockstep on this edge.
func TestMemStore_ListAllEventsPaged_BadSubjectEmpty(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	subject := uuid.New().String()
	for i := 0; i < 3; i++ {
		if err := m.AppendEvent(ctx, "system:schedd", "wake.requested", &subject, []byte(`{}`)); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	got, err := m.ListAllEventsPaged(ctx, "", "", "not-a-uuid", time.Time{}, 100)
	if err != nil {
		t.Fatalf("ListAllEventsPaged: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("unparseable subject: got %d rows, want 0 (mirrors pgstore SQL cast failure)", len(got))
	}
}

// TestMemStore_ListAllEventsPaged_ValidSubject pins the
// happy path on the same code path — defense against a future
// "return empty on any non-empty subject" over-correction.
func TestMemStore_ListAllEventsPaged_ValidSubject(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	subjectA := uuid.New().String()
	subjectB := uuid.New().String()
	if err := m.AppendEvent(ctx, "system:schedd", "wake.requested", &subjectA, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := m.AppendEvent(ctx, "system:schedd", "wake.requested", &subjectB, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	got, err := m.ListAllEventsPaged(ctx, "", "", subjectA, time.Time{}, 100)
	if err != nil {
		t.Fatalf("ListAllEventsPaged: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("subject match: got %d rows, want 1 (subjectA only)", len(got))
	}
	if got[0].Subject == nil || got[0].Subject.String() != subjectA {
		t.Errorf("subject: got %v, want %s", got[0].Subject, subjectA)
	}
}

// TestMemStoreAppendDeploymentStage covers the four cases of
// AppendDeploymentStage + MarkDeploymentStageFailed + CloseDeploymentStage
// (ADR-117 §3 + PR-A review fixes): forward transition, failure
// stamp on the in-flight stage, terminal close, and the stale-read
// guard. The PGStore path is exercised by the JSONB + CHECK migration
// test (migrations/00302_deployments_stage_state_test.go); the
// in-memory store mirrors the same shape so the same test contract
// fits both backends.
func TestMemStoreAppendDeploymentStage(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	acc, err := s.CreateAccount(ctx, "deploy-stage@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := s.CreateApp(ctx, App{AccountID: acc.ID, Slug: "deploy-stage-app"})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := s.CreateDeployment(ctx, Deployment{ID: "d-stage-test", AppID: app.ID, Status: DeployPending, ImageDigest: "sha:latest"})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	now := time.Now().UTC()
	// Case 1: forward transition source_download -> dependency_restore.
	got, err := s.AppendDeploymentStage(ctx, dep.ID, StageSourceDownload, StageDependencyRestore, now, "")
	if err != nil {
		t.Fatalf("forward transition: %v", err)
	}
	if got.Status != DeployPending {
		t.Errorf("forward transition flipped status: got %s, want %s", got.Status, DeployPending)
	}
	// Case 2: from==to is now an error (PR-A review fix F3: the
	// previous failure-stamp overload mutated history[len-1] which
	// was the wrong stage). Failure stamps must go through
	// MarkDeploymentStageFailed.
	if _, err := s.AppendDeploymentStage(ctx, dep.ID, StageDependencyRestore, StageDependencyRestore, now.Add(time.Second), ""); err == nil {
		t.Errorf("from==to AppendDeploymentStage should error, got nil")
	}
	// Case 3: MarkDeploymentStageFailed stamps the in-flight stage
	// (dependency_restore) and moves it into history with status
	// "failed". The customer's ticker sees the actual failing
	// stage, not the previously-closed one.
	got, err = s.MarkDeploymentStageFailed(ctx, dep.ID, now.Add(time.Second), "build failed: dep foo")
	if err != nil {
		t.Fatalf("MarkDeploymentStageFailed: %v", err)
	}
	if got.Status != DeployPending {
		t.Errorf("failure stamp flipped status: got %s, want %s", got.Status, DeployPending)
	}
	var state StageState
	if err := json.Unmarshal(got.StageState, &state); err != nil {
		t.Fatalf("decode stage_state: %v", err)
	}
	if state.Current != "" {
		t.Errorf("after failure stamp: current = %q, want \"\" (cleared)", state.Current)
	}
	// History must contain dependency_restore as the failing stage
	// (PR-A review fix F3: the previous version stamped history[0]
	// which was source_download — the previously-closed stage —
	// instead of the in-flight dependency_restore).
	if len(state.History) != 2 {
		t.Fatalf("history length after failure: got %d, want 2: %+v", len(state.History), state.History)
	}
	if state.History[1].Name != StageDependencyRestore {
		t.Errorf("history[1].Name = %q, want %q (the in-flight stage)", state.History[1].Name, StageDependencyRestore)
	}
	if state.History[1].Status != "failed" {
		t.Errorf("history[1].Status = %q, want failed", state.History[1].Status)
	}
	if state.History[1].Reason != "build failed: dep foo" {
		t.Errorf("history[1].Reason = %q, want %q", state.History[1].Reason, "build failed: dep foo")
	}
	// Case 4: stale transition (current != from) returns ErrNotFound.
	if _, err := s.AppendDeploymentStage(ctx, dep.ID, StageImageBuild, StageSnapshotPrepare, now, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("stale from: expected ErrNotFound, got %v", err)
	}
	// Case 5: CloseDeploymentStage on a fresh row stamps the
	// in-flight stage as completed and clears Current. Used by
	// imaged.MarkDeploymentLive to close the readiness stage so
	// the customer's ticker carries a duration_ms.
	dep2, err := s.CreateDeployment(ctx, Deployment{ID: "d-stage-test-2", AppID: app.ID, Status: DeployPending, ImageDigest: "sha:latest2"})
	if err != nil {
		t.Fatalf("CreateDeployment #2: %v", err)
	}
	if _, err := s.AppendDeploymentStage(ctx, dep2.ID, StageSourceDownload, StageReadiness, now, ""); err != nil {
		t.Fatalf("AppendDeploymentStage readiness: %v", err)
	}
	got, err = s.CloseDeploymentStage(ctx, dep2.ID, StageReadiness, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("CloseDeploymentStage: %v", err)
	}
	if err := json.Unmarshal(got.StageState, &state); err != nil {
		t.Fatalf("decode stage_state #2: %v", err)
	}
	if state.Current != "" {
		t.Errorf("after CloseDeploymentStage: current = %q, want \"\"", state.Current)
	}
	if len(state.History) != 2 {
		t.Fatalf("history length after close: got %d, want 2: %+v", len(state.History), state.History)
	}
	if state.History[1].Name != StageReadiness {
		t.Errorf("history[1].Name = %q, want %q", state.History[1].Name, StageReadiness)
	}
	if state.History[1].Status != "completed" {
		t.Errorf("history[1].Status = %q, want completed", state.History[1].Status)
	}
	// Case 6: CloseDeploymentStage on a row whose Current != name
	// returns ErrNotFound (programming-error guard).
	if _, err := s.CloseDeploymentStage(ctx, dep2.ID, StageSourceDownload, now); !errors.Is(err, ErrNotFound) {
		t.Errorf("CloseDeploymentStage with wrong name: expected ErrNotFound, got %v", err)
	}
}

// TestMemStoreAppendDeploymentStage_HistoryTrimmedAtMax — ADR-117
// §Production-ready follow-on, C1. Pins the FIFO trim: pushing
// more than MaxStageHistory transitions leaves exactly the last
// MaxStageHistory entries in history, with state.Current intact.
// Schema-unchanged; the trim is Go-side at the read-modify-write
// site in AppendDeploymentStage (no jsonb CHECK).
//
// We loop the 6-stage vocabulary (since the closed-6 set's
// internal cycle is the natural way to push > 64 entries) and
// assert the resulting history length + boundary entries.
func TestMemStoreAppendDeploymentStage_HistoryTrimmedAtMax(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	acc, err := s.CreateAccount(ctx, "trim-cap@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := s.CreateApp(ctx, App{AccountID: acc.ID, Slug: "trim-cap-app"})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := s.CreateDeployment(ctx, Deployment{ID: "d-trim-cap", AppID: app.ID, Status: DeployPending, ImageDigest: "sha:trim"})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	// Build a long transition sequence that exceeds MaxStageHistory.
	// The closed-6 vocabulary is forward-only, so we wrap via a
	// distinct cycle that always has from != to. We use two stages
	// (source_download -> dependency_restore) as a synthetic ping-
	// pong; the production pipeline never does this but the trim
	// is independent of the stage identity.
	cycle := []StageName{StageSourceDownload, StageDependencyRestore}
	now := time.Now().UTC()
	// Number of transitions: we want history length to push past
	// MaxStageHistory + some headroom (so the trim is exercised).
	const transitions = MaxStageHistory + 10
	for i := 0; i < transitions; i++ {
		from := cycle[i%len(cycle)]
		to := cycle[(i+1)%len(cycle)]
		if _, err := s.AppendDeploymentStage(ctx, dep.ID, from, to, now.Add(time.Duration(i)*time.Second), ""); err != nil {
			t.Fatalf("transition %d (%s -> %s): %v", i, from, to, err)
		}
	}

	got, err := s.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	var state StageState
	if err := json.Unmarshal(got.StageState, &state); err != nil {
		t.Fatalf("decode stage_state: %v", err)
	}
	if len(state.History) != MaxStageHistory {
		t.Errorf("history length after %d transitions: got %d, want %d (FIFO trim)",
			transitions, len(state.History), MaxStageHistory)
	}
	// The oldest retained entry must be the (transitions - MaxStageHistory + 1)-th
	// transition's `from`. We compute it the same way the loop did.
	wantOldestIdx := transitions - MaxStageHistory
	wantOldestFrom := cycle[wantOldestIdx%len(cycle)]
	if state.History[0].Name != wantOldestFrom {
		t.Errorf("history[0].Name = %q, want %q (oldest retained after FIFO trim)",
			state.History[0].Name, wantOldestFrom)
	}
	// The newest entry is the most recent append's `from`.
	wantNewestFrom := cycle[(transitions-1)%len(cycle)]
	if state.History[len(state.History)-1].Name != wantNewestFrom {
		t.Errorf("history[last].Name = %q, want %q (most recent)",
			state.History[len(state.History)-1].Name, wantNewestFrom)
	}
	// Current must NOT be trimmed — it's the in-flight stage the
	// loop just advanced to. Length is 1 entry, not "trimmed".
	if state.Current == "" {
		t.Errorf("Current was trimmed; want the in-flight stage to be retained")
	}
}

// TestMemStoreAppendDeploymentStage_TrimExactBoundary — pushes
// exactly MaxStageHistory + 1 transitions and asserts the trim
// engages at the (cap+1)-th append, not before. This is the
// off-by-one guard: a regression that trimmed early would silently
// drop one row of legitimate history.
func TestMemStoreAppendDeploymentStage_TrimExactBoundary(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	acc, err := s.CreateAccount(ctx, "trim-boundary@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := s.CreateApp(ctx, App{AccountID: acc.ID, Slug: "trim-boundary-app"})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := s.CreateDeployment(ctx, Deployment{ID: "d-trim-boundary", AppID: app.ID, Status: DeployPending, ImageDigest: "sha:trim2"})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	cycle := []StageName{StageSourceDownload, StageDependencyRestore}
	now := time.Now().UTC()
	// Push exactly MaxStageHistory transitions (boundary: history
	// length = MaxStageHistory, no trim yet).
	for i := 0; i < MaxStageHistory; i++ {
		from := cycle[i%len(cycle)]
		to := cycle[(i+1)%len(cycle)]
		if _, err := s.AppendDeploymentStage(ctx, dep.ID, from, to, now.Add(time.Duration(i)*time.Second), ""); err != nil {
			t.Fatalf("transition %d: %v", i, err)
		}
	}
	got, err := s.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatalf("DeploymentByID at boundary: %v", err)
	}
	var state StageState
	if err := json.Unmarshal(got.StageState, &state); err != nil {
		t.Fatalf("decode stage_state at boundary: %v", err)
	}
	if len(state.History) != MaxStageHistory {
		t.Errorf("at exact cap (no trim): history length = %d, want %d", len(state.History), MaxStageHistory)
	}

	// One more transition → trim engages, dropping the oldest row.
	from := cycle[MaxStageHistory%len(cycle)]
	to := cycle[(MaxStageHistory+1)%len(cycle)]
	if _, err := s.AppendDeploymentStage(ctx, dep.ID, from, to, now.Add(time.Duration(MaxStageHistory)*time.Second), ""); err != nil {
		t.Fatalf("overflow transition: %v", err)
	}
	got, err = s.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatalf("DeploymentByID post-overflow: %v", err)
	}
	if err := json.Unmarshal(got.StageState, &state); err != nil {
		t.Fatalf("decode stage_state post-overflow: %v", err)
	}
	if len(state.History) != MaxStageHistory {
		t.Errorf("post-overflow history length = %d, want %d (trim engages)", len(state.History), MaxStageHistory)
	}
	// The oldest entry must be the SECOND `from` of the cycle
	// (cycle[1] = StageDependencyRestore), not the first
	// (cycle[0] = StageSourceDownload — that one's been trimmed).
	if state.History[0].Name != cycle[1] {
		t.Errorf("post-overflow history[0] = %q, want %q (oldest trimmed)", state.History[0].Name, cycle[1])
	}
}

// TestMemStore_DeploymentActorRoundtrip (issue #606) pins the
// four actor-attribution fields on the in-memory Deployment shape.
// MemStore stores the Deployment struct directly
// (m.deployments[d.ID] = d), so the "round-trip" is a write+read
// of the struct fields themselves — the closed-set CHECK and FK
// are DB-only and not exercised here. Mirrors the MemStore half
// of the PR #984 annotation round-trip coverage (the PgStore
// counterpart at TestPg_DeploymentActorRoundtrip covers the
// DB-side CHECK + FK contract).
func TestMemStore_DeploymentActorRoundtrip(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	app, err := m.CreateApp(ctx, App{
		AccountID: "acct-actor", Slug: "actor-mem", Type: AppTypeApp,
		RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	// 1. Full payload — the four actor fields round-trip cleanly
	//    through the in-memory map.
	d, err := m.CreateDeployment(ctx, Deployment{
		AppID:            app.ID,
		ImageDigest:      "sha256:actor-mem",
		Kind:             DeploymentKindGitHub,
		Status:           DeployPending,
		DeployedByUserID: "11111111-1111-1111-1111-111111111111",
		DeployedVia:      "github",
		DeployedFromIP:   "203.0.113.42",
		PusherLogin:      "octocat",
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if d.DeployedByUserID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("deployed_by_user_id = %q, want %q", d.DeployedByUserID, "11111111-1111-1111-1111-111111111111")
	}
	if d.DeployedVia != "github" {
		t.Errorf("deployed_via = %q, want %q", d.DeployedVia, "github")
	}
	if d.DeployedFromIP != "203.0.113.42" {
		t.Errorf("deployed_from_ip = %q, want %q", d.DeployedFromIP, "203.0.113.42")
	}
	if d.PusherLogin != "octocat" {
		t.Errorf("pusher_login = %q, want %q", d.PusherLogin, "octocat")
	}

	// 2. Zero payload — every field collapses to its Go zero,
	//    including DeployedVia="". The Go-zero is the
	//    MemStore-side analogue of the pgstore nullif()/coalesce()
	//    chain (which produces "" on the read side via
	//    coalesce(deployed_via, 'api') + scanDeploymentInto's
	//    plain string destination). The PgStore test asserts
	//    the deployed_via='api' fallback; here we only assert
	//    the in-memory shape stays consistent.
	dEmpty, err := m.CreateDeployment(ctx, Deployment{
		AppID:       app.ID,
		ImageDigest: "sha256:actor-mem-empty",
		Kind:        DeploymentKindImage,
		Status:      DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(empty): %v", err)
	}
	if dEmpty.DeployedByUserID != "" {
		t.Errorf("empty deployed_by_user_id = %q, want \"\"", dEmpty.DeployedByUserID)
	}
	if dEmpty.DeployedVia != "" {
		t.Errorf("empty deployed_via = %q, want \"\" (MemStore does not apply the coalesce('api') fallback)", dEmpty.DeployedVia)
	}
	if dEmpty.DeployedFromIP != "" {
		t.Errorf("empty deployed_from_ip = %q, want \"\"", dEmpty.DeployedFromIP)
	}
	if dEmpty.PusherLogin != "" {
		t.Errorf("empty pusher_login = %q, want \"\"", dEmpty.PusherLogin)
	}
}

// TestMemStore_DeploymentAuditRoundtrip (issue #976 / ADR-122 /
// SAFE-RELEASES-E.2) pins the AppendDeploymentAudit + ListDeploymentAudit
// memstore surface. MemStore stores the DeploymentAudit struct
// directly in m.deploymentAudit; the "round-trip" is a write+read
// of the struct fields. The closed-set kind CHECK and the no-FK
// shape are DB-only (TestMigrations_00332_DeploymentAudit covers
// those); here we only assert the in-memory shape is consistent
// with the new methods.
func TestMemStore_DeploymentAuditRoundtrip(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	deploymentID := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	accountID := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")

	// (1) Write a deploy.created row with full payload — every field
	// round-trips cleanly through the in-memory slice.
	id1, err := m.AppendDeploymentAudit(ctx, DeploymentAudit{
		DeploymentID: deploymentID,
		AccountID:    &accountID,
		Kind:         DeployCreated,
		Actor:        "apid:dashboard",
		Data:         json.RawMessage(`{"ref":"sha256:abc","supersedes":""}`),
	})
	if err != nil {
		t.Fatalf("AppendDeploymentAudit: %v", err)
	}
	if id1 == 0 {
		t.Errorf("AppendDeploymentAudit id = 0, want non-zero (monotonic counter)")
	}

	// (2) Write a deploy.source_ref row for a different deployment —
	// the ListDeploymentAudit filter must NOT cross-contaminate.
	otherDeploymentID := uuid.MustParse("99999999-8888-7777-6666-555555555555")
	if _, err := m.AppendDeploymentAudit(ctx, DeploymentAudit{
		DeploymentID: otherDeploymentID,
		AccountID:    &accountID,
		Kind:         DeploySourceRef,
		Actor:        "apid:cli",
		Data:         json.RawMessage(`{"ref":"refs/heads/main"}`),
	}); err != nil {
		t.Fatalf("AppendDeploymentAudit (other): %v", err)
	}

	// (3) ListDeploymentAudit for the original deployment returns
	// exactly the one row we wrote (the other-deployment row is
	// filtered out by deployment_id).
	rows, err := m.ListDeploymentAudit(ctx, deploymentID.String(), 0)
	if err != nil {
		t.Fatalf("ListDeploymentAudit: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListDeploymentAudit len = %d, want 1 (other-deployment row must NOT bleed in)", len(rows))
	}
	if rows[0].Kind != DeployCreated {
		t.Errorf("rows[0].Kind = %q, want %q", rows[0].Kind, DeployCreated)
	}
	if rows[0].Actor != "apid:dashboard" {
		t.Errorf("rows[0].Actor = %q, want %q", rows[0].Actor, "apid:dashboard")
	}
	if rows[0].DeploymentID != deploymentID {
		t.Errorf("rows[0].DeploymentID = %v, want %v", rows[0].DeploymentID, deploymentID)
	}
	if rows[0].AccountID == nil || *rows[0].AccountID != accountID {
		t.Errorf("rows[0].AccountID = %v, want %v", rows[0].AccountID, accountID)
	}
	if string(rows[0].Data) != `{"ref":"sha256:abc","supersedes":""}` {
		t.Errorf("rows[0].Data = %q, want verbatim payload", rows[0].Data)
	}

	// (4) ListDeploymentAudit with limit=0 caps to the 100-row
	// default (MemStore sets limit=100 when caller passes <= 0).
	// The single row in scope stays under that ceiling.
	rowsLimited, err := m.ListDeploymentAudit(ctx, deploymentID.String(), 1)
	if err != nil {
		t.Fatalf("ListDeploymentAudit(limit=1): %v", err)
	}
	if len(rowsLimited) != 1 {
		t.Errorf("ListDeploymentAudit(limit=1) len = %d, want 1", len(rowsLimited))
	}

	// (5) Zero At — MemStore defaults to time.Now() (the
	// pgstore path uses the column default coalesce($5, now())).
	// Assert At is non-zero on the read-back.
	if rows[0].At.IsZero() {
		t.Errorf("rows[0].At is zero — MemStore must default At to time.Now() when caller omits it")
	}
}

// TestMemStoreUpdateTrigger_FilterCriteriaPersists is the regression
// test for REVIEW-FIX MED-1 (PR #993 / issue #757 closure): the
// UpdateTrigger signature gained a filterCriteria *[]byte argument
// and the inline SQL grew a coalesce($9::jsonb, filter_criteria)
// branch. Pre-fix, a PATCH that flipped filter_criteria was
// silently dropped on the floor; post-fix, the JSONB column is
// replaced and TriggerByID reads it back.
//
// The memstore path mirrors the pgstore coalesce() semantics:
// nil pointer → leave column unchanged; non-nil byte slice →
// replace.
func TestMemStoreUpdateTrigger_FilterCriteriaPersists(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	// Seed an account + app so the trigger can be created.
	acct, err := m.CreateAccount(ctx, "alice@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{
		AccountID: acct.ID,
		Slug:      "smoke",
		Runtime:   "node22",
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	// CreateTriggerIfUnderQuota doesn't accept filter_criteria in
	// the memstore path (the column starts at nil); use the pgtest
	// path for the seeded filter check, then UpdateTrigger to
	// install one.
	trig, err := m.CreateTriggerIfUnderQuota(ctx, app.ID, "kafka", "orders", true, []byte(`{"brokers":["localhost:9092"]}`), 100, 1000, 3, 1024, "commit", api.Limits{})
	if err != nil {
		t.Fatalf("CreateTriggerIfUnderQuota: %v", err)
	}
	triggerID := uuidFromPgtype(trig.ID).String()

	// Round 1: nil pointer → filter_criteria stays nil.
	if _, err := m.UpdateTrigger(ctx, triggerID, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("UpdateTrigger nil filterCriteria: %v", err)
	}
	got, err := m.TriggerByID(ctx, triggerID)
	if err != nil {
		t.Fatalf("TriggerByID after nil patch: %v", err)
	}
	if got.FilterCriteria != nil {
		t.Errorf("after nil filterCriteria patch, got %q, want nil", got.FilterCriteria)
	}

	// Round 2: non-nil → filter_criteria is replaced.
	payload := []byte(`{"payload":[{"op":"eq","path":"$.event.type","value":"order.created"}]}`)
	if _, err := m.UpdateTrigger(ctx, triggerID, nil, nil, nil, nil, nil, nil, nil, &payload); err != nil {
		t.Fatalf("UpdateTrigger with filterCriteria: %v", err)
	}
	got, err = m.TriggerByID(ctx, triggerID)
	if err != nil {
		t.Fatalf("TriggerByID after set patch: %v", err)
	}
	if string(got.FilterCriteria) != string(payload) {
		t.Errorf("after set filterCriteria patch, got %q, want %q", got.FilterCriteria, payload)
	}

	// Round 3: a second patch with a different payload replaces
	// again (not coalesced to the previous one) — proves the
	// replacement semantics.
	replacement := []byte(`{"$or":[{"payload":[{"op":"neq","path":"$.x","value":1}]}]}`)
	if _, err := m.UpdateTrigger(ctx, triggerID, nil, nil, nil, nil, nil, nil, nil, &replacement); err != nil {
		t.Fatalf("UpdateTrigger replacement: %v", err)
	}
	got, err = m.TriggerByID(ctx, triggerID)
	if err != nil {
		t.Fatalf("TriggerByID after replacement: %v", err)
	}
	if string(got.FilterCriteria) != string(replacement) {
		t.Errorf("after replacement patch, got %q, want %q", got.FilterCriteria, replacement)
	}
}

//   - closed-vocab guard (unknown fromStage → ErrInvalidArgument)
//   - the original row is NOT mutated (failure history stays
//     observable alongside the retry)
func TestMemStoreRetryDeploymentFromStage(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	acc, err := s.CreateAccount(ctx, "retry@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := s.CreateApp(ctx, App{AccountID: acc.ID, Slug: "retry-app"})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	// Create a failed deployment with non-default input primitives
	// so we can pin the copy. The status here is terminal (failed),
	// the input primitives are what the retry path must copy.
	sidecarsJSON := json.RawMessage(`[{"name":"redis","image":"redis:7"}]`)
	overrideEnv := json.RawMessage(`{"LOG_LEVEL":"debug"}`)
	customStages := json.RawMessage(`[{"percent":5,"duration":"1m"},{"percent":50,"duration":"1m"},{"percent":100,"duration":"0s"}]`)
	oldStepStarted := time.Now().UTC().Add(-time.Hour)
	failed, err := s.CreateDeployment(ctx, Deployment{
		ID:                  "d-failed-retry",
		AppID:               app.ID,
		Status:              DeployFailed,
		ImageDigest:         "sha256:orig",
		Kind:                DeploymentKindTarball,
		SourceRoot:          "apps/api",
		SourceURL:           "https://github.com/example/repo",
		CommitSHA:           "abc1234",
		Sidecars:            sidecarsJSON,
		OverrideEnv:         overrideEnv,
		OverridePort:        9090,
		TrafficPercent:      50,
		MinInstances:        1,
		Priority:            7,
		Scope:               "staging",
		OverrideEntrypoint:  []string{"node", "server.js"},
		RollbackOn5xx:       true,
		Reason:              "retry after dependency recovery",
		Tag:                 "hotfix",
		DeployedBy:          "Release Operator",
		PRNumber:            1419,
		CanaryPreset:        "custom",
		CanaryStep:          2,
		CanaryTotalSteps:    3,
		CanaryStepStartedAt: &oldStepStarted,
		CanaryStages:        customStages,
		RolloutState:        "rolling_out",
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	// Happy path: retry from snapshot_prepare.
	got, err := s.RetryDeploymentFromStage(ctx, failed.ID, StageSnapshotPrepare)
	if err != nil {
		t.Fatalf("RetryDeploymentFromStage: %v", err)
	}
	if got.ID == failed.ID {
		t.Errorf("retry returned same id %q; want a fresh id", failed.ID)
	}
	if got.AppID != failed.AppID {
		t.Errorf("AppID not copied: got %q, want %q", got.AppID, failed.AppID)
	}
	if got.ImageDigest != failed.ImageDigest {
		t.Errorf("ImageDigest not copied: got %q, want %q", got.ImageDigest, failed.ImageDigest)
	}
	if got.Kind != failed.Kind {
		t.Errorf("Kind not copied: got %q, want %q", got.Kind, failed.Kind)
	}
	if got.SourceRoot != failed.SourceRoot {
		t.Errorf("SourceRoot not copied: got %q, want %q", got.SourceRoot, failed.SourceRoot)
	}
	if got.SourceURL != failed.SourceURL {
		t.Errorf("SourceURL not copied: got %q, want %q", got.SourceURL, failed.SourceURL)
	}
	if got.CommitSHA != failed.CommitSHA {
		t.Errorf("CommitSHA not copied: got %q, want %q", got.CommitSHA, failed.CommitSHA)
	}
	if string(got.Sidecars) != string(failed.Sidecars) {
		t.Errorf("Sidecars not copied: got %s, want %s", got.Sidecars, failed.Sidecars)
	}
	if string(got.OverrideEnv) != string(failed.OverrideEnv) {
		t.Errorf("OverrideEnv not copied: got %s, want %s", got.OverrideEnv, failed.OverrideEnv)
	}
	if got.OverridePort != failed.OverridePort {
		t.Errorf("OverridePort not copied: got %d, want %d", got.OverridePort, failed.OverridePort)
	}
	if got.TrafficPercent != 5 {
		t.Errorf("TrafficPercent = %d, want first custom canary stage 5", got.TrafficPercent)
	}
	if got.MinInstances != failed.MinInstances {
		t.Errorf("MinInstances not copied: got %d, want %d", got.MinInstances, failed.MinInstances)
	}
	if got.Scope != failed.Scope {
		t.Errorf("Scope not copied: got %q, want %q", got.Scope, failed.Scope)
	}
	if len(got.OverrideEntrypoint) != len(failed.OverrideEntrypoint) {
		t.Errorf("OverrideEntrypoint not copied: got %v, want %v", got.OverrideEntrypoint, failed.OverrideEntrypoint)
	}
	if got.Status != DeployPending {
		t.Errorf("Status = %q, want %q (reset for imaged pickup)", got.Status, DeployPending)
	}
	if !got.RollbackOn5xx || got.Reason != failed.Reason || got.Tag != failed.Tag || got.DeployedBy != failed.DeployedBy || got.PRNumber != failed.PRNumber || got.Priority != failed.Priority {
		t.Errorf("retry lost policy or annotation metadata: got=%+v", got)
	}
	if got.CanaryPreset != "custom" || got.CanaryStep != 0 || got.CanaryTotalSteps != 3 || string(got.CanaryStages) != string(customStages) {
		t.Errorf("retry canary state = preset=%q step=%d total=%d stages=%s", got.CanaryPreset, got.CanaryStep, got.CanaryTotalSteps, got.CanaryStages)
	}
	if got.CanaryStepStartedAt == nil || !got.CanaryStepStartedAt.After(oldStepStarted) {
		t.Errorf("retry canary timer = %v, want fresh timestamp after %v", got.CanaryStepStartedAt, oldStepStarted)
	}
	if got.RolloutState != "pending" || got.RolloutStartedAt != nil || got.RolloutCompletedAt != nil || got.RolloutAbortedAt != nil {
		t.Errorf("retry rollout execution state not reset: %+v", got)
	}
	// Stage-state seed.
	var state StageState
	if err := json.Unmarshal(got.StageState, &state); err != nil {
		t.Fatalf("decode new stage_state: %v", err)
	}
	if state.Current != StageSourceDownload || state.RetryRequestedStage != StageSnapshotPrepare {
		t.Errorf("retry stage state = %+v; want actual source_download, requested snapshot_prepare", state)
	}
	if state.CurrentStartedAt != nil {
		t.Errorf("new stage_state.CurrentStartedAt = %v, want nil", state.CurrentStartedAt)
	}
	if len(state.History) != 0 {
		t.Errorf("new stage_state.History length = %d, want 0", len(state.History))
	}
	// Original row not mutated.
	original, err := s.DeploymentByID(ctx, failed.ID)
	if err != nil {
		t.Fatalf("DeploymentByID (failed): %v", err)
	}
	if original.Status != DeployFailed {
		t.Errorf("original.Status flipped: got %q, want %q", original.Status, DeployFailed)
	}
	if _, err := s.RetryDeploymentFromStage(ctx, got.ID, StageSourceDownload); !errors.Is(err, ErrConflict) {
		t.Errorf("nonfailed deployment retry: got %v, want ErrConflict", err)
	}

	// Closed-vocab guard: unknown fromStage → ErrInvalidArgument.
	if _, err := s.RetryDeploymentFromStage(ctx, failed.ID, StageName("not_a_stage")); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("unknown fromStage: got %v, want ErrInvalidArgument", err)
	}
	// Empty fromStage → ErrInvalidArgument.
	if _, err := s.RetryDeploymentFromStage(ctx, failed.ID, StageName("")); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("empty fromStage: got %v, want ErrInvalidArgument", err)
	}
	// Unknown failedID → ErrNotFound.
	if _, err := s.RetryDeploymentFromStage(ctx, "d-does-not-exist", StageSourceDownload); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown failedID: got %v, want ErrNotFound", err)
	}

	// Retry-from-top: fromStage=source_download re-runs the whole
	// pipeline. Intentional — that's how a user "retry from the top"
	// works.
	top, err := s.RetryDeploymentFromStage(ctx, failed.ID, StageSourceDownload)
	if err != nil {
		t.Fatalf("retry from source_download: %v", err)
	}
	var topState StageState
	if err := json.Unmarshal(top.StageState, &topState); err != nil {
		t.Fatalf("decode top retry stage_state: %v", err)
	}
	if topState.Current != StageSourceDownload {
		t.Errorf("top retry stage_state.Current = %q, want %q", topState.Current, StageSourceDownload)
	}
}

// TestIsStageName covers the closed-vocab lookup helper. Used by
// the apid retry handler to validate wire-supplied from_stage
// values before the storage call.
func TestIsStageName(t *testing.T) {
	for _, n := range AllStageNames {
		if !IsStageName(n) {
			t.Errorf("IsStageName(%q) = false, want true (closed-6 vocabulary)", n)
		}
	}
	for _, n := range []StageName{"", "not_a_stage", "SOURCE_DOWNLOAD", "Source_Download"} {
		if IsStageName(n) {
			t.Errorf("IsStageName(%q) = true, want false", n)
		}
	}
}

// TestMemStore_StampFirstWake covers the wake-window stamp on
// the deployments row (Mega-C PR-2 / issue #961 leaf 8). The
// stamp is idempotent — a second wake must NOT shift the window
// boundary. windowMinutes defaults to 5 when non-positive.
//
// Migration 00297 / Mega-C PR-2 / issue #961 leaf 8.
func TestMemStore_StampFirstWake(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	acc, err := m.CreateAccount(ctx, "p@p", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{
		AccountID: acc.ID, Slug: "first-wake", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
		Status: AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:fw"})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	// (1) First wake stamps both fields. windowMinutes defaults to 5.
	got, err := m.StampFirstWake(ctx, dep.ID, 0)
	if err != nil {
		t.Fatalf("StampFirstWake 1st: %v", err)
	}
	if got.FirstWakeAt == nil {
		t.Fatal("FirstWakeAt: nil after first stamp")
	}
	if got.First5xxWindowEndsAt == nil {
		t.Fatal("First5xxWindowEndsAt: nil after first stamp")
	}
	firstEnd := *got.First5xxWindowEndsAt
	firstStart := *got.FirstWakeAt
	if !firstEnd.After(firstStart) {
		t.Errorf("window end %v not after start %v", firstEnd, firstStart)
	}

	// (2) Second wake is idempotent: same start, same end.
	got2, err := m.StampFirstWake(ctx, dep.ID, 5)
	if err != nil {
		t.Fatalf("StampFirstWake 2nd: %v", err)
	}
	if got2.FirstWakeAt == nil || !got2.FirstWakeAt.Equal(firstStart) {
		t.Errorf("Second stamp shifted FirstWakeAt: got %v, want %v", got2.FirstWakeAt, firstStart)
	}
	if got2.First5xxWindowEndsAt == nil || !got2.First5xxWindowEndsAt.Equal(firstEnd) {
		t.Errorf("Second stamp shifted First5xxWindowEndsAt: got %v, want %v", got2.First5xxWindowEndsAt, firstEnd)
	}

	// (3) Unknown deployment → ErrNotFound.
	if _, err := m.StampFirstWake(ctx, "no-such", 5); !errors.Is(err, ErrNotFound) {
		t.Errorf("StampFirstWake unknown: err = %v, want ErrNotFound", err)
	}
}

// TestMemStore_BumpFirst5xxCount covers the atomic 5xx-counter
// bump on the deployments row. Returns the post-increment count
// so schedd can do the threshold check immediately.
//
// Migration 00297 / Mega-C PR-2 / issue #961 leaf 8.
func TestMemStore_BumpFirst5xxCount(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	acc, err := m.CreateAccount(ctx, "p@p", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{
		AccountID: acc.ID, Slug: "bump-5xx", Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
		Status: AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:bump"})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	for want := 1; want <= 5; want++ {
		got, err := m.BumpFirst5xxCount(ctx, dep.ID)
		if err != nil {
			t.Fatalf("BumpFirst5xxCount #%d: %v", want, err)
		}
		if got != want {
			t.Errorf("BumpFirst5xxCount #%d = %d, want %d", want, got, want)
		}
	}

	if _, err := m.BumpFirst5xxCount(ctx, "no-such"); !errors.Is(err, ErrNotFound) {
		t.Errorf("BumpFirst5xxCount unknown: err = %v, want ErrNotFound", err)
	}
}

// TestMemStore_AutoRollbackDeploymentsTx covers the §6.2-1
// invariant guard inside AutoRollbackDeploymentsTx. A non-live
// current deployment must surface ErrNotFound; a live current
// deployment with no prior superseded must surface ErrNotFound
// too (no candidate to swap to).
//
// Migration 00297 / Mega-C PR-2 / issue #961 leaf 8.
func TestMemStore_AutoRollbackDeploymentsTx(t *testing.T) {
	ctx := context.Background()

	t.Run("not_live_current_returns_NotFound", func(t *testing.T) {
		m := NewMemStore()
		acc, err := m.CreateAccount(ctx, "p@p", api.PlanPro)
		if err != nil {
			t.Fatalf("CreateAccount: %v", err)
		}
		app, err := m.CreateApp(ctx, App{
			AccountID: acc.ID, Slug: "ar-notlive", Type: AppTypeApp,
			RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
			Status: AppActive,
		})
		if err != nil {
			t.Fatalf("CreateApp: %v", err)
		}
		dep, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:nl"})
		if err != nil {
			t.Fatalf("CreateDeployment: %v", err)
		}
		// dep is in 'pending' (default), not 'live'.
		if _, err := m.AutoRollbackDeploymentsTx(ctx, app.ID, dep.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("AutoRollbackDeploymentsTx on non-live: err = %v, want ErrNotFound", err)
		}
	})
}

// TestDeployment_DeploymentPreviewActive (issue #976 / ADR-122 /
// SAFE-RELEASES-C) pins the predicate the cert allowlist consults
// for the deployment-preview branch. The method lives on state.Deployment
// (not pkg/gateway) so pkg/gateway stays free of pkg/state. The
// allowlist tests in pkg/gateway/allowlist_test.go use a
// fakeDeploymentRow that mirrors this method's logic — this test is
// the source of truth that the production method agrees with the
// fake.
func TestDeployment_DeploymentPreviewActive(t *testing.T) {
	cases := []struct {
		status DeploymentStatus
		want   bool
	}{
		{DeployPending, true},
		{DeployBuilding, true},
		{DeployImaging, true},
		{DeploySnapshotting, true},
		{DeployLive, true},
		{DeployFailed, false},
		{DeploySuperseded, false},
		{"", false}, // zero status denies — defensive against an un-stamped row
	}
	for _, tc := range cases {
		t.Run(string(tc.status), func(t *testing.T) {
			d := Deployment{Status: tc.status}
			if got := d.DeploymentPreviewActive(); got != tc.want {
				t.Errorf("DeploymentPreviewActive(status=%q) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

// TestMemStore_DeploymentOrdinal (issue #976 / ADR-122 /
// SAFE-RELEASES-C.2) pins the per-app 1-based ordinal the
// deployment-preview URL surface stamps. The round-trip is
// stable: ordinal(N) == N regardless of how many later deploys
// are inserted (the rank is recomputed across the whole window).
//
// Five assertions per app:
//   - The first deployment (by created_at) is ordinal 1.
//   - The third deployment is ordinal 3.
//   - The second deployment is ordinal 2.
//   - A deployment in a different app is ordinal 1 in that app.
//   - A missing deployment_id for the app is ErrNotFound.
//
// MemStore mirrors the pg-side row_number() — both implementations
// MUST agree (the memstore test is the regression pin for the
// pgstore test under TestPg_DeploymentOrdinal). Drift between
// the two impls corrupts every existing deployment-preview URL
// the moment a new deploy lands.
func TestMemStore_DeploymentOrdinal(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()

	a := uuid.NewString()
	b := uuid.NewString()
	d1 := uuid.NewString()
	d2 := uuid.NewString()
	d3 := uuid.NewString()
	dX := uuid.NewString()
	now := time.Now()
	// Insert out-of-order to exercise the (created_at, id) sort.
	mustInsertDeployment(t, m, Deployment{
		ID: d3, AppID: a, Status: DeployLive, CreatedAt: now.Add(2 * time.Second),
	})
	mustInsertDeployment(t, m, Deployment{
		ID: d1, AppID: a, Status: DeployLive, CreatedAt: now,
	})
	mustInsertDeployment(t, m, Deployment{
		ID: d2, AppID: a, Status: DeployLive, CreatedAt: now.Add(1 * time.Second),
	})
	mustInsertDeployment(t, m, Deployment{
		ID: dX, AppID: b, Status: DeployLive, CreatedAt: now,
	})

	if got, _ := m.DeploymentOrdinal(ctx, a, d1); got != 1 {
		t.Errorf("ord(d1) = %d, want 1", got)
	}
	if got, _ := m.DeploymentOrdinal(ctx, a, d2); got != 2 {
		t.Errorf("ord(d2) = %d, want 2", got)
	}
	if got, _ := m.DeploymentOrdinal(ctx, a, d3); got != 3 {
		t.Errorf("ord(d3) = %d, want 3", got)
	}
	if got, _ := m.DeploymentOrdinal(ctx, b, dX); got != 1 {
		t.Errorf("ord(dX in app b) = %d, want 1 (separate counter)", got)
	}
	if _, err := m.DeploymentOrdinal(ctx, a, uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing deployment: err = %v, want ErrNotFound", err)
	}
}

// mustInsertDeployment is a thin helper for the ordinal test;
// mirrors the production CreateDeployment stub pattern but inserts
// directly into the memstore map (CreateDeployment is heavier
// than necessary — for the ordinal query we only need the {id,
// app_id, status, created_at} columns on a single tenant).
func mustInsertDeployment(t *testing.T, m *MemStore, d Deployment) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deployments[d.ID] = d
}

// TestRecoverRolloutStuckAfter_EnvOverride pins the
// env-scoped stuck-after threshold setter
// (production-leveling Stream C): a positive duration
// updates the var; zero / negative values are silently
// ignored so a bad env parse never inverts the stuck
// predicate. The test saves + restores the original
// var so the package-level mutation doesn't leak into
// sibling tests in the same `go test` run.
func TestRecoverRolloutStuckAfter_EnvOverride(t *testing.T) {
	original := RecoverRolloutStuckAfter
	t.Cleanup(func() { RecoverRolloutStuckAfter = original })

	// 1) positive duration applies.
	SetRecoverRolloutStuckAfter(5 * time.Minute)
	if got := RecoverRolloutStuckAfter; got != 5*time.Minute {
		t.Errorf("after SetRecoverRolloutStuckAfter(5m): got %s, want 5m", got)
	}

	// 2) zero is silently ignored (var stays at 5m).
	SetRecoverRolloutStuckAfter(0)
	if got := RecoverRolloutStuckAfter; got != 5*time.Minute {
		t.Errorf("after SetRecoverRolloutStuckAfter(0): got %s, want 5m (zero must be ignored)", got)
	}

	// 3) negative is silently ignored (would invert the
	// stuck predicate if applied — silent ignore is the
	// documented contract).
	SetRecoverRolloutStuckAfter(-1 * time.Second)
	if got := RecoverRolloutStuckAfter; got != 5*time.Minute {
		t.Errorf("after SetRecoverRolloutStuckAfter(-1s): got %s, want 5m (negative must be ignored)", got)
	}
}
