package workerpool

import (
	"context"
	"io"
	"testing"
	"time"
)

type completionRequest struct {
	Value      string `json:"value"`
	Completion bool   `json:"completion"`
}

func TestCompletionPipeFailureWaitsForWorkerExit(t *testing.T) {
	for _, duringCancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "already-failed", true: "awaiting-ack"}[duringCancel], func(t *testing.T) {
			p, pid := muxPool(t, 1, "HELPER_COMPLETION_VERSION=1", "HELPER_CANCEL_ACK_DELAY_MS=1000")
			lines, err := p.open(t.Context(), completionRequest{Value: "hold", Completion: true})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := lines.Next(); err != nil {
				t.Fatal(err)
			}
			if duringCancel {
				lines.Close(false)
				deadline := time.Now().Add(time.Second)
				for {
					lines.w.mux.mu.Lock()
					pending := lines.w.mux.cancels[lines.id] != nil
					lines.w.mux.mu.Unlock()
					if pending {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("cancellation fence was not registered")
					}
					time.Sleep(time.Millisecond)
				}
			}
			// Simulate the stdout reader failing while this helper process
			// remains alive. A failed pipe is not a cleanup acknowledgement.
			lines.w.mux.fail(io.EOF)
			if !duringCancel {
				lines.Close(false)
			}
			select {
			case <-lines.Released():
			case <-time.After(3 * time.Second):
				t.Fatal("failed worker was not retired")
			}
			if processRunning(pid) || p.liveCount() != 0 {
				t.Fatal("execution ownership released before worker exit")
			}
		})
	}
}

func (r completionRequest) RequiresCompletion() bool { return r.Completion }

func TestCompletionCapabilityRefusesBeforeSending(t *testing.T) {
	p, _ := muxPool(t, 2)
	if _, err := p.open(t.Context(), completionRequest{Value: "side-effect", Completion: true}); err == nil {
		t.Fatal("accepted background work on an unsupported adapter")
	}
	p.mu.Lock()
	w := p.shared[0]
	active := w.active
	p.mu.Unlock()
	w.mux.mu.Lock()
	requests := w.mux.nextID
	w.mux.mu.Unlock()
	if active != 0 || requests != 0 {
		t.Fatal("unsupported request reached the adapter or leaked a slot")
	}
}

func TestCompletionRetainsSingleSlotUntilCleanup(t *testing.T) {
	p, _ := muxPool(t, 1, "HELPER_COMPLETION_VERSION=1", "HELPER_CANCEL_ACK_DELAY_MS=200")
	lines, err := p.open(t.Context(), completionRequest{Value: "hold", Completion: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lines.Next(); err != nil {
		t.Fatal(err)
	}
	lines.Close(true) // Only the response was read; completion is still pending.
	select {
	case <-lines.Released():
		t.Fatal("response completion released a busy slot")
	case <-time.After(50 * time.Millisecond):
	}
	p.mu.Lock()
	active := lines.w.active
	p.mu.Unlock()
	if active != 1 {
		t.Fatalf("active slots during cleanup = %d, want 1", active)
	}
	select {
	case <-lines.Released():
	case <-time.After(3 * time.Second):
		t.Fatal("acknowledged cleanup did not release its slot")
	}
	// Explicit completion permits an immediate healthy release, without
	// waiting for the helper's deliberately slow cancellation acknowledgement.
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	next, err := p.open(ctx, completionRequest{Value: "done", Completion: true})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := next.Next(); err != nil {
			t.Fatal(err)
		}
	}
	next.Close(true)
	select {
	case <-next.Released():
	default:
		t.Fatal("explicit execution completion did not release its slot")
	}
}
