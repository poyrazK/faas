// adr: 642
package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Every guest boots on the restore-safe timer profile: the default
// tsc/lapic-deadline timers lost interrupts after a Firecracker 1.7 restore
// (production-us hunt #5, H5-25).
func TestEveryGuestBootsWithTheRestoreSafeTimers(t *testing.T) {
	for name, args := range map[string]string{
		"app":       BuildColdBootConfig(ColdBootSpec{KernelKey: "k", BaseKey: "b", LayerKey: "l", VcpuCount: 1, MemSizeMiB: 128}, 0).BootSource.BootArgs,
		"execution": BuildColdBootConfig(ColdBootSpec{KernelKey: "k", BaseKey: "b", LayerKey: "l", VcpuCount: 1, MemSizeMiB: 128, Networkless: true}, 0).BootSource.BootArgs,
		"job":       withGuestTimer(coldBootArgs),
	} {
		if !strings.Contains(args, "clocksource=kvm-clock") || !strings.Contains(args, "lapic=notscdeadline") {
			t.Errorf("%s boot args lack the timer profile: %q", name, args)
		}
		if !strings.HasPrefix(args, guestBootConsoleArgs) || strings.Count(args, "console=") != 1 {
			t.Errorf("%s boot args = %q, want the console arguments once, first", name, args)
		}
	}
}

// A capture records the timer profile its VM booted with, and a restore onto
// a different profile, or from a version-1 capture without one, is refused so
// the next wake cold-boots and the next park captures the safe profile.
func TestVerifySnapshotBackingRefusesAnotherTimerProfile(t *testing.T) {
	ctx := context.Background()
	f := newBackingFixture(ctx, t)
	snap := f.capture(ctx, t, "snap/d1/captures/c1/v2/mem")
	if err := f.m.verifySnapshotBacking(ctx, snap, f.base); err != nil {
		t.Fatalf("same profile: %v", err)
	}

	rc, err := f.m.storage.Get(ctx, "snap/d1/captures/c1/v2/backing")
	if err != nil {
		t.Fatal(err)
	}
	var recorded BackingIdentity
	if err := json.NewDecoder(rc).Decode(&recorded); err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()
	if recorded.Timer != strings.TrimSpace(guestTimerArgs) {
		t.Fatalf("recorded timer = %q, want %q", recorded.Timer, strings.TrimSpace(guestTimerArgs))
	}

	write := func(identity BackingIdentity) {
		t.Helper()
		body, _ := json.Marshal(identity)
		if err := f.m.storage.Put(ctx, "snap/d1/captures/c1/v2/backing", bytes.NewReader(body)); err != nil {
			t.Fatal(err)
		}
	}
	legacy := recorded
	legacy.Version, legacy.Timer = 1, ""
	write(legacy)
	if err := f.m.verifySnapshotBacking(ctx, snap, f.base); !errors.Is(err, ErrSnapshotBackingUnverified) {
		t.Fatalf("version-1 capture: %v, want ErrSnapshotBackingUnverified", err)
	}
	other := recorded
	other.Timer = "clocksource=tsc"
	write(other)
	if err := f.m.verifySnapshotBacking(ctx, snap, f.base); !errors.Is(err, ErrSnapshotBackingChanged) {
		t.Fatalf("other timer profile: %v, want ErrSnapshotBackingChanged", err)
	}
}
