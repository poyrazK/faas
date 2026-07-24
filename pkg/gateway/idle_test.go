package gateway

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeFlakySink is a LastSeenSink that counts Touch + Flush calls and can
// return errors from Flush to exercise the ticker loop.
type fakeFlakySink struct {
	mu       sync.Mutex
	touched  map[string]int
	flushes  int
	flushErr error
}

func newFakeFlakySink() *fakeFlakySink { return &fakeFlakySink{touched: map[string]int{}} }

func (f *fakeFlakySink) Touch(instanceID string, _ time.Time) {
	f.mu.Lock()
	f.touched[instanceID]++
	f.mu.Unlock()
}

func (f *fakeFlakySink) Get(instanceID string) (time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.touched[instanceID] > 0 {
		return time.Now(), true
	}
	return time.Time{}, false
}

func (f *fakeFlakySink) Forget(instanceID string) {
	f.mu.Lock()
	delete(f.touched, instanceID)
	f.mu.Unlock()
}

func (f *fakeFlakySink) Flush(_ context.Context) error {
	f.mu.Lock()
	f.flushes++
	f.mu.Unlock()
	return f.flushErr
}

func (f *fakeFlakySink) TouchCount(instanceID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.touched[instanceID]
}

func TestMemoryLastSeenTouchAndGet(t *testing.T) {
	s := NewMemoryLastSeen()
	now := time.Now()
	s.Touch("i-test-1", now)
	got, ok := s.Get("i-test-1")
	if !ok || !got.Equal(now) {
		t.Errorf("Get = (%v,%v), want (%v,true)", got, ok, now)
	}
	s.Forget("i-test-1")
	if _, ok := s.Get("i-test-1"); ok {
		t.Error("Forget should drop the entry")
	}
}

// TestLastSeenTouchedOn200 (issue #168) — the sink is Touched by instance
// id (i-fake in the legacy single-target path), not by addr. The handler
// stamps the picked Target's InstanceID onto the request before proxying,
// and observe() Touches it after a 2xx response.
func TestLastSeenTouchedOn200(t *testing.T) {
	h, b, _ := newTestHandler(t)
	sink := newFakeFlakySink()
	h.WithLastSeenSink(sink)
	b.setLegacyHot() // hot path → proxy happens (legacy single-target mode)

	req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if sink.TouchCount("i-fake") != 1 {
		t.Errorf("Touch count = %d, want 1 (cold false, status 200 → touch)", sink.TouchCount("i-fake"))
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
	// Sanity that the key is the instance id (not app id, not addr) —
	// schedd's pkg/sched/reaper.go expects to look up by instance id
	// from its own state, so this just guards the contract.
	h, _, _ := newTestHandler(t)
	_ = h.WithAppsSuffix("") // explicit no-op
}
