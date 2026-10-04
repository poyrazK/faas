// adr: 192
// spec: §6.3
package fcvm

import (
	"bytes"
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
		unmount: func(mp string) error { return exec.Command("umount", mp).Run() },
	})
}

// The command seam lets tests exercise timing and cleanup order using
// owned temporary directories, without executing a host mount command.
type loopMountCommands struct {
	mount   func(drive, mountpoint string) ([]byte, error)
	unmount func(mountpoint string) error
}

func runLoopMountSession(drive, prefix string, fn func(string) error, timings *loopMountTimings, commands loopMountCommands) error {
	if timings == nil {
		timings = &loopMountTimings{}
	}
	*timings = loopMountTimings{}
	mp, err := os.MkdirTemp("", prefix)
	if err != nil {
		return fmt.Errorf("mkdir mountpoint: %w", err)
	}
	defer func() { _ = os.RemoveAll(mp) }()
	started := time.Now()
	out, err := commands.mount(drive, mp)
	timings.Mount = time.Since(started)
	if err != nil {
		return fmt.Errorf("mount loop: %w (%s)", err, bytes.TrimSpace(out))
	}
	defer func() {
		started := time.Now()
		_ = commands.unmount(mp)
		timings.Unmount = time.Since(started)
	}()
	return fn(mp)
}
