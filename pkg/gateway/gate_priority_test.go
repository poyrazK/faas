package gateway

// adr: 20261018181846

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// pathClassifier classifies wake waiters by path. ready false mimics an app
// whose priorities are not cached yet: a non-blocking call cannot classify.
type pathClassifier struct {
	mu      sync.Mutex
	classes map[string]routePriority
	ready   bool
	peeks   int
}

func (c *pathClassifier) classifyWake(_ context.Context, _, _ string, reqs []routePriorityRequest, wait bool) ([]routePriority, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !wait {
		c.peeks++
		if !c.ready {
			return nil, false
		}
	}
	out := make([]routePriority, len(reqs))
	for i, req := range reqs {
		class, ok := c.classes[req.path]
		if !ok {
			class = routePriorityNormal
		}
		out[i] = class
	}
	return out, true
}

type wakeResult struct {
	path string
	err  error
	at   time.Time
}

// wakeHarness runs requests through one app's wake; the wake finishes when
// finish is called, with err.
type wakeHarness struct {
	t       *testing.T
	gate    *WakeGate
	policy  WakeAdmissionPolicy
	done    chan struct{}
	err     error
	results chan wakeResult
}

func newWakeHarness(t *testing.T, classifier wakeClassifier, maxWaiters int) *wakeHarness {
	g := NewWakeGate(512, 30*time.Second)
	g.classifier = classifier
	return &wakeHarness{t: t, gate: g, policy: WakeAdmissionPolicy{MaxWaiters: maxWaiters, MaxWait: 10 * time.Second},
		done: make(chan struct{}), results: make(chan wakeResult, 16)}
}

// wait sends one request for path and blocks until the gate holds want waiters.
func (h *wakeHarness) wait(path string, want int) {
	h.t.Helper()
	go func() {
		ctx := withRoutePriorityRequest(context.Background(), httptest.NewRequest("GET", path, nil))
		err := h.gate.WaitWithPolicy(ctx, "app", "acct", h.policy, func() bool { return true },
			func(context.Context) error { <-h.done; return h.err }, nil, nil)
		h.results <- wakeResult{path: path, err: err, at: time.Now()}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for h.gate.InflightWaiters("app") != want {
		if time.Now().After(deadline) {
			h.t.Fatalf("waiters = %d, want %d after %s", h.gate.InflightWaiters("app"), want, path)
		}
		time.Sleep(time.Millisecond)
	}
}

func (h *wakeHarness) finish(err error) {
	h.err = err
	close(h.done)
}

func (h *wakeHarness) next() wakeResult {
	h.t.Helper()
	select {
	case r := <-h.results:
		return r
	case <-time.After(5 * time.Second):
		h.t.Fatal("no request returned")
		return wakeResult{}
	}
}

func TestWakeGateFullQueueAdmitsHigherClass(t *testing.T) {
	h := newWakeHarness(t, &pathClassifier{ready: true, classes: map[string]routePriority{
		"/checkout": routePriorityCritical, "/export": routePriorityBulk, "/export/a": routePriorityBulk, "/export/b": routePriorityBulk,
	}}, 3)
	h.wait("/export", 1) // the leader
	h.wait("/export/a", 2)
	h.wait("/export/b", 3)
	// The queue is full: checkout takes the newest bulk waiter's place.
	h.wait("/checkout", 3)
	var full *WakeQueueFullError
	if r := h.next(); r.path != "/export/b" || !errors.As(r.err, &full) || full.Limit != 3 {
		t.Fatalf("displaced = %+v", r)
	}
	h.wait("/home", 3) // normal outranks the remaining bulk follower
	if r := h.next(); r.path != "/export/a" || !errors.As(r.err, &full) {
		t.Fatalf("displaced = %+v", r)
	}
	// Only the leader is bulk now, and the leader never moves.
	ctx := withRoutePriorityRequest(context.Background(), httptest.NewRequest("GET", "/about", nil))
	err := h.gate.WaitWithPolicy(ctx, "app", "acct", h.policy, func() bool { return true }, func(context.Context) error { return nil }, nil, nil)
	if !errors.As(err, &full) || full.Depth != 3 {
		t.Fatalf("a normal request with no lower class to displace: %v", err)
	}
	h.finish(nil)
	got := map[string]error{}
	for range 3 {
		r := h.next()
		got[r.path] = r.err
	}
	for _, path := range []string{"/export", "/checkout", "/home"} {
		if err, ok := got[path]; !ok || err != nil {
			t.Fatalf("%s: %v (results %v)", path, err, got)
		}
	}
	if n := h.gate.InflightWaiters("app"); n != 0 {
		t.Fatalf("waiters left = %d", n)
	}
}

func TestWakeGateReleasesCriticalWaitersFirst(t *testing.T) {
	h := newWakeHarness(t, &pathClassifier{ready: true, classes: map[string]routePriority{
		"/checkout": routePriorityCritical, "/export": routePriorityBulk,
	}}, 8)
	h.gate.releaseStagger = 300 * time.Millisecond
	h.wait("/export", 1)
	h.wait("/home", 2)
	h.wait("/checkout", 3)
	start := time.Now()
	h.finish(nil)
	first, second, third := h.next(), h.next(), h.next()
	if first.path != "/checkout" || second.path != "/home" || third.path != "/export" {
		t.Fatalf("release order = %s, %s, %s", first.path, second.path, third.path)
	}
	if first.err != nil || second.err != nil || third.err != nil {
		t.Fatalf("errors = %v %v %v", first.err, second.err, third.err)
	}
	if first.at.Sub(start) > 200*time.Millisecond || third.at.Sub(start) < 600*time.Millisecond {
		t.Fatalf("critical after %s, bulk after %s", first.at.Sub(start), third.at.Sub(start))
	}
}

func TestWakeGateReleasesTogetherWithoutPriorities(t *testing.T) {
	classifier := &pathClassifier{classes: map[string]routePriority{"/checkout": routePriorityCritical, "/export": routePriorityBulk}}
	h := newWakeHarness(t, classifier, 8)
	h.gate.releaseStagger = time.Second
	h.wait("/export", 1)
	h.wait("/checkout", 2)
	start := time.Now()
	h.finish(nil)
	for range 2 {
		if r := h.next(); r.err != nil || r.at.Sub(start) > 500*time.Millisecond {
			t.Fatalf("%+v after %s: uncached priorities must not delay anyone", r, r.at.Sub(start))
		}
	}
	classifier.mu.Lock()
	defer classifier.mu.Unlock()
	if classifier.peeks < 2 {
		t.Fatalf("the second waiter should start loading priorities and the release should try them: %d peeks", classifier.peeks)
	}
}

func TestWakeGateFailedWakeReleasesEveryoneAtOnce(t *testing.T) {
	h := newWakeHarness(t, &pathClassifier{ready: true, classes: map[string]routePriority{"/checkout": routePriorityCritical, "/export": routePriorityBulk}}, 8)
	h.gate.releaseStagger = time.Second
	h.wait("/export", 1)
	h.wait("/checkout", 2)
	boom := errors.New("restore failed")
	start := time.Now()
	h.finish(boom)
	for range 2 {
		if r := h.next(); !errors.Is(r.err, boom) || r.at.Sub(start) > 500*time.Millisecond {
			t.Fatalf("%+v after %s", r, r.at.Sub(start))
		}
	}
}

func TestHandlerClassifyWake(t *testing.T) {
	h := NewHandlerWith(&fakeBackend{}, NewMetrics(), nil)
	var mu sync.Mutex
	loads := 0
	h.SetRoutePrioritySource(func(context.Context, string, string) ([]api.RoutePriorityRule, error) {
		mu.Lock()
		defer mu.Unlock()
		loads++
		return []api.RoutePriorityRule{{Method: "POST", Path: "/checkout", Class: api.RoutePriorityCritical}}, nil
	})
	checkout := httptest.NewRequest("POST", "/checkout", nil)
	bot := httptest.NewRequest("GET", "/blog", nil)
	bot.Header.Set("User-Agent", "Googlebot/2.1")
	reqs := []routePriorityRequest{
		{method: "POST", path: "/checkout", trigger: ClassifyWakeTrigger(checkout), known: true},
		{method: "GET", path: "/blog", trigger: ClassifyWakeTrigger(bot), known: true},
		{method: "POST", path: "/checkout"}, // not from a request: normal
	}
	if _, ok := h.classifyWake(context.Background(), "app", "acct", reqs, false); ok {
		t.Fatal("uncached priorities must not be read on the release path")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := h.routePriorities.cached("app"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the prefetch never loaded the priorities")
		}
		time.Sleep(time.Millisecond)
	}
	classes, ok := h.classifyWake(context.Background(), "app", "acct", reqs, false)
	if !ok || len(classes) != 3 || classes[0] != routePriorityCritical || classes[1] != routePriorityBulk || classes[2] != routePriorityNormal {
		t.Fatalf("classes = %v %v", classes, ok)
	}
	if classes, ok := h.classifyWake(context.Background(), "other", "acct", reqs[:1], true); !ok || classes[0] != routePriorityCritical {
		t.Fatalf("a full queue may read the priorities: %v %v", classes, ok)
	}
	mu.Lock()
	defer mu.Unlock()
	if loads != 2 {
		t.Fatalf("loads = %d, want one per app", loads)
	}
	if h.gate.classifier == nil {
		t.Fatal("the handler orders its own wake queue")
	}
}
