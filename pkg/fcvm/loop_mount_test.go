// adr: 192
// spec: §6.3
package fcvm

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLoopMountSessionTimings_CommandBoundariesAndCleanup(t *testing.T) {
	for _, callbackFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "callback failure"}[callbackFails], func(t *testing.T) {
			var timings loopMountTimings
			var mountpoint string
			var mountAtCallback time.Duration
			var callbackRan, unmountRan bool
			callbackErr := errors.New("file write failed")
			const commandDelay = 5 * time.Millisecond
			commands := loopMountCommands{
				mount: func(drive, mp string) ([]byte, error) {
					if drive != "fixture-drive" {
						t.Fatalf("unexpected drive %q", drive)
					}
					mountpoint = mp
					if _, err := os.Stat(mp); err != nil {
						t.Fatal(err)
					}
					time.Sleep(commandDelay)
					return nil, nil
				},
				unmount: func(mp string) error {
					if mp != mountpoint || !callbackRan || timings.Unmount != 0 {
						t.Fatal("unmount did not run after callback with a fresh timing")
					}
					unmountRan = true
					time.Sleep(commandDelay)
					return nil
				},
			}
			err := runLoopMountSession("fixture-drive", "faas-test-mount-timing-", func(mp string) error {
				if mp != mountpoint || timings.Mount < commandDelay || timings.Unmount != 0 {
					t.Fatal("mount timing was not finalized before callback")
				}
				callbackRan = true
				mountAtCallback = timings.Mount
				// File-work delay must not be charged to either host command.
				time.Sleep(commandDelay)
				if callbackFails {
					return callbackErr
				}
				return nil
			}, &timings, commands)
			if callbackFails && !errors.Is(err, callbackErr) || !callbackFails && err != nil {
				t.Fatalf("callback error was changed: %v", err)
			}
			if !unmountRan || timings.Unmount < commandDelay || timings.Mount != mountAtCallback {
				t.Fatalf("command timings/order: %+v, unmounted=%v", timings, unmountRan)
			}
			if _, err := os.Stat(mountpoint); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("mountpoint not cleaned up: %v", err)
			}
		})
	}
}

func TestLoopMountSessionTimings_MountFailure(t *testing.T) {
	mountErr := errors.New("fixture mount failure")
	timings := loopMountTimings{Unmount: time.Hour}
	var mountpoint string
	err := runLoopMountSession("fixture-drive", "faas-test-mount-failure-", func(string) error {
		t.Fatal("callback ran after failed mount")
		return nil
	}, &timings, loopMountCommands{
		mount: func(_, mp string) ([]byte, error) {
			mountpoint = mp
			return []byte("fixture diagnostic\n"), mountErr
		},
		unmount: func(string) error {
			t.Fatal("unmount ran after failed mount")
			return nil
		},
	})
	if !errors.Is(err, mountErr) || !strings.Contains(err.Error(), "fixture diagnostic") || timings.Unmount != 0 {
		t.Fatalf("mount failure attribution/error: %+v, %v", timings, err)
	}
	if _, err := os.Stat(mountpoint); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed mountpoint not cleaned up: %v", err)
	}
}
