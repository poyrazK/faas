//go:build linux

package fcvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const nativeLoopMarker = "gregale-loop:"

type linuxNativeLoopMounts struct{}

func newNativeLoopMountBackend() nativeLoopMountBackend { return linuxNativeLoopMounts{} }

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

func nativeLoopNamespaceIdentity() (nativeLoopIdentity, error) {
	var stat unix.Stat_t
	if err := unix.Stat("/proc/self/ns/mnt", &stat); err != nil {
		return nativeLoopIdentity{}, err
	}
	return nativeLoopIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}, nil
}

func nativeLoopDirectoryIdentity(path string) (nativeLoopIdentity, error) {
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		return nativeLoopIdentity{}, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0o077 != 0 || stat.Uid != uint32(os.Geteuid()) {
		return nativeLoopIdentity{}, errors.New("native loop mount: point is not a private owned directory")
	}
	return nativeLoopIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}, nil
}

func nativeLoopDevicePath(number int) string { return "/dev/loop" + strconv.Itoa(number) }

func openNativeLoopDevice(number int) (*os.File, uint64, error) {
	file, err := os.OpenFile(nativeLoopDevicePath(number), os.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, 0, err
	}
	var stat unix.Stat_t
	err = unix.Fstat(int(file.Fd()), &stat)
	if err == nil && (stat.Mode&unix.S_IFMT != unix.S_IFBLK || unix.Major(uint64(stat.Rdev)) != 7 || uint64(unix.Minor(uint64(stat.Rdev))) != uint64(number)) {
		err = errors.New("native loop mount: loop path differs from its block device")
	}
	if err != nil {
		return nil, 0, errors.Join(err, file.Close())
	}
	return file, uint64(stat.Rdev), nil
}

func (linuxNativeLoopMounts) Prepare(drive, id string) (reservation nativeLoopReservation, err error) {
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
	if err := unix.Mount(nativeLoopDevicePath(r.device.Number), point, "ext4", unix.MS_NODEV|unix.MS_NOSUID|unix.MS_NOEXEC, ""); err != nil {
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

func matchesNativeLoop(info *unix.LoopInfo64, record nativeLoopMountRecord) bool {
	name := strings.TrimRight(string(info.File_name[:]), "\x00")
	return name == nativeLoopMarker+record.ID && info.Number == uint32(record.Device.Number) && info.Device == record.Device.Source.Device && info.Inode == record.Device.Source.Inode && info.Flags == unix.LO_FLAGS_AUTOCLEAR && info.Offset == 0 && info.Sizelimit == 0
}

type nativeLoopMountEntry struct {
	id     uint64
	device uint64
}

func readNativeLoopMount(point string) (*nativeLoopMountEntry, error) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	return parseNativeLoopMount(data, point)
}

func parseNativeLoopMount(data []byte, point string) (*nativeLoopMountEntry, error) {
	below, err := nativeMountsBelow(data, point)
	if err != nil {
		return nil, err
	}
	if len(below) == 0 {
		return nil, nil
	}
	if len(below) != 1 || below[0] != point {
		return nil, errors.New("native loop mount: unknown nested or stacked mounts")
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		path, err := decodeNativeMountPath(fields[4])
		if err != nil {
			return nil, err
		}
		if path != point {
			continue
		}
		id, err := strconv.ParseUint(fields[0], 10, 64)
		major, minor, ok := strings.Cut(fields[2], ":")
		maj, majErr := strconv.ParseUint(major, 10, 32)
		min, minErr := strconv.ParseUint(minor, 10, 32)
		sep := slices.Index(fields, "-")
		options := strings.Split(fields[5], ",")
		if errors.Join(err, majErr, minErr) != nil || !ok || id == 0 || sep < 6 || fields[3] != "/" || fields[sep+1] != "ext4" {
			return nil, errors.New("native loop mount: invalid ext4 mount identity")
		}
		for _, option := range []string{"rw", "nodev", "nosuid", "noexec"} {
			if !slices.Contains(options, option) {
				return nil, errors.New("native loop mount: unsafe mount options")
			}
		}
		return &nativeLoopMountEntry{id: id, device: unix.Mkdev(uint32(maj), uint32(min))}, nil
	}
	return nil, errors.New("native loop mount: missing mount identity")
}

func checkNativeLoopNamespace(record nativeLoopMountRecord) error {
	identity, err := nativeLoopNamespaceIdentity()
	if err != nil {
		return err
	}
	if identity != record.Device.Namespace {
		return errors.New("native loop mount: recovery cannot inspect the original mount namespace")
	}
	return nil
}

func (linuxNativeLoopMounts) Retire(ctx context.Context, record nativeLoopMountRecord, point string) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkNativeLoopNamespace(record); err != nil {
		return err
	}
	entry, err := readNativeLoopMount(point)
	if err != nil {
		return err
	}
	loop, rdev, err := openNativeLoopDevice(record.Device.Number)
	if errors.Is(err, os.ErrNotExist) && entry == nil {
		return nil
	} else if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, loop.Close()) }()
	if rdev != record.Device.Rdev {
		return errors.New("native loop mount: original block device changed")
	}
	info, statusErr := unix.IoctlLoopGetStatus64(int(loop.Fd()))
	if errors.Is(statusErr, unix.ENXIO) && entry == nil {
		return nil
	} else if statusErr != nil {
		return statusErr
	}
	if !matchesNativeLoop(info, record) {
		if entry == nil && !strings.HasPrefix(strings.TrimRight(string(info.File_name[:]), "\x00"), nativeLoopMarker+record.ID) {
			// Shared loop numbers can be reused after AUTOCLEAR. The original
			// token is absent; never detach somebody else's attachment.
			return nil
		}
		return errors.New("native loop mount: attachment differs from its original identity")
	}
	if entry != nil {
		if record.Directory.Inode == 0 || entry.device != record.Device.Rdev || record.MountID != 0 && entry.id != record.MountID {
			return errors.New("native loop mount: mounted resource was replaced")
		}
		if err := unix.Unmount(point, 0); err != nil {
			return err
		}
		if entry, err := readNativeLoopMount(point); err != nil || entry != nil {
			return errors.Join(err, errors.New("native loop mount: unmount was not confirmed"))
		}
	}
	// An extra open FD can defer detach. Removed checks the token after this
	// FD closes, so a successful ioctl alone never grants acknowledgement.
	return unix.IoctlSetInt(int(loop.Fd()), unix.LOOP_CLR_FD, 0)
}

func (linuxNativeLoopMounts) Removed(record nativeLoopMountRecord, point string) (err error) {
	if err := checkNativeLoopNamespace(record); err != nil {
		return err
	}
	entry, err := readNativeLoopMount(point)
	if err != nil {
		return err
	}
	if entry != nil {
		return errors.New("native loop mount: acknowledged mount reappeared")
	}
	if _, err := os.Lstat(point); !errors.Is(err, os.ErrNotExist) {
		return errors.Join(err, errors.New("native loop mount: original mountpoint remains"))
	}
	loop, rdev, err := openNativeLoopDevice(record.Device.Number)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, loop.Close()) }()
	if rdev != record.Device.Rdev {
		return errors.New("native loop mount: original block device changed")
	}
	info, err := unix.IoctlLoopGetStatus64(int(loop.Fd()))
	if errors.Is(err, unix.ENXIO) {
		return nil
	} else if err != nil {
		return err
	}
	if strings.TrimRight(string(info.File_name[:]), "\x00") == nativeLoopMarker+record.ID {
		return errors.New("native loop mount: original loop attachment remains")
	}
	return nil
}

func (linuxNativeLoopMounts) Inventory(records []nativeLoopMountRecord) error {
	byID := make(map[string]nativeLoopMountRecord, len(records))
	for _, record := range records {
		if _, found := byID[record.ID]; found {
			return errors.New("native loop mount: duplicate session ownership")
		}
		byID[record.ID] = record
		if record.Removed {
			// Journal point paths are checked by the caller's requireRemoved.
			if err := checkNativeLoopNamespace(record); err != nil {
				return err
			}
		}
	}
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		value, ok := strings.CutPrefix(entry.Name(), "loop")
		if !ok {
			continue
		}
		number, err := strconv.Atoi(value)
		if err != nil || number < 0 || strconv.Itoa(number) != value {
			return errors.New("native loop mount: invalid kernel loop inventory")
		}
		file, rdev, err := openNativeLoopDevice(number)
		if err != nil {
			return err
		}
		info, statusErr := unix.IoctlLoopGetStatus64(int(file.Fd()))
		if err := file.Close(); err != nil {
			return err
		}
		if errors.Is(statusErr, unix.ENXIO) {
			continue
		} else if statusErr != nil {
			return statusErr
		}
		id, managed := strings.CutPrefix(strings.TrimRight(string(info.File_name[:]), "\x00"), nativeLoopMarker)
		if !managed {
			continue
		}
		record, found := byID[id]
		if !found || record.Removed || rdev != record.Device.Rdev || !matchesNativeLoop(info, record) {
			return fmt.Errorf("native loop mount: kernel attachment %s has no unfinished original owner", id)
		}
		if err := checkNativeLoopNamespace(record); err != nil {
			return err
		}
	}
	return nil
}
