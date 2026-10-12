//go:build metal

// adr: 985 — a cold-booted VM carries the free-page-reporting balloon, and a
// VM restored from its snapshot keeps it.
package fcvm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestMetalFreePageReportingBalloon(t *testing.T) {
	kernel, base, layer := metalImages(t)
	ctx := context.Background()
	fcVersion, err := DetectFirecrackerVersion(ctx)
	if err != nil {
		t.Fatalf("detect firecracker version: %v", err)
	}
	if !FreePageReportingSupported(fcVersion) {
		t.Skipf("Firecracker %s predates free page reporting", fcVersion)
	}
	snapshotRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(snapshotRoot, "snap"), 0o2770); err != nil {
		t.Fatalf("create snapshot root: %v", err)
	}
	store, err := storage.NewLocalStorageBackend(snapshotRoot)
	if err != nil {
		t.Fatalf("create snapshot storage: %v", err)
	}
	v := newMetalVMM(t, 30*time.Second).WithStorage(store).WithFreePageReporting(true)
	m := NewManager(wire.ExecRunner{}, v, Paths{Kernel: kernel}, fcVersion, nil, nil)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	const id = "fpr"
	snapDir := t.TempDir()
	snap := &Snapshot{FCVersion: fcVersion, StorageKey: "snap/fpr/mem", VMStateStorageKey: "snap/fpr/vmstate", VMStatePath: snapDir + "/vmstate"}
	if _, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance: id, Plan: "pro", BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 256,
	}); err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), id) })
	assertFreePageReportingBalloon(t, v, id, "cold boot")

	if _, err := m.Park(ctx, id, SnapshotSpec{VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey}); err != nil {
		t.Fatalf("park: %v", err)
	}
	inst, err := m.Wake(ctx, WakeRequest{Instance: id, Plan: "pro", BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 256, Snapshot: snap})
	if err != nil {
		t.Fatalf("restore wake: %v", err)
	}
	if inst.Method != WakeRestore {
		t.Fatalf("wake fell back to %s; the snapshot with a balloon did not restore", inst.Method)
	}
	assertFreePageReportingBalloon(t, v, id, "restore")
	if err := m.Destroy(ctx, id); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if m.LeasedCount() != 0 {
		t.Errorf("leaked leases: %d", m.LeasedCount())
	}
}

func assertFreePageReportingBalloon(t *testing.T, v *JailerVMM, instance, phase string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://localhost/balloon", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := v.fcClient(instance).Do(req)
	if err != nil {
		t.Fatalf("%s: GET /balloon: %v", phase, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var got Balloon
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &got) != nil {
		t.Fatalf("%s: GET /balloon = %d %s", phase, resp.StatusCode, body)
	}
	if !got.FreePageReporting || got.AmountMiB != 0 {
		t.Fatalf("%s: balloon = %s, want free_page_reporting and amount_mib 0", phase, body)
	}
}
