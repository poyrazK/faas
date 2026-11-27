//go:build metal

// adr: 20261122132055 §4 — a persistent session's /workspace is its volume:
// a sparse ext4 file vmmd attaches as drive 2 and guest-init mounts. Writes
// land in the volume, a pause and its restore keep it attached, the VM's end
// leaves it on the node, and a later cold boot of the session, after the
// volume grew while no VM had it, sees the same files on the larger disk.
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

func TestMetalSandboxVolume(t *testing.T) {
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
	store, err := storage.NewLocalStorageBackend(snapRoot)
	if err != nil {
		t.Fatal(err)
	}
	clean := []byte(`{"image":"metal-sandbox-volume","findings":{"CRITICAL":0}}`)
	if err := store.Put(context.Background(), wire.ScanKeyForBaseKey(base), bytes.NewReader(clean)); err != nil {
		t.Fatal(err)
	}
	m := NewManager(wire.ExecRunner{}, newMetalVMM(t, 120*time.Second).WithStorage(store), Paths{Kernel: kernel}, os.Getenv("FAAS_TEST_FC_VERSION"), slog.Default(), nil)
	m.WithStorage(store)
	// Volumes live on the node's /srv/fc (XFS, reflinks) as in production.
	volumeRoot, err := os.MkdirTemp("/srv/fc", ".sbx-volume-metal-")
	if err != nil {
		volumeRoot = t.TempDir()
	} else {
		t.Cleanup(func() { _ = os.RemoveAll(volumeRoot) })
	}
	volumes := NewSandboxVolumes(volumeRoot)
	ctx := context.Background()
	const account, sandboxID = "acct-volume", "sbx-volume-session"
	volume, created, err := volumes.Ensure(ctx, account, sandboxID, 2)
	if err != nil || !created {
		t.Fatalf("volume: %v (created %v)", err, created)
	}
	req := SandboxWakeRequest{Instance: "sbx-volume-1", SandboxID: sandboxID, AccountID: account, Plan: api.PlanPro,
		BaseKey: base, LayerKey: sandboxRootLayer(t), VcpuCount: 1, MemSizeMiB: 512, CPUMillicores: 1000,
		Network: api.SandboxNetworkNone, Volume: volume}
	if _, err := m.WakeSandbox(ctx, req); err != nil {
		t.Fatalf("boot: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), req.Instance) })
	out, code := userExec(t, m, req.Instance, sandboxID, `echo on-the-volume > /workspace/v.txt && sync
awk '$2=="/workspace"{print "mount="$1}' /proc/mounts; df -k /workspace | awk 'NR==2{print "kb="$2}'`)
	if code != 0 || !strings.Contains(out, "mount=/dev/vdc") {
		t.Fatalf("workspace is not the volume (exit %d):\n%s", code, out)
	}
	if kb := volumeKB(out); kb < 1_900_000 || kb > 2_100_000 {
		t.Fatalf("a 2 GB volume shows %d KB:\n%s", kb, out)
	}

	// Pause and resume: the capture refers to the volume, which stays put.
	paused, kept, err := m.PauseSandbox(ctx, SandboxPause{Instance: req.Instance, SandboxID: sandboxID, AccountID: account, CaptureID: uuid.NewString()})
	if err != nil || kept {
		t.Fatalf("pause: %v (vm kept %v)", err, kept)
	}
	if _, err := os.Stat(volume); err != nil {
		t.Fatalf("the pause lost the volume: %v", err)
	}
	resume := req
	resume.Snapshot, resume.SessionCapture = paused.Snapshot, true
	restored, err := m.WakeSandbox(ctx, resume)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), resume.Instance) })
	if restored.Method != WakeRestore {
		t.Fatalf("resumed by %s: %q (%s)", restored.Method, restored.RestoreError, restored.RestoreFallbackReason)
	}
	out, code = userExec(t, m, resume.Instance, sandboxID, `cat /workspace/v.txt; echo after-resume >> /workspace/v.txt && sync`)
	if code != 0 || !strings.Contains(out, "on-the-volume") {
		t.Fatalf("the restored session lost its volume (exit %d):\n%s", code, out)
	}
	if err := m.Destroy(ctx, resume.Instance); err != nil {
		t.Fatal(err)
	}

	// The VM is gone, the volume is not. A backup, then the node's copy is
	// lost: the backup restores it, and it grows while no VM has it.
	begun := time.Now()
	backup, err := volumes.Backup(ctx, store, account, sandboxID, "b1")
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	t.Logf("backed up in %d ms: read %d chunks, uploaded %d (%d KB)", time.Since(begun).Milliseconds(),
		backup.Stats.ChunksRead, backup.Stats.ChunksUploaded, backup.Stats.BytesUploaded>>10)
	again, err := volumes.Backup(ctx, store, account, sandboxID, "b2")
	if err != nil || again.Stats.ChunksUploaded != 0 {
		t.Fatalf("an unchanged volume's backup uploaded %+v: %v", again.Stats, err)
	}
	t.Logf("unchanged backup read %d chunks (incremental %v)", again.Stats.ChunksRead, again.Incremental)
	if err := volumes.Remove(account, sandboxID); err != nil {
		t.Fatal(err)
	}
	begun = time.Now()
	if _, err := volumes.RestoreBackup(ctx, store, account, sandboxID, "b2"); err != nil {
		t.Fatalf("restore backup: %v", err)
	}
	t.Logf("restored from the backup in %d ms", time.Since(begun).Milliseconds())
	if _, created, err := volumes.Ensure(ctx, account, sandboxID, 3); err != nil || created {
		t.Fatalf("grow: %v (created %v)", err, created)
	}
	cold := req
	cold.Instance = "sbx-volume-3"
	inst, err := m.WakeSandbox(ctx, cold)
	if err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), cold.Instance) })
	if inst.Method == WakeRestore {
		t.Fatal("a session with a volume restored a warm snapshot")
	}
	out, code = userExec(t, m, cold.Instance, sandboxID, `cat /workspace/v.txt; df -k /workspace | awk 'NR==2{print "kb="$2}'; touch /workspace/own && echo writable`)
	if code != 0 || !strings.Contains(out, "on-the-volume\nafter-resume\n") || !strings.Contains(out, "writable") {
		t.Fatalf("the cold-booted session lost its volume (exit %d):\n%s", code, out)
	}
	if kb := volumeKB(out); kb < 2_900_000 || kb > 3_200_000 {
		t.Fatalf("a volume grown to 3 GB shows %d KB:\n%s", kb, out)
	}
	if err := m.Destroy(ctx, cold.Instance); err != nil {
		t.Fatal(err)
	}
	if err := volumes.Remove(account, sandboxID); err != nil {
		t.Fatal(err)
	}
}

func volumeKB(out string) int64 {
	_, rest, ok := strings.Cut(out, "kb=")
	if !ok {
		return 0
	}
	var kb int64
	for _, c := range rest {
		if c < '0' || c > '9' {
			break
		}
		kb = kb*10 + int64(c-'0')
	}
	return kb
}
