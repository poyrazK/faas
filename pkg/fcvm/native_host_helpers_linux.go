//go:build linux

package fcvm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

type nativeLinuxHostHelperGroups struct{}

func newNativeHostHelperGroups() nativeHostHelperGroups { return nativeLinuxHostHelperGroups{} }

func nativeHostHelperStartTime(pid int) (uint64, error) {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, err
	}
	return nativeProcessStartTime(string(raw), pid)
}

func (nativeLinuxHostHelperGroups) Plan(id string) (nativeHostHelperGroup, error) {
	var result nativeHostHelperGroup
	if !canonicalNativeHelperID(id) {
		return result, errors.New("native helper: invalid command identity")
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(cgroupRoot, &stat); err != nil {
		return result, err
	}
	if stat.Type != unix.CGROUP2_SUPER_MAGIC {
		return result, errors.New("native helper: unified cgroup v2 mount is required")
	}
	raw, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return result, err
	}
	path, err := nativeHostHelperParent(string(raw))
	if err != nil {
		return result, err
	}
	result.Path = filepath.Join(path, nativeHostHelperScope, id)
	return result, nil
}

func (nativeLinuxHostHelperGroups) Create(planned nativeHostHelperGroup) (nativeHostHelperGroup, error) {
	if !validNativeHelperGroupPath(planned.Path, filepath.Base(planned.Path)) || !canonicalNativeHelperID(filepath.Base(planned.Path)) || planned.Device != 0 || planned.Inode != 0 {
		return planned, errors.New("native helper: invalid cgroup creation frame")
	}
	path := filepath.Join(cgroupRoot, planned.Path)
	parent := filepath.Dir(path)
	// The parent is in vmmd's delegated service cgroup. Inherited control
	// plane limits remain in force; no tenant or builder cgroup is borrowed.
	if err := os.Mkdir(parent, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return planned, err
	}
	if err := checkNativeHelperCgroupDirectory(parent); err != nil {
		return planned, err
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		return planned, err // never adopt a previously existing command cgroup
	}
	fd, err := openNativeHelperCgroup(planned)
	if err != nil {
		return planned, err
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return planned, err
	}
	planned.Device, planned.Inode = uint64(stat.Dev), stat.Ino
	return planned, nil
}

func checkNativeHelperCgroupDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || int(stat.Uid) != os.Geteuid() || info.Mode().Perm()&0o022 != 0 {
		return errors.New("native helper: cgroup directory is not owned and private for writes")
	}
	return nil
}

func openNativeHelperCgroup(group nativeHostHelperGroup) (int, error) {
	if !validNativeHelperGroupPath(group.Path, filepath.Base(group.Path)) || !canonicalNativeHelperID(filepath.Base(group.Path)) {
		return -1, errors.New("native helper: invalid cgroup path")
	}
	path := filepath.Join(cgroupRoot, group.Path)
	if err := checkNativeHelperCgroupDirectory(filepath.Dir(path)); err != nil {
		return -1, err
	}
	fd, err := unix.Open(path, unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_RDONLY, 0)
	if err != nil {
		return -1, err
	}
	var stat unix.Stat_t
	var fs unix.Statfs_t
	err = errors.Join(unix.Fstat(fd, &stat), unix.Fstatfs(fd, &fs))
	if err == nil && (fs.Type != unix.CGROUP2_SUPER_MAGIC || stat.Uid != uint32(os.Geteuid()) || stat.Mode&0o022 != 0 || group.Inode != 0 && (stat.Ino != group.Inode || uint64(stat.Dev) != group.Device)) {
		err = errors.New("native helper: cgroup kernel identity changed")
	}
	if err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

func (nativeLinuxHostHelperGroups) Attach(cmd *exec.Cmd, group nativeHostHelperGroup) (io.Closer, error) {
	if group.Inode == 0 || group.Device == 0 || cmd.SysProcAttr != nil {
		return nil, errors.New("native helper: incomplete cgroup identity or conflicting fork attributes")
	}
	fd, err := openNativeHelperCgroup(group)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "native-helper-cgroup")
	cmd.SysProcAttr = &syscall.SysProcAttr{UseCgroupFD: true, CgroupFD: fd}
	return file, nil
}

func nativeHelperCgroupControl(fd int, name string, flags int) (*os.File, error) {
	control, err := unix.Openat(fd, name, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(control), name), nil
}

func nativeHelperCgroupPopulated(fd int) (bool, error) {
	file, err := nativeHelperCgroupControl(fd, "cgroup.events", unix.O_RDONLY)
	if err != nil {
		return false, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, api.NativeHostHelperCgroupEventsMaxBytes+1))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return false, err
	}
	if len(raw) > api.NativeHostHelperCgroupEventsMaxBytes {
		return false, errors.New("native helper: cgroup events are oversized")
	}
	return parseNativeHelperCgroupPopulated(string(raw))
}

func (nativeLinuxHostHelperGroups) Retire(ctx context.Context, group nativeHostHelperGroup) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	fd, err := openNativeHelperCgroup(group)
	if errors.Is(err, os.ErrNotExist) {
		// Every executable task was born into this group. The kernel refuses
		// its removal while live tasks remain, including descendants.
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unix.Close(fd)) }()
	populated, err := nativeHelperCgroupPopulated(fd)
	if err != nil {
		return err
	}
	if group.Inode == 0 && populated {
		return errors.New("native helper: unpublished cgroup identity contains tasks")
	}
	if populated {
		file, err := nativeHelperCgroupControl(fd, "cgroup.kill", unix.O_WRONLY)
		if err != nil {
			return fmt.Errorf("native helper: open cgroup kill: %w", err)
		}
		_, writeErr := file.WriteString("1")
		if err := errors.Join(writeErr, file.Close()); err != nil {
			return err
		}
	}
	for populated {
		if err := ctx.Err(); err != nil {
			return err
		}
		populated, err = nativeHelperCgroupPopulated(fd)
		if err != nil {
			return err
		}
		if populated {
			timer := time.NewTimer(10 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	// Revalidate the pathname against the pinned directory immediately before
	// removing it. Unexpected descendant cgroups keep the frame held.
	current, err := openNativeHelperCgroup(group)
	if err != nil {
		return err
	}
	var pinnedStat, currentStat unix.Stat_t
	statErr := errors.Join(unix.Fstat(fd, &pinnedStat), unix.Fstat(current, &currentStat), unix.Close(current))
	if statErr != nil {
		return statErr
	}
	if pinnedStat.Ino != currentStat.Ino || pinnedStat.Dev != currentStat.Dev {
		return errors.New("native helper: cgroup changed during retirement")
	}
	return unix.Rmdir(filepath.Join(cgroupRoot, group.Path))
}

func (nativeLinuxHostHelperGroups) Removed(group nativeHostHelperGroup) error {
	if !validNativeHelperGroupPath(group.Path, filepath.Base(group.Path)) || !canonicalNativeHelperID(filepath.Base(group.Path)) {
		return errors.New("native helper: invalid resource acknowledgement path")
	}
	if _, err := os.Lstat(filepath.Join(cgroupRoot, group.Path)); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("native helper: acknowledged command cgroup reappeared")
}

func (g nativeLinuxHostHelperGroups) Inventory(records []nativeHostHelperRecord) error {
	parents := make(map[string]bool)
	owned := make(map[string]nativeHostHelperRecord, len(records))
	planned, err := g.Plan("fd4dfbab-66b3-45f7-8cda-f5c4cd857681")
	if err != nil {
		return err
	}
	parents[filepath.Dir(planned.Path)] = true
	for _, record := range records {
		if _, duplicate := owned[record.Group.Path]; duplicate {
			return errors.New("native helper: duplicate cgroup ownership")
		}
		owned[record.Group.Path] = record
		parents[filepath.Dir(record.Group.Path)] = true
		if record.Launch.ResourcesRemoved {
			if err := g.Removed(record.Group); err != nil {
				return err
			}
		}
	}
	for parent := range parents {
		path := filepath.Join(cgroupRoot, parent)
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		if err := checkNativeHelperCgroupDirectory(path); err != nil {
			return err
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 {
				return errors.New("native helper: unexpected cgroup symlink")
			}
			if !entry.IsDir() {
				continue // kernel control files
			}
			groupPath := filepath.Join(parent, entry.Name())
			record, ok := owned[groupPath]
			if !ok || !canonicalNativeHelperID(entry.Name()) {
				return errors.New("native helper: cgroup has no durable command provenance")
			}
			fd, err := openNativeHelperCgroup(record.Group)
			if err != nil {
				return err
			}
			populated, readErr := nativeHelperCgroupPopulated(fd)
			if err := errors.Join(readErr, unix.Close(fd)); err != nil {
				return err
			}
			if populated && record.Group.Inode == 0 {
				return errors.New("native helper: unpublished cgroup contains tasks")
			}
		}
	}
	return nil
}
