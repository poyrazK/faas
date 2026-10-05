//go:build linux

package fcvm

import (
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

type nativePIDFD struct{ fd int }

func nativeKernelBootID() (string, error) {
	raw, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return strings.TrimSpace(string(raw)), err
}

func openNativeProcess(pid int) (nativeProcessHandle, error) {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return nil, err
	}
	return &nativePIDFD{fd: fd}, nil
}

func (p *nativePIDFD) Signal(signal syscall.Signal) error {
	return unix.PidfdSendSignal(p.fd, unix.Signal(signal), nil, 0)
}

func (p *nativePIDFD) Wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		fds := []unix.PollFd{{Fd: int32(p.fd), Events: unix.POLLIN}}
		_, err := unix.Poll(fds, 50)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return err
		}
		if fds[0].Revents&(unix.POLLNVAL|unix.POLLERR) != 0 {
			return fmt.Errorf("native recovery: invalid process handle events %#x", fds[0].Revents)
		}
		if fds[0].Revents&(unix.POLLIN|unix.POLLHUP) != 0 {
			return nil
		}
	}
}

func (p *nativePIDFD) Close() error {
	fd := p.fd
	p.fd = -1
	return unix.Close(fd)
}
