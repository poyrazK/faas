//go:build metal

// adr: 20261027090013 step 10 part two — a seller-image session boots the
// customer's image as a full-rootfs drive1 in place of the platform sandbox
// layer. vmmd stages /etc/faas/sandbox.json on the VM's private copy, so
// guest-init serves the session from the image's root, and the cached image
// itself is never changed.
package fcvm

import (
	"context"
	"crypto/sha256"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sandboxproto"
	"github.com/onebox-faas/faas/pkg/wire"
)

// sellerImageDrive packs an unpacked image root as imaged publishes a job
// image: the full-rootfs marker written by the platform, no sandbox marker.
func sellerImageDrive(t *testing.T, root string) string {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, "etc", "faas", "sandbox.json")); err == nil {
		t.Fatal("the image already carries a sandbox marker")
	}
	marker := filepath.Join(root, strings.TrimPrefix(api.FullRootfsMarkerPath, "/"))
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(api.FullRootfsMarkerValue), 0o444); err != nil {
		t.Fatal(err)
	}
	drive := filepath.Join(t.TempDir(), "seller-image.ext4")
	if out, err := exec.Command("truncate", "-s", "512M", drive).CombinedOutput(); err != nil {
		t.Fatalf("truncate: %v %s", err, out)
	}
	if out, err := exec.Command("mkfs.ext4", "-q", "-O", "^has_journal", "-d", root, "-L", "seller-image", "-F", drive).CombinedOutput(); err != nil {
		t.Fatalf("mkfs.ext4: %v %s", err, out)
	}
	return drive
}

func fileSHA256(t *testing.T, path string) [32]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// userExec runs script as the session's default user.
func userExec(t *testing.T, m *Manager, instance, sandboxID, script string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	conn, err := m.DialSandboxSession(ctx, instance, sandboxID)
	if err != nil {
		t.Fatalf("dial sandbox: %v", err)
	}
	var out strings.Builder
	var result sandboxproto.ExecResult
	req := sandboxproto.Request{Op: sandboxproto.OpExec, Argv: []string{"/bin/sh", "-c", script}, TimeoutMillis: 60_000, MaxOutputBytes: 1 << 20}
	if err := sandboxproto.Do(ctx, conn, req, func(_ uint32, body []byte) error {
		out.Write(body)
		return nil
	}, &result); err != nil {
		t.Fatalf("exec %q: %v", script, err)
	}
	return out.String(), result.ExitCode
}

func TestMetalSandboxFromASellerImage(t *testing.T) {
	kernel := os.Getenv("FAAS_TEST_KERNEL")
	base := os.Getenv("FAAS_TEST_AGENT_RUN_BASE")
	imageRoot := os.Getenv("FAAS_TEST_SANDBOX_IMAGE_ROOT")
	if kernel == "" || base == "" || imageRoot == "" {
		t.Skip("set FAAS_TEST_KERNEL, FAAS_TEST_AGENT_RUN_BASE and FAAS_TEST_SANDBOX_IMAGE_ROOT (an unpacked Debian image; the agent base is Alpine)")
	}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	image := sellerImageDrive(t, imageRoot)
	before := fileSHA256(t, image)
	m := NewManager(wire.ExecRunner{}, newMetalVMM(t, 120*time.Second), Paths{Kernel: kernel}, os.Getenv("FAAS_TEST_FC_VERSION"), slog.Default(), nil)
	const instance, sandboxID = "sbx-image", "sbx-image-1"
	ctx := context.Background()
	// LayerKey is the platform layer a preset session would boot; ImageKey
	// replaces it.
	inst, err := m.WakeSandbox(ctx, SandboxWakeRequest{Instance: instance, SandboxID: sandboxID, AccountID: "acct-sandbox", Plan: api.PlanPro,
		BaseKey: base, LayerKey: sandboxRootLayer(t), ImageKey: image, VcpuCount: 1, MemSizeMiB: 1024, CPUMillicores: 1000,
		Network: api.SandboxNetworkNone})
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })
	if !inst.Lease.SandboxImage {
		t.Fatal("the lease does not mark a seller-image session")
	}

	t.Run("runs from the image's root", func(t *testing.T) {
		out, code := userExec(t, m, instance, sandboxID, ". /etc/os-release; echo id=$ID; cat /etc/faas/sandbox.json; echo; id -u")
		if code != 0 {
			t.Fatalf("exit %d: %s", code, out)
		}
		for _, want := range []string{"id=debian\n", `{"kind":"sandbox","version":1}`, "\n1000\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q in:\n%s", want, out)
			}
		}
	})

	t.Run("has a writable workspace", func(t *testing.T) {
		out, code := userExec(t, m, instance, sandboxID, "cd /workspace && echo kept > f && cat f && pwd")
		if code != 0 || out != "kept\n/workspace\n" {
			t.Fatalf("workspace = %q exit %d", out, code)
		}
	})

	t.Run("root works inside the image", func(t *testing.T) {
		if out, code := rootExec(t, m, instance, sandboxID, "id -u && touch /usr/local/root-was-here"); code != 0 || strings.TrimSpace(out) != "0" {
			t.Fatalf("root = %q exit %d", out, code)
		}
	})

	if err := m.Destroy(ctx, instance); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if after := fileSHA256(t, image); after != before {
		t.Fatal("the cached image changed: staging wrote to it instead of the VM's private copy")
	}
}
