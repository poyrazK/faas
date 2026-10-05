//go:build linux

package fcvm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

type linuxNativeSnapshotMemory struct{}

func newNativeSnapshotMemoryBackend() nativeSnapshotMemoryBackend { return linuxNativeSnapshotMemory{} }

type linuxNativeSnapshotMemoryIO struct {
	owner     nativeLaunchRecord
	group     nativeHostHelperGroup
	directory *os.File
	limit     *os.File
	process   *nativePIDFD
}

func openNativeSnapshotMemoryCgroup(owner nativeLaunchRecord) (*os.File, nativeHostHelperGroup, error) {
	fd, err := unix.Openat2(unix.AT_FDCWD, nativeCgroupScope(owner.Lease), &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		return nil, nativeHostHelperGroup{}, err
	}
	file := os.NewFile(uintptr(fd), "original-snapshot-cgroup")
	var stat unix.Stat_t
	var fs unix.Statfs_t
	if err := errors.Join(unix.Fstat(fd, &stat), unix.Fstatfs(fd, &fs)); err != nil {
		return nil, nativeHostHelperGroup{}, errors.Join(err, file.Close())
	}
	if fs.Type != unix.CGROUP2_SUPER_MAGIC || stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return nil, nativeHostHelperGroup{}, errors.Join(errors.New("native snapshot memory: unified original cgroup is required"), file.Close())
	}
	return file, nativeHostHelperGroup{Path: nativeSnapshotMemoryPath(owner), Device: uint64(stat.Dev), Inode: stat.Ino}, nil
}

func openNativeSnapshotMemoryIO(ctx context.Context, owner nativeLaunchRecord) (p *linuxNativeSnapshotMemoryIO, result error) {
	if !liveNativeSnapshotOwner(owner) {
		return nil, errors.New("native snapshot memory: live original process required")
	}
	if _, _, err := nativeSnapshotMemoryLimits(owner); err != nil {
		return nil, err
	}
	p = &linuxNativeSnapshotMemoryIO{owner: owner}
	defer func() {
		if result != nil {
			result = errors.Join(result, p.Close())
			p = nil
		}
	}()
	process, err := openNativeProcess(owner.PID)
	if err != nil {
		return p, err
	}
	p.process = process.(*nativePIDFD)
	p.directory, p.group, err = openNativeSnapshotMemoryCgroup(owner)
	if err != nil {
		return p, err
	}
	fd, err := unix.Openat(int(p.directory.Fd()), "memory.max", unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return p, err
	}
	p.limit = os.NewFile(uintptr(fd), "original-snapshot-memory-limit")
	if err := p.Check(ctx); err != nil {
		return p, err
	}
	base, _, _ := nativeSnapshotMemoryLimits(owner)
	value, err := p.Read()
	if err != nil || value != base {
		return p, errors.Join(err, errors.New("native snapshot memory: initial original fence differs from policy"))
	}
	return p, nil
}

func (p *linuxNativeSnapshotMemoryIO) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.process == nil || p.directory == nil || p.limit == nil {
		return errors.New("native snapshot memory: original descriptors are closed")
	}
	if err := checkNativeSnapshotProcess(p.process, p.owner); err != nil {
		return err
	}
	file, group, err := openNativeSnapshotMemoryCgroup(p.owner)
	if err != nil {
		return err
	}
	err = file.Close()
	if group != p.group {
		return errors.Join(err, errors.New("native snapshot memory: original cgroup path was replaced"))
	}
	if err != nil {
		return err
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(p.owner.PID) + "/cgroup")
	if err != nil {
		return err
	}
	if string(data) != "0::/"+p.group.Path+"\n" {
		return errors.New("native snapshot memory: original process left its exact cgroup")
	}
	data, err = os.ReadFile("/proc/" + strconv.Itoa(p.owner.PID) + "/status")
	if err != nil {
		return err
	}
	uid := strconv.Itoa(p.owner.Lease.UID)
	gid := strconv.Itoa(p.owner.Lease.GID)
	var haveUID, haveGID bool
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 5 {
			continue
		}
		if fields[0] == "Uid:" {
			haveUID = fields[1] == uid && fields[2] == uid && fields[3] == uid && fields[4] == uid
		}
		if fields[0] == "Gid:" {
			haveGID = fields[1] == gid && fields[2] == gid && fields[3] == gid && fields[4] == gid
		}
	}
	if !haveUID || !haveGID {
		return errors.New("native snapshot memory: original process credentials changed")
	}
	return checkNativeSnapshotProcess(p.process, p.owner)
}

func (p *linuxNativeSnapshotMemoryIO) Read() (uint64, error) {
	if _, err := p.limit.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	data, err := io.ReadAll(io.LimitReader(p.limit, 32))
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
}

func (p *linuxNativeSnapshotMemoryIO) Write(expected, next uint64) error {
	if err := p.Check(context.Background()); err != nil {
		return err
	}
	value, err := p.Read()
	if err != nil || value != expected {
		return errors.Join(err, errors.New("native snapshot memory: original limit changed before write"))
	}
	if value != next {
		if _, err := p.limit.Seek(0, io.SeekStart); err != nil {
			return err
		}
		body := strconv.FormatUint(next, 10) + "\n"
		n, err := io.WriteString(p.limit, body)
		if err != nil || n != len(body) {
			return errors.Join(err, io.ErrShortWrite)
		}
	}
	value, err = p.Read()
	if err != nil || value != next {
		return errors.Join(err, errors.New("native snapshot memory: original limit readback differs"))
	}
	return p.Check(context.Background())
}

func (p *linuxNativeSnapshotMemoryIO) Close() error {
	var err error
	if p.limit != nil {
		err = errors.Join(err, p.limit.Close())
		p.limit = nil
	}
	if p.directory != nil {
		err = errors.Join(err, p.directory.Close())
		p.directory = nil
	}
	if p.process != nil {
		err = errors.Join(err, p.process.Close())
		p.process = nil
	}
	return err
}

func (linuxNativeSnapshotMemory) Check(ctx context.Context, _ *JailerVMM, owner nativeLaunchRecord) error {
	file, err := openNativeSnapshotMemoryIO(ctx, owner)
	if err != nil {
		return err
	}
	return file.Close()
}

// The caller holds the original physical lock; the joined namespace handoff
// is mandatory. The planned frame is durable before any limit write or pause.
func (linuxNativeSnapshotMemory) Prepare(ctx context.Context, v *JailerVMM, owner nativeLaunchRecord) (grant nativeSnapshotMemoryAllowance, result error) {
	file, err := openNativeSnapshotMemoryIO(ctx, owner)
	if err != nil {
		return nil, err
	}
	defer func() {
		if result != nil {
			result = errors.Join(result, file.Close())
		}
	}()
	j := &nativeHostHelperJournal{owner: v.nativeRecovery.journal}
	record, err := j.snapshotMemoryRecord(owner)
	if err != nil {
		return nil, err
	}
	permit, _, err := nativeSnapshotPublicationKeys(ctx, owner.Lease)
	if err != nil {
		return nil, err
	}
	if record == nil || record.SnapshotOutput.Scope.CaptureID != permit.Capture.CaptureID || record.SnapshotOutput.Receipt == nil || record.SnapshotOutput.Memory != nil {
		return nil, errors.New("native snapshot memory: fresh original joined handoff is required")
	}
	if _, err := v.checkNativeSnapshotPublicationOwner(ctx, owner.Lease); err != nil {
		return nil, err
	}
	record.SnapshotOutput.Memory = &nativeSnapshotMemoryFrame{Group: file.group, Phase: nativeSnapshotMemoryPlanned}
	if err := j.write(owner, *record); err != nil {
		return nil, err
	}
	return &nativeSnapshotMemoryGrant{j: j, owner: owner, record: *record, io: file,
		checkRestoreOwner: func(cleanup context.Context) error {
			return v.checkNativeSnapshotMemoryRestorationOwner(cleanup, owner, permit, ctx)
		},
		checkOwner: func(ctx context.Context) error {
			_, err := v.checkNativeSnapshotPublicationOwner(ctx, owner.Lease)
			return err
		}}, nil
}

// A failed capture never reconstructs its live allowance after daemon death.
// Retirement removes only the recorded original empty cgroup and journals
// disposal before physical resource acknowledgement or generation replacement.
func (j *nativeHostHelperJournal) removeSnapshotMemoryCgroup(ctx context.Context, owner nativeLaunchRecord) (handled bool, result error) {
	lock, err := j.owner.lock(ctx, owner.Lease.Instance)
	if err != nil {
		return true, err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	current, err := j.owner.read(owner.Lease.Instance)
	if err != nil || current.Generation != owner.Generation || current.KernelBootID != owner.KernelBootID || current.PID != owner.PID || current.StartTime != owner.StartTime || !sameNativePhysicalLease(current.Lease, owner.Lease) || !current.Revoked || !current.ExitConfirmed {
		return true, errors.Join(err, errors.New("native snapshot memory: original retired physical owner changed"))
	}
	owner = current
	record, err := j.snapshotMemoryRecord(owner)
	if err != nil {
		return true, err
	}
	if record == nil || record.SnapshotOutput.Memory == nil {
		return false, nil
	}
	if !owner.Revoked || !owner.ExitConfirmed {
		return true, errors.New("native snapshot memory: live cgroup cannot be disposed")
	}
	if record.SnapshotOutput.Memory.Phase == nativeSnapshotMemoryRemoved {
		return true, nil
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	file, group, err := openNativeSnapshotMemoryCgroup(owner)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return true, err
	}
	if file == nil {
		if err := requireNativeSnapshotMemoryInodeGone(ctx, record.SnapshotOutput.Memory.Group); err != nil {
			return true, err
		}
	}
	if file != nil {
		defer func() { result = errors.Join(result, file.Close()) }()
		if group != record.SnapshotOutput.Memory.Group {
			return true, errors.New("native snapshot memory: replacement cgroup cannot be disposed")
		}
		entries, err := file.ReadDir(-1)
		if err != nil {
			return true, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				return true, errors.New("native snapshot memory: original scope retains child cgroups")
			}
		}
		parent, err := unix.Openat2(unix.AT_FDCWD, filepath.Dir(nativeCgroupScope(owner.Lease)), &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS})
		if err != nil {
			return true, err
		}
		parentFile := os.NewFile(uintptr(parent), "original-snapshot-cgroup-parent")
		defer func() { result = errors.Join(result, parentFile.Close()) }()
		var stat unix.Stat_t
		name := filepath.Base(group.Path)
		if err := unix.Fstatat(parent, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return true, err
		}
		if uint64(stat.Dev) != group.Device || stat.Ino != group.Inode {
			return true, errors.New("native snapshot memory: original scope changed before disposal")
		}
		if err := unix.Unlinkat(parent, name, unix.AT_REMOVEDIR); err != nil {
			return true, fmt.Errorf("native snapshot memory: remove original empty scope: %w", err)
		}
	}
	record.SnapshotOutput.Memory.Phase = nativeSnapshotMemoryRemoved
	return true, j.write(owner, *record)
}

// Missing names alone do not discharge original kernel ownership. Inspect the
// original unified hierarchy through directory FDs without following symlinks
// or crossing mounts. Concurrent disappearance, changed hierarchy and parser
// exhaustion all retain the scope for a later recovery pass.
func requireNativeSnapshotMemoryInodeGone(ctx context.Context, original nativeHostHelperGroup) (result error) {
	fd, err := unix.Openat2(unix.AT_FDCWD, cgroupRoot, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS})
	if err != nil {
		return err
	}
	root := os.NewFile(uintptr(fd), "original-snapshot-cgroup-hierarchy")
	defer func() { result = errors.Join(result, root.Close()) }()
	var stat unix.Stat_t
	var fs unix.Statfs_t
	if err := errors.Join(unix.Fstat(fd, &stat), unix.Fstatfs(fd, &fs)); err != nil {
		return err
	}
	if fs.Type != unix.CGROUP2_SUPER_MAGIC || uint64(stat.Dev) != original.Device {
		return errors.New("native snapshot memory: original unified hierarchy changed")
	}
	count := 0
	return inspectNativeSnapshotMemoryDirectories(ctx, root, original, &count)
}

func inspectNativeSnapshotMemoryDirectories(ctx context.Context, directory *os.File, original nativeHostHelperGroup, count *int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	*count = *count + 1
	if *count > api.NativeSnapshotCgroupInventoryMaxDirectories {
		return errors.New("native snapshot memory: hierarchy inventory exceeds its directory bound")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(directory.Fd()), &stat); err != nil {
		return err
	}
	if uint64(stat.Dev) != original.Device {
		return errors.New("native snapshot memory: hierarchy inventory crossed a mount")
	}
	if stat.Ino == original.Inode {
		return errors.New("native snapshot memory: original cgroup inode still exists")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := directory.ReadDir(api.NativeSnapshotCgroupInventoryReadBatch)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 {
				return errors.New("native snapshot memory: hierarchy inventory found a symlink")
			}
			if !entry.IsDir() {
				continue
			}
			fd, err := unix.Openat2(int(directory.Fd()), entry.Name(), &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV})
			if err != nil {
				return err
			}
			child := os.NewFile(uintptr(fd), "original-snapshot-cgroup-inventory")
			if err := errors.Join(inspectNativeSnapshotMemoryDirectories(ctx, child, original, count), child.Close()); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}

// Returning the original fence is a cleanup capability, including after the
// effect deadline. It grants no pause/create/resume/publication and still
// requires the original daemon, process, incoming, capture and durable intent.
func (v *JailerVMM) checkNativeSnapshotMemoryRestorationOwner(ctx context.Context, owner nativeLaunchRecord, permit nativeSnapshotCapturePermit, original context.Context) error {
	r := v.nativeRecovery
	if err := errors.Join(ctx.Err(), r.checkDaemonOwnership()); err != nil {
		return err
	}
	if r.generation(owner.Lease.Instance) != owner.Generation {
		return errors.New("native snapshot memory: original daemon producer changed")
	}
	current, err := r.journal.read(owner.Lease.Instance)
	if err != nil || !sameNativeSnapshotProcess(current, owner) {
		return errors.Join(err, errors.New("native snapshot memory: original physical owner changed"))
	}
	q := r.journal.qualifications(permit.Incoming.Execution.NodeID)
	incoming, err := q.read(owner.Lease.Instance)
	if err != nil || incoming != permit.Incoming {
		return errors.Join(err, errors.New("native snapshot memory: original incoming changed"))
	}
	capture, err := q.readCapture(incoming)
	if err != nil || capture != permit.Capture {
		return errors.Join(err, errors.New("native snapshot memory: original capture changed"))
	}
	publication, ok := original.Value(nativeSnapshotPublicationContextKey{}).(nativeSnapshotPublicationPermit)
	if !ok || publication.journal != r.publications {
		return errors.New("native snapshot memory: original publication intent unavailable")
	}
	return publication.journal.Require(ctx, publication.intent)
}
