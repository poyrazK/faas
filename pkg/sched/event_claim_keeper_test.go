package sched_test

// adr: 20261028131500

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
)

func TestEventClaimKeeperRenewsUntilStopped(t *testing.T) {
	var calls atomic.Int64
	l := &sched.Loop{}
	stop := l.XKeepEventClaims(context.Background(), time.Millisecond, func(context.Context, time.Time) error {
		calls.Add(1)
		return nil
	})
	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	stop()
	if calls.Load() < 3 {
		t.Fatalf("keeper renewed %d times in 5 s, want at least 3", calls.Load())
	}
	after := calls.Load()
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != after {
		t.Fatalf("keeper renewed after stop: %d then %d", after, calls.Load())
	}
}
