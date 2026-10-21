// Memstore parity tests for the IAM hardening (PR #653) provenance
// + binding-hash + retention shape. The new methods on Store land at
// MemStore and PgStore in lockstep; this file lifts MemStore coverage
// so the pkg/state ≥ 70% gate stays green even before the pgstore
// parity tests fire (those run against a real PG service container).
//
// Mirrors the iam6 org-keys pattern in memstore_iam6_org_keys_test.go.
package state_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStore_DeleteOldEvents_RetentionTrim(t *testing.T) {
	m, ctx, _, _, _ := state.MemCoverageFixture(t)
	subj := uuid.NewString()
	if err := m.AppendEvent(ctx, "test", "auth.session.binding_mismatch", &subj, []byte(`{"x":1}`)); err != nil {
		t.Fatalf("append event: %v", err)
	}
	cutoff := time.Now().Add(time.Minute)
	// Sleep so the appended event is before cutoff.
	time.Sleep(10 * time.Millisecond)
	removed, err := m.DeleteOldEvents(ctx, cutoff)
	if err != nil {
		t.Fatalf("DeleteOldEvents: %v", err)
	}
	if removed != 1 {
		t.Errorf("DeleteOldEvents removed = %d, want 1", removed)
	}
	if _, err := m.DeleteOldEvents(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Errorf("DeleteOldEvents no-op: %v", err)
	}
}

func TestMemStore_CreateAPIKeyWithExpiryAndProvenance_RoundTrip(t *testing.T) {
	m, ctx, account, _, _ := state.MemCoverageFixture(t)
	exp := time.Now().Add(24 * time.Hour)
	parent := "parent-" + uuid.NewString()
	key, err := m.CreateAPIKeyWithExpiryAndProvenance(ctx, account.ID, []byte{0x01, 0x02, 0x03, 0x04}, "cov-key", []string{"read"}, &exp, "10.0.0.5", "Mozilla/5.0", &parent)
	if err != nil {
		t.Fatalf("CreateAPIKeyWithExpiryAndProvenance: %v", err)
	}
	if key.CreatedIP != "10.0.0.5" {
		t.Errorf("CreatedIP = %q, want 10.0.0.5", key.CreatedIP)
	}
	if key.CreatedUA != "Mozilla/5.0" {
		t.Errorf("CreatedUA = %q, want Mozilla/5.0", key.CreatedUA)
	}
	if key.ParentKeyID == nil || *key.ParentKeyID != parent {
		t.Errorf("ParentKeyID = %v, want %q", key.ParentKeyID, parent)
	}

	// Nil provenance: empty IP/UA, nil parent — pre-PR keys have NULL everywhere.
	key2, err := m.CreateAPIKeyWithExpiryAndProvenance(ctx, account.ID, []byte{0x05}, "cov-key-nil", []string{"read"}, &exp, "", "", nil)
	if err != nil {
		t.Fatalf("CreateAPIKeyWithExpiryAndProvenance nil: %v", err)
	}
	if key2.CreatedIP != "" || key2.CreatedUA != "" || key2.ParentKeyID != nil {
		t.Errorf("nil provenance: ip=%q ua=%q parent=%v", key2.CreatedIP, key2.CreatedUA, key2.ParentKeyID)
	}
}

func TestMemStore_CreateOrgAPIKeyWithProvenance_RoundTrip(t *testing.T) {
	m, ctx, account, _, _ := state.MemCoverageFixture(t)
	orgID := "org-" + uuid.NewString()
	exp := time.Now().Add(time.Hour)
	parent := "parent-" + uuid.NewString()
	key, err := m.CreateOrgAPIKeyWithProvenance(ctx, orgID, account.ID, []byte{0x10, 0x20}, "ci-deploy", []string{"deploy:write"}, &exp, "192.0.2.10", "curl/8.0", &parent)
	if err != nil {
		t.Fatalf("CreateOrgAPIKeyWithProvenance: %v", err)
	}
	if key.OrgID != orgID {
		t.Errorf("OrgID = %q, want %q", key.OrgID, orgID)
	}
	if key.CreatedIP != "192.0.2.10" || key.CreatedUA != "curl/8.0" {
		t.Errorf("provenance = %q/%q", key.CreatedIP, key.CreatedUA)
	}
	if key.ParentKeyID == nil || *key.ParentKeyID != parent {
		t.Errorf("ParentKeyID = %v", key.ParentKeyID)
	}

	// Unknown account → MemStore does not pre-validate; the row is
	// accepted and a later GetOrgAPIKey would surface the issue.
	// (PgStore enforces the FK; MemStore mirrors pgstore semantics
	// only where the invariants are local — no FK here.) We only
	// assert the call doesn't panic and returns a row.
	if _, err := m.CreateOrgAPIKeyWithProvenance(ctx, orgID, "missing", []byte{0x11}, "x", []string{"admin"}, nil, "", "", nil); err != nil {
		t.Errorf("missing account err = %v, want nil (MemStore no FK check)", err)
	}

	// Duplicate hash → "state: duplicate key hash" sentinel (matches
	// the MemStore CreateOrgAPIKey convention at memstore.go:1422).
	if _, err := m.CreateOrgAPIKeyWithProvenance(ctx, orgID, account.ID, []byte{0x10, 0x20}, "dup", []string{"admin"}, nil, "", "", nil); err == nil || err.Error() != "state: duplicate key hash" {
		t.Errorf("duplicate hash err = %v, want state: duplicate key hash", err)
	}
}

func TestMemStore_RotateOrgAPIKeyWithProvenance_Chain(t *testing.T) {
	m, ctx, account, _, _ := state.MemCoverageFixture(t)
	orgID := "org-" + uuid.NewString()
	exp := time.Now().Add(time.Hour)
	old, err := m.CreateOrgAPIKey(ctx, orgID, account.ID, []byte{0x30}, "to-rotate", []string{"admin"}, &exp)
	if err != nil {
		t.Fatalf("CreateOrgAPIKey: %v", err)
	}
	newHash := []byte{0x31, 0x32}
	newLabel := "to-rotate-rotated"
	parent := old.ID
	createdIP := "198.51.100.7"
	createdUA := "Mozilla/5.0"
	newKey, _, err := m.RotateOrgAPIKeyWithProvenance(ctx, orgID, old.ID, newHash, newLabel, time.Hour, createdIP, createdUA, &parent)
	if err != nil {
		t.Fatalf("RotateOrgAPIKeyWithProvenance: %v", err)
	}
	if newKey.Label != newLabel {
		t.Errorf("rotated label = %q, want %q", newKey.Label, newLabel)
	}
	if newKey.CreatedIP != createdIP || newKey.CreatedUA != createdUA {
		t.Errorf("provenance = %q/%q", newKey.CreatedIP, newKey.CreatedUA)
	}
	if newKey.ParentKeyID == nil || *newKey.ParentKeyID != old.ID {
		t.Errorf("ParentKeyID = %v, want %q", newKey.ParentKeyID, old.ID)
	}
}

func TestMemStore_CreateSessionWithBinding_RoundTrip(t *testing.T) {
	m, ctx, account, _, _ := state.MemCoverageFixture(t)
	id := "sid-" + uuid.NewString()
	hash := "binding-" + uuid.NewString()
	s, err := m.CreateSessionWithBinding(ctx, id, account.ID, "10.0.0.5", "Mozilla/5.0", hash)
	if err != nil {
		t.Fatalf("CreateSessionWithBinding: %v", err)
	}
	if s.BindingHash != hash {
		t.Errorf("BindingHash = %q, want %q", s.BindingHash, hash)
	}
	if s.IssuedIP != "10.0.0.5" || s.IssuedUA != "Mozilla/5.0" {
		t.Errorf("IssuedIP/UA = %q/%q", s.IssuedIP, s.IssuedUA)
	}

	// Empty binding hash — pre-PR envelope round-trips with zero value.
	id2 := "sid-" + uuid.NewString()
	s2, err := m.CreateSessionWithBinding(ctx, id2, account.ID, "", "", "")
	if err != nil {
		t.Fatalf("CreateSessionWithBinding empty hash: %v", err)
	}
	if s2.BindingHash != "" {
		t.Errorf("empty hash round-tripped as %q", s2.BindingHash)
	}

	// Duplicate sid → ErrConflict.
	if _, err := m.CreateSessionWithBinding(ctx, id, account.ID, "", "", ""); !errors.Is(err, state.ErrConflict) {
		t.Errorf("duplicate sid err = %v, want ErrConflict", err)
	}

	// Unknown account → ErrNotFound.
	if _, err := m.CreateSessionWithBinding(ctx, "sid-missing", "missing", "", "", ""); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("missing account err = %v, want ErrNotFound", err)
	}
}

func TestMemStore_UpdateSessionBinding_IDORSafety(t *testing.T) {
	m, ctx, account, _, _ := state.MemCoverageFixture(t)
	id := "sid-" + uuid.NewString()
	hash1 := "hash-A"
	if _, err := m.CreateSessionWithBinding(ctx, id, account.ID, "10.0.0.5", "Mozilla/5.0", hash1); err != nil {
		t.Fatalf("CreateSessionWithBinding: %v", err)
	}

	hash2 := "hash-B"
	if err := m.UpdateSessionBinding(ctx, id, account.ID, hash2); err != nil {
		t.Fatalf("UpdateSessionBinding: %v", err)
	}
	got, err := m.GetSession(ctx, id)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.BindingHash != hash2 {
		t.Errorf("BindingHash = %q, want %q", got.BindingHash, hash2)
	}

	// Cross-account update → ErrNotFound (IDOR-safe).
	if err := m.UpdateSessionBinding(ctx, id, "other-acct", hash2); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("cross-account err = %v, want ErrNotFound", err)
	}

	// Missing sid → ErrNotFound.
	if err := m.UpdateSessionBinding(ctx, "sid-missing", account.ID, hash2); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("missing sid err = %v, want ErrNotFound", err)
	}

	// After revoke, UpdateSessionBinding must reject (matches pgstore).
	if _, err := m.RevokeSession(ctx, id, account.ID); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if err := m.UpdateSessionBinding(ctx, id, account.ID, hash2); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("post-revoke err = %v, want ErrNotFound", err)
	}
}

// TestMemStore_AppendEvent_DataCopy pins the defensive copy of the data
// slice (so a caller mutating their buffer post-Emit cannot corrupt the
// audit row). Lifted from the existing append-event semantics; the IAM
// PR touches no audit payload shape but the new retention path reads
// every event so the copy invariant is load-bearing.
func TestMemStore_AppendEvent_DataCopy(t *testing.T) {
	m, ctx, _, _, _ := state.MemCoverageFixture(t)
	original := bytes.Repeat([]byte{0xAB}, 8)
	if err := m.AppendEvent(ctx, "test", "iam.coverage", nil, original); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	// Mutate the caller's buffer.
	for i := range original {
		original[i] = 0x00
	}
	events, err := m.ListEvents(ctx, "", 10)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("no events")
	}
	for _, b := range events[0].Data {
		if b != 0xAB {
			t.Fatalf("AppendEvent stored a reference, not a copy")
		}
	}

	_ = api.PlanPro // anchor import for future plan-touching coverage.
}
