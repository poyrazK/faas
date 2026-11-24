//go:build metal

// adr: 20261124075005 rollout step 4 — the sandbox desktop image boots in a
// Firecracker session VM with agentd's start and ready commands, answers
// gregale-desktop actions as the session's user, and keeps its desktop
// running across a fork capture and restore. §3b: the shared warm desktop
// snapshot, prewarmed and stopped before its capture, restores and runs the
// session's commands to a working desktop. It logs the numbers the ADR
// records: cold boot to ready, the VM's resident memory idle and with
// Chromium open, the capture's memory size, the restore to a working screen
// and the warm snapshot's capture and restore to ready.
//
//	FAAS_TEST_SANDBOX_DESKTOP_ROOT  an unpacked sandbox-desktop image
package fcvm

import (
	"bytes"
	"context"
	"encoding/base64"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/storagekey"
	"github.com/onebox-faas/faas/pkg/wire"
)

// desktopImageDrive packs the unpacked desktop image as imaged publishes the
// platform desktop drive, sized to fit it, and places it in the shared store
// under the drive's key (ADR-20261124075005 §3a).
func desktopImageDrive(t *testing.T, root string, shared string) string {
	t.Helper()
	marker := filepath.Join(root, strings.TrimPrefix(api.FullRootfsMarkerPath, "/"))
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(api.FullRootfsMarkerValue), 0o444); err != nil {
		t.Fatal(err)
	}
	drive := filepath.Join(t.TempDir(), "desktop-image.ext4")
	if out, err := exec.Command("truncate", "-s", "4G", drive).CombinedOutput(); err != nil {
		t.Fatalf("truncate: %v %s", err, out)
	}
	if out, err := exec.Command("mkfs.ext4", "-q", "-O", "^has_journal", "-d", root, "-L", "desktop", "-F", drive).CombinedOutput(); err != nil {
		t.Fatalf("mkfs.ext4: %v %s", err, out)
	}
	key := storagekey.SandboxDesktopImage("sha256:" + strings.Repeat("0d", 32))
	published := filepath.Join(shared, key)
	if err := os.MkdirAll(filepath.Dir(published), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(drive, published); err != nil {
		t.Fatal(err)
	}
	return key
}

func desktopRun(t *testing.T, m *Manager, instance, sandboxID string, args ...string) string {
	t.Helper()
	out, code := userExec(t, m, instance, sandboxID, "/usr/local/bin/gregale-desktop "+strings.Join(args, " "))
	if code != 0 {
		t.Fatalf("gregale-desktop %s: exit %d: %s", strings.Join(args, " "), code, out)
	}
	return out
}

// desktopScreenshot takes a PNG and checks it is one with more than a bare
// root window on it.
func desktopScreenshot(t *testing.T, m *Manager, instance, sandboxID string) int {
	t.Helper()
	png, err := base64.StdEncoding.DecodeString(strings.TrimSpace(desktopRun(t, m, instance, sandboxID, "screenshot", "--base64")))
	if err != nil || !bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")) || len(png) < 4096 {
		t.Fatalf("screenshot: %d bytes, %v", len(png), err)
	}
	return len(png)
}

func vmResidentMB(inst *Instance) int64 {
	cur, err := readCgroupInt(vmCgroupScope(inst.Lease), "memory.current")
	if err != nil {
		return -1
	}
	return cur >> 20
}

func TestMetalSandboxDesktop(t *testing.T) {
	kernel := os.Getenv("FAAS_TEST_KERNEL")
	base := os.Getenv("FAAS_TEST_AGENT_RUN_BASE")
	root := os.Getenv("FAAS_TEST_SANDBOX_DESKTOP_ROOT")
	if kernel == "" || base == "" || root == "" {
		t.Skip("set FAAS_TEST_KERNEL, FAAS_TEST_AGENT_RUN_BASE and FAAS_TEST_SANDBOX_DESKTOP_ROOT (an unpacked sandbox-desktop image)")
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
	clean := []byte(`{"image":"metal-sandbox-desktop","findings":{"CRITICAL":0}}`)
	if err := shared.Put(context.Background(), wire.ScanKeyForBaseKey(base), bytes.NewReader(clean)); err != nil {
		t.Fatal(err)
	}
	cache, err := storage.NewLocalCacheBackend(shared, filepath.Join(t.TempDir(), "cache"), 0)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(wire.ExecRunner{}, newMetalVMM(t, 180*time.Second).WithStorage(cache), Paths{Kernel: kernel}, os.Getenv("FAAS_TEST_FC_VERSION"), slog.Default(), nil)
	m.WithStorage(cache)
	image := desktopImageDrive(t, root, snapRoot)
	ctx := context.Background()
	const account, source = "acct-desktop", "sbx-desktop-1"
	// agentd's commands for a 1280x800 desktop (pkg/agentd desktopCommands).
	req := SandboxWakeRequest{Instance: "sbx-desktop", SandboxID: source, AccountID: account, Plan: api.PlanPro,
		BaseKey: base, LayerKey: sandboxRootLayer(t), ImageKey: image, VcpuCount: 2, MemSizeMiB: 2048, CPUMillicores: 2000,
		Network: api.SandboxNetworkNone, StartCmd: "gregale-desktop start --resolution 1280x800", ReadyCmd: "gregale-desktop wait",
		ReadyTimeout: time.Duration(api.SandboxDesktopReadyTimeoutSec) * time.Second}
	begun := time.Now()
	inst, err := m.WakeSandbox(ctx, req)
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), req.Instance) })
	t.Logf("desktop cold boot to ready: %d ms", time.Since(begun).Milliseconds())
	// Where the time went, in guest seconds since boot: when the X socket,
	// each helper's log and the start command's log first appeared.
	breakdown, _ := userExec(t, m, req.Instance, source, `up=$(cut -d' ' -f1 /proc/uptime); boot=$(awk -v n="$(date +%s.%N)" -v u="$up" 'BEGIN{printf "%.3f", n-u}')
for f in /tmp/gregale-start.log /tmp/.X11-unix/X0 /tmp/gregale-desktop/*.log; do
  [ -e "$f" ] && echo "$f $(stat -c %.3W "$f") $(stat -c %.3Y "$f")"; done | awk -v b="$boot" '{w=($2>0?$2:$3); printf "%-40s created +%.2fs  last write +%.2fs\n", $1, w-b, $3-b}'
echo "uptime at check ${up}s"`)
	t.Logf("boot breakdown:\n%s", breakdown)

	if got := strings.TrimSpace(desktopRun(t, m, req.Instance, source, "size")); got != "1280x800" {
		t.Fatalf("size = %q", got)
	}
	shot := desktopScreenshot(t, m, req.Instance, source)
	shotAt := time.Now()
	desktopScreenshot(t, m, req.Instance, source)
	t.Logf("screenshot round trip through exec: %d ms", time.Since(shotAt).Milliseconds())
	clickAt := time.Now()
	desktopRun(t, m, req.Instance, source, "click", "640", "400")
	t.Logf("click round trip through exec: %d ms", time.Since(clickAt).Milliseconds())
	time.Sleep(3 * time.Second)
	t.Logf("idle desktop: VM resident %d MB, screenshot %d bytes", vmResidentMB(inst), shot)

	acted := time.Now()
	desktopRun(t, m, req.Instance, source, "move", "100", "200")
	if got := strings.TrimSpace(desktopRun(t, m, req.Instance, source, "cursor")); got != "100 200" {
		t.Fatalf("cursor = %q", got)
	}
	t.Logf("move + cursor round trip: %d ms", time.Since(acted).Milliseconds())
	desktopRun(t, m, req.Instance, source, "launch", "mousepad")
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(desktopRun(t, m, req.Instance, source, "windows", "--app", "mousepad"), "ousepad") {
		if time.Now().After(deadline) {
			t.Fatal("mousepad opened no window")
		}
		time.Sleep(250 * time.Millisecond)
	}
	time.Sleep(time.Second)
	desktopRun(t, m, req.Instance, source, "type", "--", "'hello from a microVM'")
	if !strings.Contains(desktopRun(t, m, req.Instance, source, "window"), "Mousepad") {
		t.Fatal("mousepad is not focused after typing")
	}
	desktopRun(t, m, req.Instance, source, "launch", "chromium", "--", "about:blank")
	deadline = time.Now().Add(60 * time.Second)
	for !strings.Contains(desktopRun(t, m, req.Instance, source, "windows", "--app", "chromium"), "hromium") {
		if time.Now().After(deadline) {
			t.Fatal("chromium opened no window")
		}
		time.Sleep(500 * time.Millisecond)
	}
	time.Sleep(5 * time.Second)
	t.Logf("with Chromium and Mousepad open: VM resident %d MB", vmResidentMB(inst))

	captured := time.Now()
	forked, err := m.ForkSandbox(ctx, SandboxFork{Instance: req.Instance, SandboxID: source, AccountID: account, CaptureID: uuid.NewString()})
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	t.Logf("capture: %d ms, %d MB memory", time.Since(captured).Milliseconds(), forked.Info.MemBytes>>20)

	fork := req
	fork.Instance, fork.SandboxID = "sbx-desktop-fork", "sbx-desktop-2"
	fork.Snapshot, fork.SessionCapture, fork.ForkedFrom = forked.Snapshot, true, source
	restored := time.Now()
	finst, err := m.WakeSandbox(ctx, fork)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), fork.Instance) })
	if finst.Method != WakeRestore {
		t.Fatalf("the fork cold-booted (%v); want a restore of the capture", finst.Method)
	}
	// The desktop was running when it was captured: nothing starts it again.
	desktopRun(t, m, fork.Instance, fork.SandboxID, "wait")
	t.Logf("restore to a working desktop: %d ms", time.Since(restored).Milliseconds())
	if !strings.Contains(desktopRun(t, m, fork.Instance, fork.SandboxID, "windows"), "Mousepad") {
		t.Fatal("the restored desktop lost mousepad's window")
	}
	desktopScreenshot(t, m, fork.Instance, fork.SandboxID)
	desktopRun(t, m, fork.Instance, fork.SandboxID, "click", "640", "400")
	time.Sleep(3 * time.Second)
	t.Logf("restored desktop: VM resident %d MB", vmResidentMB(finst))
	for _, id := range []string{req.Instance, fork.Instance} {
		if err := m.Destroy(ctx, id); err != nil {
			t.Fatalf("destroy %s: %v", id, err)
		}
	}

	// §3b: the warm snapshot of the shared slot — booted, prewarmed with
	// Chromium, everything stopped, then captured — restores and runs the
	// session's own commands at the session's own screen.
	warm := req
	warm.Instance, warm.SandboxID = "sbx-desktop-warm", "sbx-desktop-warm"
	warm.StartCmd, warm.ReadyCmd, warm.ReadyTimeout = "", "", 0
	warmAt := time.Now()
	snap, info, err := m.CaptureSandboxSnapshot(ctx, SandboxCapture{Namespace: storagekey.SandboxSnapshotNamespace(account, image),
		CaptureID: uuid.NewString(), Wake: warm})
	if err != nil {
		t.Fatalf("warm capture: %v", err)
	}
	t.Logf("warm capture (boot, prewarm, snapshot): %d ms, %d MB memory", time.Since(warmAt).Milliseconds(), info.MemBytes>>20)
	session := req
	session.Instance, session.SandboxID, session.Snapshot = "sbx-desktop-session", "sbx-desktop-3", snap
	session.StartCmd = "gregale-desktop start --resolution 1024x768"
	restoredAt := time.Now()
	sinst, err := m.WakeSandbox(ctx, session)
	if err != nil {
		t.Fatalf("warm restore: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), session.Instance) })
	if sinst.Method != WakeRestore {
		t.Fatalf("the session cold-booted (%v); want a restore of the warm snapshot", sinst.Method)
	}
	t.Logf("warm restore to a ready desktop: %d ms", time.Since(restoredAt).Milliseconds())
	if got := strings.TrimSpace(desktopRun(t, m, session.Instance, session.SandboxID, "size")); got != "1024x768" {
		t.Fatalf("restored size = %q", got)
	}
	desktopScreenshot(t, m, session.Instance, session.SandboxID)
	launchedAt := time.Now()
	desktopRun(t, m, session.Instance, session.SandboxID, "launch", "chromium", "--", "about:blank")
	deadline = time.Now().Add(60 * time.Second)
	for !strings.Contains(desktopRun(t, m, session.Instance, session.SandboxID, "windows", "--app", "chromium"), "hromium") {
		if time.Now().After(deadline) {
			t.Fatal("chromium opened no window after the warm restore")
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("chromium window after the warm restore: %d ms", time.Since(launchedAt).Milliseconds())
	time.Sleep(3 * time.Second)
	t.Logf("warm-restored desktop: VM resident %d MB", vmResidentMB(sinst))
}
