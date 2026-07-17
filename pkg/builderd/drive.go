//go:build linux

// Package builderd — drive1 preparation for ephemeral builder VMs.
//
// CreateBuildDrive1 materialises the per-VM ext4 that the builder VM boots
// with. It writes an 8 GiB image, formats it ext4, mounts it loopback rw,
// writes /etc/faas/build.json (the BuildManifest guest-init reads to know
// it's a build VM), and unmounts. The same binary runs in app VMs with a
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
	"encoding/json"
	"fmt"
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
// BuildManifest at /etc/faas/build.json. Idempotent on host filesystem
// blocks — overwrites dest. Returns immediately on permission errors so
// unit tests that lack loopback rights can skip the path via os.Geteuid.
func CreateBuildDrive1(ctx context.Context, dest string, m api.BuildManifest) error {
	if dest == "" {
		return fmt.Errorf("builderd: empty drive1 path")
	}
	if m.BuildID == "" {
		return fmt.Errorf("builderd: empty build_id")
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

	// 3. Loopback-mount rw, write the manifest, unmount.
	mp, err := os.MkdirTemp("", "faas-buildmnt-")
	if err != nil {
		return fmt.Errorf("builderd: mktemp mount: %w", err)
	}
	defer os.RemoveAll(mp)

	if out, err := exec.CommandContext(ctx, "mount", "-o", "loop,rw", dest, mp).CombinedOutput(); err != nil {
		return fmt.Errorf("builderd: loopback mount: %w (%s)", err, string(out))
	}
	defer func() { _ = exec.Command("umount", mp).Run() }()

	if err := writeBuildManifest(mp, m); err != nil {
		return fmt.Errorf("builderd: write manifest: %w", err)
	}
	return nil
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
