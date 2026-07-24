package gateway

import (
	"context"
	"errors"
	"testing"
)

func TestFakeSchedulerAdmitInstance(t *testing.T) {
	s := NewFakeScheduler("node-fake-1").WithInstanceID("i-7").WithWakeID("w-9")
	instanceID, nodeID, wakeID, atCap, err := s.AdmitInstance(context.Background(), "app-1")
	if err != nil {
		t.Fatalf("AdmitInstance err = %v", err)
	}
	if atCap {
		t.Errorf("atCapacity = true; want false on admit path")
	}
	if nodeID != "node-fake-1" {
		t.Errorf("nodeID = %q, want node-fake-1", nodeID)
	}
	if instanceID != "i-7" {
		t.Errorf("instanceID = %q, want i-7", instanceID)
	}
	if wakeID != "w-9" {
		t.Errorf("wakeID = %q, want w-9", wakeID)
	}
	if got := s.Calls(); got != 1 {
		t.Errorf("Calls = %d, want 1", got)
	}
	if got := s.AdmitsFor("app-1"); got != 1 {
		t.Errorf("AdmitsFor = %d, want 1", got)
	}
}

func TestFakeSchedulerMintsFreshInstanceID(t *testing.T) {
	s := NewFakeScheduler("node-fake-1")
	ids := map[string]bool{}
	for i := 0; i < 3; i++ {
		id, _, _, _, err := s.AdmitInstance(context.Background(), "app-1")
		if err != nil {
			t.Fatalf("AdmitInstance: %v", err)
		}
		if ids[id] {
			t.Errorf("duplicate instance id %q on call #%d", id, i)
		}
		ids[id] = true
	}
}

func TestFakeSchedulerWithErr(t *testing.T) {
	want := errors.New("boom")
	s := NewFakeScheduler("node-fake-1").WithErr(want)
	_, _, _, _, err := s.AdmitInstance(context.Background(), "app-1")
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

func TestNoopSchedulerReturnsUnconfigured(t *testing.T) {
	_, _, _, _, err := NoopScheduler{}.AdmitInstance(context.Background(), "app-1")
	if !errors.Is(err, ErrSchedulerUnconfigured) {
		t.Errorf("err = %v, want ErrSchedulerUnconfigured", err)
	}
}
