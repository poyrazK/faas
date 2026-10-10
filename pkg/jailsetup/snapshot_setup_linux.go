//go:build linux

package jailsetup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"

	"golang.org/x/sys/unix"
)

func awaitSnapshotOutputSetup(args []string) error {
	if len(args) != 4 || args[2] != "3" {
		return errors.New("native snapshot handoff: exact gate and scope required")
	}
	var scope SnapshotOutputScope
	if err := json.Unmarshal([]byte(args[3]), &scope); err != nil {
		return err
	}
	if err := scope.Validate(); err != nil {
		return err
	}
	gate := os.NewFile(3, "native-snapshot-gate")
	err := AwaitLaunchGate(gate)
	if err := errors.Join(err, gate.Close()); err != nil {
		return err
	}
	return setupPinnedSnapshotOutputs(scope)
}

// One-shot process; namespace/root changes never return to a runtime worker.
func setupPinnedSnapshotOutputs(scope SnapshotOutputScope) (result error) {
	runtime.LockOSThread()
	procFD, err := unix.Open("/proc/thread-self", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, unix.Close(procFD)) }()
	for i, expected := range scope.Identities() {
		fd := 4 + i
		defer func() { result = errors.Join(result, unix.Close(fd)) }()
		actual, err := deviceFDIdentity(fd)
		if err != nil || actual != expected {
			return errors.Join(err, errors.New("native snapshot handoff: original descriptor changed"))
		}
	}
	if err := unix.PidfdSendSignal(6, 0, nil, 0); err != nil {
		return err
	}
	pidInfo, err := readDeviceProcFile(procFD, "fdinfo/6")
	if err != nil || !devicePIDHandleMatches(pidInfo, scope.PID) {
		return errors.Join(err, errors.New("native snapshot handoff: original task changed"))
	}
	var root unix.Stat_t
	if err := unix.Fstat(4, &root); err != nil {
		return err
	}
	if root.Mode&unix.S_IFMT != unix.S_IFDIR || root.Mode&0o022 != 0 || root.Uid != uint32(scope.UID) || root.Gid != uint32(scope.GID) {
		return errors.New("native snapshot handoff: original jail credentials changed")
	}
	for i := 0; i < 2; i++ {
		var source unix.Stat_t
		if err := unix.Fstat(7+i, &source); err != nil {
			return err
		}
		if source.Mode&unix.S_IFMT != unix.S_IFREG || source.Mode&0o7777 != 0o600 || source.Uid != uint32(scope.UID) || source.Gid != uint32(scope.GID) || source.Size != 0 || source.Nlink != 1 {
			return errors.New("native snapshot handoff: output is not the original empty private inode")
		}
	}
	// open_tree can clone only mounts in the caller's current namespace.
	// These sources belong to the original host output anchors, unlike the
	// root and target descriptors belonging to the VM. Pin detached copies
	// before setns; move_mount transfers only those copies into the VM.
	var mounts [2]int
	for i := range mounts {
		mount, err := prepareSnapshotOutputMount(7 + i)
		if err != nil {
			return fmt.Errorf("native snapshot handoff: pin original output %d: %w", i, err)
		}
		mounts[i] = mount
		defer func() { result = errors.Join(result, unix.Close(mount)) }()
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
	if err := errors.Join(statErr, unix.Close(nsFD)); err != nil {
		return err
	}
	if identity != scope.Namespace {
		return errors.New("native snapshot handoff: namespace changed")
	}
	if err := unix.Fchdir(4); err != nil {
		return err
	}
	if err := unix.Chroot("."); err != nil {
		return err
	}
	if err := unix.Chdir("/"); err != nil {
		return err
	}
	info, err := readDeviceProcFile(procFD, "fdinfo/4")
	id, valid := deviceFDMountID(info)
	if err != nil || !valid || id != scope.RootMountID {
		return errors.Join(err, errors.New("native snapshot handoff: original root mount changed"))
	}
	data, err := readDeviceProcFile(procFD, "mountinfo")
	if err != nil || !deviceMountPresent(data, scope.RootMountID) {
		return errors.Join(err, errors.New("native snapshot handoff: root is outside original namespace"))
	}
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return err
	}
	var targets [2]int
	for i, name := range scope.Names() {
		fd, err := unix.Openat(4, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		targets[i] = fd
		defer func() { result = errors.Join(result, unix.Close(fd)) }()
		actual, err := deviceFDIdentity(fd)
		var target unix.Stat_t
		statErr := unix.Fstat(fd, &target)
		if err != nil || statErr != nil || actual != scope.Targets[i] || target.Mode&unix.S_IFMT != unix.S_IFREG || target.Size != 0 || target.Mode&0o7777 != 0o600 || target.Uid != 0 || target.Nlink != 1 {
			return errors.Join(err, statErr, errors.New("native snapshot handoff: original placeholder changed"))
		}
	}
	if err := unix.PidfdSendSignal(6, 0, nil, 0); err != nil {
		return err
	}
	var receipt SnapshotOutputReceipt
	receipt.Scope = scope
	for i, target := range targets {
		if err := unix.MoveMount(mounts[i], "", target, "", unix.MOVE_MOUNT_F_EMPTY_PATH|unix.MOVE_MOUNT_T_EMPTY_PATH); err != nil {
			return fmt.Errorf("native snapshot handoff: attach original output %d: %w", i, err)
		}
		// Reopen only the derived name inside the pinned original root, never
		// a host path. Verify the new inode and its own namespace mount ID.
		fd, err := unix.Openat(4, scope.Names()[i], unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		actual, statErr := deviceFDIdentity(fd)
		info, infoErr := readDeviceProcFile(procFD, "fdinfo/"+strconv.Itoa(fd))
		if err := errors.Join(statErr, infoErr, unix.Close(fd)); err != nil {
			return err
		}
		id, valid := deviceFDMountID(info)
		data, err := readDeviceProcFile(procFD, "mountinfo")
		if err != nil || actual != scope.Outputs[i] || !valid || !snapshotMountAccess(data, id) {
			return errors.Join(err, errors.New("native snapshot handoff: output mount proof incomplete"))
		}
		receipt.MountIDs[i] = id
	}
	if err := unix.PidfdSendSignal(6, 0, nil, 0); err != nil {
		return err
	}
	if err := receipt.Validate(scope); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(receipt)
}

func prepareSnapshotOutputMount(source int) (int, error) {
	mount, err := unix.OpenTree(source, "", unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC|unix.AT_EMPTY_PATH)
	if err != nil {
		return -1, err
	}
	attr := unix.MountAttr{Attr_set: unix.MOUNT_ATTR_NOSUID | unix.MOUNT_ATTR_NODEV | unix.MOUNT_ATTR_NOEXEC, Attr_clr: unix.MOUNT_ATTR_RDONLY}
	if err := unix.MountSetattr(mount, "", unix.AT_EMPTY_PATH, &attr); err != nil {
		return -1, errors.Join(err, unix.Close(mount))
	}
	return mount, nil
}
