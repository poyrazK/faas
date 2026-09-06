package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type partialWakeBackend struct {
	*cappedBurstBackend
	publish bool
	ready   chan struct{}
	wakes   atomic.Int32
}

func (b *partialWakeBackend) EnsureWarmCapacity(context.Context, string, string, string, int) (string, WakeMethod, bool, error) {
	b.wakes.Add(1)
	if b.ready == nil {
		<-b.release
	}
	if b.publish {
		b.AddTarget(Target{NodeID: "ssd", InstanceID: "ready-primary", WakeID: "01900000-0000-7000-8000-000000000001"})
	}
	if b.ready != nil {
		close(b.ready)
		<-b.release
	}
	// Model a healthy primary published through notifications while the
	// batch RPC times out waiting for another node. No RPC result is returned.
	return "", WakeMethodUnspecified, false, context.DeadlineExceeded
}

func TestHandlerPartialWakeServesReadyPrimary(t *testing.T) {
	for _, publish := range []bool{true, false} {
		name, want := "no ready target", http.StatusServiceUnavailable
		if publish {
			name, want = "ready primary", http.StatusOK
		}
		t.Run(name, func(t *testing.T) {
			b := &partialWakeBackend{cappedBurstBackend: &cappedBurstBackend{
				fakeBackend: &fakeBackend{app: App{ID: "app-1", Plan: api.PlanScale, MaxConcurrency: 2}, host: "app.example.com"},
				release:     make(chan struct{}), burstErr: context.DeadlineExceeded,
			}, publish: publish}
			h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
			var forwarded atomic.Int32
			h.WithForwarding(func(target Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if target.InstanceID != "ready-primary" {
						t.Errorf("forwarded to unready target: %+v", target)
					}
					forwarded.Add(1)
					w.WriteHeader(http.StatusOK)
				})
			})
			var once sync.Once
			release := func() { once.Do(func() { close(b.release) }) }
			t.Cleanup(release)
			results := make(chan int, 100)
			for range 100 {
				go func() {
					rec := httptest.NewRecorder()
					h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://app.example.com/", nil))
					results <- rec.Code
				}()
			}
			deadline := time.After(3 * time.Second)
			for h.gate.InflightWaiters(b.app.ID) != 100 {
				select {
				case <-deadline:
					t.Fatal("burst did not join the shared wake")
				case <-time.After(time.Millisecond):
				}
			}
			release()
			for range 100 {
				select {
				case got := <-results:
					if got != want {
						t.Errorf("status = %d, want %d", got, want)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("burst did not finish")
				}
			}
			if b.wakes.Load() != 1 {
				t.Errorf("initial batch calls = %d, want one", b.wakes.Load())
			}
			wantForwarded := int32(0)
			if publish {
				wantForwarded = 100
			}
			if forwarded.Load() != wantForwarded {
				t.Errorf("forwarded = %d, want %d", forwarded.Load(), wantForwarded)
			}
		})
	}
}

func TestPartialWakeDoesNotOverrideRequestCancellation(t *testing.T) {
	b := &partialWakeBackend{cappedBurstBackend: &cappedBurstBackend{
		fakeBackend: &fakeBackend{app: App{ID: "app-1", Plan: api.PlanScale, MaxConcurrency: 2}}, release: make(chan struct{}),
	}, publish: true, ready: make(chan struct{})}
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer close(b.release)
	done := make(chan error, 1)
	go func() {
		_, _, _, err := h.coldStart(ctx, b.app.ID, "", "", 2, api.PlanScale)
		done <- err
	}()
	select {
	case <-b.ready:
	case <-time.After(time.Second):
		t.Fatal("primary was not published")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled request returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled request remained blocked")
	}
}
