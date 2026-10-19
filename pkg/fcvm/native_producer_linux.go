//go:build linux

package fcvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"
)

func nativeRepairProducerSupported() bool { return true }

func nativeProducerExited(ctx context.Context, expected nativeProducerIdentity) (result error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := expected.validate(); err != nil {
		return err
	}
	fd, err := unix.PidfdOpen(expected.PID, 0)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("native producer: pin original daemon: %w", err)
	}
	defer func() { result = errors.Join(result, unix.Close(fd)) }()
	// A reused PID proves the old incarnation has exited. The new process is
	// never signalled or waited on. Other probe failures remain unknown.
	start, err := nativeHostHelperStartTime(expected.PID)
	if err == nil && start != expected.StartTime {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(expected.PID), "status"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil {
			uid, err := nativeStatusUID(string(raw))
			if err != nil || uid != expected.UID {
				return errors.New("native producer: daemon credentials changed or are unknown")
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	if _, err := unix.Poll(poll, 0); err != nil {
		return err
	}
	if poll[0].Revents&(unix.POLLERR|unix.POLLNVAL) != 0 || poll[0].Revents&(unix.POLLIN|unix.POLLHUP) == 0 {
		return errors.New("native producer: original daemon exit is unconfirmed")
	}
	return nil
}
