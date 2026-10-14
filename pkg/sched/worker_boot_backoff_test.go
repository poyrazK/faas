package sched

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// H10-28: a worker that fails every boot must not be re-admitted at once
// forever. The first failure replaces it immediately; later ones back off to
// five minutes, and RUNNING resets the count.
func TestWorkerFailureBackoff(t *testing.T) {
	var delays []time.Duration
	e := &Engine{
		log:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		workerRetryAfter: func(d time.Duration, _ func()) { delays = append(delays, d) },
	}
	worker := state.Instance{ID: "i", DeploymentID: "dep", Mode: string(state.InstanceModeWorker)}
	fail := func() {
		e.reconcileAfterInstanceTransition(context.Background(), worker, state.StateFailed)
	}
	for range 9 {
		fail()
	}
	want := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second,
		80 * time.Second, 160 * time.Second, 300 * time.Second, 300 * time.Second}
	if len(delays) != len(want) {
		t.Fatalf("delayed retries = %v, want %v (the first failure retries at once)", delays, want)
	}
	for i := range want {
		if delays[i] != want[i] {
			t.Fatalf("delays = %v, want %v", delays, want)
		}
	}

	e.reconcileAfterInstanceTransition(context.Background(), worker, state.StateRunning)
	delays = nil
	fail()
	if len(delays) != 0 {
		t.Fatalf("first failure after RUNNING was delayed %v", delays)
	}
	fail()
	if len(delays) != 1 || delays[0] != 5*time.Second {
		t.Fatalf("second failure after RUNNING delayed %v, want 5s", delays)
	}

	other := worker
	other.DeploymentID = "other"
	delays = nil
	e.reconcileAfterInstanceTransition(context.Background(), other, state.StateFailed)
	if len(delays) != 0 {
		t.Fatalf("another deployment inherited the backoff: %v", delays)
	}
}
