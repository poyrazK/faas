//go:build metal

package fcvm

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestMetalEphemeralDiskCapacity exercises the customer-visible filesystem in
// a real restored Firecracker guest. The ordinary write remains visible for
// that instance, filling the disk returns ENOSPC, and a second restore from the
// original snapshot starts from the unchanged canonical deployment layer.
func TestMetalEphemeralDiskCapacity(t *testing.T) {
	kernel, _, _ := metalImages(t)
	dir := t.TempDir()
	base := filepath.Join(dir, "capacity-base.ext4")
	layer := filepath.Join(dir, "capacity-layer.ext4")
	if err := buildV6BaseExt4(base, repoRoot(t)); err != nil {
		t.Fatal(err)
	}
	wantSize := api.MustLimitsFor(api.PlanFree).EphemeralDiskMaxMB()
	if err := buildV6LayerExt4Size(layer, wantSize); err != nil {
		t.Fatal(err)
	}
	canonicalBefore := hashMetalFile(t, layer)

	snapshotRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(snapshotRoot, "snap"), 0o2770); err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewLocalStorageBackend(snapshotRoot)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(wire.ExecRunner{}, newMetalVMM(t, 30*time.Second).WithStorage(store),
		Paths{Kernel: kernel}, os.Getenv("FAAS_TEST_FC_VERSION"), nil, nil)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	const instance = "disk-capacity"
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })

	if _, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance: instance, Plan: "free", BaseKey: base, LayerKey: layer,
		VcpuCount: 1, MemSizeMiB: 128,
	}); err != nil {
		t.Fatalf("prime cold boot: %v", err)
	}
	snap := &Snapshot{
		FCVersion:         os.Getenv("FAAS_TEST_FC_VERSION"),
		StorageKey:        "snap/disk-capacity/mem",
		VMStateStorageKey: "snap/disk-capacity/vmstate",
		VMStatePath:       filepath.Join(t.TempDir(), "vmstate"),
	}
	if _, err := m.Park(ctx, instance, SnapshotSpec{
		VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey,
		VMStateStorageKey: snap.VMStateStorageKey,
	}); err != nil {
		t.Fatalf("prime park: %v", err)
	}

	first, err := m.Wake(ctx, WakeRequest{
		Instance: instance, Plan: "free", BaseKey: base, LayerKey: layer,
		VcpuCount: 1, MemSizeMiB: 128, Snapshot: snap,
	})
	if err != nil || first.Method != WakeRestore {
		t.Fatalf("first restore method=%s err=%v", first.Method, err)
	}
	client := &http.Client{Timeout: 45 * time.Second}
	probe := func(action string) string {
		t.Helper()
		url := fmt.Sprintf("http://%s:8080/cgi-bin/disk", first.Lease.HostIP)
		if action != "" {
			url += "?action=" + action
		}
		resp, err := client.Get(url)
		if err != nil {
			t.Fatalf("capacity probe %q: %v", action, err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	if body := probe("small"); !strings.Contains(body, "write=ok") {
		t.Fatalf("within-capacity write failed: %q", body)
	}
	if body := probe(""); !strings.Contains(body, "small=present") {
		t.Fatalf("write did not persist for instance lifetime: %q", body)
	}
	if body := probe("fill"); !strings.Contains(body, "limit=hit") {
		t.Fatalf("filesystem did not cross its limit predictably: %q", body)
	}
	if got := hashMetalFile(t, layer); got != canonicalBefore {
		t.Fatal("guest writes changed the canonical deployment layer")
	}

	if err := m.Destroy(ctx, instance); err != nil {
		t.Fatal(err)
	}
	second, err := m.Wake(ctx, WakeRequest{
		Instance: instance, Plan: "free", BaseKey: base, LayerKey: layer,
		VcpuCount: 1, MemSizeMiB: 128, Snapshot: snap,
	})
	if err != nil || second.Method != WakeRestore {
		t.Fatalf("second restore method=%s err=%v", second.Method, err)
	}
	first = second
	if body := probe(""); !strings.Contains(body, "small=absent") {
		t.Fatalf("ephemeral write survived a fresh restore: %q", body)
	}
	if got := hashMetalFile(t, layer); got != canonicalBefore {
		t.Fatal("second restore changed the canonical deployment layer")
	}
}

func hashMetalFile(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return sum
}
