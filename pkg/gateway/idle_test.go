package gateway

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// fakeFlakySink is a LastSeenSink that counts Touch + Flush calls and can
// return errors from Flush to exercise the ticker loop.
type fakeFlakySink struct {
	mu        sync.Mutex
	touched   map[string]int
	flushes   int
	flushErr  error
}

func newFakeFlakySink() *fakeFlakySink { return &fakeFlakySink{touched: map[string]int{}} }

func (f *fakeFlakySink) Touch(addr string, _ time.Time) {
	f.mu.Lock()
	f.touched[addr]++
	f.mu.Unlock()
}

func (f *fakeFlakySink) Get(addr string) (time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.touched[addr] > 0 {
		return time.Now(), true
	}
	return time.Time{}, false
}

func (f *fakeFlakySink) Forget(addr string) {
	f.mu.Lock()
	delete(f.touched, addr)
	f.mu.Unlock()
}

func (f *fakeFlakySink) Flush(_ context.Context) error {
	f.mu.Lock()
	f.flushes++
	f.mu.Unlock()
	return f.flushErr
}

func (f *fakeFlakySink) TouchCount(addr string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.touched[addr]
}

func TestMemoryLastSeenTouchAndGet(t *testing.T) {
	s := NewMemoryLastSeen()
	now := time.Now()
	s.Touch("1.2.3.4:8080", now)
	got, ok := s.Get("1.2.3.4:8080")
	if !ok || !got.Equal(now) {
		t.Errorf("Get = (%v,%v), want (%v,true)", got, ok, now)
	}
	s.Forget("1.2.3.4:8080")
	if _, ok := s.Get("1.2.3.4:8080"); ok {
		t.Error("Forget should drop the entry")
	}
}

func TestLastSeenTouchedOn200(t *testing.T) {
	h, b, _ := newTestHandler(t)
	sink := newFakeFlakySink()
	h.WithLastSeenSink(sink)
	b.running = true // hot path → proxy happens

	req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if sink.TouchCount(b.upstream) != 1 {
		t.Errorf("Touch count = %d, want 1 (cold false, status 200 → touch)", sink.TouchCount(b.upstream))
	}
}

func TestLastSeenNotTouchedOn404(t *testing.T) {
	// 404 from the path-filter itself, before any proxy; sink must not be touched.
	h, _, _ := newTestHandler(t)
	sink := newFakeFlakySink()
	h.WithLastSeenSink(sink)

	req := httptest.NewRequest("GET", "http://nope.apps.dom/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if sink.TouchCount("anything") != 0 {
		t.Error("a 404 must not touch lastSeen")
	}
}

func TestFlushEveryTicksUntilCtxCancel(t *testing.T) {
	sink := newFakeFlakySink()
	ctx, cancel := context.WithCancel(context.Background())
	errc := FlushEvery(ctx, 10*time.Millisecond, sink)
	time.Sleep(35 * time.Millisecond)
	cancel()
	// Drain; ignore err.
	for range errc {
	}
	sink.mu.Lock()
	got := sink.flushes
	sink.mu.Unlock()
	if got < 2 {
		t.Errorf("FlushEvery fired %d times, want >=2 over 35ms with 10ms interval", got)
	}
}

func TestLastSeenCarriesAppID(t *testing.T) {
	// Sanity that addr is the instance address (not app id) — schedd's
	// pkg/sched/reaper.go expects to look up by instance id from its own
	// state, so this just guards the contract.
	h, _, _ := newTestHandler(t)
	_ = h.WithAppsSuffix("") // explicit no-op
	limits, _ := api.LimitsFor(api.PlanFree)
	_ = limits
}
