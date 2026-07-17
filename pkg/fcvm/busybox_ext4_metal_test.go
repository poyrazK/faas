//go:build metal

// busybox_ext4_metal_test.go — fetch (or fall back to build) a minimal
// busybox ext4 image used by TestMetalHelloBoot (M0 acceptance).
//
// The M0 acceptance gate (spec §14) requires test-metal to run "a
// hello firecracker VM from CI kernel + busybox rootfs". We:
//
//  1. Try to download a pre-built `busybox.ext4` from a pinned fixture
//     URL (the github.com/onebox-faas/faas-fixtures release). This is
//     the fast, hermetic path.
//  2. Fall back to building one with `mkfs.ext4 -d` from a busybox
//     static binary — needed on the dev EX44 before fixture releases
//     exist.
//
// Both paths produce a single ~4 MB ext4 with /init -> /bin/busybox.
// The M0 test uses the SAME image for BasePath and LayerPath — see
// manager_metal_test.go for why this is the documented M0-only
// exception to the two-drive scheme (spec §4.6 is load-bearing but
// M0 doesn't have a real app-layer yet).

package fcvm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// busyboxFixtureURL is the canonical source for the pre-built image.
// Set FAAS_TEST_BUSYBOX_URL on the runner to override (CI runners may
// proxy through a local mirror).
const (
	defaultBusyboxURL = "https://github.com/onebox-faas/faas-fixtures/releases/download/v0.1.0/busybox.ext4"
	// Set this to the real SHA-256 the first time you download the
	// fixture. Until it's a real digest, the download still proceeds
	// but logs a loud WARN (so we don't false-positive a tampered
	// file but also don't pin theatre).
	busyboxSHA256 = "REPLACE_WITH_SHA256_OF_REAL_BUSYBOX_EXT4_AFTER_FIRST_DOWNLOAD"
)

// ensureBusyboxExt4 returns the path to a busybox ext4 image, creating
// one in dir if none exists. Idempotent on the same dir.
func ensureBusyboxExt4(t *testing.T, dir string) string {
	t.Helper()
	dst := filepath.Join(dir, "busybox.ext4")
	if _, err := os.Stat(dst); err == nil {
		return dst
	}

	if err := fetchBusyboxExt4(dst); err != nil {
		t.Logf("busybox fetch failed (%v); falling back to mkfs.ext4 -d build", err)
		if err := buildBusyboxExt4(dst); err != nil {
			t.Fatalf("busybox fixture fallback also failed: %v", err)
		}
	}
	return dst
}

// fetchBusyboxExt4 downloads + sha256-verifies the pinned busybox ext4.
func fetchBusyboxExt4(dst string) error {
	url := os.Getenv("FAAS_TEST_BUSYBOX_URL")
	if url == "" {
		url = defaultBusyboxURL
	}
	client := &http.Client{Timeout: 30 * time.Second}

	resp, err := client.Get(url) // #nosec G107 — pinned URL, not user input
	if err != nil {
		return fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch %s: status %d", url, resp.StatusCode)
	}

	// Stream to disk while hashing.
	tmp, err := os.CreateTemp(filepath.Dir(dst), "busybox.ext4.tmp.*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	h := sha256.New()
	if _, err := io.Copy(tmp, io.TeeReader(resp.Body, h)); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("download body: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	got := hex.EncodeToString(h.Sum(nil))
	if busyboxSHA256 == "REPLACE_WITH_SHA256_OF_REAL_BUSYBOX_EXT4_AFTER_FIRST_DOWNLOAD" {
		// Pin placeholder — proceed but emit a one-shot WARN so the
		// operator catches it on the first run.
		fmt.Fprintf(os.Stderr, "WARN: busybox SHA-256 pin unset; downloaded SHA-256=%s. "+
			"Edit busybox_ext4.go and set busyboxSHA256 = %q\n", got, got)
	} else if got != busyboxSHA256 {
		return fmt.Errorf("busybox SHA-256 mismatch: got %s, want %s", got, busyboxSHA256)
	}

	if err := os.Rename(tmp.Name(), dst); err != nil {
		// src/dst on same dir; if dst exists (rare race), remove first.
		_ = os.Remove(dst)
		if err := os.Rename(tmp.Name(), dst); err != nil {
			return err
		}
	}
	_ = os.Chmod(dst, 0o644)
	return nil
}

// buildBusyboxExt4 makes a tiny ext4 image from the busybox binary on
// PATH. Used when the pin URL is not reachable (e.g. air-gapped runners).
// Requires `mkfs.ext4` (e2fsprogs) on the runner.
func buildBusyboxExt4(dst string) error {
	bb, err := exec.LookPath("busybox")
	if err != nil {
		return fmt.Errorf("busybox not on PATH and no fixture URL reachable: %w", err)
	}

	work, err := os.MkdirTemp("", "busybox-skel-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	// minimal POSIX-ish tree
	for _, sub := range []string{"bin", "sbin", "dev", "sys", "proc", "etc"} {
		if err := os.MkdirAll(filepath.Join(work, sub), 0o755); err != nil {
			return err
		}
	}

	// /bin/busybox from PATH (executable), then symlink-bake /bin/sh + /init to
	// it (busybox auto-dispatches by argv[0]).
	if err := bbCopyFile(bb, filepath.Join(work, "bin/busybox")); err != nil {
		return err
	}
	// Absolute targets: a relative "bin/busybox" symlink placed at /bin/sh
	// resolves to /bin/bin/busybox and the guest panics with init ENOENT.
	for _, name := range []string{"bin/sh", "bin/ash", "init"} {
		if err := os.Symlink("/bin/busybox", filepath.Join(work, name)); err != nil {
			return err
		}
	}

	// /sbin/init is what the cold-boot cmdline execs (config.go: init=/sbin/init),
	// and the platform's readiness probe (vmm.go waitReady) polls the guest on
	// :8080. Make PID 1 a tiny script that serves an empty tree there so the TCP
	// accept succeeds — the kernel ip= autoconfig already brought eth0 up on
	// 10.0.0.2 (ADR-009), so httpd binding 0.0.0.0:8080 is reachable.
	initScript := "#!/bin/sh\nexec /bin/busybox httpd -f -p 8080 -h /\n"
	if err := os.WriteFile(filepath.Join(work, "sbin/init"), []byte(initScript), 0o755); err != nil {
		return err
	}

	// Pre-size the output file: modern e2fsprogs (≥1.47) refuses to create a
	// filesystem in a not-yet-existing file without an explicit block count.
	// 64 MiB is ample for a static busybox skeleton (~1 MB).
	if f, err := os.Create(dst); err != nil {
		return fmt.Errorf("create ext4 file: %w", err)
	} else if err := f.Truncate(64 << 20); err != nil {
		_ = f.Close()
		return fmt.Errorf("size ext4 file: %w", err)
	} else if err := f.Close(); err != nil {
		return fmt.Errorf("close ext4 file: %w", err)
	}

	// mkfs.ext4 -d <skel> takes a files-as-initramfs source. -O ^has_journal
	// makes it journal-less so it mounts as the read-only root (drive0 in the
	// two-drive scheme is is_read_only=true → kernel boots root=/dev/vda ro; an
	// ext4 journal can't be replayed on a ro mount and the guest panics).
	cmd := exec.Command("mkfs.ext4", "-O", "^has_journal", "-d", work, "-L", "faas-hello", "-F", dst)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// Don't ignore "I/O error" diagnostics from mkfs — surface them.
		return fmt.Errorf("mkfs.ext4: %w", err)
	}
	// drive1 (the app layer) is opened read-write by firecracker, which the jailer
	// runs as an unprivileged uid; make this throwaway image world-writable so that
	// uid can open it. Production per-app layers need per-instance ownership under
	// the jailer uid instead — an M1/M2 concern, out of scope for this M0 fixture.
	if err := os.Chmod(dst, 0o666); err != nil {
		return fmt.Errorf("chmod busybox ext4: %w", err)
	}
	return nil
}

// bbCopyFile copies src->dst as an executable (0755) — the busybox binary must
// be runnable for the kernel to exec it as init.
// Renamed from copyFile to avoid colliding with pkg/fcvm/vmm.go:341.
func bbCopyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o755)
}
