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
		// #96 slice 3 contract: StorageKey is the only mem blob locator;
		// vmstate is acceptable via either VMStatePath (legacy host path,
		// default-local / single-box) or VMStateStorageKey (canonical
		// StorageBackend key, remote / multi-node, #121 ADR-025 axis 2
		// slice 4).
		{"missing storage key", &Snapshot{FCVersion: "1.7.0", VMStatePath: "/s"}, "1.7.0", false},
		{"missing vmstate", &Snapshot{FCVersion: "1.7.0", StorageKey: "snap/d/mem"}, "1.7.0", false},
		// #121: vmstate via storage key alone (remote-node shape, no host
		// path) is usable. A regression that re-tightens the predicate to
		// require VMStatePath would silently fail every multi-node wake.
		{"vmstate via storage key", &Snapshot{FCVersion: "1.7.0", StorageKey: "snap/d/mem", VMStateStorageKey: "snap/d/vmstate"}, "1.7.0", true},
		// #121: vmstate via either locator, the engine-populated shape
		// (both fields carried for diagnostic logging on remote nodes).
		{"vmstate via both locators", &Snapshot{FCVersion: "1.7.0", StorageKey: "snap/d/mem", VMStatePath: "/s", VMStateStorageKey: "snap/d/vmstate"}, "1.7.0", true},
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
