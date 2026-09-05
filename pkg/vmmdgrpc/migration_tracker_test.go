package vmmdgrpc

// migration_tracker_test.go: covers the migrationTracker accessors +
// sentinel errors + mintLeaseToken on pkg/vmmdgrpc/migration_handlers.go
// that PR #753 did not reach. The tracker is pure in-memory state —
// no Firecracker, no netns — so these are deterministic unit tests.

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// TestMigrationTracker_PutNewEntry — happy path: a fresh tracker
// accepts a put and round-trips via get.
func TestMigrationTracker_PutNewEntry(t *testing.T) {
	tr := newMigrationTracker()
	m := &activeMigration{instanceID: "inst-1", leaseToken: "tok"}
	if err := tr.put(m); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := tr.get("inst-1", "tok")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != m {
		t.Errorf("get returned %p, want same pointer %p", got, m)
	}
}

// TestMigrationTracker_PutDuplicate — second put on the same
// instanceID returns errAlreadyActive. This is the "duplicate
// Phase 1" gRPC codes.AlreadyExists branch.
func TestMigrationTracker_PutDuplicate(t *testing.T) {
	tr := newMigrationTracker()
	m := &activeMigration{instanceID: "inst-1", leaseToken: "tok1"}
	if err := tr.put(m); err != nil {
		t.Fatalf("first put: %v", err)
	}
	err := tr.put(&activeMigration{instanceID: "inst-1", leaseToken: "tok2"})
	if err == nil {
		t.Fatal("duplicate put returned nil; want errAlreadyActive")
	}
	var ea errAlreadyActive
	if !errors.As(err, &ea) {
		t.Errorf("put error = %v, want errors.As errAlreadyActive", err)
	}
	if ea.instanceID != "inst-1" {
		t.Errorf("errAlreadyActive.instanceID = %q, want inst-1", ea.instanceID)
	}
}

// TestMigrationTracker_GetUnknownInstance — get on an instanceID
// that was never put returns errNoLease. The handler maps this to
// codes.NotFound.
func TestMigrationTracker_GetUnknownInstance(t *testing.T) {
	tr := newMigrationTracker()
	_, err := tr.get("never", "tok")
	if err == nil {
		t.Fatal("get(unknown) returned nil; want errNoLease")
	}
	var nl errNoLease
	if !errors.As(err, &nl) {
		t.Errorf("get error = %v, want errors.As errNoLease", err)
	}
	if nl.instanceID != "never" {
		t.Errorf("errNoLease.instanceID = %q, want never", nl.instanceID)
	}
}

// TestMigrationTracker_GetLeaseMismatch — get with the wrong
// leaseToken returns errLeaseMismatch. Pins the contract that
// "right instance, wrong token" is NOT a NotFound — it's a
// PermissionDenied (lease mismatch → likely an attacker replaying).
func TestMigrationTracker_GetLeaseMismatch(t *testing.T) {
	tr := newMigrationTracker()
	if err := tr.put(&activeMigration{instanceID: "inst-1", leaseToken: "right"}); err != nil {
		t.Fatalf("put: %v", err)
	}
	_, err := tr.get("inst-1", "wrong")
	if err == nil {
		t.Fatal("get(wrong token) returned nil; want errLeaseMismatch")
	}
	var lm errLeaseMismatch
	if !errors.As(err, &lm) {
		t.Errorf("get error = %v, want errors.As errLeaseMismatch", err)
	}
	if lm.instanceID != "inst-1" {
		t.Errorf("errLeaseMismatch.instanceID = %q, want inst-1", lm.instanceID)
	}
}

// TestMigrationTracker_DeleteIsIdempotent — delete on a known
// instanceID removes it; subsequent get returns errNoLease; a
// second delete is a no-op (no panic, no error).
func TestMigrationTracker_DeleteIsIdempotent(t *testing.T) {
	tr := newMigrationTracker()
	if err := tr.put(&activeMigration{instanceID: "inst-1", leaseToken: "tok"}); err != nil {
		t.Fatalf("put: %v", err)
	}
	tr.delete("inst-1")
	if _, err := tr.get("inst-1", "tok"); err == nil {
		t.Errorf("get after delete returned nil; want errNoLease")
	}
	// Idempotent: a second delete must not panic.
	tr.delete("inst-1")
	tr.delete("never-existed")
}

// TestMigrationTracker_ListExpired — listExpired returns only
// entries whose LeaseExpiresAt is strictly before now. The active
// (non-expired) entry must NOT appear in the result.
func TestMigrationTracker_ListExpired(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	tr := newMigrationTracker()
	expired := &activeMigration{instanceID: "old", leaseToken: "t1", leaseExpiresAt: now.Add(-time.Minute)}
	live := &activeMigration{instanceID: "fresh", leaseToken: "t2", leaseExpiresAt: now.Add(time.Minute)}
	if err := tr.put(expired); err != nil {
		t.Fatalf("put expired: %v", err)
	}
	if err := tr.put(live); err != nil {
		t.Fatalf("put live: %v", err)
	}
	got := tr.listExpired(now)
	if len(got) != 1 {
		t.Fatalf("listExpired returned %d entries, want 1", len(got))
	}
	if got[0].instanceID != "old" {
		t.Errorf("listExpired[0] = %q, want old", got[0].instanceID)
	}
}

// TestMigrationTracker_ListExpiredEmpty — listExpired on an empty
// tracker returns nil/empty (not a panic).
func TestMigrationTracker_ListExpiredEmpty(t *testing.T) {
	tr := newMigrationTracker()
	got := tr.listExpired(time.Now())
	if len(got) != 0 {
		t.Errorf("listExpired(empty) = %v, want empty", got)
	}
}

func TestMigrationTracker_DeleteByLeaseTokenDoesNotDeleteReplacement(t *testing.T) {
	now := time.Now().UTC()
	tr := newMigrationTracker()
	old := &activeMigration{
		instanceID:     "inst-replaced",
		leaseToken:     "old-token",
		leaseExpiresAt: now.Add(-time.Minute),
	}
	if err := tr.put(old); err != nil {
		t.Fatalf("put old: %v", err)
	}
	expired := tr.listExpired(now)
	if len(expired) != 1 {
		t.Fatalf("listExpired = %d, want 1", len(expired))
	}
	tr.delete(old.instanceID)
	replacement := &activeMigration{
		instanceID:     old.instanceID,
		leaseToken:     "new-token",
		leaseExpiresAt: now.Add(time.Minute),
	}
	if err := tr.put(replacement); err != nil {
		t.Fatalf("put replacement: %v", err)
	}
	if tr.deleteByLeaseToken(expired[0].leaseToken) {
		t.Fatal("deleteByLeaseToken(old) = true, want false after replacement")
	}
	if _, err := tr.get(replacement.instanceID, replacement.leaseToken); err != nil {
		t.Fatalf("replacement lease was deleted: %v", err)
	}
}

// TestErrAlreadyActive_Message — pins the user-visible error
// string. Phase-1 / Phase-3 callers log this verbatim; a string
// change breaks log-grep tooling.
func TestErrAlreadyActive_Message(t *testing.T) {
	err := errAlreadyActive{instanceID: "inst-x"}
	if !strings.Contains(err.Error(), "inst-x") {
		t.Errorf("errAlreadyActive.Error() = %q, want substring inst-x", err.Error())
	}
	if !strings.Contains(err.Error(), "already active") {
		t.Errorf("errAlreadyActive.Error() = %q, want substring 'already active'", err.Error())
	}
}

// TestErrNoLease_Message — pins the user-visible error string.
func TestErrNoLease_Message(t *testing.T) {
	err := errNoLease{instanceID: "inst-y"}
	if !strings.Contains(err.Error(), "inst-y") {
		t.Errorf("errNoLease.Error() = %q, want substring inst-y", err.Error())
	}
	if !strings.Contains(err.Error(), "no active migration") {
		t.Errorf("errNoLease.Error() = %q, want substring 'no active migration'", err.Error())
	}
}

// TestErrLeaseMismatch_Message — pins the user-visible error string.
func TestErrLeaseMismatch_Message(t *testing.T) {
	err := errLeaseMismatch{instanceID: "inst-z"}
	if !strings.Contains(err.Error(), "inst-z") {
		t.Errorf("errLeaseMismatch.Error() = %q, want substring inst-z", err.Error())
	}
	if !strings.Contains(err.Error(), "lease token mismatch") {
		t.Errorf("errLeaseMismatch.Error() = %q, want substring 'lease token mismatch'", err.Error())
	}
}

// TestMintLeaseToken_Shape — the token is 32 hex characters
// (16 bytes encoded). Verify the shape on a successful RNG read
// (the production happy path; the uuid.NewString fallback is
// exercised by code review rather than a test — crypto/rand.Read
// failure is a degenerate host condition).
func TestMintLeaseToken_Shape(t *testing.T) {
	tok := mintLeaseToken()
	if len(tok) != 32 {
		t.Errorf("mintLeaseToken len = %d, want 32 hex chars", len(tok))
	}
	for i, c := range tok {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("mintLeaseToken[%d] = %q, want hex digit", i, c)
		}
	}
}

// TestMintLeaseToken_Unique — two consecutive tokens must differ
// (a duplicate would break the lease-mismatch contract; the
// tracker relies on token uniqueness to detect replay).
func TestMintLeaseToken_Unique(t *testing.T) {
	a := mintLeaseToken()
	b := mintLeaseToken()
	if a == b {
		t.Errorf("mintLeaseToken() returned identical token twice: %q", a)
	}
}

// TestToEgressAllowlist_Empty — empty input returns (nil, nil).
// The handler treats nil as "no allowlist configured" rather than
// "deny everything".
func TestToEgressAllowlist_Empty(t *testing.T) {
	got, err := toEgressAllowlist(nil)
	if err != nil {
		t.Errorf("toEgressAllowlist(nil): %v", err)
	}
	if got != nil {
		t.Errorf("toEgressAllowlist(nil) = %v, want nil", got)
	}
}

// TestToEgressAllowlist_Valid — valid CIDRs are parsed without
// error. The handler's ValidateEgressAllowlist upstream gate is
// what guarantees correctness; the toEgressAllowlist adapter
// trusts that input.
func TestToEgressAllowlist_Valid(t *testing.T) {
	in := []string{"10.0.0.0/8", "192.168.0.0/16", "2001:db8::/32"}
	got, err := toEgressAllowlist(in)
	if err != nil {
		t.Fatalf("toEgressAllowlist(valid): %v", err)
	}
	if len(got) != len(in) {
		t.Errorf("len(got) = %d, want %d", len(got), len(in))
	}
}

// TestToEgressAllowlist_Invalid — a malformed CIDR surfaces as a
// non-nil error wrapping api.Problem with CodeValidation. The
// handler maps this to codes.InvalidArgument on the wire.
func TestToEgressAllowlist_Invalid(t *testing.T) {
	_, err := toEgressAllowlist([]string{"10.0.0.0/8", "not-a-cidr"})
	if err == nil {
		t.Fatal("toEgressAllowlist(invalid) = nil; want validation error")
	}
	if !strings.Contains(err.Error(), "not-a-cidr") {
		t.Errorf("err = %v, want substring 'not-a-cidr'", err)
	}
}

// TestNewMigrationTracker_EmptyMap — fresh tracker has an empty
// (not nil) state map so listExpired/delete never panic on a nil-map
// write race.
func TestNewMigrationTracker_EmptyMap(t *testing.T) {
	tr := newMigrationTracker()
	if tr.state == nil {
		t.Fatal("newMigrationTracker().state = nil; want empty map")
	}
	if len(tr.state) != 0 {
		t.Errorf("newMigrationTracker().state has %d entries, want 0", len(tr.state))
	}
}
