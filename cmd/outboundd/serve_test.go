package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

type fakeGeneration struct {
	mu        sync.Mutex
	snapshots int
	ended     chan struct{}
	endOnce   sync.Once
	drained   bool
}

func newFakeGeneration() *fakeGeneration { return &fakeGeneration{ended: make(chan struct{})} }

func (f *fakeGeneration) WriteCacheSnapshot(string, time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshots++
	return 1, nil
}

func (f *fakeGeneration) EndAICalls() { f.endOnce.Do(func() { close(f.ended) }) }

// DrainAICalls stands for AI calls that run until ended or the drain ends.
func (f *fakeGeneration) DrainAICalls(ctx context.Context) {
	select {
	case <-f.ended:
	case <-ctx.Done():
	}
	f.mu.Lock()
	f.drained = true
	f.mu.Unlock()
}

func testServer(t *testing.T, handler http.Handler) *drainServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return &drainServer{name: "control", server: &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}, listener: listener}
}

// ADR-20261128150000: a stopping outboundd finishes its requests, ends its
// AI calls so they settle, and writes its caches for the next start.
func TestServeGenerationStopWritesCachesForTheNextStart(t *testing.T) {
	release := make(chan struct{})
	server := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		_, _ = io.WriteString(w, "done")
	}))
	gen := newFakeGeneration()
	stopping, stop := context.WithCancel(context.Background())
	generation, endGeneration := context.WithCancel(stopping)
	defer endGeneration()
	returned := make(chan error, 1)
	go func() {
		returned <- serveGeneration(stopping, generation, make(chan struct{}), []*drainServer{server}, gen, gen, "dir", slog.Default())
	}()
	answered := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + server.listener.Addr().String())
		if err != nil {
			answered <- err.Error()
			return
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		answered <- string(body)
	}()
	time.Sleep(100 * time.Millisecond)
	stop()
	time.AfterFunc(100*time.Millisecond, func() { gen.EndAICalls(); close(release) })
	if got := <-answered; got != "done" {
		t.Fatalf("request in flight at stop = %q, want it finished", got)
	}
	if err := <-returned; err != nil {
		t.Fatal(err)
	}
	if gen.snapshots != 1 || !gen.drained {
		t.Fatalf("snapshots = %d, drained = %v; want the caches written once after the AI calls drained", gen.snapshots, gen.drained)
	}
}

// A handed-off generation leaves the caches to the hand-off (written before
// the successor started) and, asked to stop while it drains, ends its AI
// calls at once instead of after the hand-off's half hour.
func TestServeGenerationAfterHandoffEndsAICallsWhenStopped(t *testing.T) {
	server := testServer(t, http.NotFoundHandler())
	gen := newFakeGeneration()
	stopping, stop := context.WithCancel(context.Background())
	defer stop()
	generation, endGeneration := context.WithCancel(stopping)
	handoff := make(chan struct{})
	returned := make(chan error, 1)
	go func() {
		returned <- serveGeneration(stopping, generation, handoff, []*drainServer{server}, gen, gen, "dir", slog.Default())
	}()
	close(handoff)
	endGeneration()
	select {
	case <-returned:
		t.Fatal("a handed-off generation returned while its AI calls still ran")
	case <-time.After(200 * time.Millisecond):
	}
	stop()
	select {
	case <-gen.ended:
	case <-time.After(5 * time.Second):
		t.Fatal("stopping a handed-off generation did not end its AI calls")
	}
	if err := <-returned; err != nil {
		t.Fatal(err)
	}
	if gen.snapshots != 0 {
		t.Fatalf("snapshots = %d; a handed-off generation's caches went with the hand-off", gen.snapshots)
	}
}
