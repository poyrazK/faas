// Control-plane listener for gatewayd-internal (spec §11, §12). The /healthz, /readyz,
// and /metrics endpoints MUST NOT be on the public listener — they leak
// operational data and CSRF-style probes have no business hitting them. This
// file owns a SECOND *http.Server on a private listener (default :9090) that
// serves only the control routes. The public listener stays single-purpose:
//
//	public   :80/:443   → Handler.ServeHTTP       (proxies customer apps)
//	private  :9090      → ControlMux              (health + metrics)
//
// The private listener is wired into the cmd/gatewayd-internal/main alongside the
// public server with its own graceful-shutdown context.
package gateway

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/drain"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ControlAddr is the bind address for the control-plane listener. Kept on
// the daemon registry so an operator can override via env without editing
// source. The /metrics endpoint is intentionally unauthenticated because it
// is reached by the local Prometheus scraper on a private interface only.
const ControlAddr = ":9090"

// ControlMux returns the *http.ServeMux with /healthz, /readyz, /metrics.
// /readyz is wired to a Ready func the daemon registers on construction
// (e.g. "true once routing cache is hydrated from Postgres").
//
// nil-ready behaviour is the SAFE default post-Tier-A7: a daemon that
// forgets to wire a probe is marked not-ready rather than silently ready,
// so the LB never routes traffic to a partial-boot instance. The pre-split
// always-200 default was a latent bug (cmd/gatewayd-internal/main.go:878 wired nil
// and /readyz was useless); ADR-070 closes that.
//
// tracker (issue #587 / PR-A) is the per-request drain WaitGroup
// the graceful-shutdown drain waits on. nil = drain disabled
// (unit tests + pre-PR-A behaviour). When non-nil, every control
// request (including /metrics scrapes during shutdown) is tracked
// so a hung scraper can't block the drain. The control endpoints
// are tiny but the same property holds: a curl during shutdown
// that hangs the connection open past srv.Shutdown's grace must
// not keep the daemon alive past TimeoutStopSec=30s.
func ControlMux(m *Metrics, ready ReadyFunc, tracker *drain.Tracker) *http.ServeMux {
	mux := http.NewServeMux()
	wrap := func(label string, h http.HandlerFunc) http.HandlerFunc {
		if tracker == nil {
			return h
		}
		return func(w http.ResponseWriter, r *http.Request) {
			defer tracker.Begin(label)()
			h(w, r)
		}
	}
	mux.HandleFunc("/healthz", wrap("control", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	mux.HandleFunc("/readyz", wrap("control", func(w http.ResponseWriter, _ *http.Request) {
		if ready == nil {
			// A nil probe is a wiring bug post-#568. Mark not-ready so
			// the LB drain kicks in; the operator sees /readyz=503 and
			// fixes the registration. Surface the reason in the body
			// for grep-friendliness.
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not-ready: no probe registered"))
			return
		}
		if ready() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ready"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not-ready"))
	}))
	if m != nil {
		mux.Handle("/metrics", wrap("control", m.Handler().ServeHTTP))
		mux.Handle("/metrics/gateway-requests", wrap("control", m.RequestCountHandler().ServeHTTP))
	}
	return mux
}

// ControlMuxWithExtra is the gatewayd-public / daemon-without-default-metrics
// variant: builds the same /healthz, /readyz, /metrics mux as ControlMux
// but plumbs an extra gatherer into the /metrics handler so callers can
// expose their own metric families without re-implementing the control
// listener. extra == nil mirrors ControlMux's behaviour (only m's
// registry is exposed, if any). The two gatherers are combined into a
// prometheus.Gatherers slice so a single scrape serves both — the
// Prometheus text exposition format doesn't support per-gatherer
// sections, but a single promhttp.HandlerFor over a Gatherers chain is
// the documented way to expose multiple registries side-by-side.
//
//	ADR-093: reqbudget registers its histogram + counter against a
//	fresh prometheus.NewRegistry() in cmd/gatewayd-public/main.go and
//	passes that registry here, so /metrics scrapes the budget
//	outcomes without the budget metrics having to live on a
//	pre-existing daemon-default registry.
func ControlMuxWithExtra(m *Metrics, extra prometheus.Gatherer, ready ReadyFunc, tracker *drain.Tracker) *http.ServeMux {
	mux := http.NewServeMux()
	wrap := func(label string, h http.HandlerFunc) http.HandlerFunc {
		if tracker == nil {
			return h
		}
		return func(w http.ResponseWriter, r *http.Request) {
			defer tracker.Begin(label)()
			h(w, r)
		}
	}
	mux.HandleFunc("/healthz", wrap("control", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	mux.HandleFunc("/readyz", wrap("control", func(w http.ResponseWriter, _ *http.Request) {
		if ready == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not-ready: no probe registered"))
			return
		}
		if ready() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ready"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not-ready"))
	}))
	switch {
	case m != nil && extra != nil:
		// Combine both gatherers; prometheus.Gatherers is itself a
		// Gatherer that fans out to its children. The returned
		// handler renders the union on a single scrape.
		mux.Handle("/metrics", wrap("control", promhttp.HandlerFor(
			prometheus.Gatherers{m.Registry(), extra},
			promhttp.HandlerOpts{Registry: prometheus.NewRegistry()},
		).ServeHTTP))
	case m != nil:
		mux.Handle("/metrics", wrap("control", m.Handler().ServeHTTP))
	case extra != nil:
		mux.Handle("/metrics", wrap("control", promhttp.HandlerFor(extra, promhttp.HandlerOpts{Registry: prometheus.NewRegistry()}).ServeHTTP))
	}
	if m != nil {
		mux.Handle("/metrics/gateway-requests", wrap("control", m.RequestCountHandler().ServeHTTP))
	}
	return mux
}

// ReadyFunc reports whether the daemon is ready to serve traffic. Used by
// /readyz. The pre-split contract ("returns true by default") was
// intentionally inverted by ADR-070 — every post-split daemon must wire a
// real probe (see pkg/gateway/readiness.go::ReadyzProbe.ReadyFunc).
type ReadyFunc func() bool

// RunControlServer starts the control-plane listener and blocks until ctx is
// cancelled, then performs a graceful shutdown bounded by 5 s. Errors other
// than http.ErrServerClosed are returned.
//
// Knob set (ADR-122 / post-merge audit, issue #995 closure): RHT=5s
// (tight, control-plane probe latency) + RT=10s + WT=10s + IT=60s +
// MHB=api.DefaultMaxHeaderBytes (1 MiB). The four timeout knobs
// pre-date this PR; MaxHeaderBytes is the new pin (was 0, relying on
// stdlib default). RHT stays at 5s rather than the 10s canonical
// // metrics variant because the control plane only serves /healthz,
// // /readyz, /metrics — no slow-client body, no slow headers.
func RunControlServer(ctx context.Context, addr string, mux *http.ServeMux) error {
	if addr == "" {
		addr = ControlAddr
	}
	srv := NewControlHTTPServer(addr, mux)
	errc := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		//nolint:contextcheck // shutdown ctx must outlive the cancelled caller ctx (net/http contract).
		return srv.Shutdown(sctx)
	}
}

// NewControlHTTPServer constructs the *http.Server carrying the
// canonical metrics variant for the control-plane listener. Mirrors
// pkg/githubd.NewWebhookHTTPServer — the helper is exported so a
// test can pin the knob set against the production literal without
// running a real listener. Used by RunControlServer.
func NewControlHTTPServer(addr string, mux *http.ServeMux) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    int(api.DefaultMaxHeaderBytes),
	}
}

// Handler is the http.Handler interface assertion for the control mux; this
// file's only job is owning the listener and its endpoints.
var _ http.Handler = (*http.ServeMux)(nil)
