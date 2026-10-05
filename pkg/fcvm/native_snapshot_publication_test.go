//go:build linux || darwin

// adr: 568 — unsupported native capture refuses before native/storage effects.
package fcvm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type nativeSnapshotPublicationTrap struct {
	storage.StorageBackend
	storageCalls atomic.Int32
	apiCalls     atomic.Int32
}

func (b *nativeSnapshotPublicationTrap) Put(ctx context.Context, key string, reader io.Reader) error {
	b.storageCalls.Add(1)
	return b.StorageBackend.Put(ctx, key, reader)
}

func (b *nativeSnapshotPublicationTrap) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	b.storageCalls.Add(1)
	return b.StorageBackend.Get(ctx, key)
}

func (b *nativeSnapshotPublicationTrap) Delete(ctx context.Context, key string) error {
	b.storageCalls.Add(1)
	return b.StorageBackend.Delete(ctx, key)
}

func (b *nativeSnapshotPublicationTrap) LocalPath(string) (string, bool, error) {
	b.storageCalls.Add(1)
	return "", false, errors.New("native snapshot publication preflight was bypassed")
}

func (b *nativeSnapshotPublicationTrap) RoundTrip(*http.Request) (*http.Response, error) {
	b.apiCalls.Add(1)
	return nil, errors.New("native snapshot API preflight was bypassed")
}

func TestNativeSnapshotPublicationRefusesBeforeEffects(t *testing.T) {
	for _, operation := range []string{"keep_alive", "snapshot"} {
		for _, namespace := range []string{"host", "legacy", "capture"} {
			for _, canceled := range []bool{false, true} {
				name := operation + "/" + namespace + "/live"
				if canceled {
					name = operation + "/" + namespace + "/canceled"
				}
				t.Run(name, func(t *testing.T) {
					checkNativeSnapshotPublicationRefusal(t, operation, namespace, canceled)
				})
			}
		}
	}
}

func checkNativeSnapshotPublicationRefusal(t *testing.T, operation, namespace string, canceled bool) {
	t.Helper()
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	trap := &nativeSnapshotPublicationTrap{StorageBackend: backend}
	v := NewJailerVMM(t.TempDir(), 0).WithStorage(trap)
	v.nativeRecovery = &nativeProcessRecoveryRuntime{}
	lease := leaseForSlot("native-publication-refusal", 3)
	v.clients[lease.Instance] = &http.Client{Transport: trap}
	root := v.chrootRoot(lease.Instance)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mem", "vmstate", layerImageName} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("original-jail-file"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	spec := SnapshotSpec{VMStatePath: filepath.Join(t.TempDir(), "state"), ResumeBeforePublish: true}
	if err := os.WriteFile(spec.VMStatePath, []byte("original-host-state"), 0o600); err != nil {
		t.Fatal(err)
	}
	switch namespace {
	case "legacy":
		spec.StorageKey = state.SnapMemKey("dep")
	case "capture":
		spec.StorageKey = state.SnapshotCaptureMemKey("dep", state.SnapshotTierWarm, "original")
	}
	var keys []string
	if spec.StorageKey != "" {
		spec.VMStateStorageKey = state.SnapshotVMStateKey(state.Snapshot{StorageKey: spec.StorageKey})
		keys = []string{spec.StorageKey, spec.VMStateStorageKey, state.SnapshotDriveKey(state.Snapshot{StorageKey: spec.StorageKey})}
	}
	for _, key := range keys {
		if key != "" {
			if err := backend.Put(t.Context(), key, strings.NewReader("original-storage-object")); err != nil {
				t.Fatal(err)
			}
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if canceled {
		cancel()
	}
	capture := v.SnapshotKeepAlive
	if operation == "snapshot" {
		capture = v.Snapshot
	}
	info, err := capture(ctx, lease, spec)
	if !errors.Is(err, state.ErrConflict) || info != (SnapshotInfo{}) {
		t.Fatalf("unsupported native capture: info=%+v error=%v", info, err)
	}
	if trap.apiCalls.Load() != 0 || trap.storageCalls.Load() != 0 || v.clients[lease.Instance] == nil {
		t.Fatalf("refusal performed effects: API=%d storage=%d client=%v", trap.apiCalls.Load(), trap.storageCalls.Load(), v.clients[lease.Instance])
	}
	for _, name := range []string{"mem", "vmstate", layerImageName} {
		body, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(body) != "original-jail-file" {
			t.Fatalf("refusal changed jail %s: %q %v", name, body, err)
		}
	}
	if body, err := os.ReadFile(spec.VMStatePath); err != nil || string(body) != "original-host-state" {
		t.Fatalf("refusal changed host state: %q %v", body, err)
	}
	for _, key := range keys {
		if key == "" {
			continue
		}
		reader, err := backend.Get(t.Context(), key)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(reader)
		if err := errors.Join(readErr, reader.Close()); err != nil || string(body) != "original-storage-object" {
			t.Fatalf("refusal changed original object %s: %q %v", key, body, err)
		}
	}
}
