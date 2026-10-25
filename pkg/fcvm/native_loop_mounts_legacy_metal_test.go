//go:build linux && metal

// adr: 568, 20261025090000
package fcvm

// The loop-mount staging producer vmmd ran before ADR-20261025090000. It is
// kept only so the native acceptance host can create the inherited mounts
// that recovery must still retire on an upgraded node.

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

type linuxNativeLoopReservation struct {
	source *os.File
	loop   *os.File
	device nativeLoopDevice
	id     string
}

func (r *linuxNativeLoopReservation) Device() nativeLoopDevice { return r.device }

func (r *linuxNativeLoopReservation) Close() error {
	return errors.Join(r.loop.Close(), r.source.Close())
}

func legacyNativeLoopPrepare(drive, id string) (reservation *linuxNativeLoopReservation, err error) {
	if !filepath.IsAbs(drive) || filepath.Clean(drive) != drive || !canonicalNativeHelperID(id) {
		return nil, errors.New("native loop mount: invalid source or session identity")
	}
	source, err := os.OpenFile(drive, os.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer func() {
		if reservation == nil {
			err = errors.Join(err, source.Close())
		}
	}()
	var stat unix.Stat_t
	if err := unix.Fstat(int(source.Fd()), &stat); err != nil {
		return nil, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Size <= 0 {
		return nil, errors.New("native loop mount: backing is not a nonempty regular file")
	}
	namespace, err := nativeLoopNamespaceIdentity()
	if err != nil {
		return nil, err
	}
	control, err := os.OpenFile("/dev/loop-control", os.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	number, ioctlErr := unix.IoctlRetInt(int(control.Fd()), unix.LOOP_CTL_GET_FREE)
	if err := errors.Join(ioctlErr, control.Close()); err != nil {
		return nil, err
	}
	loop, rdev, err := openNativeLoopDevice(number)
	if err != nil {
		return nil, err
	}
	return &linuxNativeLoopReservation{source: source, loop: loop, id: id, device: nativeLoopDevice{Number: number, Rdev: rdev, Source: nativeLoopIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}, Namespace: namespace}}, nil
}

func (r *linuxNativeLoopReservation) Configure() error {
	config := unix.LoopConfig{Fd: uint32(r.source.Fd()), Info: unix.LoopInfo64{Flags: unix.LO_FLAGS_AUTOCLEAR}}
	copy(config.Info.File_name[:], nativeLoopMarker+r.id)
	// No SET_FD/SET_STATUS fallback: daemon death between those calls would
	// leave an attached device without its ownership marker or AUTOCLEAR flag.
	return unix.IoctlLoopConfigure(int(r.loop.Fd()), &config)
}

func (r *linuxNativeLoopReservation) Mount(point string) (uint64, error) {
	if err := unix.Mount(nativeLoopDevicePath(r.device.Number), point, "ext4", unix.MS_NODEV|unix.MS_NOSUID|unix.MS_NOEXEC, "errors=remount-ro"); err != nil {
		return 0, err
	}
	entry, err := readNativeLoopMount(point)
	if err != nil {
		return 0, err
	}
	if entry == nil || entry.device != r.device.Rdev {
		return 0, errors.New("native loop mount: mounted image has no original block identity")
	}
	return entry.id, nil
}

// legacyNativeLoopSession is the retired journaled staging session.
func legacyNativeLoopSession(j *nativeLoopMountJournal, ctx context.Context, expected nativeLaunchRecord, drive string, fn func(string) error) (err error) {
	if j.backend == nil || fn == nil {
		return errors.New("native loop mount: native backend and writer are required")
	}
	lock, err := j.owner.lock(ctx, expected.Lease.Instance)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	owner, err := j.owner.read(expected.Lease.Instance)
	if err != nil {
		return err
	}
	if owner.Generation != expected.Generation || owner.KernelBootID != expected.KernelBootID || !sameNativePhysicalLease(owner.Lease, expected.Lease) || owner.Revoked || owner.Authorized || owner.ResourcesRemoved {
		return errors.New("native loop mount: prepared staging authority changed or was revoked")
	}
	if err := j.requireRemoved(owner); err != nil {
		return err
	}
	helpers := nativeHostHelperJournal{owner: j.owner, groups: j.helperGroups}
	if err := helpers.requireRemoved(owner); err != nil {
		return err
	}
	images := nativeImageSourceJournal{owner: j.owner, backend: j.owner.imageSources}
	if err := images.require(ctx, owner, false); err != nil {
		return err
	}
	tun := nativeTunBindJournal{owner: j.owner, backend: j.owner.tunBinds}
	if err := tun.require(owner, false); err != nil {
		return err
	}
	id := uuid.NewString()
	reservation, err := legacyNativeLoopPrepare(drive, id)
	if err != nil {
		return err
	}
	defer func() {
		if reservation != nil {
			err = errors.Join(err, reservation.Close())
		}
	}()
	record := nativeLoopMountRecord{ID: id, Owner: owner, Device: reservation.Device()}
	for _, path := range []string{filepath.Dir(j.root(owner.Generation)), j.root(owner.Generation), filepath.Dir(j.point(record))} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
		if err := checkNativeJournalPath(path, true); err != nil {
			return err
		}
	}
	if err := j.write(owner, record); err != nil {
		return err
	}
	defer func() {
		// Close the reservation before probing retirement: AUTOCLEAR can then
		// detach an attachment that never reached mount. Never remove a tree.
		closeErr := reservation.Close()
		reservation = nil
		err = errors.Join(err, closeErr, j.removeLocked(context.WithoutCancel(ctx), owner, record))
	}()
	if err := os.Mkdir(j.point(record), 0o700); err != nil {
		return err
	}
	record.Directory, err = nativeLoopDirectoryIdentity(j.point(record))
	if err != nil {
		return err
	}
	if err := j.write(owner, record); err != nil {
		return err
	}
	parent, err := os.Open(filepath.Dir(j.point(record)))
	if err != nil {
		return err
	}
	if err := errors.Join(parent.Sync(), parent.Close()); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := reservation.Configure(); err != nil {
		return err
	}
	record.MountID, err = reservation.Mount(j.point(record))
	if err != nil {
		return err
	}
	if record.MountID == 0 {
		return errors.New("native loop mount: mount has no kernel identity")
	}
	if err := j.write(owner, record); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.Join(fn(j.point(record)), ctx.Err())
}
