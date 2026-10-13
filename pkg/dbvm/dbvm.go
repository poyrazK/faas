// Package dbvm is the host side of database microVMs (ADR-1027): the
// Firecracker configuration of a database VM, its persistent volume, and the
// vsock clients for guest-init's database mode. vmmd is the only production
// caller; the package itself never launches Firecracker.
package dbvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// Drive ids. Order matters: the base is the root device (vda) and the
// volume is vdb, where guest-init's database mode mounts it.
const (
	DriveBase   = "base"
	DriveVolume = "volume"
)

// Volume sizing bounds. Plan storage limits are enforced separately; these
// only reject nonsense before a file is created.
const (
	MinVolumeBytes int64 = 256 << 20
	MaxVolumeBytes int64 = 2 << 40
)

// Spec describes one database VM boot.
type Spec struct {
	KernelPath string
	BasePath   string
	VolumePath string
	Major      int
	VcpuCount  int
	MemSizeMiB int
	// GuestCID is the vsock context id; unique per live VM on the host.
	GuestCID uint32
	// VsockUDS is the host-side AF_UNIX path Firecracker serves vsock on.
	VsockUDS string
	// VolumeRateLimiter caps the volume's bandwidth and IOPS per class.
	VolumeRateLimiter *fcvm.RateLimiter
	// Archive turns WAL archiving on in the guest; set only when the host
	// relays archive streams (pkg/dbarchive).
	Archive bool
	// RestoreTarget, when set, boots the empty volume as a point-in-time
	// restore to that instant; it needs Archive.
	RestoreTarget time.Time
}

// Validate rejects a spec that would not boot a database VM.
func (s Spec) Validate() error {
	switch {
	case s.KernelPath == "", s.BasePath == "", s.VolumePath == "", s.VsockUDS == "":
		return errors.New("dbvm: kernel, base, volume and vsock paths are required")
	case s.VcpuCount < 1 || s.VcpuCount > 32:
		return fmt.Errorf("dbvm: vcpu count %d out of range", s.VcpuCount)
	case s.MemSizeMiB < 128:
		return fmt.Errorf("dbvm: %d MiB is too little memory for postgres", s.MemSizeMiB)
	case s.GuestCID < 3:
		return fmt.Errorf("dbvm: guest cid %d is reserved", s.GuestCID)
	case !s.RestoreTarget.IsZero() && !s.Archive:
		return errors.New("dbvm: a point-in-time restore needs the archive")
	}
	return nil
}

// BuildConfig returns the Firecracker configuration of a database VM: no
// network interface, the read-only shared base as root, the persistent
// volume with write-back caching so guest flushes reach the host, entropy
// and a vsock device.
func BuildConfig(s Spec) (fcvm.VMConfig, error) {
	if err := s.Validate(); err != nil {
		return fcvm.VMConfig{}, err
	}
	bootArgs, err := fcvm.DatabaseBootArgs(s.Major)
	if err != nil {
		return fcvm.VMConfig{}, fmt.Errorf("dbvm: %w", err)
	}
	if s.Archive {
		bootArgs += " " + dbguest.ArchiveArg
	}
	if !s.RestoreTarget.IsZero() {
		bootArgs += " " + dbguest.RestoreTargetArg(s.RestoreTarget)
	}
	return fcvm.VMConfig{
		BootSource: fcvm.BootSource{KernelImagePath: s.KernelPath, BootArgs: bootArgs},
		Drives: []fcvm.Drive{
			{DriveID: DriveBase, PathOnHost: s.BasePath, IsRootDevice: true, IsReadOnly: true},
			{DriveID: DriveVolume, PathOnHost: s.VolumePath, CacheType: fcvm.DriveCacheWriteback, RateLimiter: s.VolumeRateLimiter},
		},
		MachineConfig: fcvm.Machine{VcpuCount: s.VcpuCount, MemSizeMib: s.MemSizeMiB},
		Entropy:       &fcvm.Entropy{},
		VsockDevice:   &fcvm.VsockDevice{GuestCID: s.GuestCID, UDSSocket: s.VsockUDS},
	}, nil
}

// CreateVolume creates a sparse volume file and formats it ext4, so the
// guest only mounts it. It refuses to overwrite an existing file.
func CreateVolume(ctx context.Context, path string, sizeBytes int64) (err error) {
	if sizeBytes < MinVolumeBytes || sizeBytes > MaxVolumeBytes {
		return fmt.Errorf("dbvm: volume size %d out of range", sizeBytes)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("dbvm: create volume: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	if err = f.Truncate(sizeBytes); err != nil {
		_ = f.Close()
		return fmt.Errorf("dbvm: size volume: %w", err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("dbvm: close volume: %w", err)
	}
	out, err := exec.CommandContext(ctx, "mkfs.ext4", MkfsArgs(path)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("dbvm: mkfs volume: %w: %s", err, out)
	}
	return nil
}

// MkfsArgs formats a database volume: no reserved blocks (the plan quota is
// the limit), lazy inode and journal init so creation stays fast on a sparse
// file, and a label for operators.
func MkfsArgs(path string) []string {
	return []string{"-q", "-F", "-m", "0", "-L", "gregale-db", "-E", "lazy_itable_init=1,lazy_journal_init=1", path}
}
