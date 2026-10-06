//go:build linux

package fcvm

// adr: 641
import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/unix"
)

type linuxNativeRuntimeCgroups struct{ root string }

func newNativeRuntimeCgroups() nativeRuntimeCgroups {
	return linuxNativeRuntimeCgroups{root: cgroupRoot}
}

func (b linuxNativeRuntimeCgroups) Inventory(ctx context.Context) (groups []NativeRuntimeCgroup, err error) {
	root, err := openNativeRuntimeCgroupRoot(b.root)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	for _, parent := range []string{CgroupMountRoot, ParentCgroupRoot, BuilderCgroupParent} {
		fd, err := openNativeRuntimeCgroupPath(int(root.Fd()), parent)
		if errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil {
			return nil, err
		}
		observed, walkErr := walkNativeRuntimeCgroups(ctx, fd, parent, true)
		if err := errors.Join(walkErr, unix.Close(fd)); err != nil {
			return nil, err
		}
		groups = append(groups, observed...)
	}
	slices.SortFunc(groups, func(a, b NativeRuntimeCgroup) int { return strings.Compare(a.Path, b.Path) })
	for i, group := range groups {
		if i > 0 && group.Path == groups[i-1].Path {
			return nil, errors.New("native inventory: duplicate cgroup discovery")
		}
		current, err := openNativeRuntimeCgroupPath(int(root.Fd()), group.Path)
		if err != nil {
			return nil, err
		}
		var stat unix.Stat_t
		if err := errors.Join(unix.Fstat(current, &stat), unix.Close(current)); err != nil {
			return nil, err
		}
		if uint64(stat.Dev) != group.Device || stat.Ino != group.Inode {
			return nil, errors.New("native inventory: cgroup pathname changed during discovery")
		}
	}
	return groups, ctx.Err()
}

func openNativeRuntimeCgroupRoot(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("native inventory: canonical cgroup root is required")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	if err := checkNativeRuntimeCgroupFD(int(file.Fd())); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

func checkNativeRuntimeCgroupFD(fd int) error {
	var stat unix.Stat_t
	var fs unix.Statfs_t
	if err := errors.Join(unix.Fstat(fd, &stat), unix.Fstatfs(fd, &fs)); err != nil {
		return err
	}
	if fs.Type != unix.CGROUP2_SUPER_MAGIC || stat.Uid != 0 || stat.Mode&0o022 != 0 || stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return errors.New("native inventory: protected root-owned kernel cgroup v2 directory is required")
	}
	return nil
}

func openNativeRuntimeCgroupPath(root int, path string) (int, error) {
	if path == "" || filepath.IsAbs(path) || filepath.Clean(path) != path || strings.Contains(path, "\\") {
		return -1, errors.New("native inventory: invalid cgroup path")
	}
	mount, err := nativeRuntimeCgroupMountID(root)
	if err != nil {
		return -1, err
	}
	fd, err := unix.Dup(root)
	if err != nil {
		return -1, err
	}
	unix.CloseOnExec(fd)
	for _, part := range strings.Split(path, "/") {
		if part == "." || part == ".." {
			return -1, errors.Join(errors.New("native inventory: cgroup traversal is forbidden"), unix.Close(fd))
		}
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		closeErr := unix.Close(fd)
		if err := errors.Join(openErr, closeErr); err != nil {
			if next >= 0 {
				err = errors.Join(err, unix.Close(next))
			}
			return -1, err
		}
		fd = next
		if err := checkNativeRuntimeCgroupFD(fd); err != nil {
			return -1, errors.Join(err, unix.Close(fd))
		}
		observedMount, err := nativeRuntimeCgroupMountID(fd)
		if err != nil || observedMount != mount {
			return -1, errors.Join(err, errors.New("native inventory: nested cgroup mount is unqualified"), unix.Close(fd))
		}
	}
	return fd, nil
}

func walkNativeRuntimeCgroups(ctx context.Context, fd int, path string, parent bool) (result []NativeRuntimeCgroup, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	group, err := readNativeRuntimeCgroup(fd, path)
	if err != nil {
		return nil, err
	}
	if !parent || len(group.PIDs) != 0 {
		result = append(result, group)
	}
	copyFD, err := unix.Dup(fd)
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(copyFD)
	children := os.NewFile(uintptr(copyFD), "native-cgroup-children")
	defer func() { err = errors.Join(err, children.Close()) }()
	for {
		entries, readErr := children.ReadDir(128)
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 {
				return nil, errors.New("native inventory: unexpected cgroup symlink")
			}
			if !entry.IsDir() {
				continue
			}
			child, err := openNativeRuntimeCgroupPath(fd, entry.Name())
			if err != nil {
				return nil, err
			}
			childPath := filepath.Join(path, entry.Name())
			_, _, planParent := nativeParentPlan(childPath)
			observed, walkErr := walkNativeRuntimeCgroups(ctx, child, childPath, parent && planParent)
			if err := errors.Join(walkErr, unix.Close(child)); err != nil {
				return nil, err
			}
			result = append(result, observed...)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if err := errors.Join(readErr, ctx.Err()); err != nil {
			return nil, err
		}
	}
	after, err := readNativeRuntimeCgroup(fd, path)
	if err != nil || !sameNativeRuntimeCgroups([]NativeRuntimeCgroup{group}, []NativeRuntimeCgroup{after}) {
		return nil, errors.Join(err, errors.New("native inventory: cgroup changed while walking descendants"))
	}
	return result, nil
}

func readNativeRuntimeControl(fd int, name string) (string, error) {
	file, err := nativeHelperCgroupControl(fd, name, unix.O_RDONLY)
	if err != nil {
		return "", err
	}
	wantMount, mountErr := nativeRuntimeCgroupMountID(fd)
	actualMount, actualErr := nativeRuntimeCgroupMountID(int(file.Fd()))
	var fs unix.Statfs_t
	fsErr := unix.Fstatfs(int(file.Fd()), &fs)
	if err := errors.Join(mountErr, actualErr, fsErr); err != nil || wantMount != actualMount || fs.Type != unix.CGROUP2_SUPER_MAGIC {
		return "", errors.Join(err, errors.New("native inventory: control file is not from the pinned kernel cgroup mount"), file.Close())
	}
	// Protocol bounds reject truncation; these are not tenant quotas.
	raw, readErr := io.ReadAll(io.LimitReader(file, 65537))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return "", err
	}
	if len(raw) > 65536 {
		return "", errors.New("native inventory: oversized kernel control response")
	}
	return string(raw), nil
}

func readNativeRuntimeCgroup(fd int, path string) (NativeRuntimeCgroup, error) {
	group := NativeRuntimeCgroup{Path: path}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return group, err
	}
	group.Device, group.Inode = uint64(stat.Dev), stat.Ino
	mount, err := nativeRuntimeCgroupMountID(fd)
	if err != nil {
		return group, err
	}
	group.MountID = mount
	kind, err := readNativeRuntimeControl(fd, "cgroup.type")
	if err != nil || strings.TrimSpace(kind) != "domain" {
		return group, errors.Join(err, errors.New("native inventory: unthreaded cgroup domain is required"))
	}
	procs, err := readNativeRuntimeControl(fd, "cgroup.procs")
	if err != nil {
		return group, err
	}
	group.PIDs, err = parseNativeRuntimePIDs(procs)
	if err != nil {
		return group, err
	}
	events, err := readNativeRuntimeControl(fd, "cgroup.events")
	if err != nil {
		return group, err
	}
	group.Populated, err = parseNativeHelperCgroupPopulated(events)
	if err != nil {
		return group, err
	}
	memory, err := readNativeRuntimeControl(fd, "memory.max")
	if err != nil {
		return group, err
	}
	group.MemoryMaxBytes, err = parseNativeRuntimeLimit(memory)
	if err != nil {
		return group, err
	}
	cpu, err := readNativeRuntimeControl(fd, "cpu.max")
	if err != nil {
		return group, err
	}
	group.CPUQuotaUS, group.CPUPeriodUS, err = parseNativeRuntimeCPU(cpu)
	if err != nil {
		return group, err
	}
	burst, err := readNativeRuntimeControl(fd, "cpu.max.burst")
	if err != nil {
		return group, err
	}
	limit, err := parseNativeRuntimeLimit(burst)
	if err != nil || limit == nil {
		return group, errors.Join(err, errors.New("native inventory: invalid CPU burst limit"))
	}
	group.BurstUS = *limit
	return group, nil
}

func nativeRuntimeCgroupMountID(fd int) (uint64, error) {
	var stat unix.Statx_t
	if err := unix.Statx(fd, "", unix.AT_EMPTY_PATH|unix.AT_NO_AUTOMOUNT, unix.STATX_MNT_ID, &stat); err != nil {
		return 0, err
	}
	if stat.Mask&unix.STATX_MNT_ID == 0 || stat.Mnt_id == 0 {
		return 0, errors.New("native inventory: kernel mount identity is unavailable")
	}
	return stat.Mnt_id, nil
}
