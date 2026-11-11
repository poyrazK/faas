//go:build metal

package fcvm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Shared fixture for the snapshot app-compatibility metal tests
// (ADR-20261109120000, -130000, -140000): one probe app on a runner root
// filesystem, cold-booted, parked into one snapshot and restored on demand.

// compatGoProbe compiles a Go probe (package main) into a static linux binary
// and returns its bytes for buildRuntimeBaseExt4.
func compatGoProbe(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module compatprobe\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "probe")
	build := exec.Command("go", "build", "-trimpath", "-o", out, ".")
	build.Dir = dir
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	if combined, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build go probe: %v: %s", err, combined)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// compatKernel is the guest kernel the tests boot, skipping when absent.
func compatKernel(t *testing.T) string {
	t.Helper()
	kernel := os.Getenv("FAAS_TEST_KERNEL")
	if kernel == "" {
		kernel = "/srv/fc/base/vmlinux-6.1.134"
	}
	if _, err := os.Stat(kernel); err != nil {
		t.Skipf("kernel %s unavailable: %v (set FAAS_TEST_KERNEL)", kernel, err)
	}
	return kernel
}

// compatProbe is one probe app under a Manager.
type compatProbe struct {
	t         *testing.T
	m         *Manager
	vmm       *JailerVMM
	fcVersion string
	base      string
	layer     string
	instance  string
	snap      *Snapshot
}

const compatProbeMemMiB = 256

// startCompatProbe builds the probe's drives and cold-boots it. configure
// adjusts the VMM before the Manager is built.
func startCompatProbe(t *testing.T, name, tarball, file, content string, entrypoint []string, env map[string]string, configure func(*JailerVMM)) (*compatProbe, *Instance) {
	t.Helper()
	kernel := compatKernel(t)
	dir := benchFixtureDir(t)
	p := &compatProbe{t: t, instance: name,
		base: filepath.Join(dir, name+"-base.ext4"), layer: filepath.Join(dir, name+"-layer.ext4")}
	if err := buildRuntimeBaseExt4(p.base, repoRoot(t), tarball, file, content, entrypoint, env); err != nil {
		t.Fatalf("build runtime base: %v", err)
	}
	if err := buildV6LayerExt4Size(p.layer, 64); err != nil {
		t.Fatalf("build layer: %v", err)
	}
	var err error
	if p.fcVersion, err = DetectFirecrackerVersion(context.Background()); err != nil {
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
	p.vmm = newMetalVMM(t, 60*time.Second).WithStorage(store)
	if configure != nil {
		configure(p.vmm)
	}
	stageBenchMountHelper(t, p.vmm)
	p.m = NewManager(wire.ExecRunner{}, p.vmm, Paths{Kernel: kernel}, p.fcVersion, nil, nil)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	t.Cleanup(func() { _ = p.m.Destroy(context.Background(), name) })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	inst, err := p.m.ColdBoot(ctx, ColdBootRequest{
		Instance: name, Plan: "pro", BaseKey: p.base, LayerKey: p.layer, VcpuCount: 1, MemSizeMiB: compatProbeMemMiB,
	})
	if err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	return p, inst
}

// park captures the running probe into one snapshot.
func (p *compatProbe) park() {
	p.t.Helper()
	p.snap = &Snapshot{
		FCVersion:         p.fcVersion,
		StorageKey:        "snap/" + p.instance + "/mem",
		VMStateStorageKey: "snap/" + p.instance + "/vmstate",
		VMStatePath:       filepath.Join(p.t.TempDir(), "vmstate"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if _, err := p.m.Park(ctx, p.instance, SnapshotSpec{
		VMStatePath: p.snap.VMStatePath, StorageKey: p.snap.StorageKey, VMStateStorageKey: p.snap.VMStateStorageKey,
	}); err != nil {
		p.t.Fatalf("park: %v", err)
	}
}

// restore wakes the parked probe from its snapshot and fails unless it
// really restored.
func (p *compatProbe) restore() *Instance {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	inst, err := p.m.Wake(ctx, WakeRequest{
		Instance: p.instance, Plan: "pro", BaseKey: p.base, LayerKey: p.layer,
		VcpuCount: 1, MemSizeMiB: compatProbeMemMiB, Snapshot: p.snap,
	})
	if err != nil {
		p.t.Fatalf("restore: %v", err)
	}
	if inst.Method != WakeRestore {
		p.t.Fatalf("restore: method = %s, want restore", inst.Method)
	}
	return inst
}

// getJSON fetches path from the probe and decodes its JSON reply.
func (p *compatProbe) getJSON(inst *Instance, path string) map[string]any {
	p.t.Helper()
	// No keep-alive: a connection release resets pooled connections.
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Get(fmt.Sprintf("http://%s:8080%s", inst.Lease.HostIP, path))
	if err != nil {
		p.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		p.t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal(body, &values); err != nil {
		p.t.Fatalf("GET %s: response %q: %v", path, body, err)
	}
	return values
}

// compatGoRootfs is the root filesystem tarball a static Go probe runs on.
func compatGoRootfs(t *testing.T) string {
	t.Helper()
	tarball := os.Getenv("FAAS_TEST_RNG_GO_ROOTFS")
	if tarball == "" {
		t.Skip("set FAAS_TEST_RNG_GO_ROOTFS to a root filesystem tarball (alpine works)")
	}
	return tarball
}
