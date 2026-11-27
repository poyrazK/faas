//go:build linux

package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"golang.org/x/sys/unix"
)

// mountSandboxVolume mounts the session's volume at /workspace when vmmd
// attached one, and reports whether it did. A drive 2 without the volume's
// label is left alone.
func mountSandboxVolume(log *slog.Logger) (bool, error) {
	dev := blkDriveDevice(sandboxVolumeDrive, bootRootOnPmem())
	//nolint:forbidigo // a block device vmmd attached, not a customer path
	f, err := os.Open(dev)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open %s: %w", dev, err)
	}
	label, ok := ext4Label(f)
	_ = f.Close()
	if !ok || label != sandboxVolumeLabel {
		return false, nil
	}
	//nolint:forbidigo // a fixed guest directory guest-init owns
	if err := os.MkdirAll(sandboxWorkspace, 0o755); err != nil {
		return false, err
	}
	if err := unix.Mount(dev, sandboxWorkspace, "ext4", unix.MS_NOATIME|unix.MS_NODEV|unix.MS_NOSUID, ""); err != nil {
		return false, fmt.Errorf("mount %s at %s: %w", dev, sandboxWorkspace, err)
	}
	log.Info("sandbox: workspace volume mounted", "device", dev)
	return true, nil
}
