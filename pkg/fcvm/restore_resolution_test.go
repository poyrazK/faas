package fcvm

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
)

type restoreResolutionBackend struct {
	probeDelay time.Duration
	getDelay   time.Duration
	local      bool
	gets       atomic.Int64
}

func (*restoreResolutionBackend) Put(context.Context, string, io.Reader) error { return nil }
func (b *restoreResolutionBackend) Get(context.Context, string) (io.ReadCloser, error) {
	b.gets.Add(1)
	time.Sleep(b.getDelay)
	return io.NopCloser(strings.NewReader("remote")), nil
}
func (*restoreResolutionBackend) Delete(context.Context, string) error { return nil }
func (b *restoreResolutionBackend) LocalPath(key string) (string, bool, error) {
	path, _, ok, err := b.LocalPathWithSource(key)
	return path, ok, err
}
func (b *restoreResolutionBackend) LocalPathWithSource(key string) (string, storage.LocalPathSource, bool, error) {
	time.Sleep(b.probeDelay)
	if !b.local {
		return "", "", false, nil
	}
	return "/cache/" + key, storage.LocalPathSourceCache, true, nil
}

func TestResolveRestoreArtifactsParallelizesLocalMetadata(t *testing.T) {
	backend := &restoreResolutionBackend{probeDelay: 100 * time.Millisecond, local: true}
	v := NewJailerVMM(t.TempDir(), 0).WithStorage(backend)
	specs := []restoreArtifactSpec{
		{artifact: "kernel", key: "kernel/vmlinux", errorContext: "kernel"},
		{artifact: "base", key: "base/rootfs", errorContext: "base"},
		{artifact: "main", key: "apps/layer", errorContext: "main"},
	}

	started := time.Now()
	got, err := v.resolveRestoreArtifacts(context.Background(), "instance", specs)
	if err != nil {
		t.Fatalf("resolveRestoreArtifacts: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 220*time.Millisecond {
		t.Fatalf("three 100ms local probes took %s; want parallel metadata resolution", elapsed)
	}
	if backend.gets.Load() != 0 {
		t.Fatalf("local cache hits called Get %d times; want no remote fetch/copy", backend.gets.Load())
	}
	for i := range got {
		if got[i].Source != string(storage.LocalPathSourceCache) {
			t.Errorf("artifact %s source = %q, want cache_hit", got[i].Artifact, got[i].Source)
		}
		if got[i].DurationMs < 90 {
			t.Errorf("artifact %s duration = %dms, want injected metadata delay", got[i].Artifact, got[i].DurationMs)
		}
	}
}

func TestResolveRestoreArtifactsKeepsRemoteMaterializationSequential(t *testing.T) {
	backend := &restoreResolutionBackend{getDelay: 60 * time.Millisecond}
	v := NewJailerVMM(t.TempDir(), 0).WithStorage(backend)
	specs := []restoreArtifactSpec{
		{artifact: "kernel", key: "kernel/vmlinux", errorContext: "kernel"},
		{artifact: "base", key: "base/rootfs", errorContext: "base"},
	}

	started := time.Now()
	got, err := v.resolveRestoreArtifacts(context.Background(), "instance", specs)
	if err != nil {
		t.Fatalf("resolveRestoreArtifacts: %v", err)
	}
	v.sweepMaterialised("instance")
	if elapsed := time.Since(started); elapsed < 110*time.Millisecond {
		t.Fatalf("two 60ms remote reads took %s; remote materialization became concurrent", elapsed)
	}
	if backend.gets.Load() != 2 {
		t.Fatalf("Get calls = %d, want 2 misses", backend.gets.Load())
	}
	for i := range got {
		if got[i].Source != "materialized" {
			t.Errorf("artifact %s source = %q, want materialized", got[i].Artifact, got[i].Source)
		}
	}
}

// adr: 064
