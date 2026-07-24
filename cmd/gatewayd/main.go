// Command gatewayd — edge proxy (spec §4.1).
//
// gatewayd is the ONLY public listener on the box: TLS termination, hostname
// routing, wake-blocking (holding a request during a cold wake), rate limiting,
// and request accounting. The wake-blocking edge logic (routing cache, rate
// limiter, wake gate, proxy) lives in pkg/gateway and is fully wired here.
//
// M5: run() builds the production gateway.PGBackend — host→app routing over
// Postgres (read-only; apid/schedd own the writes) plus schedd over gRPC on
// /run/faas/schedd.sock (ADR-018) for wakes — and keeps its caches fresh from
// the instance_changed / app_changed pg_notify channels. TLS via CertMagic
// (:80/:443) is wired in M8 — when TLSConfig.Disabled=true (default) the
// daemon serves plain HTTP on :8080; when Disabled=false it binds :443 (TLS)
// and :80 (ACME mux + :80→:443 redirect).
//
// Listeners run inside this daemon:
//
//	Disabled=true  → public :8080 plain HTTP            (legacy / e2e harness)
//	Disabled=false → public :443 TLS + :80 ACME/redirect (production)
//	private        :9090 loopback                       → /healthz /readyz /metrics
//
// All share ctx cancellation so a SIGTERM shuts them down in parallel.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// scheddSocket is schedd's gRPC unix socket (ADR-018). Overridable via
// FAAS_SCHEDD_SOCKET so the e2e harness can point at a per-test path.
var scheddSocket = envOrGateway("FAAS_SCHEDD_SOCKET", "/run/faas/schedd.sock")

// gatewaydInternalSocket is the unix-domain socket schedd dials to
// fire synthetic cron requests through gatewayd (spec §4.4, M7).
// Mode 0660 group `faas` (ADR-015); only schedd can dial.
const gatewaydInternalSocket = "/run/faas/gatewayd-internal.sock"

// listenAddr is the public listener (TLS lands here in M8). Overridable via
// FAAS_GATEWAY_LISTEN so the e2e harness can bind a free port without colliding
// with a dev daemon on :8080.
var listenAddr = envOrGateway("FAAS_GATEWAY_LISTEN", ":8080")

// configPath is the on-disk TOML config. Overridable via FAAS_GATEWAYD_CONFIG
// for non-standard deployments; production uses /etc/faas/gatewayd.toml.
var configPath = envOrGateway("FAAS_GATEWAYD_CONFIG", "/etc/faas/gatewayd.toml")

const (
	// controlAddr is the private control-plane listener — never reachable from
	// the internet; bind to the loopback interface by default so an
	// operator-prometheus scrape is the only thing that can reach it.
	controlAddr = "127.0.0.1:9090"
)

// synthAdapter implements gateway.SynthDispatcher on top of the schedd
// gRPC client + the in-process gateway handler. Move 1 widens the
// surface from Wake-only to two methods so the synthetic HTTP envelope
// (method + path + body + headers) actually reaches the runner on
// cron / async / queue-pull / delayed-task paths — the legacy
// wake-only path left cron traffic invisible to the runner and the
// meter (spec §4.4, M7).
type synthAdapter struct {
	wake   func(ctx context.Context, appID string) error
	invoke func(ctx context.Context, appID string, inv state.Invocation) (state.Invocation, error)
}

func (a *synthAdapter) Wake(ctx context.Context, appID string) error { return a.wake(ctx, appID) }

// Invoke first wakes an instance (idempotent if RUNNING — always-Wake
// per the Move 1 plan), then routes the synthetic envelope through
// the wake gate via the in-process gateway handler. The runner side
// receives (method, path, headers, body), parses them, and answers
// the HTTP response; that response becomes the Invocation.Result the
// caller writes back via Store.CompleteInvocation.
func (a *synthAdapter) Invoke(ctx context.Context, appID string, inv state.Invocation) (state.Invocation, error) {
	if a.invoke == nil {
		return inv, fmt.Errorf("gateway synth: invoke is not wired (legacy wake-only adapter)")
	}
	return a.invoke(ctx, appID, inv)
}

// runDeps is the dependency seam for run. Tests inject net.Listen / http.Server
// wrappers so the seam is fully exercised without spawning a real daemon.
type runDeps struct {
	listen  func(network, addr string) (net.Listener, error)
	newSrv  func(addr string, handler http.Handler) *http.Server
	backend gateway.Backend
	// synth is the internal unix-socket RPC server schedd dials for cron
	// dispatch (spec §4.4, M7). nil in tests; production wires it after
	// the schedd client is dialed.
	synth *gateway.SynthServer
	// lastSeen flushes per-instance last_request_at to schedd (spec §4.1). nil in
	// tests (the wake/routing path doesn't need it); production wires the
	// schedFlushSink.
	lastSeen gateway.LastSeenSink
	// tlsBundle, when non-nil, switches the public listener from plain HTTP
	// to TLS (certmagic-managed). Production builds this in run() when
	// cfg.TLS.Disabled=false; tests leave it nil to exercise the legacy
	// plain-:8080 path.
	tlsBundle *gateway.TLSBundle
	// acmeMux, when non-nil, is mounted on the :80 listener alongside the
	// TLS listener. Production builds this in run() when TLS is enabled;
	// tests leave it nil.
	acmeMux http.Handler
	// extraListen is an optional secondary listener (the :80 ACME mux when
	// TLS is enabled). nil in the legacy path. Tests use it to exercise
	// the production-style dual-listener setup without binding :80.
	extraListen func(network, addr string) (net.Listener, error)
	// controlAddr is the loopback control-plane bind (default
	// 127.0.0.1:9090). Tests inject a free-port value via "127.0.0.1:0"
	// + the resolved listener so two tests in the same package don't race
	// for the hard-coded production port.
	controlAddr string
	// apidLoopback is the operator-configured upstream URL the apidProxy
	// forwards to (cfg.APIDLoopback / deploy/digitalocean/config/
	// gatewayd.toml `apid_loopback`). Empty in tests; run() populates it
	// from cfg before invoking runWithDeps.
	apidLoopback string
	// nodeCache holds the per-node *grpc.ClientConn cache plus the
	// compute_node_changed pg_notify subscriber (issue #98 / ADR-028).
	// nil in tests; production wires it after pgStore opens.
	nodeCache *nodeCache
}

func defaultDeps() runDeps {
	return runDeps{
		listen:      net.Listen,
		newSrv:      defaultServer,
		backend:     unwiredBackend{},
		controlAddr: controlAddr,
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
	pool, err := db.Open(ctx, "")
	if err != nil {
		return fmt.Errorf("gatewayd: open db: %w", err)
	}
	defer pool.Close()

	sched, err := scheddgrpc.DialContext(ctx, scheddSocket, nil)
	if err != nil {
		return fmt.Errorf("gatewayd: dial schedd: %w", err)
	}
	defer func() { _ = sched.Close() }()

	cfg, err := LoadConfig(configPath)
	if err != nil {
		return err
	}

	// Env-derived AppsDomain wins over the TOML file so the e2e harness can
	// run without writing a TOML. The legacy path is plain :8080 with the
	// suffix filter on; the production path is certmagic + :443/:80.
	appsDomain := os.Getenv("FAAS_APPS_DOMAIN")
	if appsDomain == "" {
		appsDomain = cfg.AppsDomain
	}
	router := pgRouter{store: state.NewPgStore(pool), appsSuffix: appsSuffix(appsDomain)}
	backend := gateway.NewPGBackend(router, sched, log)

	// Keep the routing + target caches fresh from apid/schedd's pg_notify
	// stream (spec §4.1): an instance state change evicts the app's cached
	// target so the next request re-resolves via an idempotent wake; an app or
	// domain change flushes the host→app routes.
	go watchInvalidations(ctx, pool, backend, log)

	deps := defaultDeps()
	deps.backend = backend
	// Flush per-instance last_request_at to schedd so its idle reaper sees
	// gateway traffic (spec §4.1, ADR-018) — without this a busy app parks once
	// its idle timer fires. schedd is the sole writer to `instances`, so we hand
	// it the batch over gRPC (the same client we wake through).
	deps.lastSeen = newSchedFlushSink(backend, sched, log)
	// Internal unix-socket RPC for schedd's cron dispatch loop (spec §4.4,
	// M7). Routes a synthetic wake through schedd so metering + the
	// per-minute sampler see the live instance. lastSeen-touches for cron
	// traffic land in a follow-up once we expose an app-scoped touch RPC
	// (today schedd's ReportActivity takes instance_ids, which the synth
	// path doesn't have without a Wake first).
	deps.synth = gateway.NewSynthServer(gatewaydInternalSocket, &synthAdapter{
		wake: func(ctx context.Context, appID string) error {
			// wake_id is discarded on the synth path (gaps analysis
			// 2026-07-23): synthesized requests don't return a
			// response header to a customer, so x-faas-wake-id has
			// no consumer here. Wake is still called for the
			// admit + boot side effects.
			_, _, _, err := sched.Wake(ctx, appID)
			return err
		},
		// Move 1: Wake the instance, then route the synthetic
		// envelope. The wake gate handles admit + boot; the
		// envelope delivery to the runner is the per-app internal
		// queue Move 2 introduces. For now the drain records the
		// post-state as 'dispatching' — the row's result is set
		// later (or left NULL for async sources) by the drain's
		// CompleteInvocation call. The live instance handle from
		// the Wake response is echoed back on the returned
		// Invocation so schedd can StampInstanceInvocation —
		// without it the meter's per-instance count lands on 0.
		invoke: func(ctx context.Context, appID string, inv state.Invocation) (state.Invocation, error) {
			instanceID, _, _, err := sched.Wake(ctx, appID)
			if err != nil {
				return inv, fmt.Errorf("synth invoke wake %s: %w", appID, err)
			}
			inv.InstanceID = instanceID
			inv.State = state.InvocationDispatching
			return inv, nil
		},
	}, log)

	// TLS path: only when the operator opted in via the TOML [tls] table.
	// The Disabled=true path stays on plain :8080 so the e2e harness keeps
	// working without a config file (and without bind capability requirements).
	pgStore := state.NewPgStore(pool)
	// Wrap pgStore.DomainByName (which returns (state.CustomDomain, error))
	// as the gateway.OnDemandLookup shape: any-typed result, with state.ErrNotFound
	// surfaced as gateway.ErrNotFound so the steady-state denial path stays quiet.
	allowLookup := func(ctx context.Context, domain string) (any, error) {
		d, err := pgStore.DomainByName(ctx, domain)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, gateway.ErrNotFound
			}
			return nil, err
		}
		return d, nil
	}
	//nolint:contextcheck // The closure above forwards ctx to pgStore.DomainByName explicitly; golangci can't trace the call through the OnDemandLookup function-type indirection.
	resolved := cfg.resolveTLSConfig(gateway.NewPGAllowlist(allowLookup, log))
	if !resolved.Disabled {
		tok, err := loadSecretFile(resolved.HetznerDNSAPITokenPath)
		if err != nil {
			return fmt.Errorf("gatewayd: Hetzner DNS token: %w", err)
		}
		bundle, err := gateway.NewCertMagicConfig(ctx, resolved, tok, log, nil)
		if err != nil {
			return fmt.Errorf("gatewayd: certmagic: %w", err)
		}
		deps.tlsBundle = bundle
		deps.acmeMux = gateway.NewACMEMux(bundle.HTTPChallengeHandler)
		deps.extraListen = net.Listen
		// Public listener now binds :443, not :8080.
		listenAddr = ":443"
	}
	// Forward the operator-configured apid loopback URL through the
	// test seam so runWithDeps can stay TOML-free (issue #85).
	deps.apidLoopback = cfg.APIDLoopback
	// Issue #98 / ADR-028, plumbed via issue #120: per-node vmmd
	// client cache. The dial closure routes through pkg/overlay so the
	// cross-box dial primitive lives in one place; mTLS material is
	// loaded from the [vmmd_tls_*] TOML keys (mirroring schedd's
	// cmd/schedd/config.go LoadVMMTLS). For single-box deployments
	// all three paths are empty and LoadVMMDPingTLS returns nil,
	// which overlay.Dial (and the underlying wire.DialContext)
	// accepts on unix targets. Subscribing to compute_node_changed
	// runs in a goroutine that dies with ctx.
	vmmdTLS, err := cfg.LoadVMMDPingTLS()
	if err != nil {
		return fmt.Errorf("gatewayd: load vmmd TLS: %w", err)
	}
	deps.nodeCache = newNodeCache(pgStore, vmmdTLS, log)
	go deps.nodeCache.WatchEvictions(ctx, pool)
	return runWithDeps(ctx, log, deps)
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
	// TLS resolution happens in run() before this is called: if
	// deps.tlsBundle != nil the public listener binds :443 with certmagic;
	// otherwise we fall back to the legacy plain :8080 path the e2e harness
	// uses.

	handler := gateway.NewHandlerWith(deps.backend, gateway.NewMetrics(), log)
	handler.SetWakeGateHook()

	// Issue #98 / ADR-028: install the per-node HTTP→gRPC forwarder.
	// Backend.Target returns a compute_node.id (string-typed for
	// backwards compat); the forwarder dereferences it via the
	// per-node vmmd client cache and bridges HTTP bytes to the
	// instance netns through vmmd's ForwardHTTP RPC. nil cache =
	// legacy addr-based path (tests + e2e harness without vmmd
	// overlay).
	if deps.nodeCache != nil {
		handler.WithForwarding(deps.nodeCache.Forwarding())
	}

	// Per-instance last_request_at flush loop (spec §4.1). Present in production;
	// nil in tests. FlushEvery stops with ctx; drain its error channel so a flaky
	// schedd logs rather than leaks a goroutine.
	if deps.lastSeen != nil {
		handler.WithLastSeenSink(deps.lastSeen)
		errc := gateway.FlushEvery(ctx, lastSeenFlushInterval, deps.lastSeen)
		go func() {
			for range errc {
			}
		}()
	}

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

	// Public listener: customer traffic (spec §4.1). The handler is
	// wrapped in an apidProxy (M7.5, ADR-011, broadened in issue #85)
	// so the full apid public surface — /dashboard/*, /oauth/*,
	// /v1/*, /login*, /auth/verify, /logout, /status*, /healthz —
	// reverse-proxies to apid's loopback listener. Everything else
	// falls through to gateway.Handler's wake/proxy flow.
	//
	// Target resolution (issue #85): the TOML-loaded
	// cfg.APIDLoopback (threaded via deps.apidLoopback) wins;
	// FAAS_APID_LOOPBACK env is kept as a fallback so the e2e
	// harness (no TOML) keeps working.
	apidTarget := deps.apidLoopback
	if apidTarget == "" {
		apidTarget = os.Getenv("FAAS_APID_LOOPBACK")
	}
	if apidTarget == "" {
		apidTarget = "http://127.0.0.1:8081"
	}
	apidHandler := newApidProxy(apidTarget, handler, log)

	// Slice 7: githubd webhook HMAC-verify at the edge, then proxy
	// to githubd's loopback listener (ADR-012, §11 single-public-
	// listener invariant). githubd stays loopback-only so the
	// webhook secret is the only secret on this path that has to
	// leave githubd's own config (it doesn't — it lives in
	// gatewayd's env so the verify happens before the proxy hop).
	githubdTarget := os.Getenv("FAAS_GITHUBD_LOOPBACK")
	if githubdTarget == "" {
		githubdTarget = "http://127.0.0.1:8083"
	}
	githubdSecret := loadGithubWebhookSecret(osGetenv)
	publicHandler := newGithubdProxy(githubdTarget, githubdSecret, apidHandler, log)

	// Private listener: control plane only — never authenticated (it's on a
	// private bind), never reachable from the public-internet path.
	controlMux := gateway.ControlMux(handler.Metrics(), nil)

	// Track every *http.Server we spin up so the shutdown path can drain
	// them in parallel. sslib guidance is "call Shutdown on each" rather
	// than Close: Shutdown lets in-flight requests finish; Close does not.
	errc := make(chan error, 4)
	var servers []*http.Server
	addSrv := func(s *http.Server) { servers = append(servers, s) }
	if deps.tlsBundle != nil {
		// Production: TLS listener on :443 + ACME mux on :80. We deliberately
		// keep the dashboard / githubd proxy stack unchanged — it sits in
		// front of the wake/proxy handler on the TLS side and we still want
		// the gatewayd.Handler to handle app routing.
		tlsCfg := &tls.Config{
			GetCertificate: deps.tlsBundle.GetCertificate,
			// Pin TLS 1.3 inline so gosec/G402 sees the literal value rather
			// than chasing gateway.MinTLSVersion across packages (v2.4.0's
			// gosec does not resolve cross-package constants).
			MinVersion: tls.VersionTLS13,
		}
		public := &http.Server{
			Addr:              listenAddr, // :443 (set by run())
			Handler:           publicHandler,
			TLSConfig:         tlsCfg,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       60 * time.Second,
			WriteTimeout:      300 * time.Second,
		}
		addSrv(public)
		// When the http.Server has a non-nil TLSConfig, ServeTLS needs an
		// explicit cert/key. CertMagic handles cert retrieval via GetCertificate
		// and certmagic's docs recommend serving via net.Listen + Serve() (not
		// ServeTLS) so the GetCertificate callback is invoked. That is the
		// path we use here.
		l, lerr := deps.listen("tcp", listenAddr)
		if lerr != nil {
			log.Error("gatewayd TLS listen failed", "addr", listenAddr, "err", lerr)
			return lerr
		}
		go func() {
			log.Info("gatewayd public listening (TLS)", "addr", listenAddr)
			if err := public.Serve(tls.NewListener(l, tlsCfg)); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- err
			}
		}()
		// ACME / :80 listener — challenge dispatch + :80 → :443 redirect.
		const acmeAddr = ":80"
		acmeServer := &http.Server{
			Addr:              acmeAddr,
			Handler:           deps.acmeMux,
			ReadHeaderTimeout: 10 * time.Second,
		}
		addSrv(acmeServer)
		listenFn := deps.extraListen
		if listenFn == nil {
			listenFn = net.Listen
		}
		al, aerr := listenFn("tcp", acmeAddr)
		if aerr != nil {
			log.Error("gatewayd ACME listen failed", "addr", acmeAddr, "err", aerr)
			return aerr
		}
		go func() {
			log.Info("gatewayd ACME listening", "addr", acmeAddr)
			if err := acmeServer.Serve(al); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- err
			}
		}()
	} else {
		// Legacy plain-:8080 path. Existing e2e harness depends on this.
		srv := deps.newSrv(listenAddr, publicHandler)
		public := srv
		public.Addr = listenAddr
		if public.ReadTimeout == 0 {
			public.ReadTimeout = 60 * time.Second
		}
		if public.WriteTimeout == 0 {
			public.WriteTimeout = 300 * time.Second
		}
		addSrv(public)
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
	}
	ctrlAddr := controlAddr
	if deps.controlAddr != "" {
		ctrlAddr = deps.controlAddr
	}
	go func() {
		log.Info("gatewayd control listening", "addr", ctrlAddr)
		errc <- gateway.RunControlServer(ctx, ctrlAddr, controlMux)
	}()

	// Internal unix-socket RPC for schedd's cron dispatch (spec §4.4,
	// M7). Best-effort: if the socket can't bind (e.g. /run/faas
	// doesn't exist on a dev box), log and continue — the public +
	// control listeners are still up.
	if deps.synth != nil {
		if err := deps.synth.Start(); err != nil {
			log.Warn("gatewayd synth listen failed; cron traffic will fail until restart",
				"socket", gatewaydInternalSocket, "err", err)
		}
	}

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		//nolint:contextcheck // shutdown ctx must outlive the cancelled caller ctx (net/http contract).
		// Best-effort shutdown of every listener we may have started.
		// Servers track themselves in `servers`; certmagic owns its renew loop
		// on a separate goroutine — we Close the bundle now that the public +
		// ACME servers have drained. The bundle's Close is a no-op against
		// certmagic v0.25 (no public Stop API) but the seam lets a future
		// upgrade wire real shutdown without touching call sites.
		for _, s := range servers {
			_ = s.Shutdown(shutdownCtx)
		}
		if deps.tlsBundle != nil {
			_ = deps.tlsBundle.Close()
		}
		if deps.synth != nil {
			//nolint:contextcheck // same shutdown-ctx contract as public.Shutdown above.
			_ = deps.synth.Stop(shutdownCtx)
		}
		if deps.nodeCache != nil {
			// Closing every cached *grpc.ClientConn here means
			// in-flight ForwardHTTP RPCs see a "transport closing"
			// error → handler maps it to 502; the listener is
			// already draining so no new requests land.
			_ = deps.nodeCache.Close()
		}
		return nil
	}
}

// unwiredBackend routes nothing; every request 404s until the Postgres routing
// cache and schedd wake path are connected in M5.
type unwiredBackend struct{}

func (unwiredBackend) Lookup(context.Context, string) (gateway.App, bool) {
	return gateway.App{}, false
}
func (unwiredBackend) Target(string) (string, bool)                 { return "", false }
func (unwiredBackend) Wake(context.Context, string) (string, error) { return "", nil }

// envOrGateway returns the value of env key, or fallback when unset/empty.
// Named with the daemon prefix to avoid a collision if two daemons are ever
// linked into the same test binary.
func envOrGateway(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
