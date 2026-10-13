//go:build linux || darwin

// adr: 590
package fcvm

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestNativeArchiveEvidencePreservesOriginalAfterReplacement(t *testing.T) {
	j, lease, original := retiredNativeJournal(t)
	v := &JailerVMM{nativeRecovery: &nativeProcessRecoveryRuntime{journal: j}}
	expected := nativeLaunchEvidenceFromRecord(original)
	newer, err := j.replace(t.Context(), lease, original.Generation, false)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(j.root, "retired", original.Generation+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, backend := range []*JailerVMM{v, {nativeRecovery: &nativeProcessRecoveryRuntime{journal: nativeJournalFixture(j.root)}}} {
		observed, err := backend.ObserveArchivedNativeLaunchGeneration(t.Context(), expected.Identity)
		if err != nil || observed != expected || observed.ResourcesRemoved {
			t.Fatal("original archive changed or inferred resource closure", err)
		}
		current, err := backend.ObserveNativeLaunch(t.Context(), lease.Instance)
		if err != nil || current.Identity.Generation != newer.Generation {
			t.Fatal("archive read replaced current owner", err)
		}
	}
	// Current metadata absence must not erase a retained original generation.
	if err = os.Remove(j.path(lease.Instance)); err != nil {
		t.Fatal(err)
	}
	observed, err := v.ObserveArchivedNativeLaunchGeneration(t.Context(), expected.Identity)
	if err != nil || observed != expected {
		t.Fatal("current absence erased original evidence", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("archive observation changed evidence", err)
	}
	for _, change := range []func(*NativeLaunchIdentity){
		func(x *NativeLaunchIdentity) { x.Generation = uuid.NewString() },
		func(x *NativeLaunchIdentity) { x.InstanceID = "different-instance" },
		func(x *NativeLaunchIdentity) { x.KernelBootID = uuid.NewString() },
		func(x *NativeLaunchIdentity) { x.PID = 42; x.StartTime = 123 },
		func(x *NativeLaunchIdentity) { x.Generation = "../escape" },
	} {
		identity := expected.Identity
		change(&identity)
		if out, err := v.ObserveArchivedNativeLaunchGeneration(t.Context(), identity); err == nil || out != (NativeLaunchEvidence{}) {
			t.Fatal("foreign or invalid archive accepted")
		}
	}
}

func TestNativeArchiveEvidenceRejectsUnsafeArchives(t *testing.T) {
	for _, damage := range []string{"missing", "malformed", "unretired", "symlink", "public", "prior-boot", "canceled"} {
		t.Run(damage, func(t *testing.T) {
			j, lease, original := retiredNativeJournal(t)
			if _, err := j.replace(t.Context(), lease, original.Generation, false); err != nil {
				t.Fatal(err)
			}
			v := &JailerVMM{nativeRecovery: &nativeProcessRecoveryRuntime{journal: j}}
			expected := nativeLaunchEvidenceFromRecord(original).Identity
			path := filepath.Join(j.root, "retired", original.Generation+".json")
			ctx := t.Context()
			switch damage {
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				if err := os.WriteFile(path, []byte(`{"version":1}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "unretired":
				original.Revoked = false
				original.ExitConfirmed = false
				if err := writeNativeLaunchRecord(path, original); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				other := filepath.Join(j.root, "private-copy")
				if err := os.Rename(path, other); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, path); err != nil {
					t.Fatal(err)
				}
			case "public":
				if err := os.Chmod(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
			case "prior-boot":
				j.bootID = func() (string, error) { return uuid.NewString(), nil }
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if out, err := v.ObserveArchivedNativeLaunchGeneration(ctx, expected); err == nil || out != (NativeLaunchEvidence{}) {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}
