package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/backlog"
	"github.com/onebox-faas/faas/pkg/trace"
	"github.com/prometheus/client_golang/prometheus"
)

// spec: §4.1 (gateway hot-path overhead per proxied request)
//
// BenchmarkHandlerForwardStream measures gatewayd-internal's per-request
// work on the path production traffic takes: the trace middleware, the
// awaiting-response cap, TrustedTrafficIngress and the Handler, forwarding
// over the vmmd ForwardHTTPStream RPC (an in-process stub, so no socket
// cost hides the handler's own work). The request carries the headers a
// request has after Cloudflare and gatewayd-public.
//
// BenchmarkHandlerHotPath forwards through httputil.ReverseProxy over
// loopback TCP, which production never does; its profile is mostly
// syscalls. Compare a change here against the production profile
// (perf record -e instructions:u on gatewayd-internal) before trusting it.
func BenchmarkHandlerForwardStream(b *testing.B) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cli := &stubVmmdClient{
		resp: &vmmdpb.ForwardHTTPResponseInit{Status: 200, Headers: []*vmmdpb.Header{
			{Name: "Content-Type", Value: "text/plain; charset=utf-8"},
			{Name: "Date", Value: "Sun, 22 Nov 2026 17:38:00 GMT"},
		}},
		body: []byte("hello from app\n"),
	}
	backend := &fakeBackend{
		app:      App{ID: "dc001401-baa6-4ae8-8817-8c45906a47a3", Plan: api.PlanScale},
		host:     "loadtest-hello.gregale.dev",
		upstream: "node-uuid-1",
		running:  true,
	}
	h := NewHandlerWith(backend, NewMetrics(), log)
	// Keep the per-app limiter on the path (production runs it on every
	// request) but let its clock advance 1 ms per read, so the bucket refills
	// and a fast loop never trips the plan's burst.
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	h.WithLimiter(NewLimiterWithClock(func() time.Time { return time.Unix(0, clock.Add(int64(time.Millisecond))) }))
	h.WithForwarding(ForwardingReverseProxy(&stubLookup{cli: cli}, log))
	h.WithStreamingEnabled(true)
	h.WithRetryEnabled(true)
	h.WithRouteMetricsEnabled(true)
	var awaitingMax atomic.Int64
	awaitingMax.Store(4096)
	srv := trace.HTTPHandler("gatewayd-internal", backlog.New(TrustedTrafficIngress(h), &awaitingMax, prometheus.NewRegistry()))

	newReq := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "http://loadtest-hello.gregale.dev/", nil)
		r.Header.Set("User-Agent", "k6/1.4.2 (https://k6.io/)")
		r.Header.Set("Accept-Encoding", "gzip, br")
		r.Header.Set("Cf-Connecting-Ip", "194.27.48.68")
		r.Header.Set("Cf-Ipcountry", "TR")
		r.Header.Set("Cf-Ray", "8e1f2a3b4c5d6e7f-IST")
		r.Header.Set("Cf-Visitor", `{"scheme":"https"}`)
		r.Header.Set("Cdn-Loop", "cloudflare; loops=1")
		r.Header.Set("X-Forwarded-For", "194.27.48.68")
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-Host", "loadtest-hello.gregale.dev")
		r.Header.Set(trafficStartedHeader, strconv.FormatInt(time.Now().UnixNano(), 10))
		r.RemoteAddr = "127.0.0.1:41234"
		return r
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, newReq())
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
		cli.mu.Lock()
		cli.calls = cli.calls[:0]
		cli.mu.Unlock()
	}
}
