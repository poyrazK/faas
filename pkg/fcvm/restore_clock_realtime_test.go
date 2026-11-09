package fcvm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

// adr: 20261109120000 — clock_realtime needs Firecracker 1.16+.
func TestClockRealtimeSupported(t *testing.T) {
	for version, want := range map[string]bool{
		"1.7.0": false, "1.15.2": false, "1.16.0": true, "1.17.0": true, "": false, "garbage": false,
	} {
		if got := ClockRealtimeSupported(version); got != want {
			t.Errorf("ClockRealtimeSupported(%q) = %v, want %v", version, got, want)
		}
	}
}

// adr: 20261109120000 — only a tsc host clocksource records the realtime flag.
func TestHostClocksourceTSC(t *testing.T) {
	dir := t.TempDir()
	for name, want := range map[string]bool{"tsc\n": true, "kvm-clock\n": false, "hpet": false} {
		path := filepath.Join(dir, "clocksource")
		if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := hostClocksourceTSCAt(path); got != want {
			t.Errorf("clocksource %q: got %v, want %v", name, got, want)
		}
	}
	if hostClocksourceTSCAt(filepath.Join(dir, "missing")) {
		t.Fatal("an unreadable clocksource must not enable the clock advance")
	}
}

// adr: 20261109120000 — the load body carries clock_realtime only when enabled.
func TestSnapshotLoadBodyClockRealtime(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), 0)
	off, _ := json.Marshal(v.snapshotLoadBody("vmstate", "mem", true))
	if want := `{"mem_backend":{"backend_path":"mem","backend_type":"File"},"resume_vm":true,"snapshot_path":"vmstate"}`; string(off) != want {
		t.Fatalf("disabled body = %s, want %s", off, want)
	}
	v.WithRestoreClockRealtime(true)
	on, _ := json.Marshal(v.snapshotLoadBody("vmstate", "mem", false))
	if want := `{"clock_realtime":true,"mem_backend":{"backend_path":"mem","backend_type":"File"},"resume_vm":false,"snapshot_path":"vmstate"}`; string(on) != want {
		t.Fatalf("enabled body = %s, want %s", on, want)
	}
}

// adr: 20261109120000 — repeated ClockRealtimeNotInState refusals turn the
// clock advance off; other load errors never do.
func TestNoteSnapshotLoadErrorDisablesAfterRepeatedRefusals(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), 0).WithRestoreClockRealtime(true)
	for i := 0; i < 10; i++ {
		v.noteSnapshotLoadError(errors.New("firecracker PUT /snapshot/load: 400 Bad Request: memory file missing"))
	}
	v.noteSnapshotLoadError(nil)
	if !v.RestoreClockRealtime() {
		t.Fatal("unrelated load errors disabled the clock advance")
	}
	refusal := fmt.Errorf("vmm: load snapshot: %w", errors.New(`firecracker PUT /snapshot/load: 400 Bad Request: {"fault_message":"Load snapshot error: Failed to restore from snapshot: Failed to restore VM state: clock_realtime requested but not present in the snapshot state"}`))
	for i := 1; i < clockRealtimeRefusalLimit; i++ {
		v.noteSnapshotLoadError(refusal)
		if !v.RestoreClockRealtime() {
			t.Fatalf("disabled after %d refusals, want %d", i, clockRealtimeRefusalLimit)
		}
	}
	v.noteSnapshotLoadError(refusal)
	if v.RestoreClockRealtime() {
		t.Fatal("clock advance still enabled after the refusal limit")
	}
	if _, ok := v.snapshotLoadBody("vmstate", "mem", true)["clock_realtime"]; ok {
		t.Fatal("disabled VMM still sends clock_realtime")
	}
}

// adr: 20261109120000 — the verified load accepts the VMM's clock advance and
// still rejects any other value or extra field.
func TestVerifiedSnapshotLoadCommandClockRealtime(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), 0).WithRestoreClockRealtime(true)
	body := v.snapshotLoadBody(vmstateSnapshotName, memSnapshotName, true)
	raw, err := verifiedSnapshotLoadCommand(RestoreSpec{}, body)
	if err != nil {
		t.Fatalf("enabled clock advance rejected: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil || got["clock_realtime"] != true {
		t.Fatalf("command %s lost clock_realtime (err %v)", raw, err)
	}
	for name, value := range map[string]any{"false": false, "string": "true", "number": 1} {
		bad := v.snapshotLoadBody(vmstateSnapshotName, memSnapshotName, true)
		bad["clock_realtime"] = value
		if _, err := verifiedSnapshotLoadCommand(RestoreSpec{}, bad); !errors.Is(err, runtimeadmission.ErrInvalid) {
			t.Errorf("clock_realtime=%s accepted: %v", name, err)
		}
	}
	extra := v.snapshotLoadBody(vmstateSnapshotName, memSnapshotName, true)
	extra["track_dirty_pages"] = true
	if _, err := verifiedSnapshotLoadCommand(RestoreSpec{}, extra); !errors.Is(err, runtimeadmission.ErrInvalid) {
		t.Fatalf("extra field accepted: %v", err)
	}
}
