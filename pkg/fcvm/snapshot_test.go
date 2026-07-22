package fcvm

import "testing"

func TestSnapshotUsable(t *testing.T) {
	good := &Snapshot{FCVersion: "1.7.0", StorageKey: "snap/d/mem", VMStatePath: "/s"}
	tests := []struct {
		name    string
		snap    *Snapshot
		version string
		want    bool
	}{
		{"nil is never usable", nil, "1.7.0", false},
		{"match", good, "1.7.0", true},
		{"version mismatch (ADR-005)", good, "1.8.0", false},
		{"stale", &Snapshot{FCVersion: "1.7.0", Stale: true, StorageKey: "snap/d/mem", VMStatePath: "/s"}, "1.7.0", false},
		// #96 slice 3 contract: StorageKey is the only blob locator;
		// VMStatePath is still supplied until the vmstate-Storage slice
		// lands (out of scope here).
		{"missing storage key", &Snapshot{FCVersion: "1.7.0", VMStatePath: "/s"}, "1.7.0", false},
		{"missing vmstate", &Snapshot{FCVersion: "1.7.0", StorageKey: "snap/d/mem"}, "1.7.0", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.snap.Usable(tt.version); got != tt.want {
				t.Errorf("Usable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPlanWake(t *testing.T) {
	usable := &Snapshot{FCVersion: "1.7.0", StorageKey: "snap/d/mem", VMStatePath: "/s"}
	if PlanWake(usable, "1.7.0") != WakeRestore {
		t.Error("usable snapshot should plan a restore")
	}
	if PlanWake(usable, "9.9.9") != WakeColdBoot {
		t.Error("version-mismatched snapshot should plan a cold boot")
	}
	if PlanWake(nil, "1.7.0") != WakeColdBoot {
		t.Error("no snapshot should plan a cold boot")
	}
}

func TestWakeMethodString(t *testing.T) {
	if WakeRestore.String() != "restore" || WakeColdBoot.String() != "cold_boot" {
		t.Errorf("unexpected method strings: %s %s", WakeRestore, WakeColdBoot)
	}
}
