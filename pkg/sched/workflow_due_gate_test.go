package sched_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
)

type countingDueReader struct {
	due   bool
	err   error
	reads atomic.Int32
}

func (r *countingDueReader) WorkflowRunsDue(context.Context) (bool, error) {
	r.reads.Add(1)
	return r.due, r.err
}

// TestWorkflowDueGateReadsOncePerTick pins the shared due check: every slot a
// tick offers gets one answer from a single read, and a failed read opens the
// slots so the claims decide.
func TestWorkflowDueGateReadsOncePerTick(t *testing.T) {
	for _, tc := range []struct {
		name string
		due  bool
		err  error
		want bool
	}{
		{"nothing due", false, nil, false},
		{"due", true, nil, true},
		{"check failed", false, errors.New("db down"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &countingDueReader{due: tc.due, err: tc.err}
			open := sched.OpenWorkflowDueGate(reader)
			var wg sync.WaitGroup
			got := make([]bool, 4)
			for slot := range got {
				wg.Add(1)
				go func() {
					defer wg.Done()
					got[slot] = open(t.Context())
				}()
			}
			wg.Wait()
			for slot, open := range got {
				if open != tc.want {
					t.Fatalf("slot %d open=%t, want %t", slot, open, tc.want)
				}
			}
			if n := reader.reads.Load(); n != 1 {
				t.Fatalf("%d due reads for one tick, want 1", n)
			}
		})
	}
	if !sched.OpenWorkflowDueGate(nil)(t.Context()) {
		t.Fatal("a store without a due check must open every slot")
	}
}
