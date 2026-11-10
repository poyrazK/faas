//go:build metal

// adr: 20261027090013 §2, §12 — a session VM is captured once guest-init is
// ready, before any session data or template command; restores of one
// snapshot differ in RNG stream, and a template session restored from its
// warm snapshot runs its start command after the restore, once.
package fcvm

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestMetalSandboxWarmSnapshot(t *testing.T) {
	kernel := os.Getenv("FAAS_TEST_KERNEL")
	base := os.Getenv("FAAS_TEST_AGENT_RUN_BASE")
	imageRoot := os.Getenv("FAAS_TEST_SANDBOX_IMAGE_ROOT")
	if kernel == "" || base == "" || imageRoot == "" {
		t.Skip("set FAAS_TEST_KERNEL, FAAS_TEST_AGENT_RUN_BASE and FAAS_TEST_SANDBOX_IMAGE_ROOT")
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
	m := NewManager(wire.ExecRunner{}, newMetalVMM(t, 120*time.Second).WithStorage(store), Paths{Kernel: kernel}, os.Getenv("FAAS_TEST_FC_VERSION"), slog.Default(), nil)
	ctx := context.Background()
	layer := sandboxRootLayer(t)
	machine := func(instance, sandboxID string) SandboxWakeRequest {
		return SandboxWakeRequest{Instance: instance, SandboxID: sandboxID, AccountID: "acct-sandbox", Plan: api.PlanPro,
			BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: 1024, CPUMillicores: 1000, Network: api.SandboxNetworkNone}
	}
	restore := func(t *testing.T, req SandboxWakeRequest) *Instance {
		t.Helper()
		begun := time.Now()
		inst, err := m.WakeSandbox(ctx, req)
		if err != nil {
			t.Fatalf("wake %s: %v", req.Instance, err)
		}
		t.Cleanup(func() { _ = m.Destroy(context.Background(), req.Instance) })
		if inst.Method != WakeRestore {
			t.Fatalf("%s woke by %s, want a restore: %q (%s)", req.Instance, inst.Method, inst.RestoreError, inst.RestoreFallbackReason)
		}
		t.Logf("%s: restored and ready in %d ms", req.Instance, time.Since(begun).Milliseconds())
		return inst
	}

	t.Run("preset restores differ in RNG and start clean", func(t *testing.T) {
		snap, info, err := m.CaptureSandboxSnapshot(ctx, SandboxCapture{Namespace: "sandbox-preset-metal", CaptureID: "capture-1",
			Wake: machine("sbx-capture", "sbx-capture-1")})
		if err != nil {
			t.Fatalf("capture: %v", err)
		}
		if info.MemBytes == 0 || !snap.Networkless || m.LiveCount() != 0 {
			t.Fatalf("capture %+v networkless=%v live %d", info, snap.Networkless, m.LiveCount())
		}
		var rands []string
		for _, id := range []string{"sbx-restore-1", "sbx-restore-2"} {
			req := machine(id, id+"-session")
			req.Snapshot = snap
			restore(t, req)
			out, code := userExec(t, m, id, id+"-session", `ls -A /workspace | wc -l; echo "rand=$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n')"; echo "now=$(date +%s)"`)
			if code != 0 || !strings.HasPrefix(out, "0\n") {
				t.Fatalf("%s: %q exit %d", id, out, code)
			}
			_, rest, _ := strings.Cut(out, "rand=")
			rand, nowLine, _ := strings.Cut(rest, "\n")
			rands = append(rands, rand)
			var now int64
			if _, err := fmt.Sscan(strings.TrimPrefix(strings.TrimSpace(nowLine), "now="), &now); err != nil || time.Since(time.Unix(now, 0)).Abs() > 10*time.Second {
				t.Fatalf("%s: clock %q not stepped after restore", id, nowLine)
			}
		}
		if len(rands) != 2 || rands[0] == "" || rands[0] == rands[1] {
			t.Fatalf("restores share an RNG stream: %v", rands)
		}
		for _, id := range []string{"sbx-restore-1", "sbx-restore-2"} {
			if err := m.Destroy(ctx, id); err != nil {
				t.Fatal(err)
			}
		}
	})

	t.Run("a template runs its start command after the restore", func(t *testing.T) {
		image := sellerImageDrive(t, imageRoot)
		capture := machine("sbx-tpl-capture", "sbx-tpl-capture-1")
		capture.ImageKey = image
		snap, _, err := m.CaptureSandboxSnapshot(ctx, SandboxCapture{Namespace: "sandbox-template-metal", CaptureID: "capture-1", Wake: capture})
		if err != nil {
			t.Fatalf("capture: %v", err)
		}
		req := machine("sbx-tpl-restore", "sbx-tpl-session")
		req.ImageKey, req.Snapshot = image, snap
		req.StartCmd, req.ReadyCmd, req.ReadyTimeout = "echo starting; sleep 2; echo up > /tmp/up; exec sleep 3600", "test -e /tmp/up", 30*time.Second
		begun := time.Now()
		restore(t, req)
		if took := time.Since(begun); took < 2*time.Second {
			t.Fatalf("served after %v, before the ready command could pass", took)
		}
		out, code := userExec(t, m, req.Instance, req.SandboxID, `. /etc/os-release; echo id=$ID; cat /tmp/up; cat /tmp/gregale-start.log; n=0; for p in /proc/[0-9]*; do tr '\0' ' ' < $p/cmdline 2>/dev/null | grep -q '^sleep 3600' && n=$((n+1)); done; echo "servers=$n"`)
		for _, want := range []string{"id=debian", "up\n", "starting", "servers=1"} {
			if code != 0 || !strings.Contains(out, want) {
				t.Fatalf("missing %q (exit %d):\n%s", want, code, out)
			}
		}
		if err := m.Destroy(ctx, req.Instance); err != nil {
			t.Fatal(err)
		}
	})
}
