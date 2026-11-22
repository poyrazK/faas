//go:build linux

package runrelease

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
)

func ProbeHost(ctx context.Context) Host {
	h := Host{OS: runtime.GOOS, Architecture: runtime.GOARCH, Virtualization: "unknown"}
	cmd := exec.CommandContext(ctx, "/usr/bin/systemd-detect-virt")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
	out, err := cmd.Output()
	// systemd-detect-virt returns 1 for its explicit "none" result.
	if err == nil || cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 1 {
		h.Virtualization = strings.TrimSpace(string(out))
	}
	fd, err := unix.Open("/dev/kvm", unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err == nil {
		// KVM_GET_API_VERSION creates no VM and changes no host state.
		version, ioErr := unix.IoctlRetInt(fd, 0xAE00)
		h.KVM = ioErr == nil && version == 12
		_ = unix.Close(fd)
	}
	_, err = os.Stat("/sys/fs/cgroup/cgroup.controllers")
	h.CgroupV2 = err == nil
	return h
}
