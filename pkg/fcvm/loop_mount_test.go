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

// H10-30: a busy unmount was ignored and the still-mounted directory was
// RemoveAll'd. The session now fails (the drive must not boot while mounted
// on the host), detaches the mount lazily so it cannot leak, and never
// deletes through a mountpoint it could not unmount.
func TestLoopMountSessionUnmountFailure(t *testing.T) {
	busy := errors.New("exit status 32")
	for _, tc := range []struct {
		name     string
		lazyErr  error
		wantGone bool
		wantLazy string
	}{
		{name: "detached lazily", wantGone: true, wantLazy: "detached lazily"},
		{name: "lazy unmount fails", lazyErr: errors.New("lazy refused"), wantLazy: "lazy refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mountpoint string
			err := runLoopMountSession("fixture-drive", "faas-test-mount-busy-", func(mp string) error {
				mountpoint = mp
				// Stands in for the drive's contents while it is mounted.
				return os.WriteFile(mp+"/layer-file", []byte("keep"), 0o600)
			}, nil, loopMountCommands{
				mount:   func(string, string) ([]byte, error) { return nil, nil },
				unmount: func(string) error { return busy },
				lazyUnmount: func(mp string) error {
					if tc.lazyErr != nil {
						return tc.lazyErr
					}
					// A successful lazy unmount leaves the empty mountpoint.
					return os.Remove(mp + "/layer-file")
				},
			})
			if !errors.Is(err, busy) || !strings.Contains(err.Error(), tc.wantLazy) {
				t.Fatalf("session error = %v, want the busy unmount reported (%s)", err, tc.wantLazy)
			}
			_, statErr := os.Stat(mountpoint)
			if gone := errors.Is(statErr, os.ErrNotExist); gone != tc.wantGone {
				t.Fatalf("mountpoint removed = %v, want %v", gone, tc.wantGone)
			}
			if !tc.wantGone {
				if _, err := os.Stat(mountpoint + "/layer-file"); err != nil {
					t.Fatalf("files under a still-mounted point were deleted: %v", err)
				}
				_ = os.RemoveAll(mountpoint)
			}
		})
	}
}
