package gateway

// adr: 1155 pins slow-request reports that trigger automatic stack captures.

import (
	"context"
	"io"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSlowRequestWatcherReportsOncePerAppPerInterval(t *testing.T) {
	clock := time.Unix(1000, 0)
	w := NewSlowRequestWatcher(func(context.Context, SlowRequestReport) error { return nil }, nil)
	w.now = func() time.Time { return clock }
	endA1 := w.begin(SlowRequestReport{AppID: "a", InstanceID: "i-a", RequestID: "r1"})
	w.begin(SlowRequestReport{AppID: "a", InstanceID: "i-a", RequestID: "r2"})
	w.begin(SlowRequestReport{AppID: "b", InstanceID: "i-b", RequestID: "r3"})
	endFast := w.begin(SlowRequestReport{AppID: "c", InstanceID: "i-c", RequestID: "r4"})

	clock = clock.Add(5 * time.Second)
	endFast() // finished before the threshold: never reported
	if got := w.due(); len(got) != 0 {
		t.Fatalf("reported before the threshold: %+v", got)
	}
	clock = clock.Add(api.ProfileCaptureSlowRequestAfter)
	got := w.due()
	sort.Slice(got, func(i, j int) bool { return got[i].AppID < got[j].AppID })
	if len(got) != 2 || got[0].AppID != "a" || got[1].AppID != "b" || got[0].Inflight != 5*time.Second+api.ProfileCaptureSlowRequestAfter {
		t.Fatalf("due = %+v; want one report for app a and one for app b", got)
	}
	clock = clock.Add(time.Second)
	if again := w.due(); len(again) != 0 {
		t.Fatalf("requests reported twice: %+v", again)
	}
	endA1()
	clock = clock.Add(api.ProfileCaptureSlowRequestReportEvery)
	w.begin(SlowRequestReport{AppID: "a", InstanceID: "i-a", RequestID: "r5"})
	clock = clock.Add(api.ProfileCaptureSlowRequestAfter)
	if later := w.due(); len(later) != 1 || later[0].RequestID != "r5" {
		t.Fatalf("app a not reported again after the interval: %+v", later)
	}
}

func TestTrackSlowRequestGates(t *testing.T) {
	h := &Handler{}
	w := NewSlowRequestWatcher(func(context.Context, SlowRequestReport) error { return nil }, nil)
	paid := App{ID: "app", Plan: api.PlanPro}
	target := Target{InstanceID: "instance"}
	req := httptest.NewRequest("GET", "/orders/42", nil)
	for _, tc := range []struct {
		name      string
		watcher   *SlowRequestWatcher
		app       App
		target    Target
		streaming bool
		tracked   bool
	}{
		{"tracked", w, paid, target, false, true},
		{"no watcher", nil, paid, target, false, false},
		{"streaming", w, paid, target, true, false},
		{"no instance", w, paid, Target{}, false, false},
		{"plan without profiling", w, App{ID: "free", Plan: api.PlanFree}, target, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h.slowRequests = tc.watcher
			_, end := h.trackSlowRequest(req, tc.app, tc.target, tc.streaming)
			w.mu.Lock()
			n := len(w.inflight)
			var route string
			for _, r := range w.inflight {
				route = r.Route
			}
			w.mu.Unlock()
			if (n == 1) != tc.tracked {
				t.Fatalf("tracked = %d requests, want tracked=%v", n, tc.tracked)
			}
			if tc.tracked && route != "GET request" {
				t.Fatalf("route label = %q; the raw path must never be reported", route)
			}
			end()
			w.mu.Lock()
			defer w.mu.Unlock()
			if len(w.inflight) != 0 {
				t.Fatal("tracking did not end")
			}
		})
	}
}

// A request whose response is under way or whose body is still arriving is
// moving, not stuck in the app: it is reported only once it stops moving.
func TestSlowRequestWatcherSkipsMovingRequests(t *testing.T) {
	clock := time.Unix(1000, 0)
	w := NewSlowRequestWatcher(func(context.Context, SlowRequestReport) error { return nil }, nil)
	w.now = func() time.Time { return clock }
	var downloading, uploading atomic.Bool
	downloading.Store(true)
	uploading.Store(true)
	w.beginWithProgress(SlowRequestReport{AppID: "download", InstanceID: "i-d", RequestID: "d"}, requestProgress{responding: downloading.Load})
	w.beginWithProgress(SlowRequestReport{AppID: "upload", InstanceID: "i-u", RequestID: "u"}, requestProgress{uploading: uploading.Load})
	clock = clock.Add(api.ProfileCaptureSlowRequestAfter + time.Second)
	if got := w.due(); len(got) != 0 {
		t.Fatalf("moving requests reported as stuck: %+v", got)
	}
	uploading.Store(false) // the body arrived; the app has not answered
	if got := w.due(); len(got) != 1 || got[0].RequestID != "u" {
		t.Fatalf("due after the upload finished = %+v; want the upload", got)
	}
}

func TestTrackSlowRequestWatchesTheBodyAndFirstByte(t *testing.T) {
	w := NewSlowRequestWatcher(func(context.Context, SlowRequestReport) error { return nil }, nil)
	h := &Handler{slowRequests: w}
	rec := &firstByteRecorder{}
	req := httptest.NewRequest("POST", "/upload", strings.NewReader("payload"))
	req = req.WithContext(WithFirstByteRecorder(req.Context(), rec))
	tracked, end := h.trackSlowRequest(req, App{ID: "app", Plan: api.PlanPro}, Target{InstanceID: "i"}, false)
	defer end()
	progress := func() requestProgress {
		w.mu.Lock()
		defer w.mu.Unlock()
		for _, r := range w.inflight {
			return r.progress
		}
		return requestProgress{}
	}
	if !progress().moving() {
		t.Fatal("an unread body does not count as uploading")
	}
	if _, err := io.ReadAll(tracked.Body); err != nil || progress().moving() {
		t.Fatalf("a fully read body still counts as uploading: %v", err)
	}
	rec.record(time.Now())
	if !progress().moving() {
		t.Fatal("a started response does not count as responding")
	}
}
