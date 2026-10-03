//go:build linux || darwin

// adr: 493 — native ownership and uncertain retirement must remain fenced.
package fcvm

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestNativeStartJailerFailedPublicationStillInstallsAndJoinsWatchdog(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"firecracker-v1.7.0", "jailer"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(bin, "firecracker-v1.7.0"), filepath.Join(bin, "firecracker")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	_, v, _ := nativeManagerFixture(t)
	l := leaseForSlot("watchdog-gate", 0)
	l.Plan = api.PlanHobby
	if err := v.prepareNativeLease(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "execution")
	t.Setenv("GREGALE_NATIVE_LAUNCH_FIXTURE", "1")
	t.Setenv("GREGALE_NATIVE_LAUNCH_MARKER", marker)
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	v.nativeRecovery.helper = os.Args[0]
	v.nativeRecovery.startTime = func(int) (uint64, error) { return 101, nil }
	cause := errors.New("journal publication failed")
	v.nativeRecovery.journal.writeRecord = func(string, nativeLaunchRecord) error { return cause }
	if err := v.startJailer(t.Context(), l); !errors.Is(err, cause) {
		t.Fatalf("start=%v", err)
	}
	v.mu.Lock()
	rec := v.recs[l.Instance]
	v.mu.Unlock()
	if rec == nil || rec.done == nil {
		t.Fatal("forked child lost its watchdog on publication error")
	}
	t.Cleanup(func() { _ = rec.cmd.Process.Kill() })
	select {
	case <-rec.done:
	case <-time.After(3 * time.Second):
		t.Fatal("gate rejection did not finish watchdog")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed publication executed jailer")
	}
	v.nativeRecovery.journal.writeRecord = nil
	if err := v.Kill(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	if v.recs[l.Instance] != nil {
		t.Fatal("joined record survived confirmed retirement")
	}
}
