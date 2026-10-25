//go:build metal

// adr: 20261024225612 — a VMM configured with a CPU template boots every guest
// with it, and a VM parked under the template restores.
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

func TestMetalCPUTemplateBootAndRestore(t *testing.T) {
	kernel, base, layer := metalImages(t)
	ctx := context.Background()
	host, err := ReadHostCPU()
	if err != nil {
		t.Fatalf("read host cpu: %v", err)
	}
	template := os.Getenv("FAAS_TEST_CPU_TEMPLATE")
	if template == "" {
		template = "T2CL"
	}
	if err := ValidateCPUTemplate(template, host); err != nil {
		t.Skipf("host %s cannot carry %s: %v", host.Signature(), template, err)
	}
	t.Logf("host %s, template %s", host.Signature(), template)
	fcVersion, err := DetectFirecrackerVersion(ctx)
	if err != nil {
		t.Fatalf("detect firecracker version: %v", err)
	}
	snapshotRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(snapshotRoot, "snap"), 0o2770); err != nil {
		t.Fatalf("create snapshot root: %v", err)
	}
	store, err := storage.NewLocalStorageBackend(snapshotRoot)
	if err != nil {
		t.Fatalf("create snapshot storage: %v", err)
	}
	v := newMetalVMM(t, 30*time.Second).WithStorage(store).WithCPUTemplate(template)
	m := NewManager(wire.ExecRunner{}, v, Paths{Kernel: kernel}, fcVersion, nil, nil)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	const id = "cputpl"
	snapDir := t.TempDir()
	snap := &Snapshot{FCVersion: fcVersion, StorageKey: "snap/cputpl/mem", VMStateStorageKey: "snap/cputpl/vmstate", VMStatePath: snapDir + "/vmstate"}
	if _, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance: id, Plan: "pro", BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 256,
	}); err != nil {
		t.Fatalf("cold boot with %s: %v", template, err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), id) })
	if got := machineCPUTemplate(t, v, id); got != template {
		t.Fatalf("cold boot: machine-config cpu_template = %q, want %q", got, template)
	}

	if _, err := m.Park(ctx, id, SnapshotSpec{VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey}); err != nil {
		t.Fatalf("park: %v", err)
	}
	inst, err := m.Wake(ctx, WakeRequest{Instance: id, Plan: "pro", BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 256, Snapshot: snap})
	if err != nil {
		t.Fatalf("restore wake: %v", err)
	}
	if inst.Method != WakeRestore {
		t.Fatalf("wake fell back to %s; the %s snapshot did not restore", inst.Method, template)
	}
	t.Logf("restore under %s: machine-config cpu_template = %q", template, machineCPUTemplate(t, v, id))
	if err := m.Destroy(ctx, id); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if m.LeasedCount() != 0 {
		t.Errorf("leaked leases: %d", m.LeasedCount())
	}
}

// machineCPUTemplate reads GET /machine-config's cpu_template; Firecracker
// omits it (or reports "None") when no template is set.
func machineCPUTemplate(t *testing.T, v *JailerVMM, instance string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://localhost/machine-config", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := v.fcClient(instance).Do(req)
	if err != nil {
		t.Fatalf("GET /machine-config: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var got struct {
		CPUTemplate string `json:"cpu_template"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &got) != nil {
		t.Fatalf("GET /machine-config = %d %s", resp.StatusCode, body)
	}
	return got.CPUTemplate
}
