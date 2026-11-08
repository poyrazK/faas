package profiling

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

//go:noinline
func cpuHotWork() uint64 {
	x := uint64(1)
	for i := 0; i < 1000000; i++ {
		x = x*6364136223846793005 + 1
	}
	return x
}

// bridge is a fake guest-init bridge: it serves control documents from
// next and records uploads by kind and epoch.
type bridge struct {
	mu      sync.Mutex
	next    func() control
	uploads map[string][][]byte // "kind/epoch" → bodies
	acks    atomic.Int32
}

func newBridge(next func() control) (*bridge, *httptest.Server) {
	b := &bridge{next: next, uploads: map[string][][]byte{}}
	return b, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/control":
			_ = json.NewEncoder(w).Encode(b.next())
		case "/control/ack":
			b.acks.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case "/ingest":
			body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			kind := r.URL.Query().Get("kind")
			if kind == "" {
				kind = kindCPU
			}
			b.mu.Lock()
			key := kind + "/" + epochOf(r.URL.Query().Get("name"))
			b.uploads[key] = append(b.uploads[key], body)
			b.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func epochOf(name string) string {
	const prefix = `gregale{gregale_epoch="`
	if len(name) > len(prefix)+32 {
		return name[len(prefix) : len(prefix)+32]
	}
	return ""
}

func (b *bridge) count(key string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.uploads[key])
}

func (b *bridge) first(key string) []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.uploads[key]) == 0 {
		return nil
	}
	return b.uploads[key][0]
}

func runFor(t *testing.T, endpoint string, d time.Duration, work func(context.Context)) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); run(ctx, endpoint) }()
	if work != nil {
		go work(ctx)
	}
	time.Sleep(d)
	cancel()
	<-done
}

func TestCollectorCapturesCPUAndAcknowledgesCheckpoint(t *testing.T) {
	var suspended atomic.Bool
	b, server := newBridge(func() control {
		return control{Enabled: true, Suspended: suspended.Load(), Epoch: "11111111111111111111111111111111", WindowSeconds: 1}
	})
	defer server.Close()
	runFor(t, server.URL, 1800*time.Millisecond, func(ctx context.Context) {
		for ctx.Err() == nil {
			_ = cpuHotWork()
		}
	})
	body := b.first("cpu/11111111111111111111111111111111")
	if body == nil {
		t.Fatal("no CPU window uploaded")
	}
	if !profileFunctions(t, body)["cpuHotWork"] {
		t.Fatal("CPU samples did not identify the hot function")
	}
	suspended.Store(true)
	runFor(t, server.URL, 500*time.Millisecond, nil)
	if b.acks.Load() == 0 {
		t.Fatal("checkpoint stop was not acknowledged")
	}
}

func TestCollectorCaptureUploadsHeapOnce(t *testing.T) {
	b, server := newBridge(func() control {
		return control{Enabled: true, Epoch: "22222222222222222222222222222222", WindowSeconds: 1, Kinds: []string{"heap", "cpu"}, Capture: true}
	})
	defer server.Close()
	runFor(t, server.URL, 2500*time.Millisecond, nil)
	if b.count("heap/22222222222222222222222222222222") != 1 || b.count("cpu/22222222222222222222222222222222") != 1 {
		t.Fatalf("capture epoch must yield one CPU and one heap upload, got %v", b.uploads)
	}
	p, err := parsePprof(b.first("heap/22222222222222222222222222222222"))
	if err != nil || p.sampleTypes == 0 {
		t.Fatalf("heap profile unreadable: %v", err)
	}
}

// A capture that starts while a continuous window is open ends that window
// and sends it under its own epoch; a restore's new epoch discards it.
func TestCollectorFlushesTheContinuousWindowWhenACaptureStarts(t *testing.T) {
	const continuous, capture = "44444444444444444444444444444444", "55555555555555555555555555555555"
	start := time.Now()
	b, server := newBridge(func() control {
		if time.Since(start) < 600*time.Millisecond {
			return control{Enabled: true, Epoch: continuous, WindowSeconds: 10}
		}
		return control{Enabled: true, Epoch: capture, WindowSeconds: 1, Kinds: []string{"cpu"}, Capture: true}
	})
	defer server.Close()
	runFor(t, server.URL, 2*time.Second, nil)
	if b.count("cpu/"+continuous) != 1 {
		t.Fatalf("continuous window cut short by a capture was not sent: %v", b.uploads)
	}

	const restored = "66666666666666666666666666666666"
	start = time.Now()
	b, server = newBridge(func() control {
		if time.Since(start) < 600*time.Millisecond {
			return control{Enabled: true, Epoch: continuous, WindowSeconds: 10}
		}
		return control{Enabled: true, Epoch: restored, WindowSeconds: 10}
	})
	defer server.Close()
	runFor(t, server.URL, time.Second, nil)
	if b.count("cpu/"+continuous) != 0 {
		t.Fatal("a window frozen across a restore was sent under its old epoch")
	}
}

// A control poll that fails just as a capture's window ends still sends the
// window, once: the capture would otherwise come back empty.
func TestCollectorCaptureSurvivesFailedControlPoll(t *testing.T) {
	var mu sync.Mutex
	var first time.Time
	b, server := newBridge(func() control {
		return control{Enabled: true, Epoch: "77777777777777777777777777777777", WindowSeconds: 1, Kinds: []string{"heap", "cpu"}, Capture: true}
	})
	defer server.Close()
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/control" {
			mu.Lock()
			if first.IsZero() {
				first = time.Now()
			}
			since := time.Since(first)
			mu.Unlock()
			if since >= 900*time.Millisecond && since < 1500*time.Millisecond {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
		}
		server.Config.Handler.ServeHTTP(w, r)
	}))
	defer flaky.Close()
	runFor(t, flaky.URL, 2500*time.Millisecond, nil)
	if b.count("heap/77777777777777777777777777777777") != 1 || b.count("cpu/77777777777777777777777777777777") != 1 {
		t.Fatalf("capture window ended during a failed poll must yield one CPU and one heap upload, got %v", b.uploads)
	}
}

func TestRouteLabels(t *testing.T) {
	for _, tc := range []struct{ method, pattern, want string }{
		{"GET", "GET /orders/{id}", "GET /orders/{id}"},
		{"HEAD", "GET example.com/orders/{id}", "HEAD /orders/{id}"},
		{"POST", "/upload", "POST /upload"},
		{"GET", "", ""},
		{"BREW", "/pot", ""},
	} {
		if got := serveMuxRoute(tc.method, tc.pattern); got != tc.want {
			t.Errorf("serveMuxRoute(%q, %q) = %q, want %q", tc.method, tc.pattern, got, tc.want)
		}
	}
	startRouteRequestWindow()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/{id}", func(http.ResponseWriter, *http.Request) {})
	handler := ServeMux(mux)
	for range 3 {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/orders/42", nil))
	}
	report := stopRouteRequestWindow()
	if report == nil || report.Routes["GET /orders/{id}"] != 3 || !report.Complete {
		t.Fatalf("route request report = %+v", report)
	}
}
