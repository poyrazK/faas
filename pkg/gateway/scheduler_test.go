package gateway

import (
	"context"
	"errors"
	"testing"
)

func TestFakeSchedulerReturnsAddress(t *testing.T) {
	s := NewFakeScheduler("1.2.3.4:8080").WithInstanceID("i-7")
	instanceID, addr, err := s.Wake(context.Background(), "app-1")
	if err != nil {
		t.Fatalf("Wake err = %v", err)
	}
	if addr != "1.2.3.4:8080" {
		t.Errorf("addr = %q, want 1.2.3.4:8080", addr)
	}
	if instanceID != "i-7" {
		t.Errorf("instanceID = %q, want i-7", instanceID)
	}
	if got := s.Calls(); got != 1 {
		t.Errorf("Calls = %d, want 1", got)
	}
	if got := s.WakesFor("app-1"); got != 1 {
		t.Errorf("WakesFor = %d, want 1", got)
	}
}

func TestFakeSchedulerWithErr(t *testing.T) {
	want := errors.New("boom")
	s := NewFakeScheduler("addr").WithErr(want)
	_, _, err := s.Wake(context.Background(), "app-1")
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

func TestNoopSchedulerReturnsUnconfigured(t *testing.T) {
	_, _, err := NoopScheduler{}.Wake(context.Background(), "app-1")
	if !errors.Is(err, ErrSchedulerUnconfigured) {
		t.Errorf("err = %v, want ErrSchedulerUnconfigured", err)
	}
}
