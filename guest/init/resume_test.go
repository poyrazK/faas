package main

import (
	"fmt"
	"testing"
)

func TestResumeOrdersEntropyBeforeClock(t *testing.T) {
	var order []string
	ops := ResumeOps{
		ReseedEntropy: func() error { order = append(order, "entropy"); return nil },
		StepClock:     func() error { order = append(order, "clock"); return nil },
	}
	if err := ops.Resume(); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "entropy" || order[1] != "clock" {
		t.Errorf("resume order = %v, want [entropy clock] (entropy must precede any app RNG use)", order)
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
