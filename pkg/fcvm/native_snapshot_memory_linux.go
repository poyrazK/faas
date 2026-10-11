//go:build linux

// adr: 796
package fcvm

import (
	"context"
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

type linuxNativeSnapshotMemory struct{ root string }

func newNativeSnapshotMemoryBackend() nativeSnapshotMemoryBackend {
	return linuxNativeSnapshotMemory{root: cgroupRoot}
}

func (b linuxNativeSnapshotMemory) CheckSupport() error {
	root, err := openNativeRuntimeCgroupRoot(b.root)
	if err != nil {
		return err
	}
	return root.Close()
}

type linuxNativeSnapshotMemoryFence struct {
	root, group *os.File
	path        string
	identity    nativeLoopIdentity
	mount       uint64
	pid         int
	ordinary    uint64
	widened     uint64
	attempted   bool
	closed      bool
}

func (b linuxNativeSnapshotMemory) Open(ctx context.Context, owner nativeLaunchRecord) (fence nativeSnapshotMemoryFence, err error) {
	if !liveNativeSnapshotOwner(owner) || owner.Lease.MemoryMaxMiB <= 0 || ctx.Err() != nil {
		return nil, errors.Join(ctx.Err(), errors.New("native snapshot memory: original live app owner is required"))
	}
	ram := owner.Lease.MemoryMaxMiB
	ordinary, widened := api.BillableRAMMB(ram), api.SnapshotMemoryMaxMB(ram)
	if ordinary <= ram || widened <= ordinary || uint64(widened) > ^uint64(0)>>20 {
		return nil, errors.New("native snapshot memory: memory limit overflows kernel bytes")
	}
	root, err := openNativeRuntimeCgroupRoot(b.root)
	if err != nil {
		return nil, err
	}
	path := ParentCgroupFor(owner.Lease.Plan) + "/" + owner.Lease.Instance
	fd, err := openNativeRuntimeCgroupPath(int(root.Fd()), path)
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	f := &linuxNativeSnapshotMemoryFence{root: root, group: os.NewFile(uintptr(fd), "native-snapshot-cgroup"), path: path, pid: owner.PID,
		ordinary: uint64(ordinary) << 20, widened: uint64(widened) << 20}
	group, err := readNativeRuntimeCgroup(fd, path)
	if err != nil {
		return nil, errors.Join(err, f.Close())
	}
	f.identity, f.mount = nativeLoopIdentity{Device: group.Device, Inode: group.Inode}, group.MountID
	if err := f.check(ctx, false, f.ordinary); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return f, nil
}

func (f *linuxNativeSnapshotMemoryFence) check(ctx context.Context, retiring bool, limits ...uint64) error {
	if f.closed || ctx.Err() != nil {
		return errors.Join(ctx.Err(), errors.New("native snapshot memory: expired scope capability"))
	}
	group, err := readNativeRuntimeCgroup(int(f.group.Fd()), f.path)
	if err != nil {
		return err
	}
	occupantsMatch := slices.Equal(group.PIDs, []int{f.pid}) || retiring && len(group.PIDs) == 0 && !group.Populated
	if group.Device != f.identity.Device || group.Inode != f.identity.Inode || group.MountID != f.mount ||
		!occupantsMatch ||
		group.MemoryMaxBytes == nil || !slices.Contains(limits, *group.MemoryMaxBytes) {
		return errors.New("native snapshot memory: original scope, occupants or memory limit changed")
	}
	// A renamed/replaced pathname never selects a new cgroup for mutation.
	fd, err := openNativeRuntimeCgroupPath(int(f.root.Fd()), f.path)
	if err != nil {
		return err
	}
	var st unix.Stat_t
	if err := errors.Join(unix.Fstat(fd, &st), unix.Close(fd)); err != nil {
		return err
	}
	if uint64(st.Dev) != f.identity.Device || st.Ino != f.identity.Inode {
		return errors.New("native snapshot memory: original scope path was replaced")
	}
	return nil
}

func (f *linuxNativeSnapshotMemoryFence) write(ctx context.Context, value uint64) (err error) {
	control, err := nativeHelperCgroupControl(int(f.group.Fd()), "memory.max", unix.O_WRONLY)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, control.Close()) }()
	mount, err := nativeRuntimeCgroupMountID(int(control.Fd()))
	if err != nil || mount != f.mount {
		return errors.Join(err, errors.New("native snapshot memory: control belongs to another mount"))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	body := strconv.FormatUint(value, 10) + "\n"
	if n, err := control.WriteString(body); err != nil || n != len(body) {
		return errors.Join(err, errors.New("native snapshot memory: limit write was incomplete"))
	}
	actual, err := readNativeRuntimeControl(int(f.group.Fd()), "memory.max")
	if err != nil || strings.TrimSpace(actual) != strconv.FormatUint(value, 10) {
		return errors.Join(err, errors.New("native snapshot memory: limit write was not observed"))
	}
	return ctx.Err()
}

func (f *linuxNativeSnapshotMemoryFence) Widen(ctx context.Context) error {
	if f.attempted {
		return errors.New("native snapshot memory: widening cannot be replayed")
	}
	if err := f.check(ctx, false, f.ordinary); err != nil {
		return err
	}
	f.attempted = true // Even an uncertain write must be restored before resume.
	return f.write(ctx, f.widened)
}

func (f *linuxNativeSnapshotMemoryFence) Restore(ctx context.Context) error {
	if !f.attempted {
		return f.check(ctx, false, f.ordinary)
	}
	if err := f.check(ctx, true, f.ordinary, f.widened); err != nil {
		return err
	}
	return f.write(ctx, f.ordinary)
}

func (f *linuxNativeSnapshotMemoryFence) Close() error {
	if f.closed {
		return nil
	}
	f.closed = true
	return errors.Join(f.group.Close(), f.root.Close())
}
