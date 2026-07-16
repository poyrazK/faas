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
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/wire"
)

const listenAddr = ":8080"

func main() {
	wire.Daemon("gatewayd", run)
}

func run(ctx context.Context, log *slog.Logger) error {
	handler := gateway.NewHandler(unwiredBackend{})
	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("gatewayd listening", "addr", listenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		//nolint:contextcheck // shutdown context must outlive request ctx; detached from caller per net/http contract.
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
