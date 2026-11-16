package main

// adr: 20261030122204

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"github.com/onebox-faas/faas/pkg/api"
)

// buildkitSeedDrive is the builder VM's drive2: a reflink copy of the node's
// BuildKit seed, or an empty drive for the run that creates one.
const buildkitSeedDrive = 2

// buildkitSeedMount is where drive2 is mounted. BuildKit's root is its
// buildkit directory.
const buildkitSeedMount = "/run/faas-buildkit-seed"

// buildkitSeedMarker is a tmpfs file written once BuildKit's root is on
// drive2, so the build-done report can say so.
const buildkitSeedMarker = "/run/faas-buildkit-seeded"

// wantBuildKitSeed reports whether the manifest asks for BuildKit's root on
// drive2. A warm builder keeps its BuildKit state on drive1, which its
// snapshot carries, so it never uses a seed.
func wantBuildKitSeed(m api.BuildManifest) bool {
	return (m.BuildKitSeed || m.BuildKitSeedOnly) && !m.KeepWarm
}

// mountBuildKitSeed mounts drive2 and returns the BuildKit root on it.
func mountBuildKitSeed() (string, error) {
	return mountBuildKitSeedAt(blkDriveDevice(buildkitSeedDrive, bootRootOnPmem()), buildkitSeedMount, buildkitSeedMarker)
}

func mountBuildKitSeedAt(device, mountPoint, marker string) (string, error) {
	info, err := os.Stat(device)
	if err != nil {
		return "", fmt.Errorf("drive2 %s: %w", device, err)
	}
	if info.Mode()&os.ModeDevice == 0 {
		return "", fmt.Errorf("drive2 %s is not a block device", device)
	}
	if err := os.MkdirAll(mountPoint, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", mountPoint, err)
	}
	if err := syscall.Mount(device, mountPoint, "ext4", syscall.MS_NOSUID|syscall.MS_NODEV, ""); err != nil {
		return "", fmt.Errorf("mount %s: %w", device, err)
	}
	root := mountPoint + "/buildkit"
	if err := os.MkdirAll(root, 0o777); err != nil {
		return "", errors.Join(fmt.Errorf("mkdir %s: %w", root, err), syscall.Unmount(mountPoint, 0))
	}
	for _, dir := range []string{mountPoint, root} {
		if err := os.Chmod(dir, 0o777); err != nil {
			return "", errors.Join(fmt.Errorf("chmod %s: %w", dir, err), syscall.Unmount(mountPoint, 0))
		}
	}
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		return "", errors.Join(fmt.Errorf("write %s: %w", marker, err), syscall.Unmount(mountPoint, 0))
	}
	return root, nil
}

// unmountBuildKitSeed flushes and releases drive2 after BuildKit stops. The
// seed run's drive2 becomes the node's seed, so it must be clean.
func unmountBuildKitSeed() error {
	syscall.Sync()
	return syscall.Unmount(buildkitSeedMount, 0)
}

// buildKitSeedUsed reports whether this run put BuildKit's root on drive2.
func buildKitSeedUsed() bool {
	_, err := os.Stat(buildkitSeedMarker)
	return err == nil
}
