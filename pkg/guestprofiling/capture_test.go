package guestprofiling

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/pprof/profile"
	"github.com/onebox-faas/faas/pkg/api"
)

var retained [][]byte

//go:noinline
func retainHeap() {
	for i := 0; i < 256; i++ {
		retained = append(retained, make([]byte, 64<<10))
	}
}

func TestGoCollectorCaptureUploadsHeapOnce(t *testing.T) {
	retainHeap()
	var mu sync.Mutex
	kinds := map[string]int{}
	var heapBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/control":
			_ = json.NewEncoder(w).Encode(control{Enabled: true, Epoch: "22222222222222222222222222222222", WindowSeconds: 1, Kinds: []string{"heap", "cpu"}, Capture: true})
		case "/ingest":
			body, _ := io.ReadAll(io.LimitReader(r.Body, api.ProfileMaxCompressedBytes+1))
			kind := r.URL.Query().Get("kind")
			mu.Lock()
			kinds[kind]++
			if kind == "heap" {
				heapBody = body
			}
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); run(ctx, server.URL) }()
	time.Sleep(2500 * time.Millisecond)
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if kinds["heap"] != 1 || kinds[""] != 1 {
		t.Fatalf("capture epoch must yield one CPU and one heap upload, got %v", kinds)
	}
	p, err := profile.Parse(bytes.NewReader(heapBody))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, st := range p.SampleType {
		if st.Type == "inuse_space" && st.Unit == "bytes" {
			found = true
		}
	}
	if !found {
		t.Fatalf("heap profile lacks inuse_space: %v", p.SampleType)
	}
}

func TestGoCollectorCheckpointsHeapDuringCapture(t *testing.T) {
	retainHeap()
	var mu sync.Mutex
	checkpoints, finals := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/control":
			_ = json.NewEncoder(w).Encode(control{Enabled: true, Epoch: "44444444444444444444444444444444", WindowSeconds: 3, Kinds: []string{"heap"}, Capture: true, CheckpointSeconds: 1})
		case "/ingest":
			_, _ = io.Copy(io.Discard, r.Body)
			mu.Lock()
			if r.URL.Query().Get("kind") != "heap" {
				t.Errorf("unexpected kind %q", r.URL.Query().Get("kind"))
			}
			if r.URL.Query().Get("checkpoint") == "1" {
				checkpoints++
			} else {
				finals++
			}
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); run(ctx, server.URL) }()
	time.Sleep(3600 * time.Millisecond)
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	// A 3s window with 1s checkpoints sends about two interim heaps before
	// the final one.
	if checkpoints < 1 || checkpoints > 3 || finals != 1 {
		t.Fatalf("checkpoints=%d finals=%d", checkpoints, finals)
	}
}

//go:noinline
func parkedCollectorFixture(release <-chan struct{}) { <-release }

func TestGoCollectorSnapshotsStacksOncePerCapture(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	go parkedCollectorFixture(release)
	var mu sync.Mutex
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/control":
			_ = json.NewEncoder(w).Encode(control{Enabled: true, Epoch: "55555555555555555555555555555555", WindowSeconds: 2, Kinds: []string{"stacks"}, Capture: true})
		case "/ingest":
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			if r.URL.Query().Get("kind") != "stacks" {
				t.Errorf("unexpected kind %q", r.URL.Query().Get("kind"))
			}
			bodies = append(bodies, body)
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); run(ctx, server.URL) }()
	time.Sleep(2500 * time.Millisecond)
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("stack snapshots = %d, want one per capture epoch", len(bodies))
	}
	p, err := profile.Parse(bytes.NewReader(bodies[0]))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.SampleType) == 0 || p.SampleType[0].Type != "goroutine" {
		t.Fatalf("stacks profile sample types = %v", p.SampleType)
	}
	for _, fn := range p.Function {
		if bytes.HasSuffix([]byte(fn.Name), []byte("parkedCollectorFixture")) {
			return
		}
	}
	t.Fatal("parked goroutine missing from the snapshot")
}

func TestGoCollectorDormantPollsSlowly(t *testing.T) {
	var mu sync.Mutex
	polls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		polls++
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(control{Epoch: "33333333333333333333333333333333", WindowSeconds: 10})
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); run(ctx, server.URL) }()
	time.Sleep(1500 * time.Millisecond)
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if polls < 1 || polls > 3 {
		t.Fatalf("dormant collector polled %d times in 1.5s", polls)
	}
}
