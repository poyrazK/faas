package fcvm

// adr: 20261122132055 §4 — a persistent session's /workspace is a volume:
// a sparse ext4 file on the node's /srv/fc, attached to the session's VM as
// its third virtio-blk drive and mounted by guest-init. It outlives the VM:
// a pause keeps it beside the capture, a resume attaches it again, and it
// grows (never shrinks) while the session is paused.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const (
	// DriveSandboxVolume is a persistent session's workspace volume. It
	// follows the base and the sandbox layer, so the guest sees it as drive 2.
	DriveSandboxVolume = "sandbox-volume"
	// sandboxVolumeImageName is the volume's name inside the jail.
	sandboxVolumeImageName = "sandbox-volume.ext4"
	// SandboxVolumeLabel is the ext4 label guest-init looks for before it
	// mounts drive 2 at /workspace.
	SandboxVolumeLabel = "gregale-ws"
	// SandboxVolumeMinGB and SandboxVolumeMaxGB bound a volume's size; the
	// plan's own cap is api.SandboxVolumeMaxGB.
	SandboxVolumeMinGB = 1
	SandboxVolumeMaxGB = 1024
	// DefaultSandboxVolumeRoot is where vmmd keeps volumes: on /srv/fc, the
	// node's XFS with reflinks, beside the jails' images.
	DefaultSandboxVolumeRoot = "/srv/fc/sandbox-volumes"
	// sandboxVolumeOwnerUID is guest-init's sandboxUID, the session's user.
	sandboxVolumeOwnerUID = 1000
)

// ErrSandboxVolumeShrink refuses a smaller size: ext4 under a session's
// workspace is never shrunk.
var ErrSandboxVolumeShrink = errors.New("fcvm: a sandbox volume cannot shrink")

// SandboxVolumes keeps persistent sessions' volumes under one directory, one
// file per session: <root>/<account>/<sandbox>.ext4. The root is on the
// node's /srv/fc (XFS), so a backup's reflink snapshot shares its blocks.
type SandboxVolumes struct {
	Root string
	// run runs mkfs.ext4, dumpe2fs, e2fsck and resize2fs; tests replace it.
	run func(ctx context.Context, name string, args ...string) ([]byte, error)

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// NewSandboxVolumes keeps volumes under root.
func NewSandboxVolumes(root string) *SandboxVolumes {
	return &SandboxVolumes{Root: root, run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}}
}

// Path is the session's volume file. Account and sandbox IDs are UUIDs; any
// other shape is refused so a key never leaves the root.
func (v *SandboxVolumes) Path(accountID, sandboxID string) (string, error) {
	if v == nil || v.Root == "" {
		return "", errors.New("fcvm: sandbox volumes are not configured")
	}
	if !safeVolumeSegment(accountID) || !safeVolumeSegment(sandboxID) {
		return "", fmt.Errorf("fcvm: sandbox volume for %q/%q: invalid id", accountID, sandboxID)
	}
	return filepath.Join(v.Root, accountID, sandboxID+".ext4"), nil
}

func safeVolumeSegment(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-'; !ok {
			return false
		}
	}
	return true
}

// Ensure returns the session's volume at sizeGB, creating it when the node
// has none and growing it when it is smaller. created reports a new, empty
// volume: the caller restores a backup into it when the session has one.
func (v *SandboxVolumes) Ensure(ctx context.Context, accountID, sandboxID string, sizeGB int) (path string, created bool, err error) {
	if sizeGB < SandboxVolumeMinGB || sizeGB > SandboxVolumeMaxGB {
		return "", false, fmt.Errorf("fcvm: sandbox volume size %d GB out of range", sizeGB)
	}
	path, err = v.Path(accountID, sandboxID)
	if err != nil {
		return "", false, err
	}
	size := int64(sizeGB) << 30
	info, statErr := os.Stat(path)
	switch {
	case statErr == nil && info.Mode().IsRegular():
		if err := v.grow(ctx, path, info.Size(), size); err != nil {
			return "", false, err
		}
		return path, false, nil
	case statErr == nil:
		return "", false, fmt.Errorf("fcvm: sandbox volume %s is not a regular file", path)
	case !errors.Is(statErr, os.ErrNotExist):
		return "", false, fmt.Errorf("fcvm: sandbox volume: %w", statErr)
	}
	if err := v.create(ctx, path, size); err != nil {
		return "", false, err
	}
	return path, true, nil
}

// create makes a sparse file and formats it, through a temporary name so a
// crash never leaves a half-made volume under the session's path.
func (v *SandboxVolumes) create(ctx context.Context, path string, size int64) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("fcvm: sandbox volume dir: %w", err)
	}
	tmp := path + ".new"
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("fcvm: create sandbox volume: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if err = f.Truncate(size); err != nil {
		_ = f.Close()
		return fmt.Errorf("fcvm: size sandbox volume: %w", err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("fcvm: close sandbox volume: %w", err)
	}
	if out, err := v.run(ctx, "mkfs.ext4", sandboxVolumeMkfsArgs(tmp)...); err != nil {
		return fmt.Errorf("fcvm: mkfs sandbox volume: %w: %s", err, out)
	}
	if err = os.Rename(tmp, path); err != nil {
		return fmt.Errorf("fcvm: place sandbox volume: %w", err)
	}
	return nil
}

// sandboxVolumeMkfsArgs formats a volume whose root the session's user owns
// (guest-init's sandboxUID), so /workspace is writable without a chown.
func sandboxVolumeMkfsArgs(path string) []string {
	owner := strconv.Itoa(sandboxVolumeOwnerUID)
	return []string{"-q", "-F", "-m", "0", "-L", SandboxVolumeLabel,
		"-E", "lazy_itable_init=1,lazy_journal_init=1,root_owner=" + owner + ":" + owner, path}
}

// grow enlarges a volume that no VM has open to size, offline. A volume
// already as large is left alone; one whose file grew but whose filesystem
// did not (a crash between the two) is finished.
func (v *SandboxVolumes) grow(ctx context.Context, path string, current, size int64) error {
	if size < current {
		return ErrSandboxVolumeShrink
	}
	if size > current {
		if err := os.Truncate(path, size); err != nil {
			return fmt.Errorf("fcvm: extend sandbox volume: %w", err)
		}
	}
	super, err := v.run(ctx, "dumpe2fs", "-h", path)
	if err != nil {
		return fmt.Errorf("fcvm: read sandbox volume superblock: %w", err)
	}
	fs, err := ext4FilesystemBytes(super)
	if err != nil {
		return err
	}
	if fs >= size {
		return nil
	}
	// resize2fs grows an unmounted filesystem only after a full check;
	// e2fsck -p exits 1 when it corrected something, which is fine.
	if out, err := v.run(ctx, "e2fsck", "-p", "-f", path); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return fmt.Errorf("fcvm: check sandbox volume: %w: %s", err, out)
		}
	}
	if out, err := v.run(ctx, "resize2fs", path); err != nil {
		return fmt.Errorf("fcvm: grow sandbox volume: %w: %s", err, out)
	}
	return nil
}

// Remove deletes the session's volume from this node.
func (v *SandboxVolumes) Remove(accountID, sandboxID string) error {
	path, err := v.Path(accountID, sandboxID)
	if err != nil {
		return err
	}
	unlock := v.lockVolume(path)
	defer unlock()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("fcvm: remove sandbox volume: %w", err)
	}
	v.dropBackupBase(path)
	return nil
}

// ext4FilesystemBytes reads an ext4 filesystem's size from `dumpe2fs -h`.
func ext4FilesystemBytes(dumpe2fs []byte) (int64, error) {
	var blocks, blockSize int64
	for _, line := range strings.Split(string(dumpe2fs), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		switch strings.TrimSpace(key) {
		case "Block count":
			if err == nil {
				blocks = n
			}
		case "Block size":
			if err == nil {
				blockSize = n
			}
		}
	}
	if blocks <= 0 || blockSize <= 0 {
		return 0, errors.New("fcvm: sandbox volume superblock has no block count or size")
	}
	return blocks * blockSize, nil
}
