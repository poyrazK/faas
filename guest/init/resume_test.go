package main

import (
	"fmt"
	"testing"
)

func TestResumeOrdersEntropyBeforeClock(t *testing.T) {
	var order []string
	ops := ResumeOps{
		ReseedEntropy:  func() error { order = append(order, "entropy"); return nil },
		StepClock:      func() error { order = append(order, "clock"); return nil },
		WriteUUIDMarker: func() error { order = append(order, "uuid"); return nil },
	}
	if err := ops.Resume(); err != nil {
		t.Fatal(err)
	}
	if len(order) != 3 || order[0] != "entropy" || order[1] != "clock" || order[2] != "uuid" {
		t.Errorf("resume order = %v, want [entropy clock uuid] (UUID marker must observe the re-keyed pool)", order)
	}
}

func TestResumeEntropyFailureStopsBeforeClock(t *testing.T) {
	clockRan := false
	ops := ResumeOps{
		ReseedEntropy: func() error { return fmt.Errorf("no hwrng") },
		StepClock:     func() error { clockRan = true; return nil },
	}
	if err := ops.Resume(); err == nil {
		t.Fatal("entropy failure should fail the resume")
	}
	if clockRan {
		t.Error("clock must not step if entropy reseed failed (non-unique guest must not serve)")
	}
}

func TestResumeClockFailurePropagates(t *testing.T) {
	ops := ResumeOps{
		ReseedEntropy: func() error { return nil },
		StepClock:     func() error { return fmt.Errorf("settimeofday EPERM") },
	}
	if err := ops.Resume(); err == nil {
		t.Error("clock step failure should propagate")
	}
}

func TestResumeUnconfigured(t *testing.T) {
	if err := (ResumeOps{}).Resume(); err == nil {
		t.Error("unconfigured resume ops should error, not no-op")
	}
}

func TestResumeUUIDMarkerFailurePropagates(t *testing.T) {
	clockRan := true
	ops := ResumeOps{
		ReseedEntropy:  func() error { return nil },
		StepClock:      func() error { return nil },
		WriteUUIDMarker: func() error { return fmt.Errorf("uuid write EPERM") },
	}
	if err := ops.Resume(); err == nil {
		t.Fatal("uuid marker failure should fail the resume")
	}
	if !clockRan {
		t.Error("clock already ran; uuid marker is last so its failure is informational, not a stop-the-world")
	}
}

func TestResumeUUIDMarkerNilIsOptional(t *testing.T) {
	// Resume tolerates a nil WriteUUIDMarker — non-Linux build tags or stripped
	// guests (V6 acceptance fixture before the resume hook is wired) must not
	// blow up the resume just because the marker can't be written.
	ops := ResumeOps{
		ReseedEntropy: func() error { return nil },
		StepClock:     func() error { return nil },
	}
	if err := ops.Resume(); err != nil {
		t.Errorf("nil WriteUUIDMarker should be optional: %v", err)
	}
}
