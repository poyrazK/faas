package imaged

// adr: 20261022184951 — amendment 1: vmmd removes native runtime snapshots on imaged's behalf.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// retiringVMMClient records native retirements and removes the file the way
// vmmd would.
type retiringVMMClient struct {
	fakeVMMClient
	root    string
	retired []string
	err     error
}

func (c *retiringVMMClient) RetireNativeRuntimeSnapshotObject(_ context.Context, key string) error {
	if c.err != nil {
		return c.err
	}
	c.retired = append(c.retired, key)
	if err := os.Remove(filepath.Join(c.root, filepath.FromSlash(key))); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

type recordingDeleteBackend struct {
	storage.StorageBackend
	deleted []string
}

func (b *recordingDeleteBackend) Delete(_ context.Context, key string) error {
	b.deleted = append(b.deleted, key)
	return nil
}

func writeNativeSnapshotObject(t *testing.T, root, key string) string {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(key), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

func TestNativeRuntimeSnapshotMemKeyAcceptsOnlyV2CaptureObjects(t *testing.T) {
	memory := state.SnapshotCaptureMemKey(uuid.NewString(), state.SnapshotTierWarm, uuid.NewString())
	snap := state.Snapshot{StorageKey: memory}
	for _, key := range []string{memory, state.SnapshotVMStateKey(snap), state.SnapshotDriveKey(snap), state.SnapshotBackingKey(snap)} {
		if got, ok := nativeRuntimeSnapshotMemKey(key); !ok || got != memory {
			t.Fatalf("nativeRuntimeSnapshotMemKey(%q) = %q, %v", key, got, ok)
		}
	}
	deployment := uuid.NewString()
	for _, key := range []string{state.SnapMemKey(deployment), state.WarmSnapMemKey(deployment), memory + "x", "app/" + deployment + "/layer"} {
		if _, ok := nativeRuntimeSnapshotMemKey(key); ok {
			t.Fatalf("nativeRuntimeSnapshotMemKey(%q) accepted a key that never lives in vmmd's tree", key)
		}
	}
}

func TestDeleteSnapshotObjectRoutesNativeObjectsThroughVMMD(t *testing.T) {
	root := t.TempDir()
	vmm := &retiringVMMClient{root: root}
	h := (&Handler{}).WithVMMClient(vmm).WithNativeRuntimeSnapshotRoot(root)
	be := &recordingDeleteBackend{}
	deployment := uuid.NewString()
	native := state.SnapshotCaptureMemKey(deployment, state.SnapshotTierInit, uuid.NewString())
	shared := state.SnapshotCaptureMemKey(deployment, state.SnapshotTierInit, uuid.NewString())
	writeNativeSnapshotObject(t, root, native)
	for _, key := range []string{native, shared, state.SnapMemKey(deployment)} {
		if err := h.deleteSnapshotObject(t.Context(), be, key); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(vmm.retired, []string{native}) || slices.Contains(be.deleted, native) {
		t.Fatalf("vmmd retired %v, backend deleted %v; want only the native object through vmmd", vmm.retired, be.deleted)
	}
	if !slices.Equal(be.deleted, []string{shared, state.SnapMemKey(deployment)}) {
		t.Fatalf("backend deleted %v", be.deleted)
	}
	// A refused retirement must surface so GC keeps the row.
	writeNativeSnapshotObject(t, root, native)
	vmm.err = errors.New("vmmd unavailable")
	if err := h.deleteSnapshotObject(t.Context(), be, native); err == nil {
		t.Fatal("a failed native retirement was reported as deleted")
	}
	noClient := (&Handler{}).WithNativeRuntimeSnapshotRoot(root)
	if err := noClient.deleteSnapshotObject(t.Context(), be, native); err == nil {
		t.Fatal("a native object without a vmmd client was reported as deleted")
	}
}

func TestCleanupSnapshotCapturesRetiresTheDeploymentsNativeObjects(t *testing.T) {
	root := t.TempDir()
	vmm := &retiringVMMClient{root: root}
	h := (&Handler{store: state.NewMemStore(), log: slog.New(slog.NewTextHandler(io.Discard, nil))}).
		WithVMMClient(vmm).WithNativeRuntimeSnapshotRoot(root)
	deployment, other := uuid.NewString(), uuid.NewString()
	memory := state.SnapshotCaptureMemKey(deployment, state.SnapshotTierWarm, uuid.NewString())
	snap := state.Snapshot{StorageKey: memory}
	for _, key := range []string{memory, state.SnapshotVMStateKey(snap), state.SnapshotDriveKey(snap), state.SnapshotBackingKey(snap)} {
		writeNativeSnapshotObject(t, root, key)
	}
	kept := writeNativeSnapshotObject(t, root, state.SnapshotCaptureMemKey(other, state.SnapshotTierInit, uuid.NewString()))
	h.cleanupSnapshotCaptures(t.Context(), &recordingDeleteBackend{}, deployment)
	if len(vmm.retired) != 4 {
		t.Fatalf("retired %v, want the deployment's four capture objects", vmm.retired)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatal("another deployment's native capture was retired", err)
	}
}

func TestRetireNativeRuntimeSnapshotOrphansKeepsKnownAndRecentObjects(t *testing.T) {
	root := t.TempDir()
	vmm := &retiringVMMClient{root: root}
	h := (&Handler{}).WithVMMClient(vmm).WithNativeRuntimeSnapshotRoot(root)
	loop := &Loop{handler: h, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	now := time.Now()
	old := now.Add(-2 * localSnapshotOrphanGrace)
	orphan, known, fresh := uuid.NewString(), uuid.NewString(), uuid.NewString()
	orphanKey := state.SnapshotCaptureMemKey(orphan, state.SnapshotTierInit, uuid.NewString())
	recentKey := state.SnapshotCaptureMemKey(orphan, state.SnapshotTierWarm, uuid.NewString())
	knownKey := state.SnapshotCaptureMemKey(known, state.SnapshotTierInit, uuid.NewString())
	freshKey := state.SnapshotCaptureMemKey(fresh, state.SnapshotTierInit, uuid.NewString())
	for _, key := range []string{orphanKey, knownKey, freshKey} {
		path := writeNativeSnapshotObject(t, root, key)
		if key != freshKey {
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	// A capture published into an old directory just now is not taken.
	writeNativeSnapshotObject(t, root, recentKey)
	for _, deployment := range []string{orphan, known} {
		if err := os.Chtimes(filepath.Join(root, "snap", deployment), old, old); err != nil {
			t.Fatal(err)
		}
	}
	loop.retireNativeRuntimeSnapshotOrphans(t.Context(), map[string]struct{}{known: {}}, now.Add(-localSnapshotOrphanGrace))
	if !slices.Equal(vmm.retired, []string{orphanKey}) {
		t.Fatalf("retired %v, want only the old orphan object %s", vmm.retired, orphanKey)
	}
}
