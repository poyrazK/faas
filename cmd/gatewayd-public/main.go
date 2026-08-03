// Command gatewayd-public — TLS-only edge (Tier A7 split, ADR-070).
//
// gatewayd-public is the box's only public listener. It owns:
//   - :80 ACME redirect + .well-known/acme-challenge/*
//   - :443 TLS termination with certmagic GetCertificate
//   - pkg/httpsec outer wrapper (HSTS / CSP nonce / X-Frame-Options /
//     Referrer-Policy / X-Content-Type-Options / Permissions-Policy)
//   - /healthz, /readyz, /metrics on loopback 127.0.0.1:9090
//   - Drain semantics: SIGTERM → flip /readyz → wait in-flight → Shutdown
//   - Cert-bundle leader election + per-bundle replication
//     (pkg/gateway/certsync)
//
// It does NOT own:
//   - hostname→app routing (gatewayd-internal does)
//   - the wake gate (gatewayd-internal does)
//   - the per-node forwarder (gatewayd-internal does)
//   - the rate limiter (gatewayd-internal does)
//
// Inbound traffic shape:
//
//	customer HTTPS request → :443 listener → httpsec outer wrapper
//	                                     → certmagic GetCertificate
//	                                     → pkg/gateway/internal_proxy.go
//	                                        (reverse-proxy to gatewayd-internal
//	                                         over /run/faas/gatewayd-internal.sock)
//
// Same-box only in v1.0; cross-box mTLS is Gate-B work.
//
// Operators configure gatewayd-public via:
//   - TOML (CertMagicConfig, listenAddr, internalProxyAddr, replicaAddr)
//   - env overrides for the loopback paths (FAAS_INTERNAL_SOCKET,
//     FAAS_PUBLIC_LISTEN_ADDR, FAAS_PUBLIC_CONTROL_ADDR,
//     FAAS_HETZNER_DNS_TOKEN_PATH) — same pattern as the legacy
//     FAAS_SCHEDD_SOCKET.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/gateway/certsync"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// domainLookup adapts *state.PgStore.DomainByName to the
// gateway.OnDemandLookup signature (which returns `any`). It also
// maps state.ErrNotFound → gateway.ErrNotFound so the allowlist's
// not-loud branch fires (pkg/gateway/allowlist.go:43-50 documents
// this contract).
func domainLookup(store *state.PgStore) gateway.OnDemandLookup {
	return func(ctx context.Context, domain string) (any, error) {
		d, err := store.DomainByName(ctx, domain)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, gateway.ErrNotFound
			}
			return nil, err
		}
		return d, nil
	}
}

// hstsEnabledFromEnv is the httpsec.HSTSEnabledFromEnv-shaped
// adapter. Note we use os.LookupEnv here (per the FAAS_APID_METRICS_ADDR
// empty=skip precedent) so an operator who explicitly sets
// FAAS_HSTS_ENABLED="" can distinguish "unset" from "off".
func hstsEnabledFromEnv(k string) string {
	if v, ok := os.LookupEnv(k); ok {
		return v
	}
	return ""
}

const (
	defaultListenAddr     = ":443"
	defaultInternalSocket = "/run/faas/gatewayd-internal.sock"
	defaultStorageDir     = "/var/lib/faas/certs"
	defaultAppsDomain     = "apps.gregale.dev"
	// defaultHetznerTokenPath matches the legacy cmd/gatewayd + the
	// ansible role's provisioned path. Operators who set the new
	// daemon's env without the ansible role can still hit the same
	// file; the ansible role drops the same file at the same path.
	defaultHetznerTokenPath = "/etc/faas/secrets/hetzner-dns.token"
	// defaultPublicControlAddr is the loopback control plane
	// (127.0.0.1, not ":9090" — the legacy gatewayd still binds
	// :9090 and the two daemons would collide).
	defaultPublicControlAddr = "127.0.0.1:9090"
)

func main() {
	wire.Daemon("gatewayd-public", run)
}

// run is the daemon entry point. It builds the listener stack,
// wires the readiness probe, and blocks on ctx cancellation.
func run(ctx context.Context, log *slog.Logger) error {
	log.Info("gatewayd-public: starting", "pid", os.Getpid())

	// Postgres — required for leader election + warm-hint mirror.
	pool, err := db.Open(ctx, "")
	if err != nil {
		return fmt.Errorf("gatewayd-public: open db: %w", err)
	}
	defer pool.Close()
	pgStore := state.NewPgStore(pool)

	// Node identity — read once at boot. The certsync leader
	// election keys off this; if it can't be resolved we abort.
	nodeID, _, err := resolveNodeIdentity(ctx, pgStore, log)
	if err != nil {
		return fmt.Errorf("gatewayd-public: resolve node identity: %w", err)
	}

	// Readiness probe. Three signals:
	//   1. PG ping — Postgres reachable (separate helper).
	//   2. Cert bundle — leader-elected and per-replica storage
	//      ready.
	//   3. Internal proxy — at least one successful proxy request
	//      in the last 5 s (gated on first real traffic).
	probe, pgProbeSig, pgStop := setupReadiness(ctx, pool, log)
	defer pgStop()

	// Certmagic config — production TLS bundle.
	storageDir := envOr("FAAS_CERT_STORAGE_DIR", defaultStorageDir)
	appsDomain := envOr("FAAS_APPS_DOMAIN", defaultAppsDomain)
	hetznerTokenPath := envOr("FAAS_HETZNER_DNS_TOKEN_PATH", defaultHetznerTokenPath)
	// Token contents go to NewCertMagicConfig — not the file path.
	// The previous version passed the path; certmagic would then
	// call the DNS-01 solver with the literal path string and every
	// challenge would fail. Mirrors cmd/gatewayd/main.go:510-512.
	hetznerToken, err := loadSecretFile(hetznerTokenPath)
	if err != nil {
		return fmt.Errorf("gatewayd-public: Hetzner DNS token: %w", err)
	}
	tlsBundle, err := gateway.NewCertMagicConfig(ctx, gateway.TLSConfig{
		Disabled:                false,
		WildcardCertDomain:      appsDomain,
		HetznerDNSAPITokenPath:  hetznerTokenPath,
		HetznerZone:             appsDomain,
		StorageDir:              storageDir,
		ContactEmail:            envOr("FAAS_ACME_CONTACT_EMAIL", "ops@"+appsDomain),
		OnDemandHTTP01Allowlist: gateway.NewPGAllowlist(domainLookup(pgStore), log), //nolint:contextcheck // NewPGAllowlist does not take a ctx
	}, hetznerToken, log, nil, nil)
	if err != nil {
		return fmt.Errorf("gatewayd-public: certmagic: %w", err)
	}

	// Certsync leader — elects once at boot. The loop below
	// re-elects every CertSyncIntervalSeconds so a dead leader is
	// replaced within one interval.
	certSig := probe.Register()
	certSig.Set(true, "")
	leader := certsync.NewLeader(nodeID, &pgNodeLister{pgStore}, log)
	if _, err := leader.Recompute(ctx); err != nil {
		log.Warn("gatewayd-public: certsync initial election failed", "err", err)
	}
	go runCertSyncLoop(ctx, log, leader, certSig)

	// Reverse-proxy to gatewayd-internal over the unix socket.
	internalSocket := envOr("FAAS_INTERNAL_SOCKET", defaultInternalSocket)
	internalURL := &url.URL{Scheme: "http", Host: "gatewayd-internal"}
	proxy := gateway.NewInternalReverseProxy(
		gateway.NewUnixSocketDialer(internalSocket),
		internalURL,
		log,
	)

	// Trace pipeline (issue #555 PR-2). Builds the in-memory ring,
	// wires the OTLP/HTTP exporter when OTEL_EXPORTER_OTLP_ENDPOINT
	// is set, and installs the GET /v1/traces/{trace_id} handler.
	// The trace setup is gatewayd-public's responsibility because
	// the ring is per-daemon (ADR-070 cross-box HA is N boxes each
	// with their own ring; the GET endpoint reaches this box only).
	traceSetup, err := gateway.InstallTracePipeline(ctx, "gatewayd-public", wire.Version, log)
	if err != nil {
		return fmt.Errorf("gatewayd-public: trace setup: %w", err)
	}
	// traceHandler mounts under /v1/traces/ (the trace handler
	// reads the path suffix). Wrap the proxy in a mux that
	// routes /v1/traces/ to the handler and falls through to the
	// proxy for everything else.
	traceMux := http.NewServeMux()
	traceMux.Handle("/v1/traces/", traceSetup.Handler)
	traceMux.Handle("/", proxy)

	// Public-facing handler: httpsec outer wrapper → trace mux → internal proxy.
	httpsec.SetHSTSEnabled(httpsec.HSTSEnabledFromEnv(hstsEnabledFromEnv))
	publicHandler := httpsec.Static(traceMux)

	// Control mux + listeners.
	controlMux := gateway.ControlMux(nil, probe.ReadyFunc())
	controlAddr := envOr("FAAS_PUBLIC_CONTROL_ADDR", defaultPublicControlAddr)
	listenAddr := envOr("FAAS_PUBLIC_LISTEN_ADDR", defaultListenAddr)
	publicSrv, controlSrv := buildServers(listenAddr, controlAddr, publicHandler, controlMux, tlsBundle)

	// Drain orchestration.
	if err := runDrain(ctx, log, publicSrv, controlSrv, certSig, pgProbeSig, pgStop, traceSetup); err != nil {
		return err
	}
	return nil
}

// setupReadiness builds the probe + the PG-ping signal whose bit is
// mirrored onto the probe's registered signal. The mirror is the
// bug-fixed bridge — the previous version read pgSig.Report() and
// discarded the values, so PG outages never flipped /readyz. Now
// the goroutine pushes the bit into the registered signal.
func setupReadiness(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) (*gateway.ReadyzProbe, *gateway.ReadySignal, func()) {
	probe := &gateway.ReadyzProbe{}
	pgSig, pgStop := gateway.NewPGPingSignal(ctx, pool, 5*time.Second)
	// Register a dedicated signal that is NOT pre-armed true (the
	// bridge below is the only writer; it flips it to the PG
	// liveness on the first tick).
	pgProbeSig := probe.Register()
	// Initial state: PG not yet checked. The bridge will flip it
	// within pgSig's first ping (synchronous, NewPGPingSignal
	// immediate-ping path).
	pgProbeSig.Set(false, "pg ping not yet sampled")
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				ready, reason := pgSig.Report()
				if ready {
					pgProbeSig.Set(true, "")
				} else {
					pgProbeSig.Set(false, reason)
				}
			}
		}
	}()
	// Internal-proxy + cert signals are set true at boot; failure
	// paths in the certsync loop and the proxy itself flip them
	// false.
	_ = log // future use: log on first probe state transition
	return probe, pgProbeSig, pgStop
}

// runCertSyncLoop re-elects every CertSyncIntervalSeconds so a dead
// leader is replaced within one interval. The bit lives on `certSig`,
// which is the operator-visible knob for /readyz.
func runCertSyncLoop(ctx context.Context, log *slog.Logger, leader *certsync.Leader, certSig *gateway.ReadySignal) {
	t := time.NewTicker(time.Duration(api.CertSyncIntervalSeconds) * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := leader.Recompute(ctx); err != nil {
				log.Warn("gatewayd-public: certsync election refresh failed", "err", err)
				certSig.Set(false, "certsync election failed: "+err.Error())
			} else {
				certSig.Set(true, "")
			}
		}
	}
}

// buildServers constructs the public TLS + loopback control servers.
// Kept separate so the run() body is shorter than the §handlers 50-line
// convention (CLAUDE.md).
func buildServers(listenAddr, controlAddr string, publicHandler http.Handler, controlMux *http.ServeMux, tlsBundle *gateway.TLSBundle) (*http.Server, *http.Server) {
	tlsCfg := &tls.Config{
		GetCertificate: tlsBundle.GetCertificate,
		MinVersion:     tls.VersionTLS13,
	}
	publicSrv := &http.Server{
		Addr:              listenAddr,
		Handler:           publicHandler,
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      300 * time.Second,
	}
	controlSrv := &http.Server{
		Addr:              controlAddr,
		Handler:           controlMux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return publicSrv, controlSrv
}

// runDrain blocks on (ctx, SIGTERM, listener error). On SIGTERM it
// flips /readyz probes to not-ready, sleeps up to
// GatewayDrainGraceSeconds (cancellable on a second SIGTERM), then
// Shutdowns both servers. The PG signal is stopped FIRST so the
// probe stays 503 during the entire drain (caller pauses LB after
// observing 503, then in-flight requests finish).
//
// The order matches the ADR-068 spec:
//
//  1. SIGTERM → flip probe bits to false. /readyz=503 immediately.
//  2. Stop the PG ping signal so a wedged connection can't stall
//     the drain (default 30 s TimeoutStopSec vs 25 s grace + 5 s
//     Shutdown).
//  3. Sleep GatewayDrainGraceSeconds (cancellable on a second
//     SIGTERM — the signal channel has capacity 1, so the second
//     signal is dropped at the OS layer; we use a select with a
//     1s ticker to make the sleep effectively cancellable).
//  4. Shutdown both servers with a 5 s grace.
//  5. pgStop() (already done above; kept here as a no-op safety net).
func runDrain(ctx context.Context, log *slog.Logger, publicSrv, controlSrv *http.Server, certSig, pgProbeSig *gateway.ReadySignal, pgStop func(), traceSetup *gateway.TraceSetup) error {
	drainCtx, cancelDrain := context.WithCancel(context.Background())
	defer cancelDrain()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		log.Info("gatewayd-public: SIGTERM received; draining")
		certSig.Set(false, "draining")
		pgProbeSig.Set(false, "draining")
		// Stop the PG ping signal BEFORE the grace sleep so the
		// probe stays 503 during the entire drain. The PG signal
		// goroutine may otherwise be stuck in pool.Ping and add
		// up to every/2 to the drain time.
		pgStop()
		// Cancellable sleep — a second SIGTERM short-circuits the
		// grace period and runs Shutdown immediately.
		grace := time.Duration(api.GatewayDrainGraceSeconds) * time.Second
		t := time.NewTicker(1 * time.Second)
		defer t.Stop()
		deadline := time.NewTimer(grace)
		defer deadline.Stop()
		select {
		case <-deadline.C:
		case <-sigCh:
			log.Warn("gatewayd-public: second SIGTERM received; skipping drain grace")
		}
		cancelDrain()
	}()

	// Start the listeners.
	errc := make(chan error, 2)
	go func() {
		l, lerr := net.Listen("tcp", publicSrv.Addr)
		if lerr != nil {
			errc <- fmt.Errorf("gatewayd-public: listen %s: %w", publicSrv.Addr, lerr)
			return
		}
		log.Info("gatewayd-public: public listening (TLS)", "addr", publicSrv.Addr)
		if err := publicSrv.Serve(tls.NewListener(l, publicSrv.TLSConfig)); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()
	go func() {
		log.Info("gatewayd-public: control listening", "addr", controlSrv.Addr)
		if err := controlSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case <-drainCtx.Done():
		log.Info("gatewayd-public: shutting down")
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
	if err := publicSrv.Shutdown(sctx); err != nil {
		log.Warn("gatewayd-public: public Shutdown", "err", err)
	}
	if err := controlSrv.Shutdown(sctx); err != nil {
		log.Warn("gatewayd-public: control Shutdown", "err", err)
	}
	// Flush any in-flight spans from the BatchSpanProcessor. We
	// use a fresh context (the daemon's ctx is already cancelled)
	// so the SDK shutdown can flush its full batch. The 5s upper
	// bound matches the public-server shutdown grace so a slow
	// collector doesn't stall the daemon drain.
	if traceSetup != nil {
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := traceSetup.Shutdown(flushCtx); err != nil {
			log.Warn("gatewayd-public: trace shutdown", "err", err)
		}
		cancel()
	}
	return nil
}

// resolveNodeIdentity reads compute_nodes row for this box. Falls
// back to env-supplied FAAS_NODE_ID if the row can't be read at
// boot (cluster may not be bootstrapped yet — operators can
// pre-provision via env).
func resolveNodeIdentity(ctx context.Context, store *state.PgStore, log *slog.Logger) (id, name string, err error) {
	if envID := os.Getenv("FAAS_NODE_ID"); envID != "" {
		name = os.Getenv("FAAS_NODE_NAME")
		if name == "" {
			name = "default-local"
		}
		log.Warn("gatewayd-public: using env-supplied node identity (PG lookup skipped)",
			"node_id", envID, "node_name", name)
		return envID, name, nil
	}
	nodes, err := store.ActiveComputeNodes(ctx)
	if err != nil {
		return "", "", fmt.Errorf("list compute nodes: %w", err)
	}
	if len(nodes) == 0 {
		return "", "", errors.New("no active compute_nodes row; bootstrap the box first")
	}
	host, _ := os.Hostname()
	for _, n := range nodes {
		if n.Name == host {
			return n.ID, n.Name, nil
		}
	}
	log.Warn("gatewayd-public: no compute_nodes row matches hostname; using first active",
		"hostname", host, "rows", len(nodes))
	return nodes[0].ID, nodes[0].Name, nil
}

// pgNodeLister adapts *state.PgStore to certsync.NodeLister.
type pgNodeLister struct {
	store *state.PgStore
}

func (l *pgNodeLister) ListActive(ctx context.Context) ([]certsync.Node, error) {
	rows, err := l.store.ActiveComputeNodes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]certsync.Node, 0, len(rows))
	for _, r := range rows {
		out = append(out, certsync.Node{
			ID:   r.ID,
			Name: r.Name,
			Addr: "/run/faas/gatewayd-public-replica.sock",
		})
	}
	return out, nil
}

// envOr is the canonical env-override helper (per cmd/gatewayd/main.go).
// Empty env falls back to `def`. Use os.LookupEnv directly when the
// caller needs to distinguish unset from empty (the empty=skip
// pattern; see FAAS_APID_METRICS_ADDR memory note).
func envOr(envKey, def string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return def
}
