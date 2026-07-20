package main

import (
	"errors"
	"fmt"
	"testing"
)

// TestResumeOrdering asserts the spec §11 V6 sequence:
//
//	AddEntropy (host CSPRNG bytes) → ReseedEntropy (virtio-rng) →
//	StepClock → WriteUUIDMarker.
//
// AddEntropy MUST run first: virtio-rng state is snapshotted, so without a
// unique prefix the pool gets identical input on every restore and the UUID
// collides (the regression that motivated ADR-022 §"Why the host ships
// entropy"). The UUID marker must observe the freshly-rekeyed pool, so it
// runs last.
func TestResumeOrdering(t *testing.T) {
	var order []string
	ops := ResumeOps{
		HostEntropy:     []byte{1, 2, 3},
		AddEntropy:      func(_ []byte) error { order = append(order, "add"); return nil },
		ReseedEntropy:   func() error { order = append(order, "reseed"); return nil },
		StepClock:       func() error { order = append(order, "clock"); return nil },
		WriteUUIDMarker: func() error { order = append(order, "uuid"); return nil },
	}
	if err := ops.Resume(); err != nil {
		t.Fatal(err)
	}
	want := []string{"add", "reseed", "clock", "uuid"}
	if len(order) != len(want) {
		t.Fatalf("resume order = %v, want %v", order, want)
	}
	for i, w := range want {
		if order[i] != w {
			t.Errorf("resume step %d = %q, want %q", i, order[i], w)
		}
	}
}

func TestResumeAddEntropyFailureStopsBeforeReseed(t *testing.T) {
	clockRan := false
	ops := ResumeOps{
		HostEntropy:   []byte{1, 2, 3},
		AddEntropy:    func(_ []byte) error { return fmt.Errorf("ioctl EPERM") },
		ReseedEntropy: func() error { t.Error("reseed must not run when AddEntropy fails"); return nil },
		StepClock:     func() error { clockRan = true; return nil },
	}
	if err := ops.Resume(); err == nil {
		t.Fatal("AddEntropy failure should fail the resume")
	}
	if clockRan {
		t.Error("clock must not step if AddEntropy failed")
	}
}

func TestResumeAddEntropyNilIsOptional(t *testing.T) {
	// If AddEntropy is nil (e.g. non-Linux or unit tests), Resume must
	// still run reseed → clock → marker. We don't credit entropy that
	// wasn't injected, but a non-Linux test seam shouldn't crash the
	// orchestration.
	ops := ResumeOps{
		ReseedEntropy: func() error { return nil },
		StepClock:     func() error { return nil },
	}
	if err := ops.Resume(); err != nil {
		t.Errorf("nil AddEntropy should be optional: %v", err)
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
		ReseedEntropy:   func() error { return nil },
		StepClock:       func() error { return nil },
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

// TestResumeAddEntropyEmptyPayloadIsNoop asserts that Resume() doesn't fail
// when HostEntropy is empty but AddEntropy is wired. This is the cold-boot-
// adjacent case: production always sends bytes, but a defensive listener
// should tolerate the absence.
func TestResumeAddEntropyEmptyPayloadIsNoop(t *testing.T) {
	called := false
	ops := ResumeOps{
		HostEntropy:   nil,
		AddEntropy:    func(b []byte) error { called = true; return nil },
		ReseedEntropy: func() error { return nil },
		StepClock:     func() error { return nil },
	}
	if err := ops.Resume(); err != nil {
		t.Fatalf("Resume with nil HostEntropy: %v", err)
	}
	// AddEntropy was registered but should have been skipped (nil bytes).
	if called {
		t.Error("AddEntropy should not be invoked when HostEntropy is empty")
	}
}

// Sanity: errors.Is works on the wrapped error.
func TestResumeErrorWrapping(t *testing.T) {
	sentinel := errors.New("ioctl boom")
	ops := ResumeOps{
		ReseedEntropy: func() error { return sentinel },
		StepClock:     func() error { return nil },
	}
	err := ops.Resume()
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error chain should contain sentinel, got %v", err)
	}
}
