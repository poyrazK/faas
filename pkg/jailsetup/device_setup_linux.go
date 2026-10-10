//go:build linux

package jailsetup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"

	"golang.org/x/sys/unix"
)

func deviceFDIdentity(fd int) (DeviceFDIdentity, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return DeviceFDIdentity{}, err
	}
	return DeviceFDIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}, nil
}

func awaitDeviceSetup(args []string) error {
	if len(args) != 4 || args[2] != "3" {
		return errors.New("native jail device setup requires gate 3 and one exact scope")
	}
	var scope DeviceSetupScope
	if err := json.Unmarshal([]byte(args[3]), &scope); err != nil {
		return err
	}
	if err := scope.Validate(); err != nil {
		return err
	}
	gate := os.NewFile(3, "native-device-gate")
	if gate == nil {
		return errors.New("native jail device gate missing")
	}
	err := AwaitLaunchGate(gate)
	if closeErr := gate.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return setupPinnedDevices(scope)
}

// This command is a one-shot process. Never return its changed thread to a
// general-purpose runtime pool. Root/TUN paths resolve through pinned FDs.
func setupPinnedDevices(scope DeviceSetupScope) (result error) {
	runtime.LockOSThread()
	// The real jailer pivots away from the host root and detaches /proc.
	// Pin this locked thread's proc directory before setns; metadata remains
	// about this thread after it enters the original VM's namespace. Never
	// mount host procfs into a guest or reopen an absolute procfs pathname.
	procFD, err := unix.Open("/proc/thread-self", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("native jail device setup: pin helper proc metadata: %w", err)
	}
	defer func() { result = errors.Join(result, unix.Close(procFD)) }()
	for i, expected := range scope.Identities() {
		actual, err := deviceFDIdentity(4 + i)
		if err != nil || actual != expected {
			return errors.Join(err, errors.New("native jail device setup: inherited descriptor identity changed"))
		}
		fd := 4 + i
		defer func() { result = errors.Join(result, unix.Close(fd)) }()
	}
	if err := unix.PidfdSendSignal(6, 0, nil, 0); err != nil {
		return fmt.Errorf("native jail device setup: original task is unavailable: %w", err)
	}
	pidInfo, err := readDeviceProcFile(procFD, "fdinfo/6")
	if err != nil {
		return err
	}
	if !devicePIDHandleMatches(pidInfo, scope.PID) {
		return errors.New("native jail device setup: pidfd targets another incarnation")
	}
	var rootStat, tunStat unix.Stat_t
	if err := unix.Fstat(4, &rootStat); err != nil {
		return err
	}
	if err := unix.Fstat(7, &tunStat); err != nil {
		return err
	}
	if rootStat.Mode&unix.S_IFMT != unix.S_IFDIR || rootStat.Mode&0o022 != 0 || rootStat.Uid != 0 && rootStat.Uid != uint32(scope.UID) || tunStat.Mode&unix.S_IFMT != unix.S_IFCHR || uint64(tunStat.Rdev) != unix.Mkdev(10, 200) || tunStat.Mode&0o777 != scope.TunMode || tunStat.Mode&0o7000 != 0 {
		return errors.New("native jail device setup: original root or TUN authority changed")
	}
	if err := unix.Unshare(unix.CLONE_FS); err != nil {
		return err
	}
	if err := unix.Setns(5, unix.CLONE_NEWNS); err != nil {
		return err
	}
	nsFD, err := unix.Openat(procFD, "ns/mnt", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	identity, statErr := deviceFDIdentity(nsFD)
	closeErr := unix.Close(nsFD)
	if err := errors.Join(statErr, closeErr); err != nil {
		return err
	}
	if identity != scope.Namespace {
		return errors.New("native jail device setup: original namespace handoff changed")
	}
	// Confine this one-shot helper's FS root to the original pinned jail too.
	// mountinfo then describes mounts visible from that root, including the
	// original jailer's private root after pivot_root.
	if err := unix.Fchdir(4); err != nil {
		return err
	}
	if err := unix.Chroot("."); err != nil {
		return err
	}
	if err := unix.Chdir("/"); err != nil {
		return err
	}
	data, err := readDeviceProcFile(procFD, "mountinfo")
	if err != nil {
		return err
	}
	for _, input := range []struct {
		fd int
		id uint64
	}{{4, scope.RootMountID}, {7, scope.TunMountID}} {
		info, err := readDeviceProcFile(procFD, "fdinfo/"+strconv.Itoa(input.fd))
		id, valid := deviceFDMountID(info)
		if err != nil || !valid || id != input.id {
			return errors.Join(err, errors.New("native jail device setup: original input mount identity changed"))
		}
	}
	for _, id := range []uint64{scope.RootMountID, scope.TunMountID} {
		if !deviceMountPresent(data, id) {
			return errors.New("native jail device setup: inputs do not belong to the original namespace")
		}
	}
	if err := unix.PidfdSendSignal(6, 0, nil, 0); err != nil {
		return err
	}
	// Confine propagation before the first mount; descendants can produce only
	// private effects belonging to this original namespace.
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return err
	}
	devFD, err := unix.Openat(4, "dev", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	if err := mountDeviceTmpfs(devFD); err != nil {
		_ = unix.Close(devFD)
		return err
	}
	if err := unix.Close(devFD); err != nil {
		return err
	}
	devFD, err = unix.Openat(4, "dev", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, unix.Close(devFD)) }()
	if err := unix.Mkdirat(devFD, "net", 0o755); err != nil {
		return err
	}
	netFD, err := unix.Openat(devFD, "net", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, unix.Close(netFD)) }()
	tunFD, err := unix.Openat(netFD, "tun", unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	if err := errors.Join(mountDeviceTun(tunFD), unix.Close(tunFD)); err != nil {
		return err
	}
	if err := unix.Mknodat(devFD, "kvm", unix.S_IFCHR|0o660, int(unix.Mkdev(10, 232))); err != nil {
		return err
	}
	if err := unix.Fchownat(devFD, "kvm", scope.UID, scope.GID, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	var kvmStat unix.Stat_t
	if err := unix.Fstatat(devFD, "kvm", &kvmStat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	data, err = readDeviceProcFile(procFD, "mountinfo")
	if err != nil {
		return err
	}
	devMount, err := deviceMountAtFD(procFD, devFD, data)
	if err != nil {
		return err
	}
	tunFD, err = unix.Openat(netFD, "tun", unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	tunMount, mountErr := deviceMountAtFD(procFD, tunFD, data)
	actual, statErr := deviceFDIdentity(tunFD)
	if err := errors.Join(mountErr, statErr, unix.Close(tunFD)); err != nil {
		return err
	}
	if actual != scope.Tun {
		return errors.New("native jail device setup: target lost original TUN")
	}
	// Test the exact credentials Firecracker uses, with no host groups.
	if err := unix.Setgroups([]int{scope.GID}); err != nil {
		return err
	}
	if err := unix.Setresgid(scope.GID, scope.GID, scope.GID); err != nil {
		return err
	}
	if err := unix.Setresuid(scope.UID, scope.UID, scope.UID); err != nil {
		return err
	}
	tunFD, err = unix.Openat(netFD, "tun", unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("native jail device setup: jail UID cannot open TUN: %w", err)
	}
	if err := unix.Close(tunFD); err != nil {
		return err
	}
	kvmFD, err := unix.Openat(devFD, "kvm", unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	api, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(kvmFD), 0xAE00, 0)
	if err := errors.Join(errnoError(errno), unix.Close(kvmFD)); err != nil {
		return err
	}
	receipt := DeviceSetupReceipt{Scope: scope, DevMountID: devMount, TunMountID: tunMount, KVM: DeviceFDIdentity{Device: uint64(kvmStat.Dev), Inode: kvmStat.Ino}, KVMAPI: int(api), TunAccessible: true}
	if err := receipt.Validate(scope); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(receipt)
}

func errnoError(errno unix.Errno) error {
	if errno == 0 {
		return nil
	}
	return errno
}

func deviceMountAtFD(procFD, fd int, data []byte) (uint64, error) {
	info, err := readDeviceProcFile(procFD, "fdinfo/"+strconv.Itoa(fd))
	if err != nil {
		return 0, err
	}
	id, valid := deviceFDMountID(info)
	if valid && deviceMountPresent(data, id) && deviceMountAccess(data, id) {
		return id, nil
	}
	return 0, errors.New("native jail device setup: input has no mount identity in original namespace")
}

func readDeviceProcFile(procFD int, name string) (data []byte, err error) {
	fd, err := unix.Openat(procFD, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("native jail device setup: original helper %s: %w", name, err)
	}
	file := os.NewFile(uintptr(fd), "native-device-proc-metadata")
	defer func() { err = errors.Join(err, file.Close()) }()
	const maxMetadata = 4 << 20
	data, err = io.ReadAll(io.LimitReader(file, maxMetadata+1))
	if len(data) > maxMetadata {
		return nil, errors.New("native jail device setup: proc metadata exceeds bound")
	}
	return data, err
}

// Detached mount FDs replace /proc/self/fd mount pathnames. Every effect is
// attached through an original directory/file descriptor in the pinned VM
// namespace; unsupported kernels fail closed without a pathname fallback.
func mountDeviceTmpfs(target int) (err error) {
	fd, err := unix.Fsopen("tmpfs", unix.FSOPEN_CLOEXEC)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unix.Close(fd)) }()
	if err := unix.FsconfigSetString(fd, "mode", "0755"); err != nil {
		return err
	}
	if err := unix.FsconfigCreate(fd); err != nil {
		return err
	}
	mount, err := unix.Fsmount(fd, unix.FSMOUNT_CLOEXEC, unix.MOUNT_ATTR_NOSUID|unix.MOUNT_ATTR_NOEXEC)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unix.Close(mount)) }()
	return unix.MoveMount(mount, "", target, "", unix.MOVE_MOUNT_F_EMPTY_PATH|unix.MOVE_MOUNT_T_EMPTY_PATH)
}

func mountDeviceTun(target int) (err error) {
	mount, err := unix.OpenTree(7, "", unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC|unix.AT_EMPTY_PATH)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unix.Close(mount)) }()
	attr := unix.MountAttr{Attr_set: unix.MOUNT_ATTR_NOSUID | unix.MOUNT_ATTR_NOEXEC, Attr_clr: unix.MOUNT_ATTR_NODEV | unix.MOUNT_ATTR_RDONLY}
	if err := unix.MountSetattr(mount, "", unix.AT_EMPTY_PATH, &attr); err != nil {
		return err
	}
	return unix.MoveMount(mount, "", target, "", unix.MOVE_MOUNT_F_EMPTY_PATH|unix.MOVE_MOUNT_T_EMPTY_PATH)
}
