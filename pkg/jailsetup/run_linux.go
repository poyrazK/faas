//go:build linux

package jailsetup

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Run handles the device setup commands shared by vmmd's compatibility mode
// and the standalone jail helper. args includes argv[0]. A recognized command
// exits the process on failure; unknown commands return false. Call only from
// a helper process inside the jailer's private mount namespace.
func Run(args []string) bool {
	if len(args) <= 1 {
		return false
	}
	switch args[1] {
	case "--setup-jail":
		started := time.Now()
		if len(args) != 8 {
			fmt.Fprintln(os.Stderr, "vmmd: --setup-jail requires devTarget hostTunSrc tunTarget kvmPath uid gid")
			os.Exit(2)
		}
		uid, gid, ok := parseJailIDs(args[6], args[7])
		if !ok {
			os.Exit(2)
		}
		if err := setupJail(args[2], args[3], args[4], args[5], uid, gid); err != nil {
			fmt.Fprintf(os.Stderr, "vmmd: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("%s%d\n", SetupJailTimingPrefix, time.Since(started).Microseconds())
		return true
	case "--enter-jail":
		// Same work as --setup-jail, but this process enters the jailer's
		// mount namespace itself instead of being launched through nsenter.
		//
		// Measured on an acceptance node: the nsenter form cost ~23.7 ms per
		// restore while the work inside it was ~136 us — 99.4 % was the two
		// process creations (nsenter, then the helper it execs) forked from a
		// ~79 MB vmmd. Doing the setns here removes one of the two.
		started := time.Now()
		if len(args) != 9 {
			fmt.Fprintln(os.Stderr, "vmmd: --enter-jail requires pid devTarget hostTunSrc tunTarget kvmPath uid gid")
			os.Exit(2)
		}
		pid, pidErr := strconv.Atoi(args[2])
		if pidErr != nil || pid <= 0 {
			fmt.Fprintln(os.Stderr, "vmmd: --enter-jail pid must be a positive integer")
			os.Exit(2)
		}
		uid, gid, ok := parseJailIDs(args[7], args[8])
		if !ok {
			os.Exit(2)
		}
		if err := enterJailNamespace(pid); err != nil {
			fmt.Fprintf(os.Stderr, "vmmd: %v\n", err)
			os.Exit(1)
		}
		if err := setupJail(args[3], args[4], args[5], args[6], uid, gid); err != nil {
			fmt.Fprintf(os.Stderr, "vmmd: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("%s%d\n", SetupJailTimingPrefix, time.Since(started).Microseconds())
		return true
	case "--mount-dev":
		if len(args) != 3 {
			fmt.Fprintln(os.Stderr, "vmmd: --mount-dev requires a target")
			os.Exit(2)
		}
		// Jailer creates /dev on the chroot's nodev filesystem. Replace it
		// with a private device-capable tmpfs before creating the KVM node;
		// otherwise stat(2) looks correct but open(2) returns EACCES.
		// Do not pass `dev` as tmpfs data: util-linux translates that generic
		// mount option into the absence of MS_NODEV, while the kernel tmpfs
		// parser rejects it when supplied in the data string.
		if err := syscall.Mount("tmpfs", args[2], "tmpfs", 0, "mode=0755"); err != nil {
			fmt.Fprintf(os.Stderr, "vmmd: mount device tmpfs: %v\n", err)
			os.Exit(1)
		}
		devNet := filepath.Join(args[2], "net")
		if err := os.MkdirAll(devNet, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "vmmd: create device net directory: %v\n", err)
			os.Exit(1)
		}
		if err := unix.Mknod(filepath.Join(devNet, "tun"), unix.S_IFCHR|0660, int(unix.Mkdev(10, 200))); err != nil {
			fmt.Fprintf(os.Stderr, "vmmd: create TUN target: %v\n", err)
			os.Exit(1)
		}
		return true
	case "--mount-bind":
		if len(args) != 4 {
			fmt.Fprintln(os.Stderr, "vmmd: --mount-bind requires source and target")
			os.Exit(2)
		}
		if err := syscall.Mount(args[2], args[3], "", syscall.MS_BIND, ""); err != nil {
			fmt.Fprintf(os.Stderr, "vmmd: mount bind: %v\n", err)
			os.Exit(1)
		}
		return true
	case "--mknod-kvm":
		if len(args) != 5 {
			fmt.Fprintln(os.Stderr, "vmmd: --mknod-kvm requires path, uid and gid")
			os.Exit(2)
		}
		uid, uidErr := strconv.Atoi(args[3])
		gid, gidErr := strconv.Atoi(args[4])
		if uidErr != nil || gidErr != nil || uid < 0 || gid < 0 {
			fmt.Fprintln(os.Stderr, "vmmd: --mknod-kvm uid/gid must be non-negative integers")
			os.Exit(2)
		}
		if err := os.Remove(args[2]); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "vmmd: remove kvm device: %v\n", err)
			os.Exit(1)
		}
		// /dev/kvm is the stable misc-device ABI (major 10, minor 232).
		// Create it inside the jail namespace so the per-VM UID can open it
		// without widening the host device permissions for every local user.
		if err := unix.Mknod(args[2], unix.S_IFCHR|0660, int(unix.Mkdev(10, 232))); err != nil {
			fmt.Fprintf(os.Stderr, "vmmd: mknod kvm: %v\n", err)
			os.Exit(1)
		}
		if err := unix.Chown(args[2], uid, gid); err != nil {
			fmt.Fprintf(os.Stderr, "vmmd: chown kvm: %v\n", err)
			os.Exit(1)
		}
		// Validate the exact privilege transition Jailer will make. A root
		// helper opening the node would only prove the device exists; the
		// Firecracker process must be able to issue KVM_GET_API_VERSION as the
		// per-VM UID with no supplementary host groups.
		if err := unix.Setgroups([]int{gid}); err != nil {
			fmt.Fprintf(os.Stderr, "vmmd: set kvm groups: %v\n", err)
			os.Exit(1)
		}
		if err := unix.Setresgid(gid, gid, gid); err != nil {
			fmt.Fprintf(os.Stderr, "vmmd: set kvm gid: %v\n", err)
			os.Exit(1)
		}
		if err := unix.Setresuid(uid, uid, uid); err != nil {
			fmt.Fprintf(os.Stderr, "vmmd: set kvm uid: %v\n", err)
			os.Exit(1)
		}
		fd, err := unix.Open(args[2], unix.O_RDWR, 0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "vmmd: validate kvm open: %v\n", err)
			os.Exit(1)
		}
		api, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(0xAE00), 0)
		_ = unix.Close(fd)
		if errno != 0 {
			fmt.Fprintf(os.Stderr, "vmmd: validate KVM_GET_API_VERSION: %v\n", errno)
			os.Exit(1)
		}
		if int(api) != 12 {
			fmt.Fprintf(os.Stderr, "vmmd: unexpected KVM API version %d\n", api)
			os.Exit(1)
		}
		return true
	default:
		return false
	}
}

func parseJailIDs(uidArg, gidArg string) (int, int, bool) {
	uid, uidErr := strconv.Atoi(uidArg)
	gid, gidErr := strconv.Atoi(gidArg)
	if uidErr != nil || gidErr != nil || uid < 0 || gid < 0 {
		fmt.Fprintln(os.Stderr, "vmmd: jail uid/gid must be non-negative integers")
		return 0, 0, false
	}
	return uid, gid, true
}

// enterJailNamespace joins the jailer's mount namespace and chroots into its
// root, which is what `nsenter -t <pid> -m -r` did for us.
//
// The thread is locked and deliberately never unlocked: setns(CLONE_NEWNS)
// changes the mount namespace of the CALLING THREAD only, so the Go runtime
// must never hand this thread to another goroutine. The process performs its
// mounts and exits, so the locked thread dies with it.
//
// The root descriptor is opened before the setns, exactly as nsenter does:
// afterwards /proc/<pid>/root would be resolved through the namespace we just
// joined. File descriptors survive setns, so the pre-opened fd stays valid.
func enterJailNamespace(pid int) error {
	runtime.LockOSThread()

	// setns(CLONE_NEWNS) fails with EINVAL when the caller shares its
	// filesystem state (fs_struct: root, cwd, umask) with another thread,
	// and every thread the Go runtime creates is cloned with CLONE_FS. So a
	// plain setns from Go always returns EINVAL — this is why runc enters
	// namespaces from a C constructor before the Go runtime starts.
	//
	// unshare(CLONE_FS) gives this locked thread a private fs_struct, which
	// both makes the setns legal and confines the chroot below to this
	// thread. Verified: without it, setns returns "invalid argument" on
	// every call and the caller silently falls back to nsenter.
	if err := unix.Unshare(unix.CLONE_FS); err != nil {
		return fmt.Errorf("unshare filesystem state: %w", err)
	}

	rootFd, err := unix.Open(fmt.Sprintf("/proc/%d/root", pid), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open jail root for pid %d: %w", pid, err)
	}
	defer func() { _ = unix.Close(rootFd) }()

	nsFd, err := unix.Open(fmt.Sprintf("/proc/%d/ns/mnt", pid), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open jail mount namespace for pid %d: %w", pid, err)
	}
	defer func() { _ = unix.Close(nsFd) }()

	if err := unix.Setns(nsFd, unix.CLONE_NEWNS); err != nil {
		return fmt.Errorf("setns mount namespace for pid %d: %w", pid, err)
	}
	if err := unix.Fchdir(rootFd); err != nil {
		return fmt.Errorf("chdir to jail root for pid %d: %w", pid, err)
	}
	if err := unix.Chroot("."); err != nil {
		return fmt.Errorf("chroot to jail root for pid %d: %w", pid, err)
	}
	if err := unix.Chdir("/"); err != nil {
		return fmt.Errorf("chdir after chroot for pid %d: %w", pid, err)
	}
	return nil
}

// setupJail is the shared body of --setup-jail and --enter-jail: prepare the
// device tmpfs, bind the host TUN device in, and provision KVM. The caller is
// already inside the jailer's mount namespace and chroot.
func setupJail(devTarget, hostTunSrc, tunTarget, kvmPath string, uid, gid int) error {
	if err := syscall.Mount("tmpfs", devTarget, "tmpfs", 0, "mode=0755"); err != nil {
		return fmt.Errorf("mount device tmpfs: %w", err)
	}
	devNet := filepath.Join(devTarget, "net")
	if err := os.MkdirAll(devNet, 0o755); err != nil {
		return fmt.Errorf("create device net directory: %w", err)
	}
	if err := unix.Mknod(tunTarget, unix.S_IFCHR|0660, int(unix.Mkdev(10, 200))); err != nil {
		return fmt.Errorf("create TUN target: %w", err)
	}
	if err := syscall.Mount(hostTunSrc, tunTarget, "", syscall.MS_BIND, ""); err != nil {
		return fmt.Errorf("mount bind tun: %w", err)
	}
	if err := os.Remove(kvmPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove kvm device: %w", err)
	}
	if err := unix.Mknod(kvmPath, unix.S_IFCHR|0660, int(unix.Mkdev(10, 232))); err != nil {
		return fmt.Errorf("mknod kvm: %w", err)
	}
	if err := unix.Chown(kvmPath, uid, gid); err != nil {
		return fmt.Errorf("chown kvm: %w", err)
	}
	return nil
}
