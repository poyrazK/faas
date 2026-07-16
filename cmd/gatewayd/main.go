// Command gatewayd — edge proxy (spec §4.1).
//
// gatewayd is the ONLY public listener on the box: TLS termination, hostname
// routing, wake-blocking (holding a request during a cold wake), rate limiting,
// and request accounting. The wake-blocking edge logic (routing cache, rate
// limiter, wake gate, proxy) lives in pkg/gateway and is fully wired here; the
// Backend that fronts Postgres routing + schedd gRPC lands with M5. TLS via
// CertMagic (:80/:443) is added in M4/M8 — this skeleton serves plain HTTP on
// :8080 today.
//
// Two listeners run inside this daemon:
//   public  :8080 (placeholder; eventually :80/:443) → Handler.ServeHTTP
//   private :9090                                       → /healthz /readyz /metrics
//
// Both share ctx cancellation so a SIGTERM shuts them down in parallel.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/wire"
)

const (
	// listenAddr is the public listener (TLS lands here in M4/M8).
	listenAddr = ":8080"
	// controlAddr is the private control-plane listener — never reachable from
	// the internet; bind to the loopback interface by default so an
	// operator-prometheus scrape is the only thing that can reach it.
	controlAddr = "127.0.0.1:9090"
)

func main() {
	wire.Daemon("gatewayd", run)
}

func run(ctx context.Context, log *slog.Logger) error {
	handler := gateway.NewHandlerWith(unwiredBackend{}, gateway.NewMetrics(), log)
	handler.SetWakeGateHook()

	// Public listener: customer traffic (spec §4.1).
	public := &http.Server{
		Addr:              listenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		// 300 s total per spec §4.1.
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 300 * time.Second,
	}

	// Private listener: control plane only — never authenticated (it's on a
	// private bind), never reachable from the public-internet path.
	controlMux := gateway.ControlMux(handler.Metrics(), nil)

	errc := make(chan error, 2)
	go func() {
		log.Info("gatewayd public listening", "addr", listenAddr)
		if err := public.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errc <- err
		}
	}()
	go func() {
		log.Info("gatewayd control listening", "addr", controlAddr)
		errc <- gateway.RunControlServer(ctx, controlAddr, controlMux)
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = public.Shutdown(shutdownCtx)
		return nil
	}
}

// unwiredBackend routes nothing; every request 404s until the Postgres routing
// cache and schedd wake path are connected in M5.
type unwiredBackend struct{}

func (unwiredBackend) Lookup(context.Context, string) (gateway.App, bool) {
	return gateway.App{}, false
}
func (unwiredBackend) Target(string) (string, bool)       { return "", false }
func (unwiredBackend) Wake(context.Context, string) error { return nil }
