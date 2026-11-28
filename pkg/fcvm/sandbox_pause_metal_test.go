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
	"strconv"
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
		BaseKey: base, LayerKey: sandboxRootLayer(t), VcpuCount: 1, MemSizeMiB: metalSandboxMemMiB(t), CPUMillicores: 1000,
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
	if os.Getenv("FAAS_TEST_SANDBOX_BENCH") == "1" {
		sandboxWriteBench(t, m, instance, sandboxID)
	}
	if os.Getenv("FAAS_TEST_SANDBOX_DIFF") == "1" {
		sandboxDiffRounds(t, m, vmm, req)
	}
	for _, id := range []string{instance, filler.Instance} {
		if err := m.Destroy(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
}

// sandboxDiffRounds pauses and resumes the restored session twice more with
// diff captures on (ADR-20261122132055): each round writes fresh random data
// into the guest's tmpfs, and after each resume every round's data must read
// back intact, so pages from the restored image, the first merged diff and
// the second all survive.
func sandboxDiffRounds(t *testing.T, m *Manager, vmm *JailerVMM, req SandboxWakeRequest) {
	t.Helper()
	ctx := context.Background()
	sums := map[string]string{}
	for round := 1; round <= 2; round++ {
		if !vmm.sandboxDiff.tracking(req.Instance) {
			t.Fatalf("round %d: the restored session is not dirty-tracked", round)
		}
		name := fmt.Sprintf("/tmp/round%d", round)
		out, code := userExec(t, m, req.Instance, req.SandboxID, fmt.Sprintf("head -c 33554432 /dev/urandom > %s && md5sum %s", name, name))
		sum, _, _ := strings.Cut(strings.TrimSpace(out), " ")
		if code != 0 || len(sum) != 32 {
			t.Fatalf("round %d write: %q exit %d", round, out, code)
		}
		sums[name] = sum
		begun := time.Now()
		paused, kept, err := m.PauseSandbox(ctx, SandboxPause{Instance: req.Instance, SandboxID: req.SandboxID, AccountID: req.AccountID, CaptureID: uuid.NewString()})
		if err != nil || kept {
			t.Fatalf("round %d pause: %v (vm kept %v)", round, err, kept)
		}
		t.Logf("round %d: diff pause in %d ms, %d MB memory", round, time.Since(begun).Milliseconds(), paused.Info.MemBytes>>20)
		// Upload as the first round did: this test's shared store is a local
		// directory, whose paths the restore resolves before the cache's.
		begun = time.Now()
		if err := m.PublishSandboxCapture(ctx, paused.Snapshot.StorageKey); err != nil {
			t.Fatalf("round %d upload: %v", round, err)
		}
		t.Logf("round %d: uploaded in %d ms", round, time.Since(begun).Milliseconds())
		resume := req
		resume.Snapshot, resume.SessionCapture = paused.Snapshot, true
		begun = time.Now()
		restored, err := m.WakeSandbox(ctx, resume)
		if err != nil {
			t.Fatalf("round %d resume: %v", round, err)
		}
		if restored.Method != WakeRestore {
			t.Fatalf("round %d resumed by %s, want a restore: %q (%s)", round, restored.Method, restored.RestoreError, restored.RestoreFallbackReason)
		}
		t.Logf("round %d: resumed in %d ms", round, time.Since(begun).Milliseconds())
		check := "cat /tmp/mem.txt"
		for file := range sums {
			check += "; md5sum " + file
		}
		out, code = userExec(t, m, req.Instance, req.SandboxID, check)
		if code != 0 || !strings.Contains(out, "kept-in-memory") {
			t.Fatalf("round %d: first-boot memory lost (exit %d):\n%s", round, code, out)
		}
		for file, sum := range sums {
			if !strings.Contains(out, sum+"  "+file) {
				t.Fatalf("round %d: %s changed across the diff capture:\n%s", round, file, out)
			}
		}
	}
}

// sandboxWriteBench times guest memory writes in the restored session, the
// cost KVM dirty page tracking adds (ADR-20261122132055): pass 1 writes
// 384 MiB of tmpfs pages the restore never touched, passes 2 and 3 rewrite
// the same pages. Compare runs with FAAS_TEST_SANDBOX_DIFF=1 and without.
func sandboxWriteBench(t *testing.T, m *Manager, instance, sandboxID string) {
	t.Helper()
	for pass := 1; pass <= 3; pass++ {
		begun := time.Now()
		out, code := userExec(t, m, instance, sandboxID, "dd if=/dev/zero of=/tmp/bench bs=1M count=384 conv=notrunc 2>&1 | tail -1")
		if code != 0 {
			t.Fatalf("bench pass %d: %q exit %d", pass, out, code)
		}
		t.Logf("bench pass %d: 384 MiB guest write in %d ms", pass, time.Since(begun).Milliseconds())
	}
	if out, code := userExec(t, m, instance, sandboxID, "rm -f /tmp/bench"); code != 0 {
		t.Fatalf("bench cleanup: %q exit %d", out, code)
	}
}

// metalSandboxMemMiB is the paused session's memory: 1024 MiB, or
// FAAS_TEST_SANDBOX_MEM_MIB to measure how pause and resume scale with it.
func metalSandboxMemMiB(t *testing.T) int {
	t.Helper()
	raw := os.Getenv("FAAS_TEST_SANDBOX_MEM_MIB")
	if raw == "" {
		return 1024
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 256 {
		t.Fatalf("FAAS_TEST_SANDBOX_MEM_MIB=%q: want a size in MiB of at least 256", raw)
	}
	return n
}
