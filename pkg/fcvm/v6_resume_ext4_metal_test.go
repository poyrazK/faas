//go:build metal

// v6_resume_ext4_metal_test.go — build (or fetch) the V6 acceptance rootfs.
//
// Spec §14 V6 / §11: two guest restores from one snapshot must yield distinct
// /proc/sys/kernel/random/uuid (and the resume hook is the only thing that
// guarantees it). The unit tests in vmm_test.go cover the host-side wire
// (TriggerResumeHook dial, ack, ordering); this file builds the matching
// GUEST-side artefact — an ext4 that runs the real faas-guest-init as PID 1
// (so the AF_VSOCK listener comes up) and a tiny busybox entrypoint that
// captures a fresh /proc/sys/kernel/random/uuid to /etc/faas/uuid.txt and
// then busybox httpd-serves / on :8080 so waitReady can probe it.
//
// The helper mirrors ensureBusyboxExt4: try a pinned fixture first, fall
// back to mkfs.ext4 -d from the local repo checkout. We build guest-init
// from this worktree so the listener + resume wire format are guaranteed to
// match the host (pkg/fcvm/vmm.go::resumeHookMsgResume, ADR-022).

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

// v6FixtureBaseURL is the canonical source for the pre-built V6 image. Set
// FAAS_TEST_V6_URL on the runner to override (CI runners may proxy through
// a local mirror). The image contains a static guest-init + busybox and is
// content-addressed by the SHA-256 below.
const (
	defaultV6BaseURL  = "https://github.com/onebox-faas/faas-fixtures/releases/download/v0.1.0/v6-base.ext4"
	defaultV6LayerURL = "https://github.com/onebox-faas/faas-fixtures/releases/download/v0.1.0/v6-layer.ext4"
	// v6BaseSHA256 / v6LayerSHA256 are populated on first download by
	// fetchV6Ext4 (loud one-shot WARN if the placeholder is still set). The
	// same anti-pin-theatre pattern as busybox_ext4_metal_test.go.
	v6BaseSHA256  = "REPLACE_WITH_SHA256_OF_REAL_V6_BASE_EXT4_AFTER_FIRST_DOWNLOAD"
	v6LayerSHA256 = "REPLACE_WITH_SHA256_OF_REAL_V6_LAYER_EXT4_AFTER_FIRST_DOWNLOAD"
)

// ensureV6Ext4 returns (basePath, layerPath) — the base carries guest-init
// + busybox, the layer is the writable empty upper the guest runs its init
// script in (and where /etc/faas/uuid.txt is created).
//
// Resolution order (mirrors ensureBusyboxExt4's philosophy):
//  1. Honour FAAS_TEST_V6_BASE/LAYER env vars if set (Lima provisioner
//     pre-stages these at /srv/fc/base/v6-base.ext4 etc.). This keeps
//     the metal loop fast: the provisioner builds the rootfs once per
//     `limactl start`, every subsequent run reuses it.
//  2. Otherwise build per-test in t.TempDir from the local checkout
//     (buildV6BaseExt4 calls `go build ./guest/init` against repoRoot).
func ensureV6Ext4(t *testing.T, dir, repoRoot string) (basePath, layerPath string) {
	t.Helper()
	if envBase := os.Getenv("FAAS_TEST_V6_BASE"); envBase != "" {
		if envLayer := os.Getenv("FAAS_TEST_V6_LAYER"); envLayer != "" {
			return envBase, envLayer
		}
	}

	base := filepath.Join(dir, "v6-base.ext4")
	layer := filepath.Join(dir, "v6-layer.ext4")

	if _, err := os.Stat(base); err != nil {
		if err := fetchOrBuildV6Ext4(base, repoRoot, true); err != nil {
			t.Fatalf("v6 base rootfs: %v", err)
		}
	}
	if _, err := os.Stat(layer); err != nil {
		if err := fetchOrBuildV6Ext4(layer, repoRoot, false); err != nil {
			t.Fatalf("v6 layer rootfs: %v", err)
		}
	}
	return base, layer
}

// fetchOrBuildV6Ext4 fetches the pinned fixture (URL or FAAS_TEST_V6_*_URL
// override), verifies SHA-256 if pinned, and on any failure falls back to
// building one in place from the local checkout. base=true builds the
// guest-init+busybox image; base=false builds an empty 16 MiB writable
// ext4 (the layer upper). The layer fallback ALWAYS builds (no fixture
// is published for the empty writable).
func fetchOrBuildV6Ext4(dst, repoRoot string, base bool) error {
	if !base {
		// Layer is always built — fixture URL is a placeholder for symmetry
		// with the base path; never expected to resolve.
		return buildV6LayerExt4(dst)
	}
	url := defaultV6BaseURL
	sha := v6BaseSHA256
	if v := os.Getenv("FAAS_TEST_V6_BASE_URL"); v != "" {
		url = v
	}
	if err := fetchV6Ext4(url, sha, dst); err == nil {
		return nil
	} else {
		return buildV6BaseExt4(dst, repoRoot)
	}
}

// fetchV6Ext4 downloads + sha256-verifies a pinned fixture.
func fetchV6Ext4(url, wantSHA, dst string) error {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url) // #nosec G107 — pinned URL, not user input
	if err != nil {
		return fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch %s: status %d", url, resp.StatusCode)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dst), "v6.ext4.tmp.*")
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
	if wantSHA == "REPLACE_WITH_SHA256_OF_REAL_V6_BASE_EXT4_AFTER_FIRST_DOWNLOAD" ||
		wantSHA == "REPLACE_WITH_SHA256_OF_REAL_V6_LAYER_EXT4_AFTER_FIRST_DOWNLOAD" {
		fmt.Fprintf(os.Stderr, "WARN: v6 fixture SHA-256 pin unset; downloaded SHA-256=%s. "+
			"Edit v6_resume_ext4_metal_test.go and set wantSHA = %q\n", got, got)
	} else if got != wantSHA {
		return fmt.Errorf("v6 fixture SHA-256 mismatch: got %s, want %s", got, wantSHA)
	}

	if err := os.Rename(tmp.Name(), dst); err != nil {
		_ = os.Remove(dst)
		if err := os.Rename(tmp.Name(), dst); err != nil {
			return err
		}
	}
	return os.Chmod(dst, 0o644)
}

// buildV6BaseExt4 assembles the V6 base rootfs from the local checkout:
//
//	/bin/busybox                  from PATH (busybox-static, installed in the Lima guest)
//	/sbin/init  -> faas-guest-init built from $repoRoot/guest/init
//	/etc/faas/app.json            entrypoint: write uuid.txt, exec busybox httpd
//
// guest-init is what binds AF_VSOCK and runs RunResumeHook on restore
// (guest/init/listen_resume_linux.go). Building it here — instead of
// shipping a static fixture — guarantees the listener's wire format
// matches the host's expectation (ADR-022). The build runs in the test
// process's working directory, so any host that has `go` on PATH and the
// repo checkout can build it.
func buildV6BaseExt4(dst, repoRoot string) error {
	bb, err := exec.LookPath("busybox")
	if err != nil {
		return fmt.Errorf("busybox not on PATH and no fixture URL reachable: %w", err)
	}
	guestInitSrc := filepath.Join(repoRoot, "guest", "init")
	if _, err := os.Stat(filepath.Join(guestInitSrc, "main_linux.go")); err != nil {
		return fmt.Errorf("guest-init source not found at %s: %w", guestInitSrc, err)
	}

	work, err := os.MkdirTemp("", "v6-skel-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()

	for _, sub := range []string{"bin", "sbin", "dev", "sys", "proc", "etc", "etc/faas", "tmp"} {
		if err := os.MkdirAll(filepath.Join(work, sub), 0o755); err != nil {
			return err
		}
	}

	// Build guest-init. CGO_ENABLED=0 keeps it a pure-Go static binary so
	// the guest can exec it without an interpreter. We use `go build` against
	// the source dir, output to <work>/sbin/init directly.
	bin := filepath.Join(work, "sbin", "init")
	cmd := exec.Command("go", "build", "-trimpath", "-tags", "linux", "-o", bin, ".")
	cmd.Dir = guestInitSrc
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build guest-init: %w", err)
	}

	if err := bbCopyFile(bb, filepath.Join(work, "bin/busybox")); err != nil {
		return err
	}
	for _, name := range []string{"bin/sh", "bin/ash", "bin/cat"} {
		if err := os.Symlink("/bin/busybox", filepath.Join(work, name)); err != nil {
			return err
		}
	}

	// /etc/faas/app.json — the entrypoint captures a fresh uuid via a
	// pre-start hook in /usr/local/bin/faas-write-uuid, then execs busybox
	// httpd. We avoid the `sh -c "..."` wrapper because guest-init's
	// runAppWithSecrets (see guest/init/main_linux.go::runAppWithSecrets)
	// does NOT exec via a shell — it uses exec.Command directly with
	// argv[0]=argv[0], argv[1..]=argv[1..]. sh works on most distros but
	// the /bin/sh symlink on a busybox-only rootfs silently diverges across
	// busybox versions. Splitting into a tiny script + a direct httpd
	// invocation is hermetic.
	uuidShim := "#!/bin/sh\ncat /proc/sys/kernel/random/uuid > /etc/faas/uuid.txt\nexec /bin/busybox httpd -f -p 8080 -h /\n"
	if err := os.WriteFile(filepath.Join(work, "usr", "local", "bin", "faas-write-uuid"), []byte(uuidShim), 0o755); err != nil {
		return err
	}
	appJSON := `{"entrypoint":["/usr/local/bin/faas-write-uuid"],"port":8080}` + "\n"
	if err := os.WriteFile(filepath.Join(work, "etc/faas/app.json"), []byte(appJSON), 0o644); err != nil {
		return err
	}

	// Pre-size and mkfs. 64 MiB mirrors the busybox helper — ample for
	// guest-init (~10 MB static) + busybox (~1 MB).
	if f, err := os.Create(dst); err != nil {
		return fmt.Errorf("create ext4 file: %w", err)
	} else if err := f.Truncate(64 << 20); err != nil {
		_ = f.Close()
		return fmt.Errorf("size ext4 file: %w", err)
	} else if err := f.Close(); err != nil {
		return fmt.Errorf("close ext4 file: %w", err)
	}
	mkfs := exec.Command("mkfs.ext4", "-O", "^has_journal", "-d", work, "-L", "faas-v6", "-F", dst)
	mkfs.Stderr = os.Stderr
	if err := mkfs.Run(); err != nil {
		return fmt.Errorf("mkfs.ext4 base: %w", err)
	}
	return os.Chmod(dst, 0o644)
}

// buildV6LayerExt4 builds a small writable ext4 to use as drive1. The guest
// uses it as the overlay upper (so /etc/faas/uuid.txt is writable across
// re-runs). 16 MiB is plenty — we only ever write one short UUID line.
func buildV6LayerExt4(dst string) error {
	work, err := os.MkdirTemp("", "v6-layer-skel-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	for _, sub := range []string{"etc", "etc/faas", "tmp"} {
		if err := os.MkdirAll(filepath.Join(work, sub), 0o755); err != nil {
			return err
		}
	}
	if f, err := os.Create(dst); err != nil {
		return fmt.Errorf("create ext4 file: %w", err)
	} else if err := f.Truncate(16 << 20); err != nil {
		_ = f.Close()
		return fmt.Errorf("size ext4 file: %w", err)
	} else if err := f.Close(); err != nil {
		return fmt.Errorf("close ext4 file: %w", err)
	}
	mkfs := exec.Command("mkfs.ext4", "-O", "^has_journal", "-d", work, "-L", "faas-v6-layer", "-F", dst)
	mkfs.Stderr = os.Stderr
	if err := mkfs.Run(); err != nil {
		return fmt.Errorf("mkfs.ext4 layer: %w", err)
	}
	return os.Chmod(dst, 0o644)
}
