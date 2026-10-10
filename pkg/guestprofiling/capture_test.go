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
