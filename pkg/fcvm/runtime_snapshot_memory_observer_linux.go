//go:build linux

package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"golang.org/x/sys/unix"
)

func observeSnapshotMemoryMaps(ctx context.Context, procRoot string, pid, uid int, memory pinnedRuntimeDrive) (string, []RuntimeSnapshotMemoryRange, error) {
	if pid <= 0 || uid < 0 || memory.file == nil || memory.info == nil {
		return "", nil, runtimeadmission.ErrInvalid
	}
	info, err := memory.file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o222 != 0 || !os.SameFile(info, memory.info) || info.Size() != memory.observation.Source.Bytes {
		return "", nil, errors.Join(runtimeadmission.ErrStale, err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(memory.file.Fd()), &stat); err != nil {
		return "", nil, err
	}
	process := filepath.Join(procRoot, strconv.Itoa(pid))
	start, err := runtimeDriveProcessIdentity(ctx, process, uid)
	if err != nil {
		return "", nil, err
	}
	body, err := readRuntimeDriveProcessRecord(ctx, filepath.Join(process, "maps"))
	if err != nil {
		return "", nil, err
	}
	ranges, err := verifySnapshotMemoryMaps(body, snapshotMemoryFileIdentity{major: unix.Major(stat.Dev), minor: unix.Minor(stat.Dev), inode: stat.Ino, bytes: stat.Size}, int64(os.Getpagesize()))
	if err != nil {
		return "", nil, err
	}
	again, err := readRuntimeDriveProcessRecord(ctx, filepath.Join(process, "maps"))
	if err != nil {
		return "", nil, err
	}
	checked, err := verifySnapshotMemoryMaps(again, snapshotMemoryFileIdentity{major: unix.Major(stat.Dev), minor: unix.Minor(stat.Dev), inode: stat.Ino, bytes: stat.Size}, int64(os.Getpagesize()))
	if err != nil || !slices.Equal(checked, ranges) {
		return "", nil, errors.Join(runtimeadmission.ErrStale, err)
	}
	current, err := runtimeDriveProcessIdentity(ctx, process, uid)
	after, statErr := memory.file.Stat()
	if err != nil || start != current || statErr != nil || !after.Mode().IsRegular() || after.Mode().Perm()&0o222 != 0 || !os.SameFile(info, after) || after.Size() != info.Size() {
		return "", nil, errors.Join(runtimeadmission.ErrStale, err, statErr)
	}
	return start, ranges, ctx.Err()
}
