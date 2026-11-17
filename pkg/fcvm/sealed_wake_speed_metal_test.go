//go:build metal

package fcvm

// adr: 20261110210000 — slice 3d-5: a sealed park snapshot wakes about as
// fast as an unsealed one once its node holds the plaintext (the first wake
// decrypts the chunked format in parallel; later wakes link the copy).
// Compares, on one host and fixture: unsealed, sealed v1 without a cache
// (the 3d-3 behaviour), and sealed v2 with the node plaintext cache.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestMetalSealedWakeSpeed(t *testing.T) {
	const wakes = 5
	results := map[string][]time.Duration{}
	for _, mode := range []string{"unsealed", "sealed-v1-nocache", "sealed-v2-cache"} {
		t.Run(mode, func(t *testing.T) {
			results[mode] = sealedWakeTimes(t, mode, wakes)
		})
	}
	for _, mode := range []string{"unsealed", "sealed-v1-nocache", "sealed-v2-cache"} {
		d := results[mode]
		if len(d) == 0 {
			continue
		}
		repeat := append([]time.Duration(nil), d[1:]...)
		sort.Slice(repeat, func(i, j int) bool { return repeat[i] < repeat[j] })
		t.Logf("%-18s first=%s repeat_median=%s repeat_max=%s all=%v", mode,
			d[0].Round(time.Millisecond), repeat[len(repeat)/2].Round(time.Millisecond),
			repeat[len(repeat)-1].Round(time.Millisecond), roundAll(d))
	}
	if v2, plain := results["sealed-v2-cache"], results["unsealed"]; len(v2) > 1 && len(plain) > 1 {
		// Repeat sealed wakes link the plaintext copy: they must stay within
		// 250 ms of the unsealed median on the same host.
		if medianOf(v2[1:]) > medianOf(plain[1:])+250*time.Millisecond {
			t.Errorf("repeat sealed wakes (median %s) are far slower than unsealed (median %s)",
				medianOf(v2[1:]), medianOf(plain[1:]))
		}
	}
}

func roundAll(d []time.Duration) []time.Duration {
	out := make([]time.Duration, len(d))
	for i, v := range d {
		out[i] = v.Round(time.Millisecond)
	}
	return out
}

func medianOf(d []time.Duration) time.Duration {
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2]
}

func sealedWakeTimes(t *testing.T, mode string, wakes int) []time.Duration {
	work := t.TempDir()
	kernel, base, layerPath := memoryShareMetalImages(t, work)
	fcVersion, err := DetectFirecrackerVersion(context.Background())
	if err != nil {
		t.Fatalf("detect firecracker version: %v", err)
	}
	shared, err := storage.NewLocalStorageBackend(filepath.Join(work, "shared"))
	if err != nil {
		t.Fatal(err)
	}
	cache, err := storage.NewLocalCacheBackend(remoteOnlyBackend{shared}, filepath.Join(work, "cache"), 16<<30)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const layerKey = "layers/sealed-speed.ext4"
	f, err := os.Open(layerPath) //nolint:forbidigo // test fixture.
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Put(ctx, layerKey, f); err != nil {
		t.Fatalf("put layer: %v", err)
	}
	_ = f.Close()
	scan, err := json.Marshal(baseScanSidecar{Image: "sealed-speed-fixture",
		Findings: map[string]int{"CRITICAL": 0}, FixAvailableFindings: map[string]int{"CRITICAL": 0}})
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Put(ctx, wire.ScanKeyForBaseKey(base), bytes.NewReader(scan)); err != nil {
		t.Fatal(err)
	}

	fleet, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	vmm := newMetalVMM(t, 30*time.Second).WithStorage(cache)
	m := NewManager(wire.ExecRunner{}, vmm, Paths{Kernel: kernel}, fcVersion, nil, nil)
	m.WithStorage(cache)
	m.SetHostIdentities([]*age.X25519Identity{fleet})
	withCgroupRootAt(t, "/sys/fs/cgroup")
	if mode == "sealed-v2-cache" {
		m.SetSealChunked(true)
		plain, err := NewSealedPlainCache(filepath.Join(work, "sealed-plain"), 4<<30, 0)
		if err != nil {
			t.Fatal(err)
		}
		vmm.WithSealedPlainCache(plain)
	}

	const instance, captureID = "sealedspeed", "8a1e2c4d-3b5f-4a6e-9c7d-1e2f3a4b5c6d"
	prefix := "snap/sealed-speed/captures/" + captureID + "/" + mode + "/"
	memKey, vmstateKey := prefix+"mem", prefix+"vmstate"
	if _, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance: instance, Plan: "pro", BaseKey: base, LayerKey: layerKey, VcpuCount: 2, MemSizeMiB: 512,
	}); err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })

	spec := SnapshotSpec{VMStatePath: filepath.Join(work, "vmstate"), StorageKey: memKey, VMStateStorageKey: vmstateKey}
	var sealedKey []byte
	if mode != "unsealed" {
		var store storage.StorageBackend
		store, sealedKey, err = m.SealCaptureStorage(captureID)
		if err != nil {
			t.Fatalf("seal capture storage: %v", err)
		}
		spec.Storage = store
	}
	if _, err := m.Park(ctx, instance, spec); err != nil {
		t.Fatalf("park: %v", err)
	}
	snap := &Snapshot{FCVersion: fcVersion, StorageKey: memKey, VMStateStorageKey: vmstateKey,
		VMStatePath: filepath.Join(work, "vmstate")}
	if mode != "unsealed" {
		snap.CaptureID, snap.SealedKey, snap.SealedCache = captureID, sealedKey, true
	}

	var times []time.Duration
	for i := range wakes {
		start := time.Now()
		inst, err := m.Wake(ctx, WakeRequest{
			Instance: instance, Plan: "pro", BaseKey: base, LayerKey: layerKey, VcpuCount: 2, MemSizeMiB: 512,
			Snapshot: snap,
		})
		if err != nil {
			t.Fatalf("%s wake %d: %v", mode, i, err)
		}
		if inst.Method != WakeRestore {
			t.Fatalf("%s wake %d: woke by %s, want a restore", mode, i, inst.Method)
		}
		times = append(times, time.Since(start))
		if err := m.Destroy(ctx, instance); err != nil {
			t.Fatalf("%s wake %d: destroy: %v", mode, i, err)
		}
	}
	if m.LeasedCount() != 0 {
		t.Errorf("%s: leaked leases: %d", mode, m.LeasedCount())
	}
	return times
}
