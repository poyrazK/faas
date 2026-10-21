package workerpool

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestLifecycleBatchVisitsAllWorkersAndRetainsCancellation(t *testing.T) {
	p, _ := muxPool(t, 2, "HELPER_COMPLETION_VERSION=1", "HELPER_CANCEL_ACK_DELAY_MS=150")
	var held []*worker
	for range maxWorkers * 2 {
		w, err := p.acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, w)
	}
	for _, w := range held {
		p.release(w, true)
	}
	batch, err := p.openAll(t.Context(), testRequest{Value: "echo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Lines) != maxWorkers {
		t.Fatalf("visited %d workers, want %d", len(batch.Lines), maxWorkers)
	}
	pids := map[int]bool{}
	for _, lines := range batch.Lines {
		line, err := lines.Next()
		if err != nil {
			t.Fatal(err)
		}
		var result muxLine
		if err := json.Unmarshal(line, &result); err != nil {
			t.Fatal(err)
		}
		pids[result.PID] = true
	}
	if len(pids) != maxWorkers {
		t.Fatal("lifecycle hook missed a loaded interpreter")
	}
	batch.Close(nil) // cancellation is deliberately acknowledged later
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	if _, err := p.open(ctx, testRequest{Value: "too-early"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("admission opened during batch cleanup: %v", err)
	}
	select {
	case <-batch.Released():
		t.Fatal("batch released before cancellation cleanup")
	default:
	}
	select {
	case <-batch.Released():
	case <-time.After(3 * time.Second):
		t.Fatal("batch cleanup never released admission")
	}
	var got muxLine
	if err := p.invoke(t.Context(), testRequest{Value: "after-cleanup"}, &got); err != nil {
		t.Fatal(err)
	}
}
