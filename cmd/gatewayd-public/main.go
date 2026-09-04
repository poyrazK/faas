// Command gatewayd-public — plain-HTTP edge (Tier A7 split, ADR-070).
//
// gatewayd-public is the box's only public listener. In production
// it sits BEHIND Caddy + Cloudflare (api.gregale.dev) which handle
// TLS termination; this daemon serves plain HTTP on 127.0.0.1:8080
// and reverse-proxies to gatewayd-internal over the unix socket
// /run/faas/gatewayd-internal.sock (or a private TCP target on a split-box
// deployment).
//
// It owns:
//   - The plain-HTTP listener at FAAS_PUBLIC_LISTEN_ADDR
//     (default 127.0.0.1:8080 — loopback only, behind Caddy upstream)
//   - pkg/httpsec outer wrapper (HSTS / CSP nonce / X-Frame-Options /
//     Referrer-Policy / X-Content-Type-Options / Permissions-Policy)
//   - /healthz, /readyz, /metrics on loopback FAAS_PUBLIC_CONTROL_ADDR
//     (default 127.0.0.1:9092 — ADR-070; the legacy gatewayd daemon
//     owns :9090 and must not collide on the same node)
//   - Drain semantics: SIGTERM → flip /readyz → wait in-flight → Shutdown
//
// It does NOT own:
//   - hostname→app routing (gatewayd-internal does)
//   - the wake gate (gatewayd-internal does)
//   - the per-node forwarder (gatewayd-internal does)
//   - the rate limiter (gatewayd-internal does)
//   - TLS termination (Caddy + Cloudflare do, upstream)
//
// Inbound traffic shape:
//
//	customer HTTPS → Caddy (:443, ACME) → 127.0.0.1:8080 here →
//	                                          httpsec outer wrapper
//	                                          → gatewayd-internal over
//	                                            /run/faas/gatewayd-internal.sock
//
// Operators configure gatewayd-public via env overrides only (no
// certmagic, no TOML — TLS is upstream): FAAS_INTERNAL_SOCKET or
// FAAS_INTERNAL_TARGET, FAAS_PUBLIC_LISTEN_ADDR, FAAS_PUBLIC_CONTROL_ADDR.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"github.com/onebox-faas/faas/pkg/capdecl/runtimecheck"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/gateway/drain"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/ratelimit/peraccount"
	"github.com/onebox-faas/faas/pkg/reqbudget"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trace"
	"github.com/onebox-faas/faas/pkg/wire"
)

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
	// defaultListenAddr is the loopback bind for the public listener.
	// Caddy (api.gregale.dev) reverse-proxies here.
	defaultListenAddr = "127.0.0.1:8080"
	// defaultInternalSocket is the unix socket gatewayd-public dials
	// to reach gatewayd-internal; the path lives under faas-vmmd's
	// /run/faas tmpfs (the SOLE RuntimeDirectory=faas).
	defaultInternalSocket = "/run/faas/gatewayd-internal.sock"
	// defaultPublicControlAddr is the loopback control plane
	// (/healthz, /readyz, /metrics). Pinned at :9092 per ADR-070
	// (Tier A7 edge split); the legacy gatewayd daemon owns :9090
	// and must not collide on the same node.
	defaultPublicControlAddr = "127.0.0.1:9092"
)

// gatewaydPublicCapCheck is the DEPLOY-1 / ADR-075 capdecl gate
// seam (review finding M2). It is a package-level var instead of
// a runDeps field because gatewayd-public has a single-file
// `run()` body with no DI struct to extend. Production leaves it
// nil → run() falls back to the live runtimecheck.MustCheckOnBoot
// call against /proc/self/status. Tests override it (typically
// with func() error { return nil }) so the test runner's
// capset doesn't fail the gate. The override is package-scoped,
// not exported, so cmd/gatewayd-public_test.go can swap it within
// a t.Cleanup.
var gatewaydPublicCapCheck func() error

func main() {
	wire.Daemon("gatewayd-public", run)
}

// run is the daemon entry point. It builds the listener stack,
// wires the readiness probe, and blocks on ctx cancellation.
func run(ctx context.Context, log *slog.Logger) error {
	// DEPLOY-1 / ADR-075 capdecl gate. gatewayd-public is
	// unprivileged — no Allow, no Deny. The plain-HTTP loopback
	// listener, the httpsec outer wrapper, the unix-socket
	// reverse-proxy to gatewayd-internal, and the Postgres
	// readiness ping all run inside the unit's systemd hardening
	// (NoNewPrivileges, ProtectSystem, PrivateTmp, etc.). Any
	// future cap_ add lands here, not in the unit file.
	//
	// Note (review finding M2): unlike the runDeps-shaped
	// daemons, gatewayd-public has a single-file `run()` body —
	// there is no test seam struct to extend. The
	// capCheck-style override lives in the cmd-level
	// helper below for tests that want to drive the body
	// without exercising the live /proc/self/status check.
	capCheck := gatewaydPublicCapCheck
	if capCheck == nil {
		capCheck = func() error { return runtimecheck.MustCheckOnBoot(capsDecl, log, nil) }
	}
	if err := capCheck(); err != nil {
		return err
	}

	// Gate-B box-role gate. gatewayd-public is a control-plane
	// daemon — it refuses to start under RoleComputeOnly. The
	// role is set from FAAS_GATEWAYD_PUBLIC_ROLE at deploy time
	// (gatewayd-public has no config.go today; the env is the
	// only source); default is RoleSingleBox so single-box dev
	// boots unmoved. The gate runs before db.Open so a
	// misconfigured boot doesn't waste a Postgres connection.
	if err := role.Require("gatewayd-public", role.FromConfig("", "FAAS_GATEWAYD_PUBLIC_ROLE"),
		role.RoleSingleBox, role.RoleControlPlane); err != nil {
		return err
	}

	// Mega-PR-A (issue #911 / ADR-110 PR-1): capture FAAS_NODE_NAME
	// before any control-plane handshake so the boot log carries the
	// identity. gatewayd-public has no config.go today (env-only);
	// the systemd drop-in (deploy/ansible/roles/gatewayd_public_service/
	// files/faas-gatewayd-public.service.d/99-faas-node-name.conf) is
	// the only source. Empty + log.Info("legacy single-box") mirrors
	// the schedd owner-node line so an operator reading either journal
	// gets the same diagnostic shape.
	if nodeName := os.Getenv("FAAS_NODE_NAME"); nodeName != "" {
		log.Info("gatewayd-public owner node", "node_name", nodeName)
	} else {
		log.Info("gatewayd-public: legacy single-box (FAAS_NODE_NAME unset)")
	}

	log.Info("gatewayd-public: starting", "pid", os.Getpid())

	// Postgres — required for the readiness ping (no other PG
	// dependency: certsync leader election is gone in plain-HTTP
	// mode, the warm-hint mirror is owned by gatewayd-internal).
	pool, err := db.Open(ctx, "")
	if err != nil {
		return fmt.Errorf("gatewayd-public: open db: %w", err)
	}
	defer pool.Close()
	// pgStore is the shared state.Store. gatewayd-public only
	// needs ActiveComputeNodes (for the leader-election adapter
	// in cmd/gatewayd-public/store_adapter.go); the rest of the
	// store's surface is for gatewayd-internal (apps, accounts,
	// sessions). We construct it here so the DNSHandoff wiring
	// has a Store to call into. Mirrors cmd/gatewayd-internal/run.go:366.
	pgStore := state.NewPgStore(pool)
	hstsFlag := runtimeconfig.NewBoolFlag(httpsec.HSTSEnabledFromEnv(hstsEnabledFromEnv))
	httpsec.SetHSTSEnabled(hstsFlag.Load())
	// gatewayd-public owns the outer response headers, so it must consume the
	// same durable HSTS flag as apid and gatewayd-internal. This keeps a hot
	// operator change from producing different security headers at the edge.
	watcher := runtimeconfig.New(pgStore, pool, []string{runtimeconfig.KeyHSTS},
		func(ctx context.Context, key string, value json.RawMessage, _ int64) error {
			enabled, err := runtimeconfig.Bool(value)
			if err != nil {
				return err
			}
			if key == runtimeconfig.KeyHSTS {
				hstsFlag.Store(enabled)
				httpsec.SetHSTSEnabled(enabled)
			}
			return nil
		}, log)
	if err := watcher.Reconcile(ctx); err != nil {
		log.Warn("gatewayd-public: initial runtime config reconcile failed", "err", err)
	}
	go func() {
		if err := watcher.Run(ctx); err != nil && !runtimeconfig.IsContextDone(err) {
			log.Error("gatewayd-public: runtime config watcher exited", "err", err)
		}
	}()

	// Readiness probe. Single signal: PG ping. (certsync/internal-proxy
	// signals are gone in plain-HTTP mode — once both listeners are
	// bound, /readyz=200.)
	probe, pgProbeSig, pgStop := setupReadiness(ctx, pool, log)
	defer pgStop()

	// Tier A8 / ADR-083 wiring (code-review fix #2 + #3 + #5).
	// Built BEFORE the public listener so the in-flight tracker
	// can be hung off the listener's ConnState callback. The DNS
	// handoff subscribes to pg_notify compute_node_changed; the
	// standby warmup probes the local public listener on every
	// tick. Both block on ctx; both return nil on cancel.
	//
	// Sealed-secret seam (fix #3): the package-level
	// OpenBytesDNSProvider var in pkg/gateway starts out as
	// errSecretBoxUnconfigured. main.go reassigns it at startup
	// to a closure that loads host.age identities via
	// secretbox.LoadHostKeys (mirrors cmd/gatewayd-internal's
	// public_auth_unsealer.go). When FAAS_HOST_KEY_PATH is unset
	// the identity list is empty and the closure returns
	// errSecretBoxUnconfigured on first call — the right
	// behaviour (fail loud at the first DNS attempt, never
	// silently no-op).
	if hp := os.Getenv("FAAS_HOST_KEY_PATH"); hp != "" {
		identities, idErr := secretbox.LoadHostKeys(filepath.Dir(hp))
		if idErr != nil || len(identities) == 0 {
			errMsg := "<nil>"
			if idErr != nil {
				errMsg = idErr.Error()
			}
			log.Warn("gatewayd-public: load host identities for DNS token unseal; DNS_PROVIDER namespace unseal will fail at first call",
				"err", errMsg)
		}
		gateway.OpenBytesDNSProvider = func(sealed []byte) ([]byte, error) {
			if len(identities) == 0 {
				return nil, gateway.ErrSecretBoxUnconfigured
			}
			_, plaintext, err := secretbox.OpenBytesMulti(identities, sealed)
			if err != nil {
				return nil, fmt.Errorf("dns_provider unseal: %w", err)
			}
			return plaintext, nil
		}
	}
	inflight := gateway.NewConnStateTracker()

	// Reverse-proxy to gatewayd-internal. Same-box installs keep the unix
	// socket contract. Stable split-box control planes use the database-backed
	// compute gateway pool; FAAS_INTERNAL_TARGET remains a temporary legacy
	// fallback so an old box can converge without an outage.
	internalTarget := strings.TrimSpace(os.Getenv("FAAS_INTERNAL_TARGET"))
	computeDiscovery := strings.TrimSpace(os.Getenv("FAAS_COMPUTE_GATEWAY_DISCOVERY"))
	internalURL := &url.URL{Scheme: "http", Host: "gatewayd-internal"}
	var internalDialer gateway.InternalDialer
	switch {
	case computeDiscovery == "database" && internalTarget == "":
		internalDialer = newComputeGatewayPool(pgStore, log)
		log.Info("gatewayd-public: database-backed compute gateway pool enabled")
	case internalTarget == "":
		internalSocket := envOr("FAAS_INTERNAL_SOCKET", defaultInternalSocket)
		internalDialer = gateway.NewUnixSocketDialer(internalSocket)
	default:
		parsedTarget, parseErr := url.Parse(internalTarget)
		if parseErr != nil || parsedTarget.Scheme != "tcp" || parsedTarget.Host == "" || parsedTarget.Path != "" {
			return fmt.Errorf("gatewayd-public: FAAS_INTERNAL_TARGET must be tcp://host:port, got %q", internalTarget)
		}
		internalURL.Host = parsedTarget.Host
		internalDialer = gateway.NewTCPDialer(parsedTarget.Host)
		log.Info("gatewayd-public: split-box internal target", "target", internalTarget)
	}
	// Issue #675: FAAS_INTERNAL_H2C toggles H2C (HTTP/2 cleartext) on
	// the public→internal hop. The unix SynthServer enables native H2C;
	// the split-box TCP listener uses the ordinary HTTP/1.1 server unless
	// an operator explicitly enables H2C after both ends are configured.
	h2cEnabled := envBoolOr("FAAS_INTERNAL_H2C", internalTarget == "" && computeDiscovery != "database")
	proxy := gateway.NewInternalReverseProxy(
		internalDialer,
		internalURL,
		log,
		h2cEnabled,
	)
	// A dynamic dialer selects a different compute node per request. Do not
	// let the transport pool an idle connection under the single logical
	// gatewayd URL, otherwise a drained node could keep receiving traffic.
	if computeDiscovery == "database" && internalTarget == "" {
		if transport, ok := proxy.Transport.(*http.Transport); ok {
			transport.DisableKeepAlives = true
		}
	}
	// Issue #587 / PR-A: per-request drain tracker shared between
	// the InternalReverseProxy and the control mux so every
	// ServeHTTP surface contributes to the same in-flight count.
	// gatewayd-public has no Handler (the public listener is just
	// a TLS-terminating reverse proxy) so the proxy is the
	// load-bearing entry point here. runDrain below consumes the
	// tracker to bound shutdown by known in-flight requests
	// instead of a hard wall-clock.
	drainTracker := drain.NewTracker()
	proxy.WithInFlightTracker(drainTracker)
	// gatewayMetrics is the gateway.Metrics bundle local to the
	// public daemon. The public daemon also owns wire.OpsMetrics;
	// both registries are mounted on the control mux below so the
	// drain histogram, in-flight gauge, and daemon-level telemetry
	// are available from the same /metrics endpoint.
	gatewayMetrics := gateway.NewMetrics()
	// ADR-127 PR-D: gatewayd-public keeps wire.OpsMetrics in a
	// separate registry from gateway.Metrics. The OTel span series
	// (gatewayd_public_otel_spans_ingested_total,
	// _otel_spans_truncated_total, _otel_auth_failures_total)
	// that need a separate registry so the §12 OTel spans
	// panel can scrape them. The OpsMetrics is mounted on the
	// control mux via ControlMuxWithExtra's extraGatherer
	// alongside the request-budget registry (PR-D code-review
	// #9 — both registries are combined into a
	// prometheus.Gatherers below).
	opsMetrics := wire.NewOpsMetrics("gatewayd_public")
	wire.BootStamps(ctx, "gatewayd-public", opsMetrics)
	wire.RegisterDefaultOps(opsMetrics)
	probe.SetReadyObserver(func(ready bool, reason string) {
		opsMetrics.MarkReady("gatewayd-public", ready, reason)
	})
	// Issue #555 PR-3: mount otelhttp.NewTransport so the outbound
	// request to gatewayd-internal carries the same trace context
	// (gateway.route span). The wrapper sits UNDER the proxy's
	// RoundTripper so the inbound span context is propagated on the
	// outbound hop.
	proxy.Transport = otelhttp.NewTransport(proxy.Transport)

	// ADR-093 / PR-B: end-to-end request budgets. The BudgetMiddleware
	// stamps a per-request deadline onto r.Context() before the proxy
	// forwards to gatewayd-internal, and writes a 504 RFC 7807
	// problem envelope if the budget fires. The metrics registry is
	// a fresh one (gatewayd-public's ControlMux today exposes no
	// default metrics — /metrics is empty unless we wire a
	// registry), and is plumbed into the control mux below so
	// /metrics scrapes both the budget histogram and the
	// exceeded-counter families alongside any future series.
	//
	// ADR-093 amendment: this daemon stamps a BACKSTOP, not a policy.
	//
	// gatewayd-public does not resolve the app, so it cannot see a
	// kind=budget rule. reqbudget derives a CHILD budget from
	// whatever is already on the context, so anything stamped here
	// becomes the parent of every downstream budget. The original
	// PR-B wiring used api.RequestBudgetDefault (3 s) alongside a
	// comment saying budgets "come from the edge-rule kind=budget
	// match (resolved deeper in the chain)" — but the deeper match
	// could then only tighten the 3 s it inherited, never widen it.
	// Measured with a 25 s rule confirmed applied on 713/1000
	// requests, upstream latency still capped at max 3121 ms.
	//
	// Every request this daemon forwards lands on a hop that stamps
	// its own authoritative budget: controlPlaneProxy routes apid
	// paths to apid (api.RequestBudgetApidDefault) and everything
	// else to gatewayd-internal, whose applyEdgeRuleBudget always
	// stamps (rule match, else plan default) and owns the 504 +
	// request_budget_exceeded envelope. So the edge defers to the
	// owner and keeps only the platform ceiling as a liveness guard
	// — a wedged downstream is still cut at RequestBudgetMax, so a
	// public connection can never be pinned indefinitely.
	//
	// This also subsumes the previous sync-invoke DefaultFor
	// carve-out, which returned exactly this value for the same
	// reason (apid owns its plan-aware wait).
	budgetReg := prometheus.NewRegistry()
	budgetMetrics, err := reqbudget.NewMetrics(budgetReg, "gateway")
	if err != nil {
		return fmt.Errorf("gatewayd-public: reqbudget metrics: %w", err)
	}
	budgetCfg, err := newForwardBudgetConfig(budgetMetrics, log)
	if err != nil {
		return fmt.Errorf("gatewayd-public: reqbudget middleware config: %w", err)
	}

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
	controlPlaneTarget := envOr("FAAS_CONTROL_PLANE_API_TARGET", "http://127.0.0.1:8081")
	controlPlaneHandler, err := newControlPlaneProxy(controlPlaneTarget, proxy, log)
	if err != nil {
		return fmt.Errorf("gatewayd-public: control-plane API proxy: %w", err)
	}
	traceMux := http.NewServeMux()
	traceMux.Handle("/v1/traces/", traceSetup.Handler)
	// ADR-127 PR-D: OTLP spans writer handler. Mounted on the
	// same traceMux so it shares the same drain tracker as
	// /v1/traces/ and the control proxy. Gated behind
	// FAAS_OTEL_SPANS_WRITER_ENABLED (default true); the dial
	// of the apid Auth service is only attempted when the
	// handler is enabled, so killing the env var is the
	// fail-closed kill-switch.
	if envOr("FAAS_OTEL_SPANS_WRITER_ENABLED", "true") != "false" {
		authTarget := envOr("FAAS_APID_AUTH_SOCKET", "/run/faas/auth.sock")
		authCli, authErr := apidgrpc.DialAuth(ctx, authTarget, nil)
		if authErr != nil {
			return fmt.Errorf("gatewayd-public: dial apid auth at %q: %w", authTarget, authErr)
		}
		spansAcc := gateway.NewSpansAccumulator()
		otelHandler := gateway.NewOTelSpansHandler(gateway.OTelSpansHandlerConfig{
			AuthClient: authCli,
			Limiter:    peraccount.NewLimiter(),
			Acc:        spansAcc,
			Ops:        opsMetrics,
			Drain:      drainTracker,
		})
		traceMux.Handle("/v1/otel/v1/traces", otelHandler)

		// Stage 4 flush loop — drains the accumulator every
		// FAAS_OTEL_FLUSH_INTERVAL seconds (default 30s) and
		// ships each (trace_id, summary_json, account_id)
		// triple to apid's WriteSpansSummary RPC. The dial
		// is lazy: the loop is wired first, the actual
		// connection happens on the first flush.
		spansWriterTarget := envOr("FAAS_APID_OTEL_SPANS_WRITER_SOCKET", "/run/faas/otel_spans_writer.sock")
		spansWriterCli, spansWriterErr := apidgrpc.DialSpansWriter(ctx, spansWriterTarget, nil)
		if spansWriterErr != nil {
			return fmt.Errorf("gatewayd-public: dial apid spans writer at %q: %w", spansWriterTarget, spansWriterErr)
		}
		flushInterval := 30 * time.Second
		if v := os.Getenv("FAAS_OTEL_FLUSH_INTERVAL"); v != "" {
			if d, parseErr := time.ParseDuration(v); parseErr == nil && d > 0 {
				flushInterval = d
			}
		}
		go func() {
			err := spansAcc.RunFlushLoop(ctx, gateway.FlushLoopConfig{
				Interval: flushInterval,
				WriteFn: func(flushCtx context.Context, traceID string, summaryJSON []byte, accountID string) (string, int64, error) {
					return spansWriterCli.WriteSpansSummary(flushCtx, traceID, summaryJSON, accountID)
				},
				Log: slog.Default(),
				// PR-D code-review #5: per-trace truncation
				// applied at flush time, NOT per-POST at the
				// handler. The closure returns the plan's
				// DebugTelemetrySpansPerTrace ceiling
				// (Hobby=50 / Pro=200 / Scale=1000). The
				// handler already enforces the same cap as a
				// per-POST gate; the flush-time cap is the
				// memory bound the gateway needs across
				// chunked POSTs within one window.
				MaxSpansPerTrace: func(plan string) int {
					return api.MustLimitsFor(api.Plan(plan)).DebugTelemetrySpansPerTrace
				},
				// PR-D code-review #5: fires once per bucket
				// the flush truncated. Drives the §12 metric
				// gatewayd_public_otel_spans_truncated_total.
				OnTruncated: func(traceID string) {
					opsMetrics.IncrementGatewaydPublicOtelSpansTruncated()
				},
			})
			if err != nil {
				log.Error("otel spans flush loop exited", "err", err)
			}
			_ = spansWriterCli.Close()
		}()
	}
	traceMux.Handle("/", controlPlaneHandler)

	// Public-facing handler: httpsec outer wrapper → budget middleware →
	// trace mux → internal proxy.
	// hstsFlag is seeded above and updated by the runtime-config watcher.
	// Issue #555 PR-3: otelhttp.NewHandler extracts W3C traceparent
	// from inbound headers and starts a server span per request; the
	// emitted spans flow into the TraceRing (PR-2) + OTLP exporter.
	// This wrap sits INSIDE httpsec.Security so the response headers
	// (HSTS, CSP nonce, etc.) don't show up as span attributes (the
	// OTel view is the request itself, not the security headers).
	//
	// ADR-093 / PR-B: budget middleware wraps the otel handler so the
	// budget deadline lands on the same ctx the OTel span reads. The
	// budget middleware writes the 504 problem when the inner chain
	// (proxy → gatewayd-internal) doesn't return in time, regardless
	// of where the slowness lives.
	publicHandler := budgetCfg.Middleware(otelhttp.NewHandler(traceMux, "gatewayd-public.handler"))
	publicHandler = httpsec.Static(publicHandler)

	// Control mux + listeners. Combine opsMetrics.Registry() with
	// budgetReg into a prometheus.Gatherers so /metrics exposes
	// BOTH the apid-wires-pr-D counters (otel_spans_*) AND the
	// request-budget histogram + exceeded-counter. The previous
	// wiring passed only opsMetrics.Registry() — budgetReg was
	// constructed at line 318 but never registered, silently
	// dropping gateway_request_budget_* from the scrape (PR-D
	// code-review #9; SLO blindness for the budget tier).
	// Pass drainTracker so every control request is counted
	// during graceful shutdown.
	controlMux := gateway.ControlMuxWithExtra(gatewayMetrics,
		prometheus.Gatherers{opsMetrics.Registry(), budgetReg},
		probe.ReadyFunc(), drainTracker)
	controlAddr := envOr("FAAS_PUBLIC_CONTROL_ADDR", defaultPublicControlAddr)
	listenAddr := envOr("FAAS_PUBLIC_LISTEN_ADDR", defaultListenAddr)
	// Multi-host safety cluster PR-8 (audit F8-A): in multi-host
	// posture (FAAS_NODE_NAME != ""), refuse to boot with the
	// loopback default — operator must explicitly set
	// FAAS_PUBLIC_LISTEN_ADDR / FAAS_PUBLIC_CONTROL_ADDR. Without
	// this gate, gatewayd-public binds 127.0.0.1 on a multi-box
	// node and serves nothing; the LB can't reach it, traffic
	// disappears. PR-9 emits the env vars from the manifest
	// renderer so the box never reaches this error in a
	// correctly bootstrapped fleet; this is the backstop.
	if err := requirePublicBindInMultiHost(); err != nil {
		return fmt.Errorf("gatewayd-public: multi-host bind check failed: %w", err)
	}
	publicSrv, controlSrv := buildServers(listenAddr, controlAddr, publicHandler, controlMux)
	// Tier A8 / ADR-083 (code-review fix #5): hook the public
	// listener's ConnState to the in-flight tracker so the
	// DNSHandoff orchestrator can wait for in-flight to reach
	// zero on drain. The control listener doesn't drain — it
	// only serves /healthz, /readyz, /metrics — so it's not
	// counted.
	publicSrv.ConnState = inflight.ConnState

	// Tier A8 / ADR-083 (code-review fix #2 + #6): start the
	// DNSHandoff subscriber and the StandbyWarmup loop. Both are
	// gated on the FAAS_DNS_PROVIDER and FAAS_STANDBY_WARMUP_ENABLED
	// knobs and both return nil on ctx cancellation. The DNSHandoff
	// is skipped when no store is wired (the dev single-box path).
	if err := startHAComponents(ctx, log, pool, pgStore, inflight, envOr("FAAS_NODE_NAME", ""), envOr("FAAS_NODE_PUBLIC_IP", listenAddr), opsMetrics); err != nil {
		return err
	}

	// Drain orchestration.
	if err := runDrain(ctx, log, publicSrv, controlSrv, pgProbeSig, pgStop, traceSetup, drainTracker, gatewayMetrics); err != nil {
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
	// Single readiness signal: PG ping. /readyz=200 once PG is
	// reachable; 503 (with the PG-ping reason) otherwise.
	_ = log
	return probe, pgProbeSig, pgStop
}

// buildServers constructs the plain-HTTP public + loopback control
// servers. Both pin MaxHeaderBytes to api.DefaultMaxHeaderBytes (1 MiB)
// so a future stdlib default change cannot widen the attack surface
// on this listener; the value mirrors stdlib's historical 1 MiB ceiling.
//
// Knob set (ADR-122 post-merge audit, issue #995 closure):
//
//   - publicSrv (customer-facing TLS edge): RHT=10s + RT=60s + WT=300s +
//     IT=120s + MHB=1 MiB. RT/WT match the customer-facing values from
//     ADR-121 (kept narrower than the metrics variant because
//     customer-facing requests are larger than scrapes). IT=120s
//     matches apid's customer-facing listener at cmd/apid/main.go:452
//     (APIDIdleTimeoutSecondsDefault=120) — closes the keep-alive
//     ceiling that was previously unbounded on the edge.
//   - controlSrv (loopback :9092 healthz/readyz/metrics): the canonical
//     metrics variant (RHT=10s + RT=10s + WT=10s + IT=60s + MHB=1 MiB)
//     from pkg/api/limits.go::Metrics*SecondsDefault. RHT is bumped from
//     the pre-amendment 5s to 10s for consistency with the rest of the
//     canonical surface; the loopback bind keeps the listener under the
//     ADR-122 metrics shape regardless of which routes it grows.
func buildServers(listenAddr, controlAddr string, publicHandler http.Handler, controlMux *http.ServeMux) (*http.Server, *http.Server) {
	publicSrv := &http.Server{
		Addr:              listenAddr,
		Handler:           trace.HTTPHandler("gatewayd-public", publicHandler),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      300 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    api.DefaultMaxHeaderBytes,
	}
	controlSrv := &http.Server{
		Addr:              controlAddr,
		Handler:           trace.HTTPHandler("gatewayd-public-control", controlMux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       time.Duration(api.MetricsReadTimeoutSecondsDefault) * time.Second,
		WriteTimeout:      time.Duration(api.MetricsWriteTimeoutSecondsDefault) * time.Second,
		IdleTimeout:       time.Duration(api.MetricsIdleTimeoutSecondsDefault) * time.Second,
		MaxHeaderBytes:    int(api.DefaultMaxHeaderBytes),
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
func runDrain(ctx context.Context, log *slog.Logger, publicSrv, controlSrv *http.Server, pgProbeSig *gateway.ReadySignal, pgStop func(), traceSetup *gateway.TraceSetup, drainTracker *drain.Tracker, gMetrics *gateway.Metrics) error {
	drainCtx, cancelDrain := context.WithCancel(context.Background())
	defer cancelDrain()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		log.Info("gatewayd-public: SIGTERM received; draining")
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
		log.Info("gatewayd-public: public listening", "addr", publicSrv.Addr)
		if err := publicSrv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
	// Issue #587 / PR-A: wait for the per-request drain tracker
	// to flush BEFORE Shutdown closes the listeners. Done first
	// so we never race a Begin against srv.Shutdown's refusal to
	// accept new connections. Shutdown itself has a 5s grace
	// (next block) which is bounded by drain.DrainGrace
	// below — the two together stay inside systemd's
	// TimeoutStopSec=30s with 5s of headroom for the kernel.
	//
	// Exit-code discipline (systemd Restart=on-failure contract,
	// pkg/deploycontroller/controller.go:43-115):
	//   clean drain → return nil (no restart)
	//   deadline_exceeded / ctx_cancelled → return ctx.Err()
	//     so systemd restarts the daemon
	// Pre-PR-A this path returned nil unconditionally, which hid
	// second-SIGTERM force-exit bugs from operators.
	if drainTracker != nil {
		drainStart := time.Now()
		drainCtxInner, cancelDrainInner := context.WithTimeout(context.WithoutCancel(ctx), drain.DrainGrace)
		outcome, drainErr := drainTracker.Drain(drainCtxInner, drain.DrainGrace)
		cancelDrainInner()
		drainElapsed := time.Since(drainStart).Seconds()
		// Issue #587 / PR-A: record the per-daemon drain
		// histogram on every shutdown so the operator
		// dashboard can spot a pattern of forced exits.
		gMetrics.ObserveDrainWait("gatewayd-public", string(outcome), drainElapsed)
		if drainErr != nil {
			log.Warn("gatewayd-public: drain exited non-clean",
				"outcome", string(outcome),
				"max_inflight", drainTracker.MaxInflight(),
				"err", drainErr)
			return drainErr
		}
		if outcome != drain.OutcomeClean {
			log.Warn("gatewayd-public: drain exited non-clean",
				"outcome", string(outcome),
				"max_inflight", drainTracker.MaxInflight())
			return ctx.Err()
		}
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
	// Flush any in-flight spans from the BatchSpanProcessor.
	// WithoutCancel detaches from the daemon's cancelled ctx so the
	// SDK shutdown can flush its full batch; the 5s upper bound
	// matches the public-server shutdown grace so a slow collector
	// doesn't stall the daemon drain. (Issue #555 review: the
	// previous revision used context.Background(), discarding any
	// deadline the wire.Daemon harness set on ctx.)
	if traceSetup != nil {
		flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		if err := traceSetup.Shutdown(flushCtx); err != nil {
			log.Warn("gatewayd-public: trace shutdown", "err", err)
		}
		cancel()
	}
	return nil
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

// requirePublicBindInMultiHost enforces the multi-host safety
// cluster PR-8 (audit F8-A) invariant: when the operator has
// configured a node name (FAAS_NODE_NAME != "" — the multi-box
// posture signal) AND the listen addr is the loopback default
// the daemon would otherwise bind silently on a multi-box node,
// refuse to boot with a loud error. Single-box installs leave
// FAAS_NODE_NAME unset and the check falls through.
//
// The "explicit env override" branch is the escape hatch: an
// operator who really does want the loopback bind (a node
// behind an external LB, a smoke-test box) sets
// FAAS_PUBLIC_LISTEN_ADDR explicitly. The os.LookupEnv check
// distinguishes "unset → default → would loopback" from
// "explicitly set to loopback" — the latter is allowed.
//
// The companion check for FAAS_PUBLIC_CONTROL_ADDR mirrors this
// shape; the control listener serves /healthz, /readyz, /metrics,
// but a loopback control bind on a multi-box node is also a
// misconfiguration (no operator-side prometheus / kube-probe
// can reach it).
func requirePublicBindInMultiHost() error {
	nodeName := os.Getenv("FAAS_NODE_NAME")
	if nodeName == "" {
		return nil // single-box posture; loopback is correct.
	}

	// Listen addr: distinguish unset (default → loopback) from
	// explicitly set (operator override). The set-to-loopback
	// case is permitted (escape hatch).
	if _, explicit := os.LookupEnv("FAAS_PUBLIC_LISTEN_ADDR"); !explicit {
		// Default would be the loopback bind. Refuse.
		return fmt.Errorf(
			"gatewayd-public: FAAS_NODE_NAME=%q indicates multi-host posture, but "+
				"FAAS_PUBLIC_LISTEN_ADDR is unset and the default %q would loopback on this box. "+
				"Set FAAS_PUBLIC_LISTEN_ADDR to a reachable host:port (e.g. 0.0.0.0:443 or "+
				"the node's public IP) and ensure TLS termination is wired upstream. "+
				"See docs/adr/126-public-bind-multi-host.md",
			nodeName, defaultListenAddr)
	}

	// Control addr: same check. Single-box default loops back
	// to 127.0.0.1:9092 which is correct (the loopback kube-probe
	// path); multi-box demands an explicit bind.
	if _, explicit := os.LookupEnv("FAAS_PUBLIC_CONTROL_ADDR"); !explicit {
		return fmt.Errorf(
			"gatewayd-public: FAAS_NODE_NAME=%q indicates multi-host posture, but "+
				"FAAS_PUBLIC_CONTROL_ADDR is unset and the default %q would loopback on this box. "+
				"Set FAAS_PUBLIC_CONTROL_ADDR to a reachable host:port for kube-probes / "+
				"prometheus scrape. "+
				"See docs/adr/126-public-bind-multi-host.md",
			nodeName, defaultPublicControlAddr)
	}
	return nil
}

// envBoolOr parses common true/false spellings for an env knob.
// Unset (or empty) returns def. Accepts "1"/"true"/"yes"/"on" as true
// and "0"/"false"/"no"/"off" as false (case-insensitive); an
// unrecognised value returns def and emits a one-shot warn (the
// FAAS_LOG_LEVEL precedent — never refuse to start on log/config
// misconfiguration).
func envBoolOr(envKey string, def bool) bool {
	v := os.Getenv(envKey)
	if v == "" {
		return def
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}
