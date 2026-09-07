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

func TestHandlerForwardsToReadyTargetDuringBurstAdmission(t *testing.T) {
	for _, warm := range []bool{false, true} {
		name := "post-wake selection"
		if warm {
			name = "warm selection"
		}
		t.Run(name, func(t *testing.T) {
			b := &burstSelectionBackend{blockingBurstBackend: &blockingBurstBackend{
				burstTestBackend: &burstTestBackend{fakeBackend: &fakeBackend{app: App{ID: "app-1", Type: AppTypeApp, Plan: api.PlanScale, AutoscaleTargetRPS: 1}, host: "app.example.com"}, admitted: make(chan int, 1)},
				started:          make(chan struct{}), release: make(chan struct{}),
			}}
			b.AddTarget(Target{NodeID: "node-1", InstanceID: "original"})
			var backend Backend = b
			if warm {
				backend = &warmBurstSelectionBackend{b}
			}
			h := NewHandlerWith(backend, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
			state := h.burstPressure.state(b.app.ID)
			state.inflight.Store(80)
			state.recordArrival(time.Now())
			defer state.inflight.Store(0)
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
			case code := <-finished:
				if code != http.StatusOK {
					t.Fatalf("status %d", code)
				}
			case <-time.After(time.Second):
				t.Fatal("request waited for background capacity")
			}
			if got := <-chosen; got != "original" {
				t.Fatalf("forwarded to %q, want existing ready target", got)
			}
			wantSelections := int32(1)
			if warm {
				wantSelections = 0
			}
			if got := b.selections.Load(); got != wantSelections {
				t.Fatalf("fallback selections = %d, want %d", got, wantSelections)
			}
			release()
			select {
			case <-b.admitted:
			case <-time.After(time.Second):
				t.Fatal("background admission did not finish")
			}
		})
	}
}
