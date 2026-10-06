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
// Arm the timeout after all requests reach the guest so client startup and
// runner scheduling cannot expire a signal before there is a request to cancel.
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
	slowStarted := make(chan time.Time, slowRequests)
	slowAborted := make(chan time.Duration, slowRequests)
	slowCompleted := make(chan struct{}, slowRequests)
	var arrived atomic.Int32
	var startWork sync.Once
	workReady := make(chan struct{})
	guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fast" {
			_, _ = fmt.Fprint(w, "fast-ok")
			return
		}
		if r.URL.Path == "/ready" {
			if arrived.Load() == slowRequests {
				startWork.Do(func() { close(workReady) })
				_, _ = fmt.Fprint(w, "ready")
			} else {
				_, _ = fmt.Fprint(w, "waiting")
			}
			return
		}
		arrived.Add(1)
		select {
		case <-workReady:
		case <-r.Context().Done():
			return
		}
		requestStarted := time.Now()
		slowStarted <- requestStarted
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			slowAborted <- time.Since(requestStarted)
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
(async () => {
  const allSlow = Promise.all(slow);
  while ((await (await fetch(base + '/ready')).text()) !== 'ready') {
    await new Promise(resolve => setTimeout(resolve, 5));
  }
  for (const controller of controllers) {
    const timeout = AbortSignal.timeout(150);
    timeout.addEventListener('abort', () => controller.abort(timeout.reason), {once: true});
  }
  await allSlow;
  const response = await fetch(base + '/fast');
  const body = await response.text();
  if (!response.ok || body !== 'fast-ok') throw new Error('fast sibling failed: ' + response.status + ' ' + body);
  process.stdout.write('slow fetches timed out; fast sibling completed');
})().catch(err => {
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

	// On a loaded runner a 150 ms signal can fire before the bridge has
	// forwarded every slow request; such a request never reaches the guest,
	// which is cancellation propagating even earlier. Every request the guest
	// did see must observe cancellation. Keep watching through the guest's
	// two-second work window: a dropped signal would let a started request
	// complete there even though Node had already rejected its promise.
	watch := time.NewTimer(2300 * time.Millisecond)
	defer watch.Stop()
	for started < slowRequests || aborted < slowRequests {
		select {
		case <-slowStarted:
			started++
		case latency := <-slowAborted:
			aborted++
			if latency > maxGuestAbortLatency {
				t.Fatalf("guest cancellation latency = %s, want <= %s", latency, maxGuestAbortLatency)
			}
		case <-slowCompleted:
			completed++
			t.Fatalf("%d slow guest request(s) completed instead of observing cancellation", completed)
		case <-watch.C:
			if started == 0 || aborted != started {
				t.Fatalf("guest cancellation did not propagate: started=%d aborted=%d completed=%d", started, aborted, completed)
			}
			t.Logf("%d of %d slow request(s) were cancelled before reaching the guest", slowRequests-started, slowRequests)
			return
		}
	}
}
