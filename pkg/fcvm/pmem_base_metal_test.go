//go:build metal

// adr: 908 — an app VM boots and restores from its account's pmem copy of
// the base, another account gets its own copy, and a VMM without pmem bases
// cannot restore a pmem snapshot: it cold boots from virtio-blk instead.
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

// TestMetalPmemAccountBase needs FAAS_TEST_PMEM_BASE_DIR set to "auto" or to
// a directory on the same reflink-capable mount (XFS, Btrfs) as
// FAAS_TEST_BASE_ROOTFS. The base must carry rootfs.BasePmemLabel, and its
// guest-init must understand a pmem root.
func TestMetalPmemAccountBase(t *testing.T) {
	kernel, base, layer := metalImages(t)
	dir := os.Getenv("FAAS_TEST_PMEM_BASE_DIR")
	if dir == "" {
		t.Skip("set FAAS_TEST_PMEM_BASE_DIR to auto or a directory on the base's reflink-capable mount")
	}
	ctx := context.Background()
	fcVersion, err := DetectFirecrackerVersion(ctx)
	if err != nil {
		t.Fatalf("detect firecracker version: %v", err)
	}
	if !PmemBaseSupported(fcVersion) {
		t.Skipf("Firecracker %s has no virtio-pmem", fcVersion)
	}
	snapshotRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(snapshotRoot, "snap"), 0o2770); err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewLocalStorageBackend(snapshotRoot)
	if err != nil {
		t.Fatal(err)
	}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	v := newMetalVMM(t, 30*time.Second).WithStorage(store).WithPmemBase(dir)
	m := NewManager(wire.ExecRunner{}, v, Paths{Kernel: kernel}, fcVersion, nil, nil)
	snap := &Snapshot{FCVersion: fcVersion, StorageKey: "snap/pmem/mem", VMStateStorageKey: "snap/pmem/vmstate", VMStatePath: t.TempDir() + "/vmstate"}
	spec := SnapshotSpec{VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey}
	wake := func(m *Manager, id, account string, s *Snapshot) *Instance {
		t.Helper()
		inst, err := m.Wake(ctx, WakeRequest{Instance: id, AccountID: account, AppID: "app-" + account, Plan: "pro",
			BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 256, Snapshot: s})
		if err != nil {
			t.Fatalf("wake %s: %v", id, err)
		}
		t.Cleanup(func() { _ = m.Destroy(context.Background(), id) })
		if _, err := firstResponse("http://"+inst.Lease.HostIP.String()+":8080/", 10*time.Second); err != nil {
			t.Fatalf("%s does not serve: %v", id, err)
		}
		return inst
	}

	wake(m, "pmem-a", "acct-a", nil)
	assertBaseDevice(t, v, "pmem-a", true)
	if _, err := m.Park(ctx, "pmem-a", spec); err != nil {
		t.Fatalf("park: %v", err)
	}
	if inst := wake(m, "pmem-a", "acct-a", snap); inst.Method != WakeRestore {
		t.Fatalf("pmem snapshot came back by %s, want restore", inst.Method)
	}
	assertBaseDevice(t, v, "pmem-a", true)

	wake(m, "pmem-b", "acct-b", nil)
	copies, err := v.pmemBase.copyDir(base)
	if err != nil {
		t.Fatalf("pmem copy directory: %v", err)
	}
	accounts, err := os.ReadDir(copies)
	if err != nil || len(accounts) != 2 {
		t.Fatalf("pmem dir holds %d account copies (err %v), want 2", len(accounts), err)
	}
	for _, id := range []string{"pmem-a", "pmem-b"} {
		if err := m.Destroy(ctx, id); err != nil {
			t.Fatalf("destroy %s: %v", id, err)
		}
	}

	// The snapshot refers to base-pmem.ext4, which only a pmem VMM stages.
	plain := newMetalVMM(t, 30*time.Second).WithStorage(store)
	m2 := NewManager(wire.ExecRunner{}, plain, Paths{Kernel: kernel}, fcVersion, nil, nil)
	if inst := wake(m2, "pmem-a", "acct-a", snap); inst.Method != WakeColdBoot {
		t.Fatalf("pmem snapshot without pmem bases came back by %s, want a cold boot", inst.Method)
	}
	assertBaseDevice(t, plain, "pmem-a", false)
	if err := m2.Destroy(ctx, "pmem-a"); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if m.LeasedCount() != 0 || m2.LeasedCount() != 0 {
		t.Errorf("leaked leases: %d / %d", m.LeasedCount(), m2.LeasedCount())
	}
}

// assertBaseDevice reads the running VM's configuration from Firecracker.
func assertBaseDevice(t *testing.T, v *JailerVMM, instance string, wantPmem bool) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://localhost/vm/config", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := v.fcClient(instance).Do(req)
	if err != nil {
		t.Fatalf("%s: GET /vm/config: %v", instance, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var cfg struct {
		Drives []Drive `json:"drives"`
		Pmem   []Pmem  `json:"pmem"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &cfg) != nil {
		t.Fatalf("%s: GET /vm/config = %d %s", instance, resp.StatusCode, body)
	}
	pmem := len(cfg.Pmem) == 1 && cfg.Pmem[0].PathOnHost == pmemBaseImageName
	blkBase := false
	for _, d := range cfg.Drives {
		blkBase = blkBase || d.DriveID == DriveBase
	}
	if pmem != wantPmem || blkBase == wantPmem {
		t.Fatalf("%s: base on pmem=%v blk=%v, want pmem=%v (config %s)", instance, pmem, blkBase, wantPmem, body)
	}
}

func firstResponse(url string, within time.Duration) (*http.Response, error) {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(within)
	for {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return resp, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(5 * time.Millisecond)
	}
}
