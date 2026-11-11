// Package backlog bounds the app requests a gatewayd-internal process holds
// before their response head is written.
//
// Every request the gateway accepts holds memory while it waits for a wake,
// a concurrency slot or the guest's first byte. Nothing bounded how many it
// held at once, so on a node saturated on CPU the backlog grew until the
// kernel killed the process at its MemoryMax and the node's entry went down
// with it (2026-11-11 load test: 1,400 req/s at one node). Cap answers 503
// gateway_overloaded at once past api.GatewayAwaitingResponseMax, which
// costs almost nothing, so an overloaded node sheds load and keeps serving.
//
// A request stops counting when its response head is written, a 101 for an
// upgrade included, or when its handler returns. Long-lived responses such
// as WebSockets, server-sent events and streamed bodies therefore hold no
// slot once they have started.
//
// The limit is read on every request, so the gateway_awaiting_response_max
// runtime-config key raises or lowers it without a restart.
package backlog

import (
	"bufio"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Cap is an http.Handler that admits at most *max requests awaiting their
// response head into next.
type Cap struct {
	next    http.Handler
	max     *atomic.Int64
	waiting atomic.Int64
	refused prometheus.Counter
	rand    func() float64
}

// New wraps next. max is read on every request; nil holds
// api.GatewayAwaitingResponseMax. reg, when not nil, receives the backlog's
// gauges and the refusal counter.
func New(next http.Handler, max *atomic.Int64, reg prometheus.Registerer) *Cap {
	if max == nil {
		max = new(atomic.Int64)
		max.Store(api.GatewayAwaitingResponseMax)
	}
	c := &Cap{
		next: next,
		max:  max,
		refused: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "gateway_awaiting_response_refused_total",
			Help: "App requests answered 503 gateway_overloaded because this gatewayd-internal already held GatewayAwaitingResponseMax requests waiting for their response head.",
		}),
		rand: rand.Float64,
	}
	if reg != nil {
		reg.MustRegister(c.refused, prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "gateway_requests_awaiting_response",
			Help: "App requests this gatewayd-internal holds that have not had their response head written: waiting for a wake, a concurrency slot or the guest's first byte.",
		}, func() float64 { return float64(c.waiting.Load()) }), prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "gateway_awaiting_response_max",
			Help: "Most app requests this gatewayd-internal holds awaiting their response head before it answers 503 gateway_overloaded (runtime-config gateway_awaiting_response_max, default GatewayAwaitingResponseMax).",
		}, func() float64 { return float64(c.max.Load()) }))
	}
	return c
}

// Waiting reports how many requests are awaiting their response head.
func (c *Cap) Waiting() int64 { return c.waiting.Load() }

func (c *Cap) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if c.waiting.Add(1) > c.max.Load() {
		c.waiting.Add(-1)
		c.refused.Inc()
		c.writeOverloaded(w)
		return
	}
	hw := &headWriter{ResponseWriter: w, cap: c}
	defer hw.release()
	c.next.ServeHTTP(hw, r)
}

func (c *Cap) writeOverloaded(w http.ResponseWriter) {
	span := api.GatewayOverloadedRetryAfterMax - api.GatewayOverloadedRetryAfterMin + 1
	retry := api.GatewayOverloadedRetryAfterMin + min(int(c.rand()*float64(span)), span-1)
	w.Header().Set("Retry-After", strconv.Itoa(retry))
	api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeGatewayOverloaded,
		"Gateway overloaded", "this node is holding as many requests as it can; retry after the Retry-After delay").
		WithDocs(wire.DocsBaseURL+"/errors"))
}

// headWriter gives its request's slot back when the response head is
// written. Handlers may write from a goroutine other than the one that
// returns, so the release is a compare-and-swap.
type headWriter struct {
	http.ResponseWriter
	cap      *Cap
	released atomic.Bool
}

func (w *headWriter) release() {
	if w.released.CompareAndSwap(false, true) {
		w.cap.waiting.Add(-1)
	}
}

// WriteHeader releases on a final status or a 101; other informational
// heads (100 Continue, 103 Early Hints) leave the request waiting.
func (w *headWriter) WriteHeader(code int) {
	if code >= http.StatusOK || code == http.StatusSwitchingProtocols {
		w.release()
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *headWriter) Write(p []byte) (int, error) {
	w.release()
	return w.ResponseWriter.Write(p)
}

// Unwrap preserves connection controls such as deadlines and full duplex.
func (w *headWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// FlushError flushes the head, which writes it.
func (w *headWriter) FlushError() error {
	w.release()
	return http.NewResponseController(w.ResponseWriter).Flush()
}

// Flush supports handlers that assert http.Flusher directly.
func (w *headWriter) Flush() { _ = w.FlushError() }

// Hijack supports protocol upgrades through handlers that assert
// http.Hijacker directly. A hijacked connection is no longer awaiting a head.
func (w *headWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.release()
	return http.NewResponseController(w.ResponseWriter).Hijack()
}
