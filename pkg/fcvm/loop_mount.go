// adr: 192
// spec: §6.3
package fcvm

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// These timings are operator-only diagnostics, not fields on the customer
// wake.restore_breakdown event. Mount/Unmount measure the complete host
// command round trips, including process start, not only kernel work.
type loopMountTimings struct {
	Mount   time.Duration
	Unmount time.Duration
}

type preBootStageTimings struct {
	loopMountTimings
	Prepare      time.Duration // validation, payload construction and digest
	Check        time.Duration // checking the captured drive's existing files
	Write        time.Duration // all file writers inside the mount callback
	FilesTotal   int
	FilesWritten int // successfully completed writers
}

// loopMountSession loop-mounts an ext4 drive image read-write, runs fn
// against the mountpoint, then unmounts and removes the mountpoint. vmmd is
// the only root component, so the loopback mount is permitted by the §11
// threat model. It is a package variable so the pure-Go test tier can
// substitute a plain directory without root or a loop device.
var loopMountSession = func(drive, prefix string, fn func(mountRoot string) error, measured ...*loopMountTimings) error {
	var timings *loopMountTimings
	if len(measured) > 0 {
		timings = measured[0]
	}
	return runLoopMountSession(drive, prefix, fn, timings, loopMountCommands{
		mount: func(drive, mp string) ([]byte, error) {
			return exec.Command("mount", "-o", "loop,rw", drive, mp).CombinedOutput()
		},
		unmount:     umountRetryBusy,
		lazyUnmount: func(mp string) error { return exec.Command("umount", "-l", mp).Run() },
	})
}

// The command seam lets tests exercise timing and cleanup order using
// owned temporary directories, without executing a host mount command.
type loopMountCommands struct {
	mount   func(drive, mountpoint string) ([]byte, error)
	unmount func(mountpoint string) error
	// lazyUnmount detaches a mount that stayed busy so it cannot outlive the
	// session; nil skips that last resort.
	lazyUnmount func(mountpoint string) error
}

// runLoopMountSession fails when the drive cannot be unmounted: the caller
// must not boot a VM on an image that is still mounted read-write on the
// host. It used to ignore the unmount error and then RemoveAll the still
// mounted directory. On production-us four failed wakes left their layer
// loop-mounted under /srv/fc/base, which pinned the jail bind mounts so
// vmmd's cleanup retried "unmount owned image: exit status 32" for hours and
// kept the leases (hunt #10, H10-30).
func runLoopMountSession(drive, prefix string, fn func(string) error, timings *loopMountTimings, commands loopMountCommands) (err error) {
	if timings == nil {
		timings = &loopMountTimings{}
	}
	*timings = loopMountTimings{}
	mp, err := os.MkdirTemp("", prefix)
	if err != nil {
		return fmt.Errorf("mkdir mountpoint: %w", err)
	}
	started := time.Now()
	out, err := commands.mount(drive, mp)
	timings.Mount = time.Since(started)
	if err != nil {
		_ = os.Remove(mp)
		return fmt.Errorf("mount loop: %w (%s)", err, bytes.TrimSpace(out))
	}
	defer func() {
		started := time.Now()
		unmountErr := commands.unmount(mp)
		if unmountErr != nil && commands.lazyUnmount != nil {
			if lazyErr := commands.lazyUnmount(mp); lazyErr != nil {
				unmountErr = errors.Join(unmountErr, fmt.Errorf("lazy unmount: %w", lazyErr))
			} else {
				// Detached, but a holder may still be writing: the drive is
				// not safe to boot, so the session still fails.
				unmountErr = fmt.Errorf("drive was busy and was detached lazily: %w", unmountErr)
			}
		}
		timings.Unmount = time.Since(started)
		if unmountErr != nil {
			err = errors.Join(err, fmt.Errorf("unmount loop %s: %w", mp, unmountErr))
			if commands.lazyUnmount == nil {
				return
			}
		}
		// Only an empty, unmounted directory is removed; never RemoveAll a
		// mountpoint, which would delete the drive's files.
		_ = os.Remove(mp)
	}()
	return fn(mp)
}
