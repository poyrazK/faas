package fcvm

import (
	"errors"
	"os/exec"
	"time"
)

// Busy-unmount retry for owned image binds.
//
// A drive image's jail bind mount can stay busy for a moment after the
// pre-boot loop-mount session ends: `mount -o loop` attaches an auto-clearing
// loop device whose reference to the backing file — reached through the bind
// mount — is dropped asynchronously after `umount` returns. A restore that
// fails right after pre-boot staging (and is cleaned up before its cold-boot
// fallback) can therefore see umount(8) report the target busy, and the wake
// then fails instead of falling back. The holder is transient, so the unmount
// is retried for a bounded interval while the mount is still present.
const (
	umountBusyRetryInterval = 25 * time.Millisecond
	umountBusyRetryBudget   = time.Second
)

// umountRetryBusy runs umount(8) on mountpoint, retrying while the mount is
// still present for up to umountBusyRetryBudget. It returns nil once the
// mount is gone, whatever the individual attempts reported.
func umountRetryBusy(mountpoint string) error {
	return retryUmountWhileMounted(mountpoint,
		func(mp string) error { return exec.Command("umount", mp).Run() },
		func(mp string) (bool, error) {
			current, err := resourceMountAt(mp)
			return current != nil, err
		},
		time.Sleep, umountBusyRetryInterval, umountBusyRetryBudget)
}

func retryUmountWhileMounted(mountpoint string, umount func(string) error, mounted func(string) (bool, error), sleep func(time.Duration), interval, budget time.Duration) error {
	var lastErr error
	for waited := time.Duration(0); ; waited += interval {
		err := umount(mountpoint)
		if err == nil {
			return nil
		}
		lastErr = err
		still, checkErr := mounted(mountpoint)
		if checkErr != nil {
			return errors.Join(lastErr, checkErr)
		}
		if !still {
			// Another actor (or the first attempt) already released it.
			return nil
		}
		if waited >= budget {
			return lastErr
		}
		sleep(interval)
	}
}
