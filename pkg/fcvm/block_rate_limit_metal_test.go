//go:build metal

// adr: 910 — a VM's writable app layer is rate limited after a cold boot and
// after a snapshot restore, and the guest's writes slow to the limit.
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

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestMetalBlockRateLimit needs FAAS_TEST_BLOCK_RATE_LIMIT=1 and an app
// (FAAS_TEST_LAYER_ROOTFS) answering /disk?mb=N with {"write_ms": ...} for an
// N MB fsynced write to its root. It lowers Pro's limit to 20 MB/s so the
// limit is well under the host disk's own speed.
func TestMetalBlockRateLimit(t *testing.T) {
	kernel, base, layer := metalImages(t)
	if os.Getenv("FAAS_TEST_BLOCK_RATE_LIMIT") != "1" {
		t.Skip("set FAAS_TEST_BLOCK_RATE_LIMIT=1 and an app layer with /disk")
	}
	saved := api.BlockRateLimitPerPlan[api.PlanPro]
	t.Cleanup(func() { api.BlockRateLimitPerPlan[api.PlanPro] = saved })

	ctx := context.Background()
	fcVersion, err := DetectFirecrackerVersion(ctx)
	if err != nil {
		t.Fatalf("detect firecracker version: %v", err)
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
	v := newMetalVMM(t, 60*time.Second).WithStorage(store)
	m := NewManager(wire.ExecRunner{}, v, Paths{Kernel: kernel}, fcVersion, nil, nil)
	snap := &Snapshot{FCVersion: fcVersion, StorageKey: "snap/rl/mem", VMStateStorageKey: "snap/rl/vmstate", VMStatePath: t.TempDir() + "/vmstate"}
	wake := func(id string, plan api.Plan, s *Snapshot) string {
		t.Helper()
		inst, err := m.Wake(ctx, WakeRequest{Instance: id, AccountID: "acct", AppID: "app-" + id, Plan: plan,
			BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 1024, Snapshot: s})
		if err != nil {
			t.Fatalf("wake %s: %v", id, err)
		}
		t.Cleanup(func() { _ = m.Destroy(context.Background(), id) })
		h := "http://" + inst.Lease.HostIP.String() + ":8080"
		if _, err := firstResponse(h+"/", 120*time.Second); err != nil {
			t.Fatalf("%s does not serve: %v", id, err)
		}
		return h
	}
	// 512 MB: the 256 MiB burst, then 256 MB at the limit (about 13 s).
	writeMBps := func(h string) float64 {
		t.Helper()
		resp, err := (&http.Client{Timeout: 5 * time.Minute}).Get(h + "/disk?mb=512")
		if err != nil {
			t.Fatalf("GET /disk: %v", err)
		}
		defer resp.Body.Close()
		var out struct {
			WriteMs float64 `json:"write_ms"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode /disk: %v", err)
		}
		return 512 / (out.WriteMs / 1000)
	}

	// Baseline: a Pro VM booted with no Pro limit configured.
	delete(api.BlockRateLimitPerPlan, api.PlanPro)
	unlimited := writeMBps(wake("rl-unlimited", api.PlanPro, nil))
	api.BlockRateLimitPerPlan[api.PlanPro] = api.BlockRateLimit{BandwidthMBps: 20, IOPS: 4000}
	h := wake("rl-pro", api.PlanPro, nil)
	assertLayerLimited(t, v, "rl-pro", 20)
	booted := writeMBps(h)
	if _, err := m.Park(ctx, "rl-pro", SnapshotSpec{VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey}); err != nil {
		t.Fatalf("park: %v", err)
	}
	h = wake("rl-pro", api.PlanPro, snap)
	assertLayerLimited(t, v, "rl-pro", 20)
	restored := writeMBps(h)
	t.Logf("guest 512 MB write: unlimited %.0f MB/s, Pro limited to 20 MB/s after cold boot %.0f MB/s, after restore %.0f MB/s",
		unlimited, booted, restored)
	// The burst covers half the write, so the average must sit under 2x the limit.
	if booted > 40 || restored > 40 {
		t.Fatalf("limited writes ran at %.0f / %.0f MB/s, want under 40", booted, restored)
	}
}

func assertLayerLimited(t *testing.T, v *JailerVMM, instance string, mbps int64) {
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
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &cfg) != nil {
		t.Fatalf("%s: GET /vm/config = %d %s", instance, resp.StatusCode, body)
	}
	for _, d := range cfg.Drives {
		if writableLayerDrive(d) {
			if d.RateLimiter == nil || d.RateLimiter.Bandwidth == nil || d.RateLimiter.Bandwidth.Size != mbps<<20 {
				t.Fatalf("%s: layer drive limiter %+v, want %d MB/s (config %s)", instance, d.RateLimiter, mbps, body)
			}
			return
		}
	}
	t.Fatalf("%s: no writable layer drive in %s", instance, body)
}
