package gateway

import (
	"context"
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

// A new routable target appears only when the blocked admission completes.
// Pick deliberately exposes that generation change, independent of the
// production round-robin cursor's starting position.
type burstSelectionBackend struct {
	*blockingBurstBackend
	ready      atomic.Bool
	selections atomic.Int32
}

func (b *burstSelectionBackend) AdmitBurst(ctx context.Context, appID, scope, trigger string, max, count int) (int, error) {
	n, err := b.blockingBurstBackend.AdmitBurst(ctx, appID, scope, trigger, max, count)
	if err == nil {
		b.ready.Store(true)
	}
	return n, err
}
func (b *burstSelectionBackend) Pick(string) PickResult {
	b.selections.Add(1)
	id := "original"
	if b.ready.Load() {
		id = "newly-ready"
	}
	return PickResult{OK: true, Target: Target{NodeID: "node-1", InstanceID: id}}
}

type warmBurstSelectionBackend struct{ *burstSelectionBackend }

func (b *warmBurstSelectionBackend) PickWarm(string) PickResult {
	return PickResult{OK: true, Target: Target{NodeID: "node-1", InstanceID: "original"}}
}

func TestHandlerSelectsTargetAfterBurstAdmission(t *testing.T) {
	for _, warm := range []bool{false, true} {
		name := "post-wake selection"
		if warm {
			name = "invalidate warm selection"
		}
		t.Run(name, func(t *testing.T) {
			b := &burstSelectionBackend{blockingBurstBackend: &blockingBurstBackend{
				burstTestBackend: &burstTestBackend{fakeBackend: &fakeBackend{app: App{ID: "app-1", Plan: api.PlanScale}, host: "app.example.com"}, admitted: make(chan int, 1)},
				started:          make(chan struct{}), release: make(chan struct{}),
			}}
			b.AddTarget(Target{NodeID: "node-1", InstanceID: "original"})
			var backend Backend = b
			if warm {
				backend = &warmBurstSelectionBackend{b}
			}
			h := NewHandlerWith(backend, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
			h.burstPressure.state(b.app.ID).inflight.Store(80)
			defer h.burstPressure.state(b.app.ID).inflight.Store(0)
			chosen := make(chan string, 1)
			h.WithForwarding(func(target Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					chosen <- target.InstanceID
					w.WriteHeader(http.StatusOK)
				})
			})
			var once sync.Once
			release := func() { once.Do(func() { close(b.release) }) }
			t.Cleanup(release)
			finished := make(chan int, 1)
			go func() {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://app.example.com/", nil))
				finished <- rec.Code
			}()
			select {
			case <-b.started:
			case <-time.After(3 * time.Second):
				t.Fatal("burst admission did not start")
			}
			select {
			case <-chosen:
				t.Fatal("forwarded before capacity became ready")
			default:
			}
			release()
			select {
			case code := <-finished:
				if code != http.StatusOK {
					t.Fatalf("status %d", code)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("request did not finish")
			}
			if got := <-chosen; got != "newly-ready" {
				t.Fatalf("forwarded to %q selected before admission; want newly-ready", got)
			}
			if got := b.selections.Load(); got != 1 {
				t.Fatalf("post-capacity selections = %d, want 1", got)
			}
		})
	}
}
