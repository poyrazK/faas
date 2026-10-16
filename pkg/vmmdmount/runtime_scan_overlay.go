package vmmdmount

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// MountRuntimeScanOverlay interprets whiteouts over protected read-only ext4
// sources. A single upper-as-lower excludes the base for an opaque image root.
func MountRuntimeScanOverlay(ctx context.Context, lowers []string) (string, error) {
	if len(lowers) < 1 || len(lowers) > 2 {
		return "", ErrInvalidOverlayPath
	}
	for _, lower := range lowers {
		if strings.ContainsAny(lower, ":,\\\x00\r\n") {
			return "", ErrInvalidOverlayPath
		}
		if err := rejectSymlinkOrEscape(lower, filepath.Clean(MountRoot)+string(filepath.Separator), "runtime scan lower"); err != nil {
			return "", err
		}
		info, err := os.Lstat(lower)
		if err != nil || !info.IsDir() {
			return "", errors.Join(ErrInvalidOverlayPath, err)
		}
	}
	mp, err := os.MkdirTemp(MountRoot, ParentMountPrefix+"runtime-view-")
	if err != nil {
		return "", err
	}
	opts := "ro,nodev,nosuid,noexec,lowerdir=" + strings.Join(lowers, ":")
	if err := exec.CommandContext(ctx, "mount", "-t", "overlay", "-o", opts, "overlay", mp).Run(); err != nil {
		return "", errors.Join(err, os.Remove(mp))
	}
	return mp, nil
}
