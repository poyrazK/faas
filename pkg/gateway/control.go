// Control-plane listener for gatewayd (spec §11, §12). The /healthz, /readyz,
// and /metrics endpoints MUST NOT be on the public listener — they leak
// operational data and CSRF-style probes have no business hitting them. This
// file owns a SECOND *http.Server on a private listener (default :9090) that
// serves only the control routes. The public listener stays single-purpose:
//
//	public   :80/:443   → Handler.ServeHTTP       (proxies customer apps)
//	private  :9090      → ControlMux              (health + metrics)
//
// The private listener is wired into the cmd/gatewayd main alongside the
// public server with its own graceful-shutdown context.
package gateway

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// ControlAddr is the bind address for the control-plane listener. Kept on
// the daemon registry so an operator can override via env without editing
// source. The /metrics endpoint is intentionally unauthenticated because it
// is reached by the local Prometheus scraper on a private interface only.
const ControlAddr = ":9090"

// ControlMux returns the *http.ServeMux with /healthz, /readyz, /metrics.
// /readyz is wired to a Ready func the daemon registers on construction
// (e.g. "true once routing cache is hydrated from Postgres").
func ControlMux(m *Metrics, ready ReadyFunc) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if ready == nil || ready() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ready"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not-ready"))
	})
	if m != nil {
		mux.Handle("/metrics", m.Handler())
	}
	return mux
}

// ReadyFunc reports whether the daemon is ready to serve traffic. Used by
// /readyz. Returns true by default; replaced by main once the routing cache
// is hydrated and (post-TLS) the cert store is loaded.
type ReadyFunc func() bool

// RunControlServer starts the control-plane listener and blocks until ctx is
// cancelled, then performs a graceful shutdown bounded by 5 s. Errors other
// than http.ErrServerClosed are returned.
func RunControlServer(ctx context.Context, addr string, mux *http.ServeMux) error {
	if addr == "" {
		addr = ControlAddr
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
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

// Handler is the http.Handler interface assertion for the control mux; this
// file's only job is owning the listener and its endpoints.
var _ http.Handler = (*http.ServeMux)(nil)
