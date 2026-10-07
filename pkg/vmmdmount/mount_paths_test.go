// mount_paths_test.go — branch coverage for the mount-path
// matrix in pkg/vmmdmount/mount.go.
//
// mount_test.go covers the Registry book-keeping
// (RegisterOrEvict, LoadShed, SweepOrphans empty, Umount
// unknown idempotent). What this file pins WITHOUT a swappable
// umount hook (none exists today; the umount functions are
// direct syscall wrappers that require root + a real mount):
//
//   - MountExt4ReadOnly: empty src precondition guard
//   - MountExt4ReadOnly: os.Stat src error path (nonexistent src)
//   - UmountExt4: prefix-guard rejection on a non-scratch path
//   - UmountExt4: empty-input → ErrUnknownMountpoint
//   - Registry.Umount MountKind dispatch: zero/garbage MountKind
//     hits the default branch (the no-real-syscall path)
//   - Registry.Umount: empty-store / empty-mountpoint idempotence
//   - MountKind zero value (zero-value-of-iota is NOT iota+1=1
//     or iota+2=2 — it's 0). Pinning this prevents a future
//     regression where someone adds MountKindParentExt4 = iota
//     (without +1) and loses the default branch in the switch.
//
// Whitebox test (package vmmdmount).
package vmmdmount

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestMountExt4ReadOnly_EmptySrc pins the precondition guard at
// the top of MountExt4ReadOnly: an empty src is rejected before
// the syscall runs (no /dev/loop leak, no busy SpinLoop).
func TestMountExt4ReadOnly_EmptySrc(t *testing.T) {
	_, err := MountExt4ReadOnly(context.Background(), "")
	if err == nil {
		t.Fatal("MountExt4ReadOnly(\"\"): want err, got nil")
	}
	if !contains(err.Error(), "empty") {
		t.Errorf("err = %v, want substring 'empty'", err)
	}
}

// TestMountExt4ReadOnly_StatMissingSrc pins the os.Stat src
// branch. A non-existent src is rejected before the syscall
// runs — no /dev/loop allocation, no half-mounted state.
func TestMountExt4ReadOnly_StatMissingSrc(t *testing.T) {
	_, err := MountExt4ReadOnly(context.Background(), "/no/such/file.ext4")
	if err == nil {
		t.Fatal("MountExt4ReadOnly(missing): want stat error, got nil")
	}
	if !contains(err.Error(), "stat src") {
		t.Errorf("err = %v, want substring 'stat src'", err)
	}
}

// TestUmountExt4_RefusesEmptyArg pins the empty-input
// ErrUnknownMountpoint short-circuit at the top of UmountExt4.
// The gRPC handler lifts this to InvalidArgument so imaged's
// defer-after-error pattern is idempotent.
func TestUmountExt4_RefusesEmptyArg(t *testing.T) {
	err := UmountExt4(context.Background(), "")
	if !errors.Is(err, ErrUnknownMountpoint) {
		t.Errorf("UmountExt4(\"\") = %v, want ErrUnknownMountpoint", err)
	}
}

// TestRegistry_Umount_UnknownMountKind pins the default branch
// of the MountKind switch in Registry.Umount. An entry with a
// out-of-band MountKind (here: MountKind(0), the zero value) is
// rejected with an "unknown MountKind" error; ownership remains
// available for an operator to inspect and repair.
//
// We construct the entry directly via the whitebox `r.entries`
// field to simulate a future MountKind that hasn't been wired
// into the dispatcher yet, or a buggy serializer that wrote
// MountKind=0.
func TestRegistry_Umount_UnknownMountKind(t *testing.T) {
	r := NewRegistry(8)
	mp := "/srv/fc/parent/faas-parent-mnt-unknown-kind"
	// Whitebox injection — MountKind zero value (0). NOT
	// MountKindParentExt4 (=1) or MountKindOverlayParent (=2).
	r.entries[mp] = MountEntry{
		Kind:       MountKind(0),
		StorageKey: "k",
		SrcPath:    "/s",
		MountedAt:  time.Now(),
	}
	found, err := r.Umount(context.Background(), mp)
	if err == nil {
		t.Fatal("unknown MountKind: want err, got nil")
	}
	if found {
		t.Errorf("found = true on default branch; want false (no dispatch)")
	}
	if !contains(err.Error(), "unknown MountKind") {
		t.Errorf("err = %v, want substring 'unknown MountKind'", err)
	}
	// Refusing dispatch does not prove that the kernel resource was released.
	if entry, ok := r.Lookup(mp); !ok || entry.Kind != MountKind(0) || entry.SrcPath != "/s" {
		t.Error("unknown mount cleanup lost its exact ownership record")
	}
}

// TestRegistry_Umount_UnknownMountKind_RangeBound pins that
// MountKind(99) (a made-up beyond-range value) ALSO hits the
// default branch. The dispatcher uses a finite constant set;
// a future addition that re-numbers the iota must continue to
// hit `default:` for any unrecognised value.
func TestRegistry_Umount_UnknownMountKind_RangeBound(t *testing.T) {
	r := NewRegistry(8)
	mp := "/srv/fc/parent/faas-parent-mnt-mk-99"
	r.entries[mp] = MountEntry{
		Kind:       MountKind(99),
		StorageKey: "k",
		SrcPath:    "/s",
		MountedAt:  time.Now(),
	}
	found, err := r.Umount(context.Background(), mp)
	if err == nil {
		t.Fatal("MountKind(99): want err, got nil")
	}
	if found {
		t.Errorf("found = true on default; want false")
	}
	if !contains(err.Error(), "MountKind=99") {
		t.Errorf("err = %v, want substring 'MountKind=99'", err)
	}
}

// TestRegistry_Umount_EmptyMountpoint pins the
// "unknown mountpoint → (false, nil)" idempotence branch. A
// caller that pre-emptively Umounts (defer-after-error) on a
// path the registry never held must see found=false, err=nil.
func TestRegistry_Umount_EmptyMountpoint(t *testing.T) {
	r := NewRegistry(8)
	found, err := r.Umount(context.Background(), "")
	if err != nil {
		t.Errorf("Umount(\"\"): want nil err (idempotent), got %v", err)
	}
	if found {
		t.Errorf("Umount(\"\"): found = true, want false")
	}
}

// TestMountKind_ZeroNotEqualEnum pins the iota+1 design intent:
// the zero value of MountKind must NOT collide with the
// declared constants. If a refactor accidentally drops the +1
// from `MountKindParentExt4 = iota`, MountKind(0) becomes
// MountKindParentExt4 and the default-branch tests above lose
// coverage of the dispatch's `default:` arm.
func TestMountKind_ZeroNotEqualEnum(t *testing.T) {
	if MountKind(0) == MountKindParentExt4 {
		t.Errorf("MountKind(0) collides with MountKindParentExt4 — iota+1 lost?")
	}
	if MountKind(0) == MountKindOverlayParent {
		t.Errorf("MountKind(0) collides with MountKindOverlayParent — iota+1 lost?")
	}
	// And the enum values themselves must be distinct.
	if MountKindParentExt4 == MountKindOverlayParent {
		t.Error("MountKindParentExt4 == MountKindOverlayParent — enum collapse")
	}
}

// TestUmountExt4_RefusesForeignPath_PrefixBranch pins the
// prefix-guard branch of UmountExt4: a path outside
// /srv/fc/parent/faas-parent-* is rejected WITHOUT running the
// syscall. This is the defence-in-depth check that stops a
// caller from handing back a path under / or /home/foo and
// silently umounting whatever the kernel has there.
func TestUmountExt4_RefusesForeignPath_PrefixBranch(t *testing.T) {
	for _, bad := range []string{
		"/",
		"/home/foo",
		"/etc/passwd",
		"/var/log/faas",
		"/tmp/faas-parent-mnt-evil-prefix", // right prefix, wrong scratch root
	} {
		err := UmountExt4(context.Background(), bad)
		if err == nil {
			t.Errorf("UmountExt4(%q) = nil, want prefix-check error", bad)
			continue
		}
		if !contains(err.Error(), "outside vmmd's parent-mnt scratch") {
			t.Errorf("UmountExt4(%q) = %v, want prefix-check error", bad, err)
		}
	}
}

// --- helpers ---

// contains is a tiny strings.Contains replacement so this file
// doesn't pull in the strings package just for one or two checks.
func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
