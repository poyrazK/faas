//go:build linux

package jailsetup

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

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
		if len(args) != 8 {
			fmt.Fprintln(os.Stderr, "vmmd: --setup-jail requires devTarget hostTunSrc tunTarget kvmPath uid gid")
			os.Exit(2)
		}
		devTarget := args[2]
		hostTunSrc := args[3]
		tunTarget := args[4]
		kvmPath := args[5]
		uid, uidErr := strconv.Atoi(args[6])
		gid, gidErr := strconv.Atoi(args[7])
		if uidErr != nil || gidErr != nil || uid < 0 || gid < 0 {
			fmt.Fprintln(os.Stderr, "vmmd: --setup-jail uid/gid must be non-negative integers")
			os.Exit(2)
		}
		if err := syscall.Mount("tmpfs", devTarget, "tmpfs", 0, "mode=0755"); err != nil {
			fmt.Fprintf(os.Stderr, "vmmd: mount device tmpfs: %v\n", err)
			os.Exit(1)
		}
		devNet := filepath.Join(devTarget, "net")
		if err := os.MkdirAll(devNet, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "vmmd: create device net directory: %v\n", err)
			os.Exit(1)
		}
		if err := unix.Mknod(tunTarget, unix.S_IFCHR|0660, int(unix.Mkdev(10, 200))); err != nil {
			fmt.Fprintf(os.Stderr, "vmmd: create TUN target: %v\n", err)
			os.Exit(1)
		}
		if err := syscall.Mount(hostTunSrc, tunTarget, "", syscall.MS_BIND, ""); err != nil {
			fmt.Fprintf(os.Stderr, "vmmd: mount bind tun: %v\n", err)
			os.Exit(1)
		}
		if err := os.Remove(kvmPath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "vmmd: remove kvm device: %v\n", err)
			os.Exit(1)
		}
		if err := unix.Mknod(kvmPath, unix.S_IFCHR|0660, int(unix.Mkdev(10, 232))); err != nil {
			fmt.Fprintf(os.Stderr, "vmmd: mknod kvm: %v\n", err)
			os.Exit(1)
		}
		if err := unix.Chown(kvmPath, uid, gid); err != nil {
			fmt.Fprintf(os.Stderr, "vmmd: chown kvm: %v\n", err)
			os.Exit(1)
		}
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
