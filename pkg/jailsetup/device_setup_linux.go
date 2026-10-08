//go:build linux

package jailsetup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func deviceFDIdentity(fd int) (DeviceFDIdentity, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return DeviceFDIdentity{}, err
	}
	return DeviceFDIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}, nil
}

func deviceFDPath(fd int) string { return "/proc/self/fd/" + strconv.Itoa(fd) }

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
	pidInfo, err := os.ReadFile("/proc/self/fdinfo/6")
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
	nsFD, err := unix.Open("/proc/thread-self/ns/mnt", unix.O_RDONLY|unix.O_CLOEXEC, 0)
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
	data, err := os.ReadFile("/proc/thread-self/mountinfo")
	if err != nil {
		return err
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
	devTarget := deviceFDPath(4) + "/dev"
	if err := unix.Mount("tmpfs", deviceFDPath(devFD), "tmpfs", unix.MS_NOSUID|unix.MS_NOEXEC, "mode=0755"); err != nil {
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
	netFD, err := createDeviceNet(devFD)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, unix.Close(netFD)) }()
	tunFD, err := unix.Openat(netFD, "tun", unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	if err := unix.Close(tunFD); err != nil {
		return err
	}
	tunTarget := devTarget + "/net/tun"
	if err := unix.Mount(deviceFDPath(7), tunTarget, "", unix.MS_BIND, ""); err != nil {
		return err
	}
	attr := unix.MountAttr{Attr_set: unix.MOUNT_ATTR_NOSUID | unix.MOUNT_ATTR_NOEXEC, Attr_clr: unix.MOUNT_ATTR_NODEV | unix.MOUNT_ATTR_RDONLY}
	if err := unix.MountSetattr(unix.AT_FDCWD, tunTarget, 0, &attr); err != nil {
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
	data, err = os.ReadFile("/proc/thread-self/mountinfo")
	if err != nil {
		return err
	}
	devMount, err := deviceMountAtFD(devFD, data)
	if err != nil {
		return err
	}
	tunFD, err = unix.Openat(netFD, "tun", unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	tunMount, mountErr := deviceMountAtFD(tunFD, data)
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

// The helper inherits vmmd's umask. Establish traversal on the newly created
// private directory explicitly before Firecracker drops to its jail UID.
// Pin the directory so a symlink cannot redirect the permission change.
func createDeviceNet(devFD int) (int, error) {
	if err := unix.Mkdirat(devFD, "net", 0o755); err != nil {
		return -1, err
	}
	fd, err := unix.Openat(devFD, "net", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	if err := unix.Fchmod(fd, 0o755); err != nil {
		return -1, errors.Join(err, unix.Close(fd))
	}
	return fd, nil
}

func prepareDeviceNet(devTarget string) error {
	fd, err := unix.Open(devTarget, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	netFD, setupErr := createDeviceNet(fd)
	if netFD >= 0 {
		setupErr = errors.Join(setupErr, unix.Close(netFD))
	}
	return errors.Join(setupErr, unix.Close(fd))
}

func errnoError(errno unix.Errno) error {
	if errno == 0 {
		return nil
	}
	return errno
}

func deviceMountAtFD(fd int, data []byte) (uint64, error) {
	info, err := os.ReadFile("/proc/self/fdinfo/" + strconv.Itoa(fd))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(info), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "mnt_id:" {
			id, err := strconv.ParseUint(fields[1], 10, 64)
			if err == nil && id != 0 && deviceMountPresent(data, id) && deviceMountAccess(data, id) {
				return id, nil
			}
		}
	}
	return 0, errors.New("native jail device setup: input has no mount identity in original namespace")
}
