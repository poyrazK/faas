// Command gatewayd-internal — routing + wake + proxy daemon (Tier
// A7 split, ADR-070).
//
// gatewayd-internal owns everything that today lives in
// cmd/gatewayd EXCEPT the TLS termination, certmagic, and the
// public listener. It listens on a unix socket at
// /run/faas/gatewayd-internal.sock (the same socket shape
// schedd uses, ADR-015/018) and is reached only by gatewayd-public
// over loopback.
//
// Inbound traffic shape:
//
//	gatewayd-public → unix socket → gatewayd-internal.ServeHTTP
//	                                          → pkg/gateway/handler.go
//	                                              (hostname→app,
//	                                               wake gate,
//	                                               rate limit,
//	                                               forwarder)
//
// The handler, PGBackend, and forwarder code move verbatim from
// cmd/gatewayd to cmd/gatewayd-internal in a follow-on PR cluster.
// This PR cluster ships the daemon skeleton: the unix-socket
// listener, the loopback control plane, the readiness probe, and a
// placeholder handler that returns 200 OK with a banner so the
// proxy can be wired without producing 502s for legacy traffic.
// The full handler lands in the next PR (file moves tracked
// separately to keep review surface small).
//
// Listeners:
//
//	/run/faas/gatewayd-internal.sock   unix socket (loopback only)
//	127.0.0.1:9091                     control plane (/healthz, /readyz, /metrics)
//
// Readiness:
//
//   - PG ping (gateway.NewPGPingSignal)
//   - Routing cache hydrated (gateway.RouteCacheHydration.MarkHydrated)
//   - Schedd router has ≥1 ready client
//
// Drain: SIGTERM → /readyz=503 → GatewayDrainGraceSeconds → Shutdown.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/wire"
)

const (
	defaultInternalSocket = "/run/faas/gatewayd-internal.sock"
	defaultControlAddr    = "127.0.0.1:9091"
	// placeholderBanner is the body the placeholder handler returns.
	// It exists so the daemon can be deployed (with /readyz green)
	// while the real handler is wired in the follow-on PR. The
	// banner is short so a curl-friendly smoke test can read it
	// and the body also serves as a tripwire — if a customer sees
	// "TEMPLATE_OK" in production, a deploy slipped through.
	placeholderBanner = "gatewayd-internal: handler wiring lands in follow-on PR — TEMPLATE_OK"

	// headerXFaasReason is the response header every pre-wire-up
	// stub in this daemon emits alongside a 503 so operators can
	// tell which subsystem is not yet wired (`cmd/gatewayd-internal
	// route-registration parity with the legacy daemon requires
	// the AppLogsHandler route to be mounted even when its deps
	// are nil — see the DELETE_ME_PR_B block below; PR-B replaces
	// the stub with a real handler and the header is reused by
	// the proxy/nodecache/warmhints/scheddrouter pre-wire-up
	// stubs that land in the same PR).
	headerXFaasReason = "X-Faas-Reason"

	// reasonAppLogsNotWired is the value emitted on the
	// /v1/apps/{slug}/logs stub. Distinguishing text is critical
	// because the same header is reused (proxy/nodecache/
	// warmhints/scheddrouter each have their own value); an
	// operator reading the header sees a precise cause instead
	// of a generic "not wired".
	reasonAppLogsNotWired = "gatewayd-internal: app-logs wired in PR-B"
)

// envOr is the canonical env-override helper. Empty env falls
// back to `def`. Use os.LookupEnv directly when the caller needs
// to distinguish unset from empty (the empty=skip pattern).
func envOr(envKey, def string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return def
}

func main() {
	wire.Daemon("gatewayd-internal", run)
}

// runDeps is the production dependency bundle. Tests inject a
// custom publicDeps to swap the dialer (the unix-socket path in
// particular) so the seam is fully exercised without root-level
// /run/faas.
type runDeps struct {
	log *slog.Logger
}

func defaultDeps() runDeps {
	return runDeps{log: slog.Default()}
}

// run is the daemon entry point. The handler is the placeholder
// (200 OK) until the file moves land; the unix-socket server is
// fully wired so the inter-daemon reverse-proxy hop is exercised
// end-to-end.
func run(ctx context.Context, log *slog.Logger) error {
	d := defaultDeps()
	d.log = log
	log.Info("gatewayd-internal: starting", "pid", os.Getpid())

	// Readytime probe — three signals, all flipped to true via
	// the placeholder path (the real handler wires cacheSig to
	// RouteCacheHydration.MarkHydrated() and routerSig to the
	// schedd router's first ready client).
	probe := &gateway.ReadyzProbe{}
	// The placeholder has no Postgres pool to ping (PR-B's run.go
	// opens the pool + calls NewPGPingSignal with a real pinger).
	// NewPGPingSignal with a nil pool panics on the first tick
	// (readiness.go:296 pool.Ping on nil), so we hand-construct a
	// always-ready signal and set a no-op stopper. PR-B replaces
	// this block with `pgSig, pgStop := gateway.NewPGPingSignal(ctx, pool, ...)`.
	pgSig := probe.Register()
	pgSig.Set(true, "")
	pgStop := func() {}
	cacheSig := probe.Register()
	cacheSig.Set(false, "route cache not hydrated yet")
	routerSig := probe.Register()
	routerSig.Set(false, "schedd router not ready")
	// Mirrored-PG signal — same bridge as gatewayd-public. Until
	// the real PG pool is wired, the probe is always 200 because
	// the placeholder path is the only consumer.
	pgProbeSig := probe.Register()
	pgProbeSig.Set(true, "")

	// Placeholder handler. Marker path `/warmhint/test` flips
	// cacheSig to true so the e2e harness can verify the
	// hydration signal lands end-to-end before the real handler
	// is wired. The default path returns 200 OK with the banner.
	cacheHydration := gateway.NewRouteCacheHydration()
	placeholder := placeholderWithHydration(cacheHydration, cacheSig)

	// DELETE_ME_PR_B — Tier-A7 PR-A applogs mux registration.
	//
	// Issue #254 / Move 4 PR-2: the AppLogsHandler claims the
	// customer-facing `GET /v1/apps/{slug}/logs` route. The
	// HANDLER is wired in PR-B (authMw + pgStore + scheddRouter
	// land in run.go that PR); the MUX REGISTRATION is part of
	// the file-move surface and must land in PR-A so that
	// cmd/apid's spec-compliance parity test still sees the
	// route after cmd/gatewayd/ is deleted. The 503 stub here
	// returns the same response shape the legacy daemon
	// delivered when any dep was nil (the if-block was skipped
	// in the legacy code, so the route 404'd; a 503 with an
	// X-Faas-Reason header is a strictly more useful failure
	// mode for the gap between file-move and wire-up).
	//
	// PR-B deletes the entire `mux := http.NewServeMux(); ...;
	// placeholder = mux` block (lines below this comment
	// through the `placeholder = mux` assignment) and replaces
	// it with a single `mux.Handle("GET /v1/apps/{slug}/logs",
	// &AppLogsHandler{...})` after the AppLogsHandler is
	// constructed. The DELETE_ME_PR_B marker is greppable so
	// the PR review has a forced tripwire.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/apps/{slug}/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerXFaasReason, reasonAppLogsNotWired)
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	mux.Handle("/", placeholder)
	placeholder = mux

	// Unix-socket listener. Mode 0660 + group faas is the §11 ACL
	// (ADR-015); the daemon-bootstrap concern (group setup, umask)
	// is documented in deploy/systemd/gatewayd-internal.service.
	internalSocket := envOr("FAAS_INTERNAL_SOCKET", defaultInternalSocket)
	_ = os.Remove(internalSocket)
	unixListener, err := net.Listen("unix", internalSocket)
	if err != nil {
		return fmt.Errorf("gatewayd-internal: listen unix: %w", err)
	}
	defer func() {
		_ = unixListener.Close()
		_ = os.Remove(internalSocket)
	}()
	if err := os.Chmod(internalSocket, 0o660); err != nil {
		log.Warn("gatewayd-internal: chmod unix socket", "err", err)
	}

	// HTTP servers: unix-socket handler + loopback control plane.
	internalSrv := &http.Server{
		Handler:           placeholder,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      300 * time.Second,
	}
	controlMux := gateway.ControlMux(nil, probe.ReadyFunc())
	controlAddr := envOr("FAAS_INTERNAL_CONTROL_ADDR", defaultControlAddr)
	controlListener, err := net.Listen("tcp", controlAddr)
	if err != nil {
		return fmt.Errorf("gatewayd-internal: control listen: %w", err)
	}
	controlSrv := &http.Server{
		Handler:           controlMux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Drain orchestration.
	if err := runDrain(ctx, log, internalSrv, controlSrv, unixListener, controlListener, cacheSig, routerSig, pgProbeSig, pgStop); err != nil {
		return err
	}
	return nil
}

// runDrain blocks on (ctx, SIGTERM, listener error). On SIGTERM it
// flips /readyz probes to not-ready, sleeps up to
// GatewayDrainGraceSeconds (cancellable on a second SIGTERM), then
// Shutdowns both servers.
//
// Drain order (ADR-068): internal-first, public-second. This
// daemon is the "internal" half; the public daemon drains AFTER
// its forwarding has stopped. The placeholder path drains fast
// (no in-flight state to wait on).
func runDrain(ctx context.Context, log *slog.Logger, internalSrv, controlSrv *http.Server, unixListener, controlListener net.Listener, cacheSig, routerSig, pgProbeSig *gateway.ReadySignal, pgStop func()) error {
	drainCtx, cancelDrain := context.WithCancel(context.Background())
	defer cancelDrain()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		log.Info("gatewayd-internal: SIGTERM received; draining")
		cacheSig.Set(false, "draining")
		routerSig.Set(false, "draining")
		pgProbeSig.Set(false, "draining")
		pgStop()
		// Cancellable sleep — a second SIGTERM short-circuits the
		// grace period.
		grace := time.Duration(api.GatewayDrainGraceSeconds) * time.Second
		t := time.NewTicker(1 * time.Second)
		defer t.Stop()
		deadline := time.NewTimer(grace)
		defer deadline.Stop()
		select {
		case <-deadline.C:
		case <-sigCh:
			log.Warn("gatewayd-internal: second SIGTERM received; skipping drain grace")
		}
		cancelDrain()
	}()

	errc := make(chan error, 2)
	go func() {
		log.Info("gatewayd-internal: listening", "socket", defaultInternalSocket)
		if err := internalSrv.Serve(unixListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()
	go func() {
		log.Info("gatewayd-internal: control listening", "addr", defaultControlAddr)
		if err := controlSrv.Serve(controlListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case <-drainCtx.Done():
		log.Info("gatewayd-internal: shutting down")
	case err := <-errc:
		return err
	}
	// Shutdown both servers gracefully. 5 s grace.
	// context.WithoutCancel detaches from the parent's cancellation
	// so a SIGTERM-driven parent cancel can't short-circuit the
	// grace period before in-flight requests finish (golangci-lint
	// v8 contextcheck: `Background` would lose any deadline set by
	// the wire.Daemon harness; WithoutCancel keeps the deadline
	// without inheriting the parent cancel).
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := internalSrv.Shutdown(sctx); err != nil {
		log.Warn("gatewayd-internal: internal Shutdown", "err", err)
	}
	if err := controlSrv.Shutdown(sctx); err != nil {
		log.Warn("gatewayd-internal: control Shutdown", "err", err)
	}
	return nil
}

// placeholderWithHydration returns the placeholder handler wired
// to the cache-hydration tracker. The marker path `/warmhint/test`
// flips both the hydration bit and the cacheSig, so the e2e
// harness can verify the hydration signal lands end-to-end before
// the real handler is wired. The default path returns 200 OK
// with the banner body — important so /readyz stays green and
// the proxy can be wired without 502s.
func placeholderWithHydration(hydro *gateway.RouteCacheHydration, cacheSig *gateway.ReadySignal) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/warmhint/test" {
			hydro.MarkHydrated()
			cacheSig.Set(true, "")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("hydrated"))
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(placeholderBanner))
	})
}
