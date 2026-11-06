package faas

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type idlePool struct {
	mu    sync.Mutex
	calls []int
}

func (p *idlePool) SetMaxIdleConns(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, n)
}

func hookRequest(path, header, remote string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, path, nil)
	r.RemoteAddr = remote
	if header != "" {
		r.Header.Set(header, "1")
	}
	return r
}

func TestLifecycleHooksServeOnlyMarkedLoopbackPosts(t *testing.T) {
	hooks := NewLifecycleHooks()
	var restored atomic.Int32
	hooks.OnAfterRestore(func(context.Context) error { restored.Add(1); return nil })
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	handler := hooks.Middleware(next)

	for _, tc := range []struct {
		name string
		req  *http.Request
		want int
	}{
		{"restore", hookRequest(DefaultAfterRestorePath, "X-Faas-After-Restore", "127.0.0.1:5000"), http.StatusNoContent},
		{"ipv6 loopback", hookRequest(DefaultAfterRestorePath, "X-Faas-After-Restore", "[::1]:5000"), http.StatusNoContent},
		{"missing header", hookRequest(DefaultAfterRestorePath, "", "127.0.0.1:5000"), http.StatusForbidden},
		{"wrong header", hookRequest(DefaultAfterRestorePath, "X-Faas-Before-Checkpoint", "127.0.0.1:5000"), http.StatusForbidden},
		{"remote peer", hookRequest(DefaultAfterRestorePath, "X-Faas-After-Restore", "10.0.0.7:5000"), http.StatusForbidden},
		{"other path", hookRequest("/v1/query", "", "10.0.0.7:5000"), http.StatusTeapot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, tc.req)
			if recorder.Code != tc.want {
				t.Fatalf("status %d, want %d", recorder.Code, tc.want)
			}
		})
	}
	get := httptest.NewRequest(http.MethodGet, DefaultAfterRestorePath, nil)
	get.RemoteAddr = "127.0.0.1:1"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, get)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status %d", recorder.Code)
	}
	if got := restored.Load(); got != 2 {
		t.Fatalf("after-restore callbacks ran %d times, want 2", got)
	}
}

func TestLifecycleHooksFailOnErrorPanicAndBudget(t *testing.T) {
	for _, tc := range []struct {
		name     string
		callback func(context.Context) error
	}{
		{"error", func(context.Context) error { return errors.New("pool refused") }},
		{"panic", func(context.Context) error { panic("boom") }},
		{"budget", func(ctx context.Context) error { time.Sleep(200 * time.Millisecond); return nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hooks := NewLifecycleHooks()
			hooks.Budget = 50 * time.Millisecond
			hooks.OnBeforeCheckpoint(tc.callback)
			recorder := httptest.NewRecorder()
			hooks.ServeHTTP(recorder, hookRequest(DefaultBeforeCheckpointPath, "X-Faas-Before-Checkpoint", "127.0.0.1:1"))
			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status %d, want 500", recorder.Code)
			}
		})
	}
}

func TestLifecycleHooksShareConcurrentRun(t *testing.T) {
	hooks := NewLifecycleHooks()
	release := make(chan struct{})
	var runs atomic.Int32
	hooks.OnAfterRestore(func(context.Context) error { runs.Add(1); <-release; return nil })
	var wg sync.WaitGroup
	codes := make([]int, 3)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			recorder := httptest.NewRecorder()
			hooks.ServeHTTP(recorder, hookRequest(DefaultAfterRestorePath, "X-Faas-After-Restore", "127.0.0.1:1"))
			codes[i] = recorder.Code
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if runs.Load() != 1 {
		t.Fatalf("callbacks ran %d times for overlapping deliveries", runs.Load())
	}
	for _, code := range codes {
		if code != http.StatusNoContent {
			t.Fatalf("codes %v", codes)
		}
	}
}

func TestRecycleIdleConnections(t *testing.T) {
	hooks := NewLifecycleHooks()
	pool := &idlePool{}
	hooks.RecycleIdleConnections(pool, 7)
	for _, req := range []*http.Request{
		hookRequest(DefaultBeforeCheckpointPath, "X-Faas-Before-Checkpoint", "127.0.0.1:1"),
		hookRequest(DefaultAfterRestorePath, "X-Faas-After-Restore", "127.0.0.1:1"),
	} {
		recorder := httptest.NewRecorder()
		hooks.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("status %d", recorder.Code)
		}
	}
	if want := []int{0, 7, 0, 7}; len(pool.calls) != 4 || pool.calls[0] != want[0] || pool.calls[1] != want[1] || pool.calls[2] != want[2] || pool.calls[3] != want[3] {
		t.Fatalf("SetMaxIdleConns calls %v, want %v", pool.calls, want)
	}
}
