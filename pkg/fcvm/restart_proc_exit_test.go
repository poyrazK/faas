package fcvm

import (
	"errors"
	"io/fs"
	"path/filepath"
	"syscall"
	"testing"
)

// failProcRead makes reads of the named /proc files fail with err, as when the
// process is reaped between the open and the read.
func failProcRead(t *testing.T, err error, paths ...string) {
	t.Helper()
	prev := readProcFile
	readProcFile = func(path string) ([]byte, error) {
		for _, p := range paths {
			if path == p {
				return nil, &fs.PathError{Op: "read", Path: path, Err: err}
			}
		}
		return prev(path)
	}
	t.Cleanup(func() { readProcFile = prev })
}

// A process reaped while the startup inventory reads it is skipped, like one
// that exited before the read. Production hit this on a busy host: reading
// /proc/<pid>/cmdline returned ESRCH and vmmd failed to start.
func TestRestartInventorySkipsProcessesReapedMidRead(t *testing.T) {
	cases := []struct {
		name          string
		file          func(opts restartInventoryOptions) string
		wantInstances int
	}{
		{
			name:          "unrelated process argv",
			file:          func(o restartInventoryOptions) string { return filepath.Join(o.procRoot, "104", "cmdline") },
			wantInstances: 1,
		},
		{
			// The guest exited after its argv was read and before its UID.
			name:          "guest UID",
			file:          func(o restartInventoryOptions) string { return filepath.Join(o.procRoot, "101", "status") },
			wantInstances: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := restartFixture(t)
			restartProcessFixture(t, opts, 101, 0, "firecracker", "--id", idLive)
			restartProcessFixture(t, opts, 104, 3, "unrelated")
			failProcRead(t, syscall.ESRCH, tc.file(opts))
			inv, err := scanRestartInventory(t.Context(), opts)
			if err != nil {
				t.Fatalf("inventory failed on a process reaped mid-read: %v", err)
			}
			if len(inv.instances) != tc.wantInstances {
				t.Fatalf("instances = %v, want %d", inv.instances, tc.wantInstances)
			}
		})
	}
}

func TestRestartUIDSlotsSkipProcessesReapedMidRead(t *testing.T) {
	opts := restartFixture(t)
	restartProcessFixture(t, opts, 101, 0, "firecracker", "--id", idLive)
	restartProcessFixture(t, opts, 104, 3, "unrelated")
	failProcRead(t, syscall.ESRCH, filepath.Join(opts.procRoot, "104", "status"))
	slots, err := restartUIDSlots(t.Context(), opts.procRoot)
	if err != nil {
		t.Fatalf("UID inventory failed on a process reaped mid-read: %v", err)
	}
	if _, ok := slots[0]; !ok || len(slots) != 1 {
		t.Fatalf("slots = %v, want only the surviving guest's", slots)
	}
}

func TestResourceProcessIdentityReapedMidReadIsAbsent(t *testing.T) {
	opts := restartFixture(t)
	restartProcessFixture(t, opts, 101, 0, "firecracker", "--id", idLive)
	journalProcessFixture(t, opts, 101, 77)
	failProcRead(t, syscall.ESRCH, filepath.Join(opts.procRoot, "101", "stat"))
	if _, err := readResourceProcessIdentity(opts.procRoot, 101); !errors.Is(err, errResourceProcessAbsent) {
		t.Fatalf("identity of a reaped process: %v, want errResourceProcessAbsent", err)
	}
}

// Any other read failure still fails the inventory closed: only a gone
// process may be skipped.
func TestRestartInventoryFailsClosedOnOtherReadErrors(t *testing.T) {
	opts := restartFixture(t)
	restartProcessFixture(t, opts, 101, 0, "firecracker", "--id", idLive)
	failProcRead(t, syscall.EIO, filepath.Join(opts.procRoot, "101", "cmdline"))
	if _, err := scanRestartInventory(t.Context(), opts); !errors.Is(err, syscall.EIO) {
		t.Fatalf("inventory with an unreadable argv: %v, want EIO", err)
	}
}
