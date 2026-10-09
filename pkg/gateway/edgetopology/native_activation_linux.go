//go:build linux

package edgetopology

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

type linuxNativeActivation struct{ *linuxNativeScope }

type nativeMaskState struct {
	Identity          NativeFileIdentity
	Mode, UID, GID    uint32
	Modified, Changed unix.Timespec
}

func newNativeActivationSession(ctx context.Context, review NativeActivationReview) (nativeActivationSession, error) {
	host, err := newLinuxNativeHost(ctx)
	if err != nil {
		return nil, err
	}
	host.groups[review.Service.Cgroup] = true
	return &linuxNativeActivation{host}, nil
}

func (s *linuxNativeActivation) Capture(ctx context.Context, review NativeActivationReview) (NativeActivationSnapshot, error) {
	return captureNativeActivation(ctx, s, review)
}

func (s *linuxNativeActivation) MaskedUnit(ctx context.Context, unit string) (NativeMaskedUnit, error) {
	if !nativeUnit(unit) && !nativeUnitSuffix(unit, ".socket") {
		return NativeMaskedUnit{}, nativeReadError("canonical activation unit")
	}
	before, err := nativePersistentMask(unit)
	if err != nil {
		return NativeMaskedUnit{}, err
	}
	properties := "Id,LoadState,ActiveState,SubState,UnitFileState,NeedDaemonReload,Job,ControlPID,ControlGroup"
	if nativeUnit(unit) {
		properties += ",MainPID,NFileDescriptorStore"
	}
	raw, err := showNativeUnit(ctx, s.systemctl, unit, properties)
	if err != nil || matchNativeMaskedUnit(raw, unit) != nil {
		return NativeMaskedUnit{}, nativeReadError("inactive masked systemd unit without jobs, processes or stored descriptors")
	}
	after, err := nativePersistentMask(unit)
	if err != nil || before != after || ctx.Err() != nil {
		return NativeMaskedUnit{}, nativeReadError("persistent mask changed")
	}
	return NativeMaskedUnit{Unit: unit, Mask: before.Identity}, nil
}

func nativePersistentMask(unit string) (nativeMaskState, error) {
	for _, directory := range []string{"/etc/systemd/system", "/dev"} {
		for current := directory; ; current = filepath.Dir(current) {
			var stat unix.Stat_t
			if unix.Lstat(current, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Uid != 0 || stat.Mode&0o022 != 0 {
				return nativeMaskState{}, nativeReadError("protected persistent mask ancestors")
			}
			if current == filepath.Dir(current) {
				break
			}
		}
	}
	var null unix.Stat_t
	if unix.Lstat("/dev/null", &null) != nil || null.Mode&unix.S_IFMT != unix.S_IFCHR || null.Uid != 0 || unix.Major(uint64(null.Rdev)) != 1 || unix.Minor(uint64(null.Rdev)) != 3 {
		return nativeMaskState{}, nativeReadError("native null device")
	}
	name := "/etc/systemd/system/" + unit
	var before, after unix.Stat_t
	if unix.Lstat(name, &before) != nil || before.Mode&unix.S_IFMT != unix.S_IFLNK || before.Uid != 0 {
		return nativeMaskState{}, nativeReadError("root-owned persistent unit mask")
	}
	target, err := os.Readlink(name)
	if err != nil || target != "/dev/null" || unix.Lstat(name, &after) != nil || before.Dev != after.Dev || before.Ino != after.Ino || before.Mode != after.Mode || before.Uid != after.Uid || before.Gid != after.Gid || before.Mtim != after.Mtim || before.Ctim != after.Ctim {
		return nativeMaskState{}, nativeReadError("stable exact persistent mask target")
	}
	return nativeMaskState{Identity: NativeFileIdentity{Device: uint64(before.Dev), Inode: before.Ino}, Mode: before.Mode, UID: before.Uid, GID: before.Gid, Modified: before.Mtim, Changed: before.Ctim}, nil
}

func (s *linuxNativeActivation) EmptyCgroup(ctx context.Context, service NativeServiceReview) (NativeEmptyCgroup, error) {
	if ctx.Err() != nil || !s.groups[service.Cgroup] {
		return NativeEmptyCgroup{}, nativeReadError("reviewed historical cgroup")
	}
	name := strings.TrimPrefix(service.Cgroup, "/")
	root, err := s.cgroups.OpenRoot(name)
	if err != nil {
		// Only ENOENT at BOTH lookups is absence. EACCES, replacement,
		// malformed paths and errors reading an existing events file fail.
		_, statErr := s.cgroups.Lstat(name)
		if errors.Is(err, os.ErrNotExist) && errors.Is(statErr, os.ErrNotExist) && ctx.Err() == nil {
			return NativeEmptyCgroup{}, nil
		}
		return NativeEmptyCgroup{}, nativeReadError("historical cgroup directory")
	}
	defer func() { _ = root.Close() }()
	//nolint:forbidigo // Literal directory leaf inside retained reviewed cgroups v2 root; no caller path escape.
	directory, err := root.Open(".")
	if err != nil {
		return NativeEmptyCgroup{}, nativeReadError("retained historical cgroup")
	}
	defer func() { _ = directory.Close() }()
	var stat unix.Stat_t
	if unix.Fstat(int(directory.Fd()), &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Ino != service.CgroupInode {
		return NativeEmptyCgroup{}, nativeReadError("exact historical cgroup inode")
	}
	//nolint:forbidigo // Literal bounded read-only cgroup metadata under retained contained root.
	events, err := root.Open("cgroup.events")
	if err != nil {
		return NativeEmptyCgroup{}, nativeReadError("recursive cgroup population")
	}
	defer func() { _ = events.Close() }()
	raw, err := io.ReadAll(io.LimitReader(events, int64(api.RuntimeUpgradeNativeMetadataMaxBytes)+1))
	if err != nil || matchNativeEmptyEvents(raw) != nil {
		return NativeEmptyCgroup{}, nativeReadError("recursively empty unfrozen cgroup")
	}
	info, err := s.cgroups.Lstat(name)
	if err != nil || !info.IsDir() {
		return NativeEmptyCgroup{}, nativeReadError("historical cgroup disappeared")
	}
	//nolint:forbidigo // Frozen exact reviewed cgroup path under contained cgroups v2 root.
	current, err := s.cgroups.Open(name)
	if err != nil {
		return NativeEmptyCgroup{}, nativeReadError("current historical cgroup path")
	}
	defer func() { _ = current.Close() }()
	var after unix.Stat_t
	if unix.Fstat(int(current.Fd()), &after) != nil || stat.Dev != after.Dev || stat.Ino != after.Ino || ctx.Err() != nil {
		return NativeEmptyCgroup{}, nativeReadError("historical cgroup replaced")
	}
	return NativeEmptyCgroup{Present: true, Identity: NativeFileIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}}, nil
}
