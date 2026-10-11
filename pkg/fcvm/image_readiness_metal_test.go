//go:build linux && metal

// adr:683
package fcvm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// This opt-in qualification owns an isolated bridge and jail namespace. It
// builds guest-init from this checkout; previously published fixtures cannot
// accidentally qualify a new command-gate protocol.
func TestMetalImageHealthcheckAcrossSnapshots(t *testing.T) {
	if os.Getenv("FAAS_TEST_IMAGE_HEALTHCHECK") != "1" {
		t.Skip("requires dedicated native KVM host; set FAAS_TEST_IMAGE_HEALTHCHECK=1")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Fatal("native KVM is required")
	}
	kernel := os.Getenv("FAAS_TEST_KERNEL")
	if kernel == "" {
		t.Fatal("FAAS_TEST_KERNEL is required")
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	base, layer := filepath.Join(dir, "base.ext4"), filepath.Join(dir, "layer.ext4")
	if err := buildV6BaseExt4(base, repo); err != nil {
		t.Fatal(err)
	}
	if err := buildV6LayerExt4(layer); err != nil {
		t.Fatal(err)
	}
	manifest := `{"manifest_version":"v1","entrypoint":["/usr/local/bin/faas-write-uuid"],"port":8080,"healthcheck":{"test":["CMD-SHELL","printf x >> /var/tmp/hc-count; test ! -f /var/tmp/hc-fail"],"retries":1,"image_timing":{"interval_ns":30000000000,"timeout_ns":1000000000}}}`
	cgi := "#!/bin/sh\nprintf 'Content-Type: text/plain\\r\\n\\r\\n'\ncase \"$QUERY_STRING\" in\n action=fail) /bin/busybox touch /var/tmp/hc-fail ;;\n action=pass) /bin/busybox rm -f /var/tmp/hc-fail ;;\nesac\n/bin/busybox wc -c < /var/tmp/hc-count\n"
	writeImageReadinessFixtureFile(t, base, "/etc/faas/app.json", manifest, "0100644", true)
	writeImageReadinessFixtureFile(t, base, "/cgi-bin/healthcheck", cgi, "0100755", false)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	run := wire.ExecRunner{}
	for _, cmd := range [][]string{{"ip", "link", "add", netns.TenantBridge, "type", "bridge"}, {"ip", "addr", "add", "10.100.0.1/16", "dev", netns.TenantBridge}, {"ip", "link", "set", netns.TenantBridge, "up"}} {
		if err := run.Run(ctx, cmd); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = run.Run(context.Background(), []string{"ip", "link", "del", netns.TenantBridge}) })
	be, err := storage.NewLocalStorageBackend(filepath.Join(dir, "storage"))
	if err != nil {
		t.Fatal(err)
	}
	version, err := DetectFirecrackerVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	vmm := newMetalVMM(t, 10*time.Second).WithStorage(be)
	m := NewManager(run, vmm, Paths{Kernel: kernel}, version, nil, nil).WithLifecycleContext(ctx)
	m.alloc.free = []int{MaxSlots - 1, MaxSlots - 2}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	for _, id := range []string{"hcprime", "hca", "hcb", "hcbad"} {
		id := id
		t.Cleanup(func() { _, _, _ = m.SignalAndKill(context.Background(), id, 0, 0) })
	}
	request := WakeRequest{BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: 256, Plan: api.PlanHobby, Runtime: "go124", ImageHealthcheckRequired: true}
	request.Instance = "hcprime"
	first, err := m.Wake(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	count := readImageReadinessFixture(t, ctx, first, "")
	if count < 1 {
		t.Fatal("cold boot never executed the declared check")
	}
	snap := &Snapshot{FCVersion: version, StorageKey: "snap/hc/mem", VMStateStorageKey: "snap/hc/vmstate", VMStatePath: filepath.Join(dir, "vmstate")}
	if _, err := m.Park(ctx, first.Lease.Instance, SnapshotSpec{StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey, VMStatePath: snap.VMStatePath}); err != nil {
		t.Fatal(err)
	}
	request.Snapshot = snap
	for cycle := 0; cycle < 5; cycle++ {
		for _, id := range []string{"hca", "hcb"} {
			request.Instance = id
			instance, err := m.Wake(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			if instance.Method != WakeRestore {
				t.Fatalf("restore fell back: %s", instance.RestoreError)
			}
			if got := readImageReadinessFixture(t, ctx, instance, ""); got <= count {
				t.Fatalf("restore reused the snapshot pass: checks=%d snapshot=%d", got, count)
			}
			if _, _, err := m.SignalAndKill(ctx, id, 0, 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	request.Instance = "hca"
	request.KeepPaused = true
	warm, err := m.Wake(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.ResumeVM(ctx, warm.Lease.Instance); err != nil {
		t.Fatal(err)
	}
	if got := readImageReadinessFixture(t, ctx, warm, ""); got <= count {
		t.Fatal("warm resume reused a snapshot pass")
	}
	if _, _, err := m.SignalAndKill(ctx, warm.Lease.Instance, 0, 0); err != nil {
		t.Fatal(err)
	}
	request.KeepPaused = false
	request.Instance = "hcbad"
	bad, err := m.Wake(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	readImageReadinessFixture(t, ctx, bad, "action=fail")
	monitorCtx, stopMonitor := context.WithTimeout(ctx, time.Minute)
	reason := RunImageHealthcheckMonitor(monitorCtx, vmm.VsockUDSSocketPath(bad.Lease.Instance), nil)
	stopMonitor()
	if reason != LivenessReasonImageHealthcheck {
		t.Fatalf("listening unhealthy image did not request runtime recovery: %q", reason)
	}
	readImageReadinessFixture(t, ctx, bad, "")
	if err := vmm.WaitImageHealthcheck(ctx, bad.Lease, 1); err == nil {
		t.Fatal("listening but unhealthy guest passed")
	}
	if _, err := m.Park(ctx, bad.Lease.Instance, SnapshotSpec{StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey, VMStatePath: snap.VMStatePath}); err != nil {
		t.Fatal(err)
	}
	request.Instance = "hca"
	restored, err := m.Wake(ctx, request)
	if err == nil && restored.Method == WakeRestore {
		t.Fatal("snapshot containing an old pass admitted an unhealthy restore")
	}
	if err == nil {
		if _, _, err := m.SignalAndKill(ctx, request.Instance, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("healthcheck qualification leaked a VM or lease")
	}
}

func writeImageReadinessFixtureFile(t *testing.T, image, path, body, mode string, replace bool) {
	t.Helper()
	source := filepath.Join(t.TempDir(), "fixture")
	if err := os.WriteFile(source, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	commands := []string{fmt.Sprintf("write %s %s", source, path), fmt.Sprintf("set_inode_field %s mode %s", path, mode)}
	if replace {
		commands = append([]string{"rm " + path}, commands...)
	}
	for _, command := range commands {
		out, err := exec.Command("debugfs", "-w", "-R", command, image).CombinedOutput()
		if err != nil || strings.Contains(string(out), "File not found") {
			t.Fatalf("fixture edit: %s: %v", out, err)
		}
	}
}

func readImageReadinessFixture(t *testing.T, ctx context.Context, instance *Instance, query string) int {
	t.Helper()
	url := "http://" + instance.Lease.HostIP.String() + ":8080/cgi-bin/healthcheck?" + query
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Timeout: time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("fixture response: %s %v", body, err)
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	return count
}
