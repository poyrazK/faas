//go:build metal && linux && amd64

package fcvm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/overlaymetadata"
	"github.com/onebox-faas/faas/pkg/runtimescan"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/vmmdmount"
	"golang.org/x/sys/unix"
)

const runtimeScanParentNS = "GREGALE_TEST_RUNTIME_SCAN_PARENT_NAMESPACE"
const runtimeScanParentPID = "GREGALE_TEST_RUNTIME_SCAN_PARENT_PID"

func TestMetalApplicationStandardRuntimeScanMaterialization(t *testing.T) {
	if os.Getenv("FAAS_RUN_APPLICATION_STANDARD_RUNTIME_SCAN_TESTS") != "1" {
		t.Skip("explicit dedicated-host runtime scan acceptance opt-in required")
	}
	if os.Geteuid() != 0 {
		t.Fatal("dedicated native owner required")
	}
	if _, err := os.Stat("/etc/faas/builder-acceptance-host"); err != nil {
		t.Fatal("dedicated acceptance-host marker missing")
	}
	if info, err := os.Stat("/dev/kvm"); err != nil || info.Mode()&os.ModeCharDevice == 0 {
		t.Fatal("native Linux amd64 KVM host required")
	}
	ns, err := os.Readlink("/proc/self/ns/mnt")
	if err != nil {
		t.Fatal(err)
	}
	if parent := os.Getenv(runtimeScanParentNS); parent != "" {
		actual, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/mnt", os.Getppid()))
		if err != nil || actual != parent || ns == parent || os.Getenv(runtimeScanParentPID) != strconv.Itoa(os.Getppid()) {
			t.Fatal("private namespace handshake refused", err)
		}
		exerciseNativeRuntimeScan(t)
		return
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "unshare", "--mount", "--propagation", "private", "--", binary, "-test.run=^TestMetalApplicationStandardRuntimeScanMaterialization$", "-test.v", "-test.timeout=160s")
	cmd.Env = append(os.Environ(), runtimeScanParentNS+"="+ns, runtimeScanParentPID+"="+strconv.Itoa(os.Getpid()))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native runtime view acceptance: %v\n%s", err, out)
	}
}

func nativeRuntimeScanCommand(t *testing.T, command string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, command, args...).CombinedOutput(); err != nil {
		t.Fatalf("fixture %s: %v\n%s", command, err, out)
	}
}

func exerciseNativeRuntimeScan(t *testing.T) {
	for _, path := range []string{vmmdmount.MountRoot, vmmdmount.OverlayStagingRoot} {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || resolved != path {
			t.Fatal("bootstrap native staging roots first", err)
		}
		private := t.TempDir()
		if err := os.Chmod(private, 0755); err != nil {
			t.Fatal(err)
		}
		if path == vmmdmount.OverlayStagingRoot {
			if err := os.Chown(private, 0, 65534); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(private, 0770); err != nil {
				t.Fatal(err)
			}
		}
		nativeRuntimeScanCommand(t, "mount", "--bind", private, path)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if out, err := exec.CommandContext(ctx, "umount", "--", path).CombinedOutput(); err != nil {
				t.Errorf("release private bind: %v %s", err, out)
			}
		})
	}
	for _, opaque := range []bool{false, true} {
		t.Run(fmt.Sprintf("opaque=%v", opaque), func(t *testing.T) { nativeRuntimeScanFixture(t, opaque) })
	}
}

func nativeRuntimeScanFixture(t *testing.T, opaque bool) {
	root := t.TempDir()
	be, err := storage.NewLocalStorageBackend(filepath.Join(root, "store"))
	if err != nil {
		t.Fatal(err)
	}
	request := runtimescan.Request{Version: 1, InputHash: strings.Repeat("a", 64)}
	images := map[string][32]byte{}
	for _, kind := range []string{"base-image", "app-layer", "sidecar-layer"} {
		tree := filepath.Join(root, kind)
		if err := os.Mkdir(tree, 0755); err != nil {
			t.Fatal(err)
		}
		workload, key, guestRoot := "", "base/native.ext4", tree
		if kind != "base-image" {
			guestRoot = filepath.Join(tree, "upper")
			if err := os.Mkdir(guestRoot, 0755); err != nil {
				t.Fatal(err)
			}
			key = "apps/main.ext4"
		}
		if kind == "sidecar-layer" {
			workload, key = "metrics", "sidecar/metrics.ext4"
		}
		if err := os.WriteFile(filepath.Join(guestRoot, "own"), []byte(kind), 0644); err != nil {
			t.Fatal(err)
		}
		if kind == "base-image" {
			if err := os.WriteFile(filepath.Join(tree, "base-only"), []byte("lower"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(tree, "base-visible"), []byte("lower"), 0644); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.Symlink("/own", filepath.Join(guestRoot, "absolute")); err != nil {
				t.Fatal(err)
			}
		}
		if kind == "app-layer" {
			if err := unix.Mknod(filepath.Join(guestRoot, "base-only"), unix.S_IFCHR|0600, int(unix.Mkdev(0, 0))); err != nil {
				t.Fatal(err)
			}
			if opaque {
				if err := unix.Setxattr(guestRoot, overlaymetadata.RootOpaqueXattr, []byte("y"), 0); err != nil {
					t.Fatal(err)
				}
			}
		}
		image := filepath.Join(root, kind+".ext4")
		nativeRuntimeScanCommand(t, "mkfs.ext4", "-q", "-F", "-O", "^has_journal", "-d", tree, image, "16M")
		body, err := os.ReadFile(image)
		if err != nil {
			t.Fatal(err)
		}
		images[image] = sha256.Sum256(body)
		if err := be.Put(t.Context(), key, bytes.NewReader(body)); err != nil {
			t.Fatal(err)
		}
		request.Sources = append(request.Sources, runtimeSourceFixture(kind, workload, key, body))
	}
	request.TargetDir, err = os.MkdirTemp(vmmdmount.OverlayStagingRoot, vmmdmount.RuntimeScanTargetPrefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(request.TargetDir) })
	if err := os.Chown(request.TargetDir, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	m := &Manager{storage: be, parentMounts: vmmdmount.NewRegistry(4)}
	receipt, err := m.MaterializeRuntimeScan(t.Context(), request)
	if err != nil || receipt.Check(request) != nil {
		t.Fatal("native runtime materialization refused", err)
	}
	if _, err := os.Lstat(filepath.Join(request.TargetDir, "main", "base-only")); !os.IsNotExist(err) {
		t.Fatal("whiteout victim visible", err)
	}
	_, err = os.Stat(filepath.Join(request.TargetDir, "main", "base-visible"))
	if opaque && !os.IsNotExist(err) || !opaque && err != nil {
		t.Fatal("root opacity differs", err)
	}
	for _, name := range []string{"main", "sidecar-metrics"} {
		want := "app-layer"
		if name != "main" {
			want = "sidecar-layer"
		}
		if body, err := os.ReadFile(filepath.Join(request.TargetDir, name, "absolute")); err != nil || string(body) != want {
			t.Fatal("projection crossed independent guest roots", err)
		}
	}
	for image, want := range images {
		body, err := os.ReadFile(image)
		if err != nil || sha256.Sum256(body) != want {
			t.Fatal("source artifact mutated", err)
		}
	}
	if entries, err := os.ReadDir(vmmdmount.MountRoot); err != nil || len(entries) != 0 {
		t.Fatal("native mount/source leaked", err)
	}
	cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c", "test -r \"$1/main/own\" && test -r \"$1/sidecar-metrics/own\" && rm -r -- \"$1\"", "fixture", request.TargetDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("unprivileged scanner could not read/clean projection: %v %s", err, out)
	}
}
