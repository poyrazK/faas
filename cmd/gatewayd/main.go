// Command gatewayd — edge proxy (spec §4.1).
//
// gatewayd is the ONLY public listener on the box: TLS termination, hostname
// routing, wake-blocking (holding a request during a cold wake), rate limiting,
// and request accounting. The wake-blocking edge logic (routing cache, rate
// limiter, wake gate, proxy) lives in pkg/gateway and is fully wired here; the
// Backend that fronts Postgres routing + schedd gRPC lands with M5. TLS via
// CertMagic (:80/:443) is added in M4/M8 — this skeleton serves plain HTTP.
package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/wire"
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

const listenAddr = ":8080"

func main() {
	wire.Daemon("gatewayd", run)
}

func run(ctx context.Context, log *slog.Logger) error {
	return runWithDeps(ctx, log, defaultDeps())
}

func runWithDeps(ctx context.Context, log *slog.Logger, deps runDeps) error {
	handler := gateway.NewHandler(deps.backend)
	srv := deps.newSrv(listenAddr, handler)

	l, err := deps.listen("tcp", listenAddr)
	if err != nil {
		return err
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("gatewayd listening", "addr", listenAddr)
		if err := srv.Serve(l); err != nil && err != http.ErrServerClosed {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
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
