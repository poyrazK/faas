//go:build metal

// adr: 20261122132055 §2 — a persistent session pauses with its memory kept:
// PauseSandbox captures the running VM with its private drive and ends it
// (no VM, cgroup or jail left while paused), writing the capture to this
// node's cache only; PublishSandboxCapture then gives the shared store a
// copy. The resume here reads through an empty cache, as another node would,
// so it restores from the shared copy. The restore takes whatever identity is
// free (slot, jail uid, host IP), steps the clock, and the guest keeps its
// processes, its tmpfs and its /workspace; the network works.
package fcvm

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/storagekey"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestMetalSandboxPauseAndResume(t *testing.T) {
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
	nodeCache := func() *storage.LocalCacheBackend {
		c, err := storage.NewLocalCacheBackend(shared, filepath.Join(t.TempDir(), "cache"), 0)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	// The manager reads its store, so it gates the boot on the base's scan.
	clean := []byte(`{"image":"metal-sandbox-pause","findings":{"CRITICAL":0}}`)
	if err := shared.Put(context.Background(), wire.ScanKeyForBaseKey(base), bytes.NewReader(clean)); err != nil {
		t.Fatal(err)
	}
	cache := nodeCache()
	vmm := newMetalVMM(t, 120*time.Second).WithStorage(cache)
	m := NewManager(wire.ExecRunner{}, vmm, Paths{Kernel: kernel}, os.Getenv("FAAS_TEST_FC_VERSION"), slog.Default(), nil)
	m.WithStorage(cache)
	ctx := context.Background()
	const account, sandboxID, instance = "acct-pause", "sbx-pause-session", "sbx-pause-1"
	req := SandboxWakeRequest{Instance: instance, SandboxID: sandboxID, AccountID: account, Plan: api.PlanPro,
		BaseKey: base, LayerKey: sandboxRootLayer(t), VcpuCount: 1, MemSizeMiB: 1024, CPUMillicores: 1000,
		Network: api.SandboxNetworkRegistries}
	inst, err := m.WakeSandbox(ctx, req)
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })
	before := inst.Lease
	out, code := userExec(t, m, instance, sandboxID, `echo kept-in-memory > /tmp/mem.txt; echo kept-on-drive > /workspace/disk.txt
nohup sleep 3600 >/dev/null 2>&1 &
echo started`)
	if code != 0 || !strings.Contains(out, "started") {
		t.Fatalf("prepare: %q exit %d", out, code)
	}

	captureID := uuid.NewString()
	begun := time.Now()
	paused, kept, err := m.PauseSandbox(ctx, SandboxPause{Instance: instance, SandboxID: sandboxID, AccountID: account, CaptureID: captureID})
	if err != nil || kept {
		t.Fatalf("pause: %v (vm kept %v)", err, kept)
	}
	snap, info := paused.Snapshot, paused.Info
	t.Logf("paused in %d ms, %d MB memory, backing %v", time.Since(begun).Milliseconds(), info.MemBytes>>20, paused.HasBacking)
	if !storagekey.IsSandboxSessionSnapshotKey(snap.StorageKey, account) || snap.Networkless || info.MemBytes == 0 {
		t.Fatalf("capture %+v %+v", snap, info)
	}
	// Invariant 4: a paused session holds no VM.
	if m.LiveCount() != 0 {
		t.Fatalf("%d VMs live while the session is paused", m.LiveCount())
	}
	if _, _, err := m.PauseSandbox(ctx, SandboxPause{Instance: instance, SandboxID: sandboxID, AccountID: account, CaptureID: uuid.NewString()}); err == nil {
		t.Fatal("paused a VM that is gone")
	}
	// Node first: the pause wrote the cache only.
	keys := sandboxCaptureKeys(snap.StorageKey)
	for _, key := range keys[:3] {
		if !cache.LocalOnly(key) {
			t.Fatalf("%s is not local-only after the pause", key)
		}
		if rc, err := shared.Get(ctx, key); err == nil {
			_ = rc.Close()
			t.Fatalf("the shared store has %s before the upload", key)
		}
	}
	begun = time.Now()
	if err := m.PublishSandboxCapture(ctx, snap.StorageKey); err != nil {
		t.Fatalf("upload: %v", err)
	}
	t.Logf("uploaded in %d ms", time.Since(begun).Milliseconds())
	for _, key := range keys[:3] {
		if cache.LocalOnly(key) {
			t.Fatalf("%s is still local-only after the upload", key)
		}
		rc, err := shared.Get(ctx, key)
		if err != nil {
			t.Fatalf("the shared store lacks %s after the upload: %v", key, err)
		}
		_ = rc.Close()
	}
	// Another node: an empty cache over the same shared store.
	other := nodeCache()
	vmm.WithStorage(other)
	m.WithStorage(other)

	// Another session takes the freed slot, so the restore cannot inherit it.
	filler := req
	filler.Instance, filler.SandboxID, filler.Network = "sbx-pause-filler", "sbx-pause-filler-session", api.SandboxNetworkNone
	if _, err := m.WakeSandbox(ctx, filler); err != nil {
		t.Fatalf("filler: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), filler.Instance) })
	time.Sleep(3 * time.Second)

	resume := req
	resume.Snapshot, resume.SessionCapture = snap, true
	begun = time.Now()
	restored, err := m.WakeSandbox(ctx, resume)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if restored.Method != WakeRestore {
		t.Fatalf("resumed by %s, want a restore: %q (%s)", restored.Method, restored.RestoreError, restored.RestoreFallbackReason)
	}
	t.Logf("resumed in %d ms", time.Since(begun).Milliseconds())
	after := restored.Lease
	if after.Slot == before.Slot || after.UID == before.UID || after.HostIP == before.HostIP {
		t.Fatalf("the restore kept the paused VM's identity: slot %d→%d uid %d→%d ip %s→%s",
			before.Slot, after.Slot, before.UID, after.UID, before.HostIP, after.HostIP)
	}
	out, code = userExec(t, m, instance, sandboxID, `cat /tmp/mem.txt /workspace/disk.txt
n=0; for p in /proc/[0-9]*; do tr '\0' ' ' < $p/cmdline 2>/dev/null | grep -q '^sleep 3600' && n=$((n+1)); done; echo "sleepers=$n"
echo "now=$(date +%s)"`)
	for _, want := range []string{"kept-in-memory\n", "kept-on-drive\n", "sleepers=1\n"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("missing %q after the resume (exit %d):\n%s", want, code, out)
		}
	}
	_, nowLine, _ := strings.Cut(out, "now=")
	var now int64
	if _, err := fmt.Sscan(strings.TrimSpace(nowLine), &now); err != nil || time.Since(time.Unix(now, 0)).Abs() > 10*time.Second {
		t.Fatalf("clock %q not stepped after the resume", nowLine)
	}
	if out, _ := rootExec(t, m, instance, sandboxID, "ip -4 -o addr show eth0; ping -c 2 -W 1 "+netns.GuestGateway+" >/dev/null 2>&1 && echo answered"); !strings.Contains(out, netns.GuestIP+"/") || !strings.Contains(out, "answered") {
		t.Fatalf("no network after the resume:\n%s", out)
	}
	for _, id := range []string{instance, filler.Instance} {
		if err := m.Destroy(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
}
