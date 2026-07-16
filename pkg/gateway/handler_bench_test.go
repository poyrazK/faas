package gateway

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// BenchmarkHandlerColdWake measures the cold-path request handler (the path
// that does the WakeGate → Backend.Wake → Backend.Target → proxy). Useful for
// catching regressions in the wake-coalesce path and the metrics overhead.
//
// We rebuild Handler per iteration (so each iteration pays the cold wake
// cost) but reuse the upstream Server across iterations (HTTP/1.1 keep-alive
// to localhost avoids port-binding storm under -benchtime=1s).
func BenchmarkHandlerColdWake(b *testing.B) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello from app"))
	}))
	defer upstream.Close()
	req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
	for i := 0; i < b.N; i++ {
		backend := &fakeBackend{
			app:      App{ID: "bench-app", Plan: api.PlanScale},
			host:     "jane-api.apps.dom",
			upstream: upstream.Listener.Addr().String(),
		}
		h := NewHandlerWith(backend, NewMetrics(), nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d, want 200", rec.Code)
		}
	}
}

// BenchmarkHandlerHotPath measures the hot path (Backend already has a
// running instance). The wake gate + metrics + request id are ALL still on
// the hot path; this is the SLO budget for the warm case (spec §4.1 wants
// < 2 ms p50 added latency).
func BenchmarkHandlerHotPath(b *testing.B) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello from app"))
	}))
	defer upstream.Close()
	backend := &fakeBackend{
		app:      App{ID: "bench-app", Plan: api.PlanScale},
		host:     "jane-api.apps.dom",
		upstream: upstream.Listener.Addr().String(),
		running:  true,
	}
	h := NewHandlerWith(backend, NewMetrics(), nil)
	req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d, want 200", rec.Code)
		}
	}
}

// BenchmarkHandlerConcurrentCold simulates N concurrent first-requests to a
// parked app. The single-flight guarantee means exactly ONE Wake() should
// be called per run, regardless of N. Throughput and per-op latency tell
// us how the coalescing scales under load.
func BenchmarkHandlerConcurrentCold(b *testing.B) {
	const fans = 100
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello from app"))
	}))
	defer upstream.Close()
	for i := 0; i < b.N; i++ {
		backend := &fakeBackend{
			app:      App{ID: "bench-app", Plan: api.PlanScale},
			host:     "jane-api.apps.dom",
			upstream: upstream.Listener.Addr().String(),
		}
		h := NewHandlerWith(backend, NewMetrics(), nil)
		var wg sync.WaitGroup
		wg.Add(fans)
		for j := 0; j < fans; j++ {
			go func() {
				defer wg.Done()
				req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					b.Errorf("status = %d, want 200", rec.Code)
				}
			}()
		}
		wg.Wait()
	}
}
