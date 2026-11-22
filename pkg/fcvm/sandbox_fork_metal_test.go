//go:build metal

// adr: 20261122132055 §3 — a fork captures a running session without ending
// it: the source keeps running and writing; each fork restores the capture
// into a session of its own with a new identity, the source's memory,
// processes and files as they were at the fork, and a volume of its own
// restored from the clone taken while the capture paused the guest, so what
// the source or another fork writes later stays its own.
package fcvm

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestMetalSandboxFork(t *testing.T) {
	kernel := os.Getenv("FAAS_TEST_KERNEL")
	base := os.Getenv("FAAS_TEST_AGENT_RUN_BASE")
	if kernel == "" || base == "" {
		t.Skip("set FAAS_TEST_KERNEL and FAAS_TEST_AGENT_RUN_BASE")
	}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	snapRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(snapRoot, "snap"), 0o2770); err != nil {
		t.Fatal(err)
	}
	shared, err := storage.NewLocalStorageBackend(snapRoot)
	if err != nil {
		t.Fatal(err)
	}
	clean := []byte(`{"image":"metal-sandbox-fork","findings":{"CRITICAL":0}}`)
	if err := shared.Put(context.Background(), wire.ScanKeyForBaseKey(base), bytes.NewReader(clean)); err != nil {
		t.Fatal(err)
	}
	cache, err := storage.NewLocalCacheBackend(shared, filepath.Join(t.TempDir(), "cache"), 0)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(wire.ExecRunner{}, newMetalVMM(t, 120*time.Second).WithStorage(cache), Paths{Kernel: kernel}, os.Getenv("FAAS_TEST_FC_VERSION"), slog.Default(), nil)
	m.WithStorage(cache)
	volumeRoot, err := os.MkdirTemp("/srv/fc", ".sbx-fork-metal-")
	if err != nil {
		volumeRoot = t.TempDir()
	} else {
		t.Cleanup(func() { _ = os.RemoveAll(volumeRoot) })
	}
	volumes := NewSandboxVolumes(volumeRoot)
	ctx := context.Background()
	const account, source = "acct-fork", "sbx-fork-source"
	volume, _, err := volumes.Ensure(ctx, account, source, 2)
	if err != nil {
		t.Fatal(err)
	}
	req := SandboxWakeRequest{Instance: "sbx-fork-src", SandboxID: source, AccountID: account, Plan: api.PlanPro,
		BaseKey: base, LayerKey: sandboxRootLayer(t), VcpuCount: 1, MemSizeMiB: 512, CPUMillicores: 1000,
		Network: api.SandboxNetworkRegistries, Volume: volume}
	src, err := m.WakeSandbox(ctx, req)
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), req.Instance) })
	if out, code := userExec(t, m, req.Instance, source, `echo kept-in-memory > /tmp/mem.txt; echo on-the-volume > /workspace/v.txt; sync
nohup sleep 3600 >/dev/null 2>&1 &
echo started`); code != 0 || !strings.Contains(out, "started") {
		t.Fatalf("prepare: %q exit %d", out, code)
	}

	begun := time.Now()
	forked, err := m.ForkSandbox(ctx, SandboxFork{Instance: req.Instance, SandboxID: source, AccountID: account,
		CaptureID: uuid.NewString(), Volumes: volumes, VolumeBackupID: uuid.NewString()})
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	t.Logf("forked in %d ms, %d MB memory", time.Since(begun).Milliseconds(), forked.Info.MemBytes>>20)
	// The source runs on and writes after the fork.
	if out, code := userExec(t, m, req.Instance, source, `echo after-fork > /workspace/source-only.txt && sync && echo ok`); code != 0 || !strings.Contains(out, "ok") {
		t.Fatalf("the source stopped running after the fork: %q exit %d", out, code)
	}
	if err := m.PublishSandboxCapture(ctx, forked.Snapshot.StorageKey); err != nil {
		t.Fatalf("upload capture: %v", err)
	}
	if _, err := forked.Volume.Upload(ctx, shared); err != nil {
		t.Fatalf("upload volume: %v", err)
	}

	ips := map[string]string{src.Lease.HostIP.String(): req.Instance}
	for _, name := range []string{"a", "b"} {
		id := "sbx-fork-" + name
		vol, err := volumes.RestoreBackupFrom(ctx, shared, account, source, id, forked.Volume.ID)
		if err != nil {
			t.Fatalf("fork %s volume: %v", name, err)
		}
		fork := req
		fork.Instance, fork.SandboxID, fork.Volume = "sbx-fork-vm-"+name, id, vol
		fork.Snapshot, fork.SessionCapture = forked.Snapshot, true
		begun = time.Now()
		inst, err := m.WakeSandbox(ctx, fork)
		if err != nil {
			t.Fatalf("fork %s: %v", name, err)
		}
		t.Cleanup(func() { _ = m.Destroy(context.Background(), fork.Instance) })
		if inst.Method != WakeRestore {
			t.Fatalf("fork %s booted by %s: %q (%s)", name, inst.Method, inst.RestoreError, inst.RestoreFallbackReason)
		}
		t.Logf("fork %s restored in %d ms", name, time.Since(begun).Milliseconds())
		if other, dup := ips[inst.Lease.HostIP.String()]; dup {
			t.Fatalf("fork %s shares host IP %s with %s", name, inst.Lease.HostIP, other)
		}
		ips[inst.Lease.HostIP.String()] = fork.Instance
		out, code := userExec(t, m, fork.Instance, id, `cat /tmp/mem.txt /workspace/v.txt
test -e /workspace/source-only.txt && echo saw-source-write
test -e /workspace/other-fork.txt && echo saw-other-fork
n=0; for p in /proc/[0-9]*; do tr '\0' ' ' < $p/cmdline 2>/dev/null | grep -q '^sleep 3600' && n=$((n+1)); done; echo "sleepers=$n"
echo mine > /workspace/other-fork.txt && sync`)
		for _, want := range []string{"kept-in-memory\n", "on-the-volume\n", "sleepers=1\n"} {
			if code != 0 || !strings.Contains(out, want) {
				t.Fatalf("fork %s lacks %q (exit %d):\n%s", name, want, code, out)
			}
		}
		if strings.Contains(out, "saw-source-write") || strings.Contains(out, "saw-other-fork") {
			t.Fatalf("fork %s sees a write made after the fork:\n%s", name, out)
		}
	}
	if out, code := userExec(t, m, req.Instance, source, `test -e /workspace/other-fork.txt && echo leaked; echo checked`); code != 0 || strings.Contains(out, "leaked") {
		t.Fatalf("a fork's write reached the source:\n%s", out)
	}
	for _, id := range []string{"sbx-fork-vm-a", "sbx-fork-vm-b", req.Instance} {
		if err := m.Destroy(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
}
