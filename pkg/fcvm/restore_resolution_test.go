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

func TestResolveColdBootArtifactAttributesMaterializedBytes(t *testing.T) {
	backend := &restoreResolutionBackend{getDelay: 10 * time.Millisecond}
	v := NewJailerVMM(t.TempDir(), 0).WithStorage(backend)

	path, timing, err := v.resolveColdBootArtifact(context.Background(), "instance", "main", "apps/layer")
	if err != nil {
		t.Fatalf("resolveColdBootArtifact: %v", err)
	}
	defer v.sweepMaterialised("instance")
	if path == "" {
		t.Fatal("resolved path is empty")
	}
	if timing.Artifact != "main" || timing.Source != "materialized" {
		t.Errorf("timing identity = %#v", timing)
	}
	if timing.Bytes != int64(len("remote")) {
		t.Errorf("timing.Bytes = %d, want %d", timing.Bytes, len("remote"))
	}
	if timing.DurationMs < 8 {
		t.Errorf("timing.DurationMs = %d, want injected delay", timing.DurationMs)
	}
}

// adr: 064

// adr: 063
// TestResolveRestoreBlobAttributesMaterializedFetch pins the branch that
// decides whether cross-node placement is affordable.
//
// mem and vmstate are the largest inputs a restore touches, and before this
// they were the only ones with no source attribution: materialize_mem_ms
// timed a local bind and a full streamed copy identically. A wake placed on a
// node without a local replica therefore looked exactly like a warm-node wake
// in the timeline, which is the one comparison ADR-063's placement decisions
// need to make.
func TestResolveRestoreBlobAttributesMaterializedFetch(t *testing.T) {
	backend := &restoreResolutionBackend{local: false, getDelay: 60 * time.Millisecond}
	v := NewJailerVMM(t.TempDir(), 0).WithStorage(backend)

	path, timing, err := v.resolveRestoreBlob(
		context.Background(), "instance", "mem", "snap/dep/mem", "/legacy/vmstate")
	if err != nil {
		t.Fatalf("resolveRestoreBlob: %v", err)
	}
	t.Cleanup(func() { v.sweepMaterialised("instance") })

	if path == "/legacy/vmstate" {
		t.Fatal("resolved to the legacy host path instead of the storage key")
	}
	if timing.Source != "materialized" {
		t.Errorf("source = %q, want \"materialized\" — this backend has no local path", timing.Source)
	}
	if backend.gets.Load() != 1 {
		t.Errorf("Get called %d times, want exactly 1", backend.gets.Load())
	}
	// "remote" is what the fixture backend streams.
	if timing.Bytes != int64(len("remote")) {
		t.Errorf("bytes = %d, want %d", timing.Bytes, len("remote"))
	}
	// The duration has to include the fetch, or the metric cannot tell a slow
	// pull from a fast one.
	if timing.DurationMs < 50 {
		t.Errorf("duration = %dms, want >= the injected 60ms fetch delay", timing.DurationMs)
	}
}

// adr: 063
// TestResolveRestoreBlobUsesHostPathWithoutStorageKey pins the single-box
// path: no storage key means the legacy host locator is returned byte-for-bit,
// with no probe and no fetch. Any regression here turns every single-box wake
// into a storage round trip.
func TestResolveRestoreBlobUsesHostPathWithoutStorageKey(t *testing.T) {
	backend := &restoreResolutionBackend{local: false}
	v := NewJailerVMM(t.TempDir(), 0).WithStorage(backend)

	path, timing, err := v.resolveRestoreBlob(
		context.Background(), "instance", "vmstate", "", "/legacy/vmstate")
	if err != nil {
		t.Fatalf("resolveRestoreBlob: %v", err)
	}
	if path != "/legacy/vmstate" {
		t.Errorf("path = %q, want the unchanged host locator", path)
	}
	if timing.Source != "host_path" {
		t.Errorf("source = %q, want \"host_path\"", timing.Source)
	}
	if backend.gets.Load() != 0 {
		t.Errorf("Get called %d times, want 0 — an empty key must not reach storage", backend.gets.Load())
	}
}

// adr: 063
// TestObserveRestoreArtifactsRecordsPerSourceSeries pins that the attribution
// reaches a scrape, not just the per-wake event. The event answers "why was
// this one wake slow"; only the histogram answers "are wakes landing away
// from their snapshots", which is the trend a placement decision rests on.
func TestObserveRestoreArtifactsRecordsPerSourceSeries(t *testing.T) {
	wpm := NewWakePhaseMetrics()
	v := NewJailerVMM(t.TempDir(), 0).WithWakePhaseMetrics(wpm)

	v.observeRestoreArtifacts([]restoreArtifactTiming{
		{Artifact: "mem", Source: "materialized", DurationMs: 1200, Bytes: 136 << 20},
		{Artifact: "vmstate", Source: "backend_local", DurationMs: 2, Bytes: 4096},
	})

	families, err := wpm.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	var secCount, byteTotal float64
	var sawLocal bool
	for _, f := range families {
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, lp := range m.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			if labels["artifact"] == "mem" && labels["source"] == "materialized" {
				if f.GetName() == "vmmd_snapshot_materialize_seconds" {
					secCount = float64(m.GetHistogram().GetSampleCount())
				}
				if f.GetName() == "vmmd_snapshot_materialize_bytes_total" {
					byteTotal = m.GetCounter().GetValue()
				}
			}
			if f.GetName() == "vmmd_snapshot_materialize_seconds" &&
				labels["artifact"] == "vmstate" && labels["source"] == "backend_local" &&
				m.GetHistogram().GetSampleCount() == 1 {
				sawLocal = true
			}
		}
	}
	if secCount != 1 {
		t.Errorf("mem/materialized histogram count = %v, want 1", secCount)
	}
	if want := float64(136 << 20); byteTotal != want {
		t.Errorf("mem/materialized bytes = %v, want %v", byteTotal, want)
	}
	if !sawLocal {
		t.Error("vmstate/backend_local series absent; the local branch must be observable too")
	}
}
