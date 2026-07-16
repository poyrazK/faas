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
//
//	public  :8080 (placeholder; eventually :80/:443) → Handler.ServeHTTP
//	private :9090                                       → /healthz /readyz /metrics
//
// Both share ctx cancellation so a SIGTERM shuts them down in parallel.
package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
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

// runDeps is the dependency seam for run. Tests inject net.Listen / http.Server
// wrappers so the seam is fully exercised without spawning a real daemon.
type runDeps struct {
	listen  func(network, addr string) (net.Listener, error)
	newSrv  func(addr string, handler http.Handler) *http.Server
	backend gateway.Backend
}

func defaultDeps() runDeps {
	return runDeps{
		listen:  net.Listen,
		newSrv:  defaultServer,
		backend: unwiredBackend{},
	}
}

func defaultServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

func main() {
	wire.Daemon("gatewayd", run)
}

func run(ctx context.Context, log *slog.Logger) error {
	return runWithDeps(ctx, log, defaultDeps())
}

// runWithDeps is the test-friendly variant. It exercises:
//
//   - public listen on listenAddr via deps.listen / deps.newSrv (DI seam)
//   - control listen on controlAddr via gateway.RunControlServer
//   - SIGHUP-triggered rate-limit-bucket reset (same behaviour as production)
//
// Production calls run → runWithDeps(defaultDeps()); tests inject a custom
// deps.listen so they can probe a real socket without binding :8080.
func runWithDeps(ctx context.Context, log *slog.Logger, deps runDeps) error {
	// TLS seam (M4 lands the wiring). Disabled until then — the public
	// listener binds :8080 plain HTTP. Reading this from TOML is a future PR.
	tlsCfg := gateway.TLSConfig{Disabled: true}
	if err := tlsCfg.Validate(); err != nil {
		return err
	}

	handler := gateway.NewHandlerWith(deps.backend, gateway.NewMetrics(), log)
	handler.SetWakeGateHook()

	// SIGHUP = "drop in-memory rate-limit buckets". Operators use this after
	// a mass-app-delete (apid → publish app.deleted; once M5 ships the LISTEN
	// channel, SIGHUP becomes the manual fallback). It's also safe to send
	// if rate-limit state ever drifts.
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-hup:
				dropped := handler.Limiter().ForgetAll()
				log.Info("gatewayd sighup reload",
					"action", "rate_limit_buckets_dropped",
					"count", dropped)
			}
		}
	}()

	// Public listener: customer traffic (spec §4.1).
	srv := deps.newSrv(listenAddr, handler)
	public := srv
	public.Addr = listenAddr
	if public.ReadTimeout == 0 {
		public.ReadTimeout = 60 * time.Second
	}
	if public.WriteTimeout == 0 {
		public.WriteTimeout = 300 * time.Second
	}

	// Private listener: control plane only — never authenticated (it's on a
	// private bind), never reachable from the public-internet path.
	controlMux := gateway.ControlMux(handler.Metrics(), nil)

	errc := make(chan error, 2)
	l, lerr := deps.listen("tcp", listenAddr)
	if lerr != nil {
		log.Error("gatewayd public listen failed", "addr", listenAddr, "err", lerr)
		return lerr
	}
	go func() {
		log.Info("gatewayd public listening", "addr", listenAddr)
		if err := public.Serve(l); err != nil && err != http.ErrServerClosed {
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
		//nolint:contextcheck // shutdown ctx must outlive the cancelled caller ctx (net/http contract).
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
