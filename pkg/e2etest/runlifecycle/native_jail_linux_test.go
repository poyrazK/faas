//go:build metal && linux

// adr: 20261114010000 — isolate native journals while preserving them across daemon death.
package runlifecycle_test

import (
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"golang.org/x/sys/unix"
)

func prepareNativeRunJail(t *testing.T, diskDir string) {
	t.Helper()
	// Each fixture has a fresh native ownership root. Daemon restarts stay
	// in this namespace and retain it; no host journal is hidden or edited.
	if err := os.MkdirAll(fcvm.JailChrootBase, 0o700); err != nil {
		t.Fatal(err)
	}
	source, filesystem, flags, options := "tmpfs", "tmpfs", uintptr(unix.MS_NOSUID|unix.MS_NODEV), "mode=0700,size=1g"
	if diskDir != "" {
		if err := os.MkdirAll(diskDir, 0o700); err != nil {
			t.Fatal(err)
		}
		source, filesystem, flags, options = diskDir, "", unix.MS_BIND, ""
	}
	if err := unix.Mount(source, fcvm.JailChrootBase, filesystem, flags, options); err != nil { //nolint:forbidigo // Designated-host acceptance fixture in the runner's private mount namespace.
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := unix.Unmount(fcvm.JailChrootBase, 0); err != nil { //nolint:forbidigo // Retire only this fixture's private mount after its harness has stopped.
			t.Errorf("retire private acceptance jail mount: %v", err)
		}
	})
}
