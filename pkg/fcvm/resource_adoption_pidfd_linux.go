//go:build linux

package fcvm

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// adoptionPidfdCheck opens a pidfd for pid and re-reads its incarnation
// while the pidfd pins it: a PID reused between the /proc scan and here
// fails the comparison instead of passing it (adoption proof step 3).
func adoptionPidfdCheck(pid int, want resourceProcessIdentity) error {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return fmt.Errorf("pidfd_open %d: %w", pid, err)
	}
	defer func() { _ = unix.Close(fd) }()
	current, err := readResourceProcessIdentity("/proc", pid)
	if err != nil {
		return err
	}
	if current != want {
		return errors.New("pid was reused before the pidfd was opened")
	}
	// Signal 0 through the pidfd fails once the pinned process has exited.
	if err := unix.PidfdSendSignal(fd, 0, nil, 0); err != nil {
		return fmt.Errorf("pidfd for %d: %w", pid, err)
	}
	return nil
}
