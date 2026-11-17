//go:build linux

package fcvm

import (
	"errors"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// refusedGuestKillWait bounds how long startup waits for a refused guest
// to exit after SIGKILL.
const refusedGuestKillWait = 5 * time.Second

// killProvenGuest sends SIGKILL to the recorded guest through a pidfd that
// openAdoptedProcess proved still names it, and waits for it to exit.
func killProvenGuest(process resourceProcessIdentity) error {
	p, wait, err := openAdoptedProcess(process)
	if err != nil {
		return err
	}
	defer func() { _ = p.Release() }()
	if err := p.Signal(syscall.SIGKILL); err != nil {
		go wait()
		return err
	}
	exited := make(chan struct{})
	go func() { wait(); close(exited) }()
	select {
	case <-exited:
		return nil
	case <-time.After(refusedGuestKillWait):
		return errors.New("guest did not exit after SIGKILL")
	}
}

// openAdoptedProcess returns a pidfd-backed handle for process and a wait
// function that returns once it exits. vmmd is not its parent, so it cannot
// wait(2) for it: os.Process from FindProcess signals through a pidfd on
// Linux, and a second pidfd is polled for exit. The incarnation is checked
// after both are opened, so a PID reused in between is refused.
func openAdoptedProcess(process resourceProcessIdentity) (*os.Process, func(), error) {
	pollfd, err := unix.PidfdOpen(process.PID, 0)
	if err != nil {
		return nil, nil, err
	}
	p, err := os.FindProcess(process.PID)
	if err == nil {
		// A process that dies never returns, so a match now proves both
		// pidfds name the recorded guest.
		var current resourceProcessIdentity
		current, err = readResourceProcessIdentity("/proc", process.PID)
		if err == nil && current != process {
			err = errors.New("process incarnation changed")
		}
	}
	if err != nil {
		_ = unix.Close(pollfd)
		if p != nil {
			_ = p.Release()
		}
		return nil, nil, err
	}
	wait := func() {
		defer func() { _ = unix.Close(pollfd) }()
		fds := []unix.PollFd{{Fd: int32(pollfd), Events: unix.POLLIN}}
		for {
			if _, err := unix.Poll(fds, -1); err == nil || !errors.Is(err, unix.EINTR) {
				return
			}
		}
	}
	return p, wait, nil
}
