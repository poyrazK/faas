package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestNewHandler_NodeFetchAbortSignalCancelsPersistentGuestGET covers the
// bodyless GET path used by Node's built-in Fetch client. The bridge receives
// a persistent-stream request, forwards it to a guest handler that waits for
// two seconds, and must carry AbortSignal.timeout(150) through to that guest.
// Arm the timeout signals after all guest requests are active so cold Node
// startup or connection setup cannot consume the cancellation test's budget.
// A concurrent fast request verifies that cancelling those streams leaves the
// shared per-port transport usable.
func TestNewHandler_NodeFetchAbortSignalCancelsPersistentGuestGET(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required to exercise the WHATWG Fetch client")
	}

	const slowRequests = 8
	const maxGuestAbortLatency = 750 * time.Millisecond
	started := 0
	aborted := 0
	completed := 0
	slowStarted := make(chan struct{}, slowRequests)
	slowAborted := make(chan time.Time, slowRequests)
	slowCompleted := make(chan struct{}, slowRequests)
	allSlowStarted := make(chan struct{})
	startSlowWork := make(chan struct{})
	var activeSlow atomic.Int32
	var startWorkOnce sync.Once
	var slowWorkStarted time.Time
	guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fast" {
			_, _ = fmt.Fprint(w, "fast-ok")
			return
		}
		slowStarted <- struct{}{}
		if activeSlow.Add(1) == slowRequests {
			close(allSlowStarted)
		}
		select {
		case <-startSlowWork:
		case <-r.Context().Done():
			slowAborted <- time.Now()
			return
		}
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			slowAborted <- time.Now()
		case <-timer.C:
			slowCompleted <- struct{}{}
			_, _ = fmt.Fprint(w, "slow-completed")
		}
	}))
	t.Cleanup(guest.Close)

	guestIP, guestPortText, err := net.SplitHostPort(guest.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split guest address: %v", err)
	}
	guestPort, err := strconv.ParseUint(guestPortText, 10, 16)
	if err != nil {
		t.Fatalf("parse guest port: %v", err)
	}
	pool := newGuestTransportPool(guestIP)
	t.Cleanup(pool.closeIdleConnections)
	inner := newHandlerWithPool(guestIP, uint16(guestPort), time.Now().Add(10*time.Second), pool)
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			select {
			case <-allSlowStarted:
				startWorkOnce.Do(func() {
					slowWorkStarted = time.Now()
					close(startSlowWork)
				})
				_, _ = fmt.Fprint(w, "ready")
			case <-r.Context().Done():
			}
			return
		}
		// ForwardHTTPStream supplies these private wire headers to the
		// persistent bridge. Set them here to exercise the same dispatch and
		// pooled transport without needing a VM or gRPC server in this test.
		r.Header.Set(bridgeRequestMarkerHeader, "1")
		r.Header.Set(bridgeRequestProtocolHeader, "h1")
		r.Header.Set(bridgeRequestPortHeader, guestPortText)
		r.Header.Set(bridgeRequestHostHeader, "worker.svc.gregale")
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(bridge.Close)

	script := fmt.Sprintf(`
const base = process.argv[1];
const controllers = Array.from({length: %d}, () => new AbortController());
const slow = controllers.map(async controller => {
  try {
    await fetch(base + '/slow', {signal: controller.signal});
    throw new Error('slow fetch unexpectedly completed');
  } catch (err) {
    if (err.name !== 'TimeoutError') throw err;
  }
});
const armTimeouts = (async () => {
  const response = await fetch(base + '/ready');
  if (!response.ok || await response.text() !== 'ready') throw new Error('guest readiness barrier failed');
  for (const controller of controllers) {
    const timeout = AbortSignal.timeout(150);
    timeout.addEventListener('abort', () => controller.abort(timeout.reason), {once: true});
  }
})();
Promise.all([armTimeouts, Promise.all(slow)]).then(async () => {
  const response = await fetch(base + '/fast');
  const body = await response.text();
  if (!response.ok || body !== 'fast-ok') throw new Error('fast sibling failed: ' + response.status + ' ' + body);
  process.stdout.write('slow fetches timed out; fast sibling completed');
}).catch(err => {
  process.stderr.write(err.stack || String(err));
  process.exitCode = 1;
});
`, slowRequests)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, node, "-e", script, bridge.URL).CombinedOutput()
	if err != nil {
		t.Fatalf("Node Fetch script failed: %v; output=%s", err, output)
	}
	if got, want := string(output), "slow fetches timed out; fast sibling completed"; got != want {
		t.Fatalf("Node Fetch output = %q, want %q", got, want)
	}

	abortDeadline := time.NewTimer(time.Second)
	defer abortDeadline.Stop()
	<-startSlowWork // Synchronize the timestamp published by the ready handler.
	for started < slowRequests || aborted < slowRequests {
		select {
		case <-slowStarted:
			started++
		case at := <-slowAborted:
			aborted++
			latency := at.Sub(slowWorkStarted)
			if latency < 0 || latency > maxGuestAbortLatency {
				t.Fatalf("guest cancellation latency = %s, want <= %s", latency, maxGuestAbortLatency)
			}
		case <-slowCompleted:
			completed++
			t.Fatalf("%d slow guest request(s) completed instead of observing cancellation", completed)
		case <-abortDeadline.C:
			t.Fatalf("guest cancellation did not propagate: started=%d aborted=%d completed=%d", started, aborted, completed)
		}
	}

	// Keep the guest alive through the original two-second work window. If
	// the signal was dropped at any layer, the guest would complete during
	// this period even though Node had already rejected the fetch promises.
	remaining := time.Until(slowWorkStarted.Add(2300 * time.Millisecond))
	if remaining > 0 {
		select {
		case <-slowCompleted:
			completed++
			t.Fatalf("%d slow guest request(s) completed after Fetch timed out", completed)
		case <-time.After(remaining):
		}
	}
}
