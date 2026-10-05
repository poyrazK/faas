//go:build linux

package fcvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/jailsetup"
	"golang.org/x/sys/unix"
)

type linuxNativeJailDevices struct{ base string }

func newNativeJailDeviceBackend(base string) nativeJailDeviceBackend {
	return linuxNativeJailDevices{base: base}
}

type linuxNativeJailInputs struct {
	scope jailsetup.DeviceSetupScope
	files []*os.File
}

func (p *linuxNativeJailInputs) Scope() jailsetup.DeviceSetupScope { return p.scope }
func (p *linuxNativeJailInputs) Files() []*os.File                 { return p.files }
func (p *linuxNativeJailInputs) Close() error {
	var err error
	for _, file := range p.files {
		err = errors.Join(err, file.Close())
	}
	p.files = nil
	return err
}
func nativeDeviceFileIdentity(file *os.File) (jailsetup.DeviceFDIdentity, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return jailsetup.DeviceFDIdentity{}, err
	}
	return jailsetup.DeviceFDIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}, nil
}
func nativeDeviceFileMountID(file *os.File) (uint64, error) {
	data, err := os.ReadFile("/proc/self/fdinfo/" + strconv.Itoa(int(file.Fd())))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "mnt_id:" {
			id, err := strconv.ParseUint(fields[1], 10, 64)
			if err == nil && id != 0 {
				return id, nil
			}
		}
	}
	return 0, errors.New("native jail device setup: descriptor has no mount identity")
}
func nativeDeviceProcStart(dir *os.File, pid int) (uint64, error) {
	data, err := os.ReadFile(nativeImageFDPath(dir) + "/stat")
	if err != nil {
		return 0, err
	}
	return nativeProcessStartTime(string(data), pid)
}
func nativeDevicePIDAlive(file *os.File) error {
	poll := []unix.PollFd{{Fd: int32(file.Fd()), Events: unix.POLLIN}}
	if _, err := unix.Poll(poll, 0); err != nil {
		return err
	}
	if poll[0].Revents != 0 {
		return errors.New("native jail device setup: original task has exited")
	}
	return unix.PidfdSendSignal(int(file.Fd()), 0, nil, 0)
}

func (b linuxNativeJailDevices) Prepare(ctx context.Context, owner nativeLaunchRecord, root string, source nativeTunSource) (inputs nativeJailDeviceInputs, err error) {
	if !owner.Authorized || owner.Revoked || owner.ResourcesRemoved || owner.Lease.Networkless {
		return nil, errors.New("native jail device setup: original authorized task is unavailable")
	}
	if err := linuxNativeTunBinds(b).validateRoot(owner, root); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	expectedRoot, err := os.OpenFile(root, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, expectedRoot.Close()) }()
	if err := checkNativeImageRoot(root, nativePreparedDeviceOwner(owner)); err != nil {
		return nil, err
	}
	pidFD, err := unix.PidfdOpen(owner.PID, 0)
	if err != nil {
		return nil, err
	}
	process := os.NewFile(uintptr(pidFD), "original-device-process")
	defer func() {
		if inputs == nil {
			err = errors.Join(err, process.Close())
		}
	}()
	proc, err := os.OpenFile("/proc/"+strconv.Itoa(owner.PID), unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, proc.Close()) }()
	start, err := nativeDeviceProcStart(proc, owner.PID)
	if err != nil || start != owner.StartTime {
		return nil, errors.Join(err, errors.New("native jail device setup: original process incarnation changed"))
	}
	hostNS, err := nativeLoopNamespaceIdentity()
	if err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := nativeDevicePIDAlive(process); err != nil {
			return nil, err
		}
		nsFD, err := unix.Openat(int(proc.Fd()), "ns/mnt", unix.O_RDONLY|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, err
		}
		namespace := os.NewFile(uintptr(nsFD), "original-device-namespace")
		identity, err := nativeDeviceFileIdentity(namespace)
		if err != nil {
			return nil, errors.Join(err, namespace.Close())
		}
		rootFD, rootErr := unix.Openat(int(proc.Fd()), "root", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if rootErr != nil {
			return nil, errors.Join(rootErr, namespace.Close())
		}
		originalRoot := os.NewFile(uintptr(rootFD), "original-device-root")
		actualInfo, actualErr := originalRoot.Stat()
		expectedInfo, expectedErr := expectedRoot.Stat()
		if err := errors.Join(actualErr, expectedErr); err != nil {
			return nil, errors.Join(err, namespace.Close(), originalRoot.Close())
		}
		if identity.Inode == hostNS.Inode && identity.Device == hostNS.Device || !os.SameFile(actualInfo, expectedInfo) {
			if err := errors.Join(namespace.Close(), originalRoot.Close()); err != nil {
				return nil, err
			}
			timer := time.NewTimer(time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
			continue
		}
		tunFD, err := unix.Openat(rootFD, nativeTunTargetName, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, errors.Join(err, namespace.Close(), originalRoot.Close())
		}
		tun := os.NewFile(uintptr(tunFD), "original-device-tun")
		p := &linuxNativeJailInputs{files: []*os.File{originalRoot, namespace, process, tun}}
		if err := b.completeScope(p, owner, proc, source); err != nil {
			// Close the process descriptor here only once; the outer failure
			// defer still owns it while inputs remain unpublished.
			p.files = []*os.File{originalRoot, namespace, tun}
			return nil, errors.Join(err, p.Close())
		}
		return p, nil
	}
}

func nativePreparedDeviceOwner(owner nativeLaunchRecord) nativeLaunchRecord {
	owner.Authorized, owner.PID, owner.StartTime = false, 0, 0
	owner.Revoked, owner.ExitConfirmed, owner.ResourcesRemoved = false, false, false
	return owner
}

func (linuxNativeJailDevices) completeScope(p *linuxNativeJailInputs, owner nativeLaunchRecord, proc *os.File, source nativeTunSource) error {
	actualSource, err := nativeTunFileSource(p.files[3])
	if err != nil || actualSource != source {
		return errors.Join(err, errors.New("native jail device setup: inherited TUN source changed"))
	}
	if err := checkNativeImageRoot(nativeImageFDPath(p.files[0]), nativePreparedDeviceOwner(owner)); err != nil {
		return err
	}
	start, err := nativeDeviceProcStart(proc, owner.PID)
	if err != nil || start != owner.StartTime {
		return errors.Join(err, errors.New("native jail device setup: pinned task incarnation changed"))
	}
	if err := nativeDevicePIDAlive(p.files[2]); err != nil {
		return err
	}
	s := jailsetup.DeviceSetupScope{Generation: owner.Generation, BootID: owner.KernelBootID, PID: owner.PID, StartTime: owner.StartTime, UID: owner.Lease.UID, GID: owner.Lease.GID, ParentPID: os.Getpid(), TunMode: source.Mode}
	parent, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return err
	}
	s.ParentStartTime, err = nativeProcessStartTime(string(parent), os.Getpid())
	if err != nil {
		return err
	}
	identities := []*jailsetup.DeviceFDIdentity{&s.Root, &s.Namespace, &s.PIDHandle, &s.Tun}
	for i, file := range p.files {
		*identities[i], err = nativeDeviceFileIdentity(file)
		if err != nil {
			return err
		}
		s.ParentFDs[i] = int(file.Fd())
	}
	s.RootMountID, err = nativeDeviceFileMountID(p.files[0])
	if err != nil {
		return err
	}
	s.TunMountID, err = nativeDeviceFileMountID(p.files[3])
	if err != nil {
		return err
	}
	p.scope = s
	return s.Validate()
}

func (linuxNativeJailDevices) InputsRemoved(ctx context.Context, s jailsetup.DeviceSetupScope) (result error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	fd, err := unix.PidfdOpen(s.ParentPID, 0)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, unix.Close(fd)) }()
	proc, err := os.OpenFile("/proc/"+strconv.Itoa(s.ParentPID), unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, proc.Close()) }()
	start, err := nativeDeviceProcStart(proc, s.ParentPID)
	if err != nil {
		return err
	}
	if start != s.ParentStartTime {
		return nil
	}
	for i, number := range s.ParentFDs {
		if err := ctx.Err(); err != nil {
			return err
		}
		var stat unix.Stat_t
		err := unix.Fstatat(int(proc.Fd()), "fd/"+strconv.Itoa(number), &stat, 0)
		if errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil {
			return err
		}
		if (jailsetup.DeviceFDIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}) == s.Identities()[i] {
			return errors.New("native jail device cleanup: original producer still holds input descriptors")
		}
	}
	return nil
}

func (linuxNativeJailDevices) NamespaceRemoved(ctx context.Context, s jailsetup.DeviceSetupScope) error {
	processes, err := os.ReadDir("/proc")
	if err != nil {
		return err
	}
	for _, process := range processes {
		if _, err := strconv.Atoi(process.Name()); err != nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join("/proc", process.Name())
		tasks, err := os.ReadDir(filepath.Join(path, "task"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, task := range tasks {
			if err := nativeDeviceNamespacePathGone(filepath.Join(path, "task", task.Name(), "ns", "mnt"), s.Namespace); err != nil {
				return err
			}
		}
		descriptors, err := os.ReadDir(filepath.Join(path, "fd"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, descriptor := range descriptors {
			if err := ctx.Err(); err != nil {
				return err
			}
			link, err := os.Readlink(filepath.Join(path, "fd", descriptor.Name()))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if link == "mnt:["+strconv.FormatUint(s.Namespace.Inode, 10)+"]" {
				return fmt.Errorf("native jail device cleanup: original namespace retained by process %s descriptor", process.Name())
			}
		}
	}
	return nil
}
func nativeDeviceNamespacePathGone(path string, expected jailsetup.DeviceFDIdentity) error {
	var stat unix.Stat_t
	err := unix.Stat(path, &stat)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	if (jailsetup.DeviceFDIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}) == expected {
		return errors.New("native jail device cleanup: original namespace still has a task")
	}
	return nil
}
