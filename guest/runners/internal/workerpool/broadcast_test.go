package workerpool

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"
)

type hookRequest struct {
	Hook  string `json:"function_hook,omitempty"`
	Value string `json:"value,omitempty"`
}

// helperPool starts n workers of the mux helper with the given slots and
// returns the pool with all of them idle.
func helperPool(t *testing.T, n, slots int, extraEnv ...string) *pool {
	t.Helper()
	p := newPool(Spec{
		Executable: os.Args[0],
		Args:       []string{"-test.run=^TestWorkerpoolMuxHelperProcess$", "--"},
		Env:        append(append(os.Environ(), "GO_WANT_WORKERPOOL_MUX_HELPER=1", "HELPER_CONCURRENCY="+strconv.Itoa(slots)), extraEnv...),
	})
	t.Cleanup(func() {
		p.mu.Lock()
		idle, shared := append([]*worker(nil), p.idle...), append([]*worker(nil), p.shared...)
		p.mu.Unlock()
		for _, w := range idle {
			w.close()
		}
		for _, w := range shared {
			p.retire(w)
		}
	})
	var held []*worker
	for range n {
		w, err := p.acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, w)
	}
	for _, w := range held {
		p.release(w, true)
	}
	return p
}

// ADR-20261110190000 §3: a snapshot hook reaches every loaded worker,
// exclusive one-slot workers included, without the completion protocol.
func TestBroadcastVisitsEveryExclusiveWorker(t *testing.T) {
	p := helperPool(t, 2, 1, "HELPER_FUNCTION_HOOKS=1")
	results, err := p.broadcast(t.Context(), hookRequest{Hook: "after_restore"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Status != 204 || results[1].Status != 204 {
		t.Fatalf("results = %+v, want two 204s", results)
	}
	// The pool serves requests again afterwards.
	var got muxLine
	if err := p.invoke(t.Context(), testRequest{Value: "after"}, &got); err != nil || got.Value != "after" {
		t.Fatalf("invoke after broadcast = %+v, %v", got, err)
	}
}

func TestBroadcastVisitsSharedWorkers(t *testing.T) {
	p := helperPool(t, 1, 2, "HELPER_FUNCTION_HOOKS=1")
	results, err := p.broadcast(t.Context(), hookRequest{Hook: "before_snapshot"})
	if err != nil || len(results) != 1 || results[0].Status != 204 {
		t.Fatalf("results = %+v, %v", results, err)
	}
	results, err = p.broadcast(t.Context(), hookRequest{Hook: "fail"})
	if err != nil || len(results) != 1 || results[0].Status != 500 {
		t.Fatalf("failing hook results = %+v, %v", results, err)
	}
}

// An adapter that does not advertise hooks never sees the envelope: it would
// pass it to the customer's handler.
func TestBroadcastRefusesWorkersWithoutHooks(t *testing.T) {
	p := helperPool(t, 1, 1)
	if _, err := p.broadcast(t.Context(), hookRequest{Hook: "after_restore"}); !errors.Is(err, ErrBroadcastUnsupported) {
		t.Fatalf("err = %v, want ErrBroadcastUnsupported", err)
	}
	var got muxLine
	if err := p.invoke(t.Context(), testRequest{Value: "still-serving"}, &got); err != nil || got.Value != "still-serving" {
		t.Fatalf("invoke after refusal = %+v, %v", got, err)
	}
}

// A hook never runs beside a request: the broadcast waits for in-flight work.
func TestBroadcastWaitsForInFlightRequests(t *testing.T) {
	p := helperPool(t, 1, 1, "HELPER_FUNCTION_HOOKS=1")
	w, err := p.acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := p.broadcast(ctx, hookRequest{Hook: "after_restore"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("broadcast beside a request: %v", err)
	}
	p.release(w, true)
	if results, err := p.broadcast(t.Context(), hookRequest{Hook: "after_restore"}); err != nil || len(results) != 1 {
		t.Fatalf("broadcast after release = %+v, %v", results, err)
	}
}
