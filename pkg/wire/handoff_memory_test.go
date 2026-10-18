//go:build unix

package wire

// adr: 1580

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A reload during a transient memory spike waits for the cgroup to fall
// back under the hand-off share instead of refusing at once; a spike that
// outlasts the wait still refuses (ADR 20261018132058).
func TestWaitForMemory(t *testing.T) {
	over := errors.New("cgroup uses 300 of 512 MB")
	cases := map[string]struct {
		overFor  int // checks that report over the share
		wait     time.Duration
		wantErr  error
		wantFree int
	}{
		"under the share":         {overFor: 0, wait: time.Second, wantErr: nil, wantFree: 0},
		"freed at once":           {overFor: 1, wait: time.Second, wantErr: nil, wantFree: 1},
		"spike passes in time":    {overFor: 4, wait: time.Second, wantErr: nil, wantFree: 4},
		"spike outlasts the wait": {overFor: 1000, wait: 30 * time.Millisecond, wantErr: over},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			checks, frees := 0, 0
			allowed := func() error {
				checks++
				if checks <= tc.overFor {
					return over
				}
				return nil
			}
			err := waitForMemory(context.Background(), allowed, func() { frees++ }, tc.wait, time.Millisecond)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("waitForMemory = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && frees != tc.wantFree {
				t.Fatalf("freed memory %d times, want %d", frees, tc.wantFree)
			}
		})
	}
}

func TestWaitForMemoryStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := waitForMemory(ctx, func() error { return errors.New("over") }, func() {}, time.Minute, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForMemory = %v, want context.Canceled", err)
	}
}
