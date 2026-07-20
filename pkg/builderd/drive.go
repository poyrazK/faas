//go:build linux

// Package builderd — drive1 preparation for ephemeral builder VMs.
//
// CreateBuildDrive1 materialises the per-VM ext4 that the builder VM boots
// with. It writes an 8 GiB image, formats it ext4, mounts it loopback rw,
// writes /etc/faas/build.json (the BuildManifest guest-init reads to know
// it's a build VM), copies the customer source tarball in at /build/src.tar
// (issue #54), and unmounts. The same binary runs in app VMs with a
// different manifest (api.AppManifest); guest-init branches on which file
// exists at boot.
//
// vmmd is the only component that touches block devices (spec §11);
// CreateBuildDrive1 runs under builderd, which uses losetup+mount as a
// host-side file operation, not inside any VM. It runs as the host's
// builderd user (uid ≥ 20000), and mount/umount via sudo is the only path
// privileged enough to loopback-mount.

package builderd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
)

// BuildDriveSizeBytes is the drive1 image size for builder VMs (M6). 8 GiB
// matches spec §4.5's "8 GB scratch" budget — plenty for a Node/Python build
// dependency cache, with room to spare. The produced app layer (in
// /build/out) is typically < 500 MB; 8 GiB is just the unconstrained scratch.
const BuildDriveSizeBytes = 8 << 30

// mkfs utility + label used for the build drive1 image.
const (
	buildMkfs  = "mkfs.ext4"
	buildLabel = "faas-build"
)

// CreateBuildDrive1 writes an 8 GiB ext4 image at dest containing the
// BuildManifest at /etc/faas/build.json and (when sourcePath is non-empty)
// the customer's source tarball copied to /build/src.tar. Idempotent on
// host filesystem blocks — overwrites dest. Returns immediately on permission
// errors so unit tests that lack loopback rights can skip the path via
// os.Geteuid.
//
// sourcePath is required for any builder VM that runs a real build
// (tarball/dockerfile deploys). image: deploys never reach builderd at all,
// so passing "" here is a programmer error and is rejected explicitly — an
// empty drive1 would silently boot guest-init into `tar -xaf /build/src.tar`
// against a missing file and produce a no-op build.
func CreateBuildDrive1(ctx context.Context, dest string, m api.BuildManifest, sourcePath string) error {
	if dest == "" {
		return fmt.Errorf("builderd: empty drive1 path")
	}
	if m.BuildID == "" {
		return fmt.Errorf("builderd: empty build_id")
	}
	if sourcePath == "" {
		return fmt.Errorf("builderd: empty source_path for build %s (image deploys must not reach builderd)", m.BuildID)
	}
	srcSum, err := fileSHA256(sourcePath)
	if err != nil {
		return fmt.Errorf("builderd: stat source %s: %w", sourcePath, err)
	}

	// 1. Truncate the host file to BuildDriveSizeBytes.
	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("builderd: create drive1: %w", err)
	}
	if err := f.Truncate(BuildDriveSizeBytes); err != nil {
		_ = f.Close()
		return fmt.Errorf("builderd: truncate drive1: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("builderd: close drive1: %w", err)
	}

	// 2. mkfs.ext4 -L faas-build -F dest. mkfs is idempotent on -F.
	if out, err := exec.CommandContext(ctx, buildMkfs, "-L", buildLabel, "-F", dest).CombinedOutput(); err != nil {
		return fmt.Errorf("builderd: mkfs: %w (%s)", err, string(out))
	}

	// 3. Loopback-mount rw, write the manifest + tarball, unmount.
	mp, err := os.MkdirTemp("", "faas-buildmnt-")
	if err != nil {
		return fmt.Errorf("builderd: mktemp mount: %w", err)
	}
	defer func() { _ = os.RemoveAll(mp) }()

	if out, err := exec.CommandContext(ctx, "mount", "-o", "loop,rw", dest, mp).CombinedOutput(); err != nil {
		return fmt.Errorf("builderd: loopback mount: %w (%s)", err, string(out))
	}
	defer func() { _ = exec.Command("umount", mp).Run() }()

	if err := writeBuildManifest(mp, m); err != nil {
		return fmt.Errorf("builderd: write manifest: %w", err)
	}
	if err := copySourceTarball(mp, sourcePath); err != nil {
		return fmt.Errorf("builderd: copy source: %w", err)
	}
	// Sanity: confirm the bytes that landed on disk match the host source.
	// Catches a torn copy / quota-hit / ENOSPC that would otherwise surface
	// as a silent truncated tarball inside the VM.
	gotSum, err := fileSHA256(filepath.Join(mp, "build", "src.tar"))
	if err != nil {
		return fmt.Errorf("builderd: re-stat staged tarball: %w", err)
	}
	if gotSum != srcSum {
		return fmt.Errorf("builderd: staged tarball sha256 mismatch: got %s, want %s", gotSum, srcSum)
	}
	return nil
}

// copySourceTarball copies the host source tarball at sourcePath into the
// mounted drive1 at /build/src.tar. Called from inside the same mount loop
// that writeBuildManifest runs in — no extra umount cycle.
func copySourceTarball(mountPoint, sourcePath string) error {
	buildDir := filepath.Join(mountPoint, "build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return fmt.Errorf("mkdir build: %w", err)
	}
	in, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(filepath.Join(buildDir, "src.tar"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create dst: %w", err)
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("sync dst: %w", err)
	}
	return nil
}

// fileSHA256 returns the hex sha256 of path, hex-encoded. Used to verify
// the host source and the staged copy on drive1 match byte-for-byte.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// writeBuildManifest materialises /etc/faas/build.json inside an already-
// mounted build drive1. Split out so tests can drive it without a loopback
// mount (using a plain tmp dir as mount surrogate).
func writeBuildManifest(mountPoint string, m api.BuildManifest) error {
	dir := filepath.Join(mountPoint, "etc", "faas")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	target := filepath.Join(dir, "build.json")
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	return nil
}
