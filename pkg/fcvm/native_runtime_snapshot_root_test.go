//go:build linux || darwin

// adr: 20261022184951 — amendment 1: native runtime snapshots live in a tree only vmmd writes.
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func nativeSnapshotTreesFixture(t *testing.T) (*JailerVMM, *storage.LocalStorageBackend, *storage.LocalStorageBackend) {
	t.Helper()
	canonical, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	native, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &JailerVMM{storage: canonical, nativeRuntimeSnapshots: native}, canonical, native
}

func putSnapshotObject(t *testing.T, backend *storage.LocalStorageBackend, key string) string {
	t.Helper()
	full, _, err := backend.LocalPath(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(key), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

func TestNativeRuntimeSnapshotObjectAcceptsOnlyV2CaptureObjects(t *testing.T) {
	deployment, capture := uuid.NewString(), uuid.NewString()
	for _, tier := range []string{state.SnapshotTierInit, state.SnapshotTierWarm} {
		memory := state.SnapshotCaptureMemKey(deployment, tier, capture)
		keys := nativeRuntimeCaptureKeys(memory)
		for _, key := range []string{keys.Memory, keys.VMState, keys.Drive, keys.Backing} {
			got, captures, ok := nativeRuntimeSnapshotObject(key)
			if !ok || got != memory || !strings.HasSuffix(captures, "/captures") || !strings.HasPrefix(memory, captures+"/") {
				t.Fatalf("nativeRuntimeSnapshotObject(%q) = %q, %q, %v", key, got, captures, ok)
			}
		}
	}
	for _, key := range []string{
		state.SnapMemKey(deployment),
		state.WarmSnapMemKey(deployment),
		"snap/" + deployment + "/captures/" + capture + "/v2/other",
		"snap/" + deployment + "/captures/not-a-uuid/v2/mem",
		"snap/not-a-deployment/captures/" + capture + "/v2/mem",
		"base/" + deployment + "/captures/" + capture + "/v2/mem",
		"mem",
		"",
	} {
		if _, _, ok := nativeRuntimeSnapshotObject(key); ok {
			t.Fatalf("nativeRuntimeSnapshotObject(%q) accepted a key outside a v2 capture", key)
		}
	}
}

func TestSnapshotStoreReadsNativeRuntimeCapturesFromTheNativeTree(t *testing.T) {
	v, canonical, native := nativeSnapshotTreesFixture(t)
	deployment := uuid.NewString()
	nativeKey := state.SnapshotCaptureMemKey(deployment, state.SnapshotTierWarm, uuid.NewString())
	canonicalKey := state.SnapshotCaptureMemKey(deployment, state.SnapshotTierWarm, uuid.NewString())
	legacy := state.SnapMemKey(deployment)
	nativePath := putSnapshotObject(t, native, nativeKey)
	putSnapshotObject(t, canonical, canonicalKey)
	// A legacy key is never read from the native tree, even if a file sits there.
	putSnapshotObject(t, native, legacy)
	if got := v.snapshotStore(nativeKey); got != storage.StorageBackend(native) {
		t.Fatal("a native runtime capture was not read from the native tree")
	}
	for _, key := range []string{canonicalKey, legacy} {
		if got := v.snapshotStore(key); got != storage.StorageBackend(canonical) {
			t.Fatalf("snapshotStore(%q) left the shared backend", key)
		}
	}
	path, _, local, err := v.probeRestoreLocalPath(nativeKey)
	if err != nil || !local || path != nativePath {
		t.Fatalf("probeRestoreLocalPath = %q, %v, %v; want %q", path, local, err, nativePath)
	}
	v.nativeRuntimeSnapshots = nil
	if got := v.snapshotStore(nativeKey); got != storage.StorageBackend(canonical) {
		t.Fatal("a vmmd without a native tree must read the shared backend")
	}
}

func TestNativeSnapshotPublicationBackendSeparatesRuntimeFromQualification(t *testing.T) {
	v, canonical, native := nativeSnapshotTreesFixture(t)
	runtime := context.WithValue(t.Context(), nativeSnapshotCaptureContextKey{},
		nativeSnapshotCapturePermit{Runtime: runtimeCaptureAuthorityFixture(t, state.SnapshotTierInit)})
	if got, err := v.nativeSnapshotPublicationBackend(runtime); err != nil || got != storage.StorageBackend(native) {
		t.Fatalf("runtime publication backend = %v, %v", got, err)
	}
	qualification := context.WithValue(t.Context(), nativeSnapshotCaptureContextKey{}, nativeSnapshotCapturePermit{})
	for _, ctx := range []context.Context{qualification, t.Context()} {
		if got, err := v.nativeSnapshotPublicationBackend(ctx); err != nil || got != storage.StorageBackend(canonical) {
			t.Fatalf("qualification publication backend = %v, %v", got, err)
		}
	}
	v.nativeRuntimeSnapshots = nil
	if _, err := v.nativeSnapshotPublicationBackend(runtime); !errors.Is(err, ErrNativeRuntimeSnapshotRootUnset) {
		t.Fatal("a runtime capture fell back to the shared backend", err)
	}
}

func TestRetireNativeRuntimeSnapshotObjectRefusesOtherKeysAndMissingTree(t *testing.T) {
	v, _, _ := nativeSnapshotTreesFixture(t)
	if err := v.RetireNativeRuntimeSnapshotObject(t.Context(), state.SnapMemKey(uuid.NewString())); !errors.Is(err, storage.ErrInvalidKey) {
		t.Fatal("a legacy snapshot key was accepted for native retirement", err)
	}
	v.nativeRuntimeSnapshots = nil
	key := state.SnapshotCaptureMemKey(uuid.NewString(), state.SnapshotTierInit, uuid.NewString())
	if err := v.RetireNativeRuntimeSnapshotObject(t.Context(), key); !errors.Is(err, ErrNativeRuntimeSnapshotRootUnset) {
		t.Fatal("retirement without a native tree must refuse so imaged keeps the row", err)
	}
}

func TestNativeRuntimeCaptureRequiresTheNativeTree(t *testing.T) {
	v, _, _ := nativeSnapshotTreesFixture(t)
	v.nativeRecovery, v.nativeRuntimeCapture = &nativeProcessRecoveryRuntime{}, true
	if !v.nativeRuntimeCaptureEnabled() {
		t.Fatal("a complete native runtime capture setup was refused")
	}
	v.nativeRuntimeSnapshots = nil
	if v.nativeRuntimeCaptureEnabled() {
		t.Fatal("runtime capture opened without a tree to publish to")
	}
}
