// Command gatewayd — routing + wake + proxy daemon (legacy / Tier A7 split, ADR-070).
//
// This daemon predates the Tier A7 split (gatewayd-public + gatewayd-internal,
// ADR-068). After the split ships, gatewayd-public owns TLS termination +
// ACME :80/:443 + the httpsec outer wrapper, and gatewayd-internal owns
// routing + wake + proxy on the unix socket /run/faas/gatewayd-internal.sock.
// This legacy binary remains in-tree (and on the box) during the migration
// window (cd-controlplane.yml BINARIES="apid schedd gatewayd …" still installs
// it) but is not in FAAS_SERVICES / FAAS_RESTART_ORDER. Operators disable the
// legacy systemd unit once the split pair is healthy.
//
// In the split topology the daemon body moves verbatim into
// cmd/gatewayd-internal/ (PR-A: pure refactor; PR-B: replace the
// placeholder handler); the legacy binary keeps its current shape so
// cd-controlplane installs it byte-for-byte until operators retire it.
//
// Listeners:
//
//	public :8080 plain HTTP   (legacy / e2e harness fallback)
//	private :9090 loopback    /healthz /readyz /metrics
//
// All share ctx cancellation so a SIGTERM shuts them down in parallel.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"filippo.io/age"
	"github.com/caddyserver/certmagic"
	"github.com/jackc/pgx/v5/pgxpool"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"github.com/onebox-faas/faas/pkg/audit"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/capdecl/runtimecheck"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/gateway/drain"
	"github.com/onebox-faas/faas/pkg/gateway/egressgrpc"
	"github.com/onebox-faas/faas/pkg/gateway/egresssink"
	"github.com/onebox-faas/faas/pkg/gateway/writegate"
	"github.com/onebox-faas/faas/pkg/geoip"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/logarchive"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/role"
	schedpkg "github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trace"
	"github.com/onebox-faas/faas/pkg/wire"
)

// scheddSocket is schedd's gRPC unix socket (ADR-018). Phase 2 /
// Gate A: kept for tests and the e2e harness that point gatewayd at
// a single schedd by env override. Production wiring in run()
// ignores scheddSocket and uses the per-node scheddRouter (the
// cache derives each per-node client's dial target from
// compute_nodes.schedd_target_url, not from this var).
var scheddSocket = envOrGateway("FAAS_SCHEDD_SOCKET", "/run/faas/schedd.sock")

// gatewaydInternalSocket is the unix-domain socket schedd dials to
// fire synthetic cron requests through gatewayd (spec §4.4, M7).
// Mode 0660 group `faas` (ADR-015); only schedd can dial. Overridable
// via FAAS_GATEWAY_SYNTH_SOCKET so the e2e harness can place it on a
// per-test path without needing /run/faas on the host (PR #203).
var gatewaydInternalSocket = envOrGateway("FAAS_GATEWAY_SYNTH_SOCKET", "/run/faas/gatewayd-internal.sock")

// publicListenOffSentinel is the value of FAAS_GATEWAY_LISTEN that
// disables the public listener entirely — used by
// faas-gatewayd-internal.service in production (ADR-068 / ADR-070
// Tier A7 split), where the public edge is owned by gatewayd-public
// (which forwards here over the unix socket at
// /run/faas/gatewayd-internal.sock). Without this opt-out, both
// daemons try to bind :8080; gatewayd-internal starts first and
// wins, leaving gatewayd-public crash-looping with "address already
// in use" (run 31121004495).
const publicListenOffSentinel = "off"

// defaultPublicListenAddr is the loopback public listener fallback for
// FAAS_GATEWAY_LISTEN. Production sets FAAS_GATEWAY_LISTEN=off (see
// publicListenOffSentinel) so this default is dev/e2e only — it MUST
// match the PublicAddr default in cmd/gatewayd-internal/config.go (the
// TOML shape). Pulled into a const so goconst doesn't fire on the
// multi-package usage and so the off-sentinel test fixtures reference
// the same source of truth.
const defaultPublicListenAddr = ":8080"

// listenAddr is the public listener (TLS lands here in M8). Overridable via
// FAAS_GATEWAY_LISTEN so the e2e harness can bind a free port without colliding
// with a dev daemon on :8080. Set FAAS_GATEWAY_LISTEN to
// publicListenOffSentinel ("off") to skip the public listener — see
// the const declaration above for the production rationale.
var listenAddr = envOrGateway("FAAS_GATEWAY_LISTEN", defaultPublicListenAddr)

// configPath is the on-disk TOML config. Overridable via FAAS_GATEWAYD_CONFIG
// for non-standard deployments; production uses /etc/faas/gatewayd.toml.
var configPath = envOrGateway("FAAS_GATEWAYD_CONFIG", "/etc/faas/gatewayd.toml")

// geoipDBPath is the on-disk MaxMind-compatible .mmdb file consulted
// by applyEdgeRuleGeo (ADR-091 D21). Override via FAAS_GEOIP_DB_PATH
// for non-standard deployments; production uses /var/lib/faas/geoip/
// dbip-country-lite.mmdb. Empty string = geo kind disabled (the
// reader stays nil and the gate fail-opens).
var geoipDBPath = envOrGateway("FAAS_GEOIP_DB_PATH", "/var/lib/faas/geoip/dbip-country-lite.mmdb")

// geoipAutoRefresh, when "1", spawns the periodic DB-IP download
// goroutine (cadence: 168h/weekly). Default "0" — the operator
// owns the file (rsync'd from a control-plane mirror, mounted via
// ConfigMap, etc.). The auto-refresh path is for environments
// where the daemon is the canonical source of the DB.
var geoipAutoRefresh = envOrGateway("FAAS_GEOIP_AUTO_REFRESH", "0")

// controlAddr is the private control-plane listener — never reachable from
// the internet; bound to the loopback interface by default so an
// operator-prometheus scrape is the only thing that can reach it.
// SECURITY: the env override must stay loopback in production. An
// operator who sets FAAS_GATEWAY_CONTROL_LISTEN to a non-loopback
// address exposes /metrics to the network (the control mux is
// unauthenticated by design — it's intended for a trusted prometheus
// sidecar on the same host). Refuse anything that resolves to a
// non-loopback interface when the env knob is used (PR #218 review
// finding). Overridable via FAAS_GATEWAY_CONTROL_LISTEN so the e2e
// harness can pick a free port (without this, two parallel harness
// runs would race for the hard-coded 127.0.0.1:9090 — the
// deploy_wake_metal test is the only consumer of /metrics and lives
// behind a metal build tag, so CI on plain ubuntu-latest doesn't trip
// the race, but a local dev box with two concurrent invocations
// would). The default matches the legacy constant so existing
// deployments are unaffected.
var controlAddr = envOrGateway("FAAS_GATEWAY_CONTROL_LISTEN", "127.0.0.1:9090")

// streamingEnabledFromEnv is the per-process opt-in for the streaming
// response path (issue #471 / ADR-047). The flag flows into Handler
// end-to-end; PR-B enables the Flusher path. The runtime default is
// false — operators opt in per-cluster via FAAS_GATEWAY_STREAMING.
// cmd/e2e/streaming_metal_test.go flips this on via the harness's
// extraEnv parameter; the metal build tag keeps the streaming test
// off the default unit/e2e lane.
// streamingEnabledTruthy is the closed set of FAAS_GATEWAY_STREAMING
// values that turn the streaming path on. Mirrors the env-tristate
// convention used elsewhere (e.g. faasFlagTruthy in pkg/wire); the
// slice is package-local so the linter's goconst check stays quiet.
const (
	streamingFlagTrue  = "true"
	streamingFlagOne   = "1"
	streamingFlagYes   = "yes"
	streamingFlagFalse = "false"
)

var streamingEnabledTruthy = []string{streamingFlagOne, streamingFlagTrue, streamingFlagYes}

func streamingEnabledFromEnv() bool {
	v := strings.ToLower(strings.TrimSpace(envOrGateway("FAAS_GATEWAY_STREAMING", streamingFlagFalse)))
	for _, t := range streamingEnabledTruthy {
		if v == t {
			return true
		}
	}
	return false
}

// rawStreamEnabledFromEnv (issue #676 / ADR-080 follow-up) resolves the
// FAAS_GATEWAY_RAW_STREAM_ENABLED operator kill switch. Default is true
// — production already runs the raw-bytes Upgrade bridge; defaulting
// to false would silently regress every deployment until operators
// set the env. Accepted truthy values mirror FAAS_GATEWAY_STREAMING
// (1/true/yes, case-insensitive); anything else falls through to
// false (operator must opt out explicitly) and is logged via slog
// at startup so a typo is visible.
//
// When the env resolves to false the handler.WithRawForwarding call
// is skipped below (run.go: ~line 1090), leaving h.rawByNode nil. The
// three-input gate at pkg/gateway/handler.go:2899
// (isUpgradeRequest(r) && app.WebSocketEnabled && h.rawByNode != nil)
// falls through to writeWebSocketNotAllowed with the
// forwarderMissing=true detail — a deterministic 501 with
// x-faas-error-reason: websocket_not_on_plan. The kill switch is
// therefore load-bearing: it is the first operator control to reach
// for when the raw forwarder itself is misbehaving (per-app PATCH
// only helps if the bridge is healthy for OTHER apps on the box).
func rawStreamEnabledFromEnv() bool {
	v := strings.ToLower(strings.TrimSpace(envOrGateway("FAAS_GATEWAY_RAW_STREAM_ENABLED", streamingFlagTrue)))
	for _, t := range streamingEnabledTruthy {
		if v == t {
			return true
		}
	}
	return false
}

// routeMetricsEnabledFromEnv (ADR-093) is the per-process
// opt-in for the per-route observability surface. Mirrors
// streamingEnabledFromEnv above: env (FAAS_GATEWAY_ROUTE_METRICS)
// OR TOML ([route_metrics].enabled) flips the bit, either is
// sufficient. The closed truthy set reuses the streamingFlag*
// constants — same convention, same linter-friendly shape —
// because the four-way {1, true, yes, false} mapping is platform
// convention rather than per-feature. The runtime default is
// false; operators opt in per-cluster after the envelope is
// comfortable. cmd/e2e/per_route_metrics_e2e_test.go flips this
// on via the harness's extraEnv parameter; the metal build tag
// keeps the per-route test off the default unit/e2e lane.
func routeMetricsEnabledFromEnv() bool {
	v := strings.ToLower(strings.TrimSpace(envOrGateway("FAAS_GATEWAY_ROUTE_METRICS", streamingFlagFalse)))
	for _, t := range streamingEnabledTruthy {
		if v == t {
			return true
		}
	}
	return false
}

// certIssuerFor (ADR-100 / issue #879) constructs the per-surface
// cert-remint engine wired to PGBackend. Returns nil when the
// feature flag is off so the pg_notify subscriber no-ops (a
// tenant_surface_changed event still arrives but
// PGBackend.RequestCertForSurface short-circuits on a nil issuer).
// The flag is the same dark-launch switch as the apid HTTP
// surface (PR-C) so a single env var controls the entire
// surface stack — operator sets it on the apid + gatewayd box
// pair when PR-C is ready, unsets it to roll back without
// bouncing any daemon.
//
// metrics may be nil (tests + the pre-construction window before
// deps.metrics is built); ObserveTenantSurfaceCert guards.
//
// PR-D cert-engine-real-mint code review (PR #959 candidate 2):
// production must wire a real *LetsEncryptCertIssuer when the
// engine env config is present (FAAS_TLS_STORAGE_DIR +
// FAAS_TLS_CONTACT_EMAIL + FAAS_TLS_DNS_PROVIDER). Without
// this, every tenant surface lands cert_state=failed with
// "cert engine unwired" last_error. The factory fallback
// mirrors NewCertMagicConfig (tls_wire.go:152-172) so the
// per-host engine and the wildcard path share the same DNS
// dispatch. The token source is supplied via dnsTokenLookup
// (cmd/apid/secrets.go or env-only fallback) so the operator
// stored in a sealed secret per the §11 secrets-at-rest rule.
func certIssuerFor(store state.Store, metrics *gateway.Metrics, enabled bool, dnsTokenLookup func(provider string) string, log *slog.Logger) gateway.CertIssuer {
	if !enabled {
		return nil
	}
	// Wire the LetsEncryptCertIssuer when the env config is
	// present. The wrapper's nil-issuer degradation is the
	// fail-closed path when the operator hasn't finished the
	// cert-engine rollout yet (PR-C dark-launch shape).
	var le *gateway.LetsEncryptCertIssuer
	if api.CertEngineWired() {
		provider := api.CertEngineDNSProvider()
		token := ""
		if dnsTokenLookup != nil {
			token = dnsTokenLookup(provider)
		}
		var dnsProvider certmagic.DNSProvider
		switch provider {
		case api.DNSProviderHetzner:
			dnsProvider = gateway.NewHetznerDNSProvider(token, "")
		case api.DNSProviderCloudflare:
			dnsProvider = gateway.NewCloudflareDNSProvider(token, "")
		}
		// dnsProvider may be nil (operator hasn't supplied a
		// token, or the provider doesn't exist yet). The
		// wrapper's nil-issuer degradation handles the
		// "engine wired but DNS provider missing" case the
		// same way as the "engine not wired" case: log a
		// clear last_error and keep the state machine
		// moving.
		if dnsProvider != nil {
			built, err := gateway.NewLetsEncryptCertIssuer(
				api.CertEngineStorageDir(),
				api.CertEngineContactEmail(),
				api.CertEngineStaging(),
				dnsProvider,
				log,
			)
			if err != nil {
				log.Warn("cert engine: LetsEncryptCertIssuer construction failed; falling back to nil-issuer degradation", "err", err)
			} else {
				le = built
				log.Info("cert engine: wired", "provider", provider, "staging", api.CertEngineStaging(), "storage_dir", api.CertEngineStorageDir())
			}
		} else {
			log.Warn("cert engine: DNS provider missing; falling back to nil-issuer degradation", "provider", provider)
		}
	} else {
		log.Info("cert engine: not wired (FAAS_TLS_STORAGE_DIR + FAAS_TLS_CONTACT_EMAIL unset); nil-issuer degradation engaged")
	}
	// The auditor param is nil today (PR-D commit 6): wiring
	// a real *audit.Auditor requires the gatewayd-internal
	// audit seam that lives outside the scope of this PR
	// (cmd/apid/audit_subscriber.go handles apid-side audit;
	// gatewayd-internal has no equivalent writer yet). The
	// wrapper's emitCertTransition helper is nil-safe so the
	// state-machine audit calls degrade cleanly until the
	// gatewayd-internal audit seam lands.
	return gateway.NewTenantSurfaceCertIssuer(store, metrics, le, nil)
}

// dnsTokenLookupFromEnv resolves the DNS provider's API token
// from the FAAS_TLS_DNS_TOKEN environment variable. The token
// is sealed at rest by the operator's host.age bundle (CLAUDE.md
// §11 secret-at-rest rule); the daemon reads the unsealed form
// from env after the systemd LoadCredential / ansible decryption
// step. The sealed-at-rest refactor (gap G2, spec §17) widens
// this to a sealed-secret lookup once the gatewayd-internal
// audit seam lands (the follow-up ships the same secretbox
// unseal closure cmd/gatewayd-public/main.go uses for the
// wildcard TLS path).
func dnsTokenLookupFromEnv(provider string) string {
	return strings.TrimSpace(os.Getenv("FAAS_TLS_DNS_TOKEN"))
}

// synthAdapter implements gateway.SynthDispatcher on top of the schedd
// gRPC client + the in-process gateway handler. Move 1 widens the
// surface from Wake-only to two methods so the synthetic HTTP envelope
// (method + path + body + headers) actually reaches the runner on
// cron / async / queue-pull / delayed-task paths — the legacy
// wake-only path left cron traffic invisible to the runner and the
// meter (spec §4.4, M7).
type synthAdapter struct {
	backend gateway.Backend
	wake    func(ctx context.Context, appID string) error
	invoke  func(ctx context.Context, appID string, inv state.Invocation) (state.Invocation, error)
	// forward is the same HTTP→vmmd bridge installed on the public
	// gateway handler. Synthetic invocations must use that bridge too;
	// waking an instance without delivering the envelope leaves the row
	// looking completed while the guest never sees a request.
	forward func(gateway.Target) http.Handler
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

// forwardInvocation delivers a synthetic invocation through the same
// per-node vmmd bridge as an ordinary HTTP request and copies the response
// body into Invocation.Result. The scheduler persists that result when it
// completes the invocation row, which is what makes sync invoke and async
// polling return the function's actual response.
func (a *synthAdapter) forwardInvocation(ctx context.Context, target gateway.Target, inv state.Invocation) (state.Invocation, error) {
	if a.forward == nil {
		return inv, fmt.Errorf("gateway synth: invocation forwarder is not wired")
	}

	method := inv.Method
	if method == "" {
		method = http.MethodPost
	}
	path := inv.Path
	if path == "" {
		path = "/"
	}
	if path[0] != '/' {
		path = "/" + path
	}
	req, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(inv.Payload))
	if err != nil {
		return inv, fmt.Errorf("gateway synth: build request: %w", err)
	}
	if len(inv.Headers) > 0 {
		var headers map[string]string
		if err := json.Unmarshal(inv.Headers, &headers); err != nil {
			return inv, fmt.Errorf("gateway synth: decode invocation headers: %w", err)
		}
		for key, value := range headers {
			req.Header.Set(key, value)
		}
	}
	// These headers are platform-owned context. Set them after customer
	// headers so a queued envelope cannot spoof its invocation identity.
	req.Header.Set("x-faas-invocation-id", inv.ID)
	req.Header.Set("x-faas-app-id", inv.AppID)
	req.Header.Set("x-faas-invocation-source", string(inv.Source))
	req.Header.Set("x-faas-instance", target.InstanceID)
	req.Header.Set("x-faas-node", target.NodeID)
	// The synthetic marker is intentionally attached to this derived request
	// context so the internal bridge can preserve platform-owned headers.
	//nolint:contextcheck // gateway.WithSyntheticInvocation inherits req.Context.
	req = req.WithContext(gateway.WithSyntheticInvocation(req.Context()))

	rec := httptest.NewRecorder()
	a.forward(target).ServeHTTP(rec, req)
	if rec.Code == 0 {
		rec.Code = http.StatusOK
	}
	body := rec.Body.Bytes()
	if len(body) > 0 {
		// Function handlers conventionally return JSON. Preserve valid JSON
		// as raw result bytes; wrap plain-text responses so the API remains
		// valid JSON for CLI --json and SDK callers.
		if json.Valid(body) {
			inv.Result = append(json.RawMessage(nil), body...)
		} else {
			encoded, err := json.Marshal(string(body))
			if err != nil {
				return inv, fmt.Errorf("gateway synth: encode response: %w", err)
			}
			inv.Result = encoded
		}
	} else {
		inv.Result = nil
	}
	inv.State = state.InvocationDispatching
	return inv, nil
}

// runDeps is the dependency seam for run. Tests inject net.Listen / http.Server
// wrappers so the seam is fully exercised without spawning a real daemon.
type runDeps struct {
	listen  func(network, addr string) (net.Listener, error)
	newSrv  func(addr string, handler http.Handler) *http.Server
	backend gateway.Backend
	// drain (issue #587 / PR-A) is the per-request WaitGroup-backed
	// drain tracker the graceful-shutdown path waits on. ONE
	// tracker per daemon, shared by Handler + InternalReverseProxy +
	// TraceHandler + control mux + raw-stream forwarder so every
	// ServeHTTP surface contributes to the same in-flight count.
	// Set in runWithDeps after construction; nil in unit tests
	// that don't exercise the shutdown path.
	drain *drain.Tracker
	// streamingEnabled (issue #471 / ADR-047) is the per-process
	// opt-in for the streaming response path. Production passes
	// envOr(TOML) || env("FAAS_GATEWAY_STREAMING"); tests leave it
	// false. The flag also drives the buffered-fallback deprecation
	// log when an SSE-emitting app lands on the legacy buffered path.
	streamingEnabled bool
	// routeMetricsEnabled (ADR-093) is the per-process operator
	// kill-switch for the per-route observability surface. When
	// false (the production default), every Handler.routeSetFor
	// lookup returns nil regardless of the per-app
	// app.RouteMetricsEnabled flag — the customer's per-app flag
	// is inert. The two flags are AND-gated in Handler.ServeHTTP.
	// Production passes env(TOML) || env("FAAS_GATEWAY_ROUTE_METRICS");
	// tests leave it false. Together with the per-app flag this
	// is the two-level opt-in the ADR promises: an operator can
	// disable wholesale on a hot day without a Postgres round-trip,
	// and a customer has to opt in per-app to use any of the
	// surface.
	routeMetricsEnabled bool
	// synth is the internal unix-socket RPC server schedd dials for cron
	// dispatch (spec §4.4, M7). nil in tests; production wires it after
	// the schedd client is dialed.
	synth *gateway.SynthServer
	// egressGRPC is the ADR-046 PR-2 producer channel — a
	// *grpc.Server on a second unix socket dedicated to the egress
	// stream (one unix socket can serve either HTTP or gRPC, not
	// both; the synth socket stays HTTP). nil in tests; production
	// wires it after the Handler + EgressSink are constructed.
	egressGRPC *egressGRPCListener
	// lastSeen flushes per-instance last_request_at to schedd (spec §4.1). nil in
	// tests (the wake/routing path doesn't need it); production wires the
	// schedFlushSink.
	lastSeen gateway.LastSeenSink
	// metrics is the process-local *gateway.Metrics bundle (Prometheus
	// collectors owned by gatewayd — separate from pkg/wire/metrics.go
	// OpsMetrics, which gatewayd does not instantiate). Constructed in run()
	// before the handler is built so the handler + warm-hint consumer +
	// top-N sampler share the same registry. Tests leave it nil; every
	// downstream consumer accepts nil safely.
	metrics *gateway.Metrics
	// opsMetrics is the *wire.OpsMetrics instance — a separate
	// Prometheus registry from `metrics` because it carries
	// daemon-wide labels (`gatewayd_*_total`) that the
	// per-process registry should not. Constructed in run()
	// (line ~635) after pgStore opens; passed into runWithDeps
	// so the Tier A9 standby write-redirect gate (B7) can
	// share the daemon-wide bundle. nil in tests.
	opsMetrics *wire.OpsMetrics
	// pool is the *pgxpool.Pool — the Tier A9
	// LeaderURLPublisher (B4) needs it to subscribe to
	// compute_node_changed via db.SubscribeWithReconnect.
	// nil in tests.
	pool *pgxpool.Pool
	// config (ADR-104 amendment 5, issue #881 Phase 4 C3) is
	// the parsed TOML config — needed by runWithDeps to read
	// the [ratelimit] mode knob and wire the production
	// CentralBackend iff cfg.RateLimit.Mode = "central".
	// nil in tests; defaultDeps leaves it nil so unit tests
	// don't need a config.
	config *Config
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
	// writeTimeout is the http.Server.WriteTimeout override (issue #471 /
	// ADR-047). When 0, the legacy 300 s default (spec §4.1) applies.
	// run() resolves this from cfg.ResponseWriteTimeout || api.ResponseWriteTimeoutDefault
	// so a TOML-only override (production) and a Plan-default fallback
	// (PR-B's per-plan cap lift) both flow into the listener. nil in
	// tests; the test seam injects the bit directly.
	writeTimeout time.Duration
	// readTimeout is the http.Server.ReadTimeout override (issue #995
	// Phase 3 / ADR-121). When 0, gatewayd falls back to
	// api.GatewaydInternalReadTimeoutSecondsDefault (60s) at the
	// public-listener site. The control + unix-socket listener uses
	// a tighter default (30s) set in defaultServer itself.
	readTimeout time.Duration
	// nodeCache holds the per-node *grpc.ClientConn cache plus the
	// compute_node_changed pg_notify subscriber (issue #98 / ADR-028).
	// nil in tests; production wires it after pgStore opens. PR
	// scale-out readiness: also the sink for the heartbeat goroutine
	// that bumps gateway_compute_node_changed_subscriber_alive every
	// subscriberHeartbeatInterval (30s); newNodeCache receives
	// deps.metrics for this purpose.
	nodeCache *nodeCache
	// pgStore is the shared state.Store; used by the githubd proxy
	// for the issue #294 replay dedupe and by the gatewayd audit
	// emitter. nil in tests (the githubdProxy then skips the
	// replay check).
	pgStore *state.PgStore
	// authMw is the pkg/auth.Middleware that powers the AppLogsHandler
	// (issue #254 / Move 4 PR-2). It hosts the bearer / session / MFA
	// / scope checks + the IDOR-safe LoadApp + the shared per-IP
	// AuthLimit bucket. nil in tests → the AppLogsHandler is constructed
	// in non-auth mode (the whitebox tests in cmd/gatewayd-internal/app_logs_test.go
	// drive `serveAppLogs` directly with a stub stream and bypass the
	// chain). ADR-046.
	authMw *authmw.Middleware
	// apiAuthLimiter is the shared per-IP bucket that backs
	// `authMw.RequireLimited`. cmd/apid carries its own instance of
	// the same bucket so the spec §11 10/min/IP rule is per-process
	// (the gatewayd-edge counter is the load-bearing one for
	// internet-facing traffic; apid's bucket is the loopback-only
	// counter for management plane traffic). nil in tests.
	apiAuthLimiter *middleware.Limiter
	// sessions is the *session.Manager that issues + verifies the
	// session cookie. cmd/apid shares the same construct; the two
	// daemons are independent processes so the AEAD keys are loaded
	// separately per daemon. nil in tests.
	sessions *session.Manager
	// hostKeyDir (issue #477 / ADR-079) is the directory
	// secretbox.LoadHostKeys reads from to build the
	// multi-identity rotation-overlap slice for the basic-auth
	// unseal path. Empty = unseal disabled (unit tests + dev
	// boxes that don't have host.age loaded). Resolved from
	// FAAS_HOST_KEY_PATH (the same convention cmd/meterd +
	// cmd/vmmd use); production points at /etc/faas/keys.
	hostKeyDir string
	// audit is the *pkg/audit.Auditor that emits the auth.mfa_gate_hit
	// / auth.session.stolen rows. nil in tests.
	audit *audit.Auditor
	// requireAuthnAdapter (issue #560) wraps deps.authMw.Authn
	// to satisfy pkg/gateway's narrow RequireAuthnAuthenticator
	// interface. nil = authz branch disabled (preserves the
	// pre-issue public-by-default behaviour for unit tests +
	// dev boxes). Production wires this after deps.authMw is
	// constructed; the AppLogsHandler-style carve-out (only
	// when deps.authMw is non-nil) makes the wiring safe.
	requireAuthnAdapter *requireAuthnAdapter
	// requireAuthnAudit is the cmd/gatewayd-scope audit emitter
	// the per-deployment authz branch uses for the
	// instances.authn_* rows. nil = audit-disabled (tests).
	// Distinct from deps.audit (a *pkg/audit.Auditor) because
	// the gateway's narrow interface wants the
	// gatewaydAuditor.Emit(ctx, kind, subject, data) shape,
	// not the apid auditor's wider surface.
	requireAuthnAudit *gatewaydAuditor
	// edgeRulesMatcher (ADR-089 / issue #561 PR 3) is the
	// per-host LRU-backed gateway.EdgeRuleMatcher the handler
	// consults on every request before Backend.Lookup. nil =
	// matcher disabled (default; the handler's
	// matchAndSubstituteRoute short-circuits to false so the
	// legacy host→app lookup path runs unchanged). Production
	// wires a single *gatewaydEdgeRules constructed by
	// newGatewaydEdgeRules in run() so the cache + state.Store
	// loader share the same instance.
	edgeRulesMatcher *gatewaydEdgeRules
	// edgeJWKSAdapter (ADR-091 PR 5) is the JWT verifier handle
	// consulted by applyEdgeRuleJWT. nil = JWT kind disabled
	// (pre-PR-5 + dev posture; matches edgeRulesAudit nil
	// allowance). Production wires a real adapter backed by
	// pkg/edgejwks.NewCache + pkg/edgejwks.NewVerifier.
	edgeJWKSAdapter *edgeJWKSAdapter
	// edgeValidateAdapter (PR-B) is the validate handle consulted
	// by applyEdgeRuleValidate. nil = validate kind disabled
	// (pre-PR-B + dev posture; mirrors edgeJWKSAdapter's
	// nil-allowance). Production wires a real adapter backed by
	// pkg/edgevalidate.NewManager; the loader calls CompileSchema
	// through this adapter on every kind=validate rule at
	// loadHost time, and the applier calls Validate through it
	// on every matched rule.
	edgeValidateAdapter *edgeValidateAdapter
	// edgeRulesAudit (ADR-089 PR 3) is the audit thin wrapper
	// the handler's edge_rule.route_matched / edge_rule.route_blocked
	// rows go through. nil = audit-disabled (matches the
	// WithRequireAuthn audit nil-allowance).
	edgeRulesAudit *gatewaydEdgeRulesAud
	// geoReader (ADR-091 D21) is the pkg/geoip.Reader consulted
	// by applyEdgeRuleGeo. nil = geo kind disabled (pre-PR-7 +
	// file-missing posture; matches the WithEdgeRules nil-allowance).
	// Production wires a real reader backed by the DB-IP Lite
	// .mmdb file at FAAS_GEOIP_DB_PATH; a missing file logs a
	// WARN and the reader stays nil so the daemon boots cleanly.
	geoReader *geoip.Reader
	// geoWatcher (ADR-091 D21) is the periodic refresh goroutine.
	// nil = no auto-refresh (the file is sourced from the operator,
	// not auto-downloaded). Production wires a Watcher with a
	// 168h (weekly) cadence if FAAS_GEOIP_AUTO_REFRESH=1.
	geoWatcher *geoip.Watcher
	// publicAuthCache (issue #477 / ADR-079) is the unsealed
	// basic-auth credential cache shared between the Handler
	// (enforcePublicAuthBasic reads through it) and the
	// PGBackend (InvalidatePublicAuth drops it on
	// db.NotifyKeyChanged). nil = no caching; the basic-auth
	// path unseals per-request. Production wires a single
	// gateway.NewPublicAuthCache() constructed in run().
	publicAuthCache *gateway.PublicAuthCache
	// publicAuthUnsealer (issue #477 / ADR-079) is the
	// gateway.PublicAuthUnsealer the basic-auth branch uses
	// to convert the secretbox-sealed BasicSealed blob into
	// the {username, password} pair. nil = unseal disabled
	// (unit tests + dev boxes that don't load host.age);
	// mode='basic' returns 500 if hit. Production wires
	// the secretbox.OpenMulti closure below.
	publicAuthUnsealer gateway.PublicAuthUnsealer
	// internalSvcVerifier (ADR-119 / issue #477 #4) is the
	// per-service public-key allowlist consulted by
	// applyIngressInternalSvc (handler.go) and
	// SynthServer.applyIngressInternalSvc (synth.go) when an
	// app's public_auth_mode='internal_only'. nil = the gate
	// is disabled — an app in internal_only mode without the
	// verifier wired would 500 (operator_error) per the
	// loud-misconfig posture in pkg/gateway/internal_svc_auth.go.
	// Production wires the env-loaded FAAS_INTERNAL_SVC_PUBKEYS
	// map below; dev boxes + tests leave it nil.
	internalSvcVerifier gateway.InternalSvcVerifier
	// scheddClient is the ScheddClient interface (production: a
	// single *scheddgrpc.Client from the per-node cache) used by
	// AppLogsHandler (issue #254 / Move 4 PR-2) and the warm hint
	// stream consumer. Phase 2 / Gate A: production wires a single
	// client from the per-node cache; multi-stream fan-in is a v1.1
	// follow-up. nil in tests (the AppLogsHandler constructor
	// guards + the carve-out is silently disabled).
	scheddClient scheddgrpc.ScheddClient
	// warmHints is the long-lived StreamWarmHints consumer
	// (ADR-025 axis 4) that drains the sticky-warm affinity
	// stream from schedd and updates the picker's hint cache.
	// nil in tests; production wires it after the schedd client
	// is dialed so the consumer goroutine shares the existing
	// /run/faas/schedd.sock connection (no second dial).
	warmHints *warmHintConsumer
	// egressTLS is the server mTLS config the egress gRPC listener
	// uses when meterd dials it from a remote compute node (ADR-052).
	// nil in tests; production wires it in run() from
	// cfg.LoadEgressTLS() so the [egress_tls_*] TOML keys apply
	// before the listener binds.
	egressTLS *tls.Config
	// scheddRouter (Phase 2 / Gate A) is the per-node schedd gRPC
	// client cache. Wired in run() from newScheddRouter. nil in
	// tests — the legacy single-sched path stays in effect.
	scheddRouter *scheddRouter
	// archiveS3 (issue #562 PR-B) is the S3 client the
	// bucket-proxy read-back handler uses to fetch archived
	// .jsonl.gz objects. Constructed in run() from the same
	// FAAS_LOG_ARCHIVE_* env vars apid uses for its shipper
	// (single source of truth on the box). nil in tests +
	// when FAAS_LOG_ARCHIVE_BUCKET is unset / unseal
	// incomplete; the handler surfaces a 503
	// log_archive_unconfigured in that branch.
	archiveS3 *logarchive.S3Client
	// archiveBucket (issue #562 PR-B) is the destination bucket
	// name the shipper writes into and the read-back handler
	// reads from. Kept separate from archiveS3.Bucket so the
	// archive handler can be tested without rebuilding the
	// client. Empty when archiveS3 is nil.
	archiveBucket string
	// capCheck: DEPLOY-1 / ADR-075 capdecl gate seam (review
	// finding M2). nil → runtimecheck.MustCheckOnBoot(capsDecl,
	// log, nil) which exits on violation in production. Tests
	// inject func() error { return nil } to bypass the live
	// /proc/self/status check (the test runner doesn't carry
	// the production capset).
	capCheck func() error
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
		Handler:           trace.HTTPHandler("gatewayd-internal", handler),
		ReadHeaderTimeout: 10 * time.Second,
		// Issue #995 Phase 3 / ADR-121: tighten the control +
		// unix-socket listener (small, short-lived requests;
		// 30 s is plenty for a control-plane RPC, 60 s idle bounds
		// the keep-alive pool, and 1 MiB header cap keeps a
		// malformed client from widening the surface beyond
		// what gatewayd-public already enforces).
		ReadTimeout:    time.Duration(api.GatewaydInternalControlReadTimeoutSecondsDefault) * time.Second,
		WriteTimeout:   time.Duration(api.GatewaydInternalControlWriteTimeoutSecondsDefault) * time.Second,
		IdleTimeout:    time.Duration(api.GatewaydInternalControlIdleTimeoutSecondsDefault) * time.Second,
		MaxHeaderBytes: int(api.DefaultMaxHeaderBytes),
	}
}

// run is the production body, moved verbatim from cmd/gatewayd-internal/main.go
// during the Tier A7 split (PR-A moved the file, PR-B dropped the
// placeholder that was previously serving TEMPLATE_OK from this
// package; the `prod` prefix was the placeholder-era workaround so
// the two `run` symbols could coexist in `package main`).
func run(ctx context.Context, log *slog.Logger) error {
	pool, err := db.Open(ctx, "")
	if err != nil {
		return fmt.Errorf("gatewayd: open db: %w", err)
	}
	defer pool.Close()

	cfg, err := LoadConfig(configPath)
	if err != nil {
		return err
	}
	// Gate-B box-role gate. gatewayd-internal is a compute-only
	// daemon — it refuses to start under RoleControlPlane. The
	// role is set from TOML or FAAS_GATEWAYD_ROLE at deploy time;
	// default is RoleSingleBox so single-box dev boots unmoved.
	if err := role.Require("gatewayd-internal", cfg.Role, role.RoleSingleBox, role.RoleComputeOnly); err != nil {
		return err
	}
	// Mega-PR-A (issue #911 / ADR-110 PR-1): boot log carrying the
	// multi-box identity. Mirrors schedd/apid/meterd/githubd/
	// gatewayd-public so the playbook shape is identical across
	// daemons.
	if cfg.NodeName != "" {
		log.Info("gatewayd-internal owner node", "node_name", cfg.NodeName)
	} else {
		log.Info("gatewayd-internal: legacy single-box (cfg.NodeName empty)")
	}

	// Phase 2 / Gate A: per-node schedd client cache. Wires the
	// production dial closure (cross-box via pkg/overlay for tcp
	// targets; unix for single-box). The router's WatchNodeChanges
	// goroutine maintains the cache from compute_node_changed
	// pg_notify. The legacy FAAS_SCHEDD_SOCKET dial is still wired
	// below as a fallback for the warm-hint stream consumer and the
	// AppLogsHandler (single-stream consumers; multi-stream fan-in
	// is a v1.1 follow-up).
	pgStore := state.NewPgStore(pool)
	vmmdTLS, err := cfg.LoadVMMDPingTLS()
	if err != nil {
		return fmt.Errorf("gatewayd: load vmmd TLS: %w", err)
	}
	scheddTLS, err := cfg.LoadScheddTLS()
	if err != nil {
		return fmt.Errorf("gatewayd: load schedd TLS: %w", err)
	}
	deps := defaultDeps()
	// Issue #477 / ADR-079: resolve the host key directory the
	// secretbox unsealer reads from. Mirrors the FAAS_HOST_KEY_PATH
	// convention cmd/vmmd + cmd/meterd use (same env var so an
	// operator who rotates host.age on one daemon doesn't have to
	// update a separate knob on every other daemon). Empty →
	// basic-auth unseal disabled (mode='basic' returns 500);
	// open + bearer modes don't touch it.
	//
	// **filepath.Dir convention (foot-gun warning):** the env
	// var points at a FILE (the host.age key), but the unsealer
	// reads a DIRECTORY (LoadHostKeys walks the dir for the
	// rotation-overlap pair). The filepath.Dir() call below
	// strips the file component. Operators MUST set this to a
	// concrete file path (e.g. /etc/faas/keys/host.age), NOT
	// a directory path (/etc/faas/keys/) — the difference is
	// silent because filepath.Dir accepts both. If the
	// convention ever changes, this is the line to revisit:
	// either rename the env var to FAAS_HOST_KEY_DIR (the
	// natural name) and migrate the daemons, or add an
	// explicit "must be a file path, not a directory" check
	// here. For now the convention is documented in the
	// deploy/lima/faas-metal.yaml smoke tests and the
	// README's hostKey section.
	if hp := os.Getenv("FAAS_HOST_KEY_PATH"); hp != "" {
		deps.hostKeyDir = filepath.Dir(hp)
	}
	deps.scheddRouter = newScheddRouter(pgStore, scheddTLS, nil, log)
	go deps.scheddRouter.WatchNodeChanges(ctx, pool, nil)
	// Single-stream fallback: dial the legacy schedd socket once for
	// the consumers that don't currently fan-in (warm hints, log
	// stream). On the single-box default-local fleet this dials the
	// same client the router would resolve; on a multi-box install
	// the stream comes from one schedd only and is documented as
	// such in cmd/gatewayd-internal/warmhints.go.
	sched, err := scheddgrpc.DialContext(ctx, scheddSocket, scheddTLS)
	if err != nil {
		return fmt.Errorf("gatewayd: dial schedd: %w", err)
	}
	defer func() { _ = sched.Close() }()

	// Env-derived AppsDomain wins over the TOML file so the e2e harness can
	// run without writing a TOML. The legacy path is plain :8080 with the
	// suffix filter on; the production path is certmagic + :443/:80.
	appsDomain := os.Getenv("FAAS_APPS_DOMAIN")
	if appsDomain == "" {
		appsDomain = cfg.AppsDomain
	}
	router := pgRouter{store: pgStore, appsSuffix: appsSuffix(appsDomain)}
	// ADR-025 axis 4: sticky-warm affinity cache. Built first so the
	// picker's WarmHintFunc reads from it on every Pick. The cache
	// itself is empty at this point — it gets populated by the
	// warmHintConsumer goroutine below as the schedd stream delivers
	// events. An empty cache degrades to per-node healthyCount
	// scoring, identical to a fresh install (ADR-005 cold boot must
	// always work).
	warmHintCache := gateway.NewWarmHintCache()
	backend := gateway.NewPGBackend(router, sched, log).
		WithWarmHint(warmHintCache.HintFunc()).
		WithAppResolver(func(ctx context.Context, appID string) (gateway.App, bool, error) {
			app, err := pgStore.AppByID(ctx, appID)
			if err != nil {
				if errors.Is(err, state.ErrNotFound) {
					return gateway.App{}, false, nil
				}
				return gateway.App{}, false, err
			}
			acct, err := pgStore.AccountByID(ctx, app.AccountID)
			if err != nil {
				return gateway.App{}, false, err
			}
			return gateway.App{ID: app.ID, AccountID: acct.ID, Plan: acct.Plan, Slug: app.Slug, StreamingEnabled: app.StreamingEnabled, NodeID: app.NodeID, RequireAuthn: app.RequireAuthn, CORSDefaultEnabled: app.CORSDefaultEnabled, CORSDefaultOrigins: app.CORSDefaultOrigins, PublicAuth: gateway.PublicAuthConfig{Mode: app.PublicAuthMode, BasicSealed: app.PublicAuthBasicSealed, IPAllowlist: app.PublicAuthIPAllowlist}, RouteMetricsEnabled: app.RouteMetricsEnabled, MaintenanceMode: app.MaintenanceMode}, true, nil
		}).
		WithLiveTargetLoader(func(ctx context.Context, appID string) ([]gateway.Target, error) {
			instances, err := pgStore.ListInstancesForApp(ctx, appID)
			if err != nil {
				return nil, err
			}
			targets := make([]gateway.Target, 0, len(instances))
			for _, instance := range instances {
				if instance.State != string(state.StateRunning) || instance.ID == "" || instance.NodeID == "" {
					continue
				}
				targets = append(targets, gateway.Target{
					InstanceID:   instance.ID,
					NodeID:       instance.NodeID,
					WakeID:       instance.WakeID,
					DeploymentID: instance.DeploymentID,
				})
			}
			return targets, nil
		}).
		WithClientForApp(func(ctx context.Context, app gateway.App) (gateway.Scheduler, bool, error) {
			full, err := pgStore.AppByID(ctx, app.ID)
			if err != nil {
				return nil, false, err
			}
			cli, err := deps.scheddRouter.ScheddForApp(ctx, full)
			if err != nil {
				return nil, false, err
			}
			return cli, true, nil
		}).
		WithPublicAuthCache(deps.publicAuthCache).
		// ADR-089 / issue #561 PR 3: arm the per-host edge-rule
		// matcher the invalidator resets on db.NotifyEdgeRuleChanged.
		// Nil-safe; production always wires a real *gatewaydEdgeRules
		// in run() above.
		WithEdgeRules(deps.edgeRulesMatcher).
		// Issue #556 / PR-B: wire the per-deployment weight
		// store so a deployment_changed notify refreshes the
		// picker's weight table. The adapter translates
		// state.Deployment to gateway.DeploymentWeightsRow
		// (the gateway package does not import pkg/state).
		WithStore(weightsStoreAdapter{store: pgStore}).
		// ADR-100 / issue #879: arm the per-surface cert-remint
		// engine so a tenant_surface_changed notification
		// re-mints the SAN-aggregated cert for the affected
		// surface. The engine is gated on
		// FAAS_TENANT_SURFACES_ENABLED (same dark-launch
		// switch as the apid HTTP surface, PR-C) so a
		// misconfigured rollout can be reverted by unsetting
		// the env var without bouncing the daemon. The
		// issuer is re-armed via WithCertIssuer below
		// (after deps.metrics is built) so the
		// gateway_tenant_surface_cert_total counter ticks
		// from boot.
		WithCertIssuer(certIssuerFor(pgStore, nil, api.TenantSurfacesEnabled(), dnsTokenLookupFromEnv, log))

	// Phase 2 / Gate A: gate the resolveSched legacy fallback on the
	// active fleet. Single-box posture (only default-local active)
	// keeps the legacy fallback — every app is owned by the local
	// schedd, so a transient resolver miss is harmless. Multi-box
	// posture (any non-default-local active row) DISABLES the
	// fallback — b.sched is the legacy default-local dial and would
	// return FailedPrecondition for any foreign-owned app on transient
	// miss, surfacing as a 503 storm (PR #509 review finding F1).
	//
	// The posture check runs once at startup; subsequent compute_node
	// changes that flip the posture are out of scope (an operator
	// adding a second box restarts gatewayd alongside schedd).
	if nodes, err := pgStore.ActiveComputeNodes(ctx); err == nil {
		log.Info("gatewayd: schedd posture", "active_nodes", len(nodes))
		// Multi-host safety cluster PR-7 (audit F5): the legacy
		// single-box fallback is REMOVED. The backend always uses
		// the per-node schedd router; the resolveSched fallback
		// path is dead code. We keep the ActiveComputeNodes probe
		// so the boot log still surfaces the fleet posture, but
		// no longer use it to toggle any per-instance gate.
	} else {
		log.Warn("gatewayd: schedd posture probe failed; legacy fallback no longer exists", "err", err)
	}

	// Keep the routing + target caches fresh from apid/schedd's pg_notify
	// stream (spec §4.1): an instance state change evicts the app's cached
	// target so the next request re-resolves via an idempotent wake; an app or
	// domain change flushes the host→app routes.
	go watchInvalidations(ctx, pool, backend, log)

	deps.backend = backend
	// Flush per-instance last_request_at to schedd so its idle reaper sees
	// gateway traffic (spec §4.1, ADR-018) — without this a busy app parks once
	// its idle timer fires. schedd is the sole writer to `instances`, so we hand
	// it the batch over gRPC (the same client we wake through). Issue #168
	// dropped the addr→instance resolver hop: the handler now Touches by
	// instance_id directly, and schedd drops unknown ids on its side.
	// Phase 2 / Gate A: the flush is partitioned by instance.node_id
	// via the per-node router so touches land on the owner schedd.
	deps.lastSeen = newSchedFlushSink(deps.scheddRouter, pgStore, log)
	// Internal unix-socket RPC for schedd's cron dispatch loop (spec §4.4,
	// M7). Routes a synthetic wake through schedd so metering + the
	// per-minute sampler see the live instance. lastSeen-touches for cron
	// traffic land in a follow-up once we expose an app-scoped touch RPC
	// (today schedd's ReportActivity takes instance_ids, which the synth
	// path doesn't have without a Wake first). Phase 2 / Gate A: synth
	// calls resolve the owner schedd per app via the router — they no
	// longer dial the legacy single schedd.
	var synth *synthAdapter
	synth = &synthAdapter{
		backend: backend,
		wake: func(ctx context.Context, appID string) error {
			// wake_id is discarded on the synth path (gaps analysis
			// 2026-07-23): synthesized requests don't return a
			// response header to a customer, so x-faas-wake-id has
			// no consumer here. AdmitInstance is still called for
			// the admit + boot side effects.
			//
			// method is discarded for the same reason: the
			// wake-locality counter is observed at the public
			// handler's after-proxy chokepoint, not on the synth
			// path. Synth traffic is operational (cron, per-minute
			// sampler), not customer traffic, and would skew the
			// "what fraction of admissions were local" reading.
			//
			// Phase 2 / Gate A: resolve the owner schedd via the
			// per-node router so the cron path lands on the right
			// box.
			app, err := pgStore.AppByID(ctx, appID)
			if err != nil {
				return err
			}
			cli, err := deps.scheddRouter.ScheddForApp(ctx, app)
			if err != nil {
				return err
			}
			instanceID, nodeID, deploymentID, wakeID, _, atCapacity, port, err := cli.AdmitInstance(ctx, appID, "", "", schedpkg.TriggerGateway)
			if err == nil && !atCapacity {
				backend.RecordTarget(appID, gateway.Target{
					InstanceID:   instanceID,
					NodeID:       nodeID,
					DeploymentID: deploymentID,
					WakeID:       wakeID,
					Port:         port,
				})
			}
			return err
		},
		// Wake the instance, then deliver the synthetic envelope through
		// the same HTTP→vmmd bridge used by public traffic. The live
		// instance handle is echoed back on the returned Invocation so
		// schedd can StampInstanceInvocation; without it the meter's
		// per-instance count lands on 0.
		invoke: func(ctx context.Context, appID string, inv state.Invocation) (state.Invocation, error) {
			app, err := pgStore.AppByID(ctx, appID)
			if err != nil {
				return inv, fmt.Errorf("synth invoke resolve app %s: %w", appID, err)
			}
			cli, err := deps.scheddRouter.ScheddForApp(ctx, app)
			if err != nil {
				return inv, fmt.Errorf("synth invoke resolve schedd %s: %w", appID, err)
			}
			instanceID, nodeID, deploymentID, wakeID, port, err := cli.Wake(ctx, appID, "", "")
			if err != nil {
				return inv, fmt.Errorf("synth invoke wake %s: %w", appID, err)
			}
			target := gateway.Target{
				InstanceID:   instanceID,
				NodeID:       nodeID,
				DeploymentID: deploymentID,
				WakeID:       wakeID,
				Port:         port,
			}
			backend.RecordTarget(appID, target)
			inv.InstanceID = instanceID
			return synth.forwardInvocation(ctx, target, inv)
		},
	}
	// Construct the SynthServer before wiring the gate — the
	// WithInternalSvcVerifier / WithMetrics / WithAudit /
	// WithAppModeLookup chain below mutates deps.synth in place.
	// Round-4 rebase: the previous round had this on a separate
	// line in main; the merge put it on the closing-brace line
	// of the synthAdapter struct literal, which broke the compile.
	// Restoring the off-the-brace call keeps both sides readable.
	deps.synth = gateway.NewSynthServer(gatewaydInternalSocket, synth, log)
	// ADR-119 — wire the synth-side gate. The same per-service
	// public-key allowlist (deps.internalSvcVerifier) gates
	// /v1/synthesize so a forged cron request cannot wake an
	// internal_only app (synth bypasses Handler.ServeHTTP).
	// Same Metrics + same audit emitter + same per-app cache
	// the HTTP-front-door gate uses — single source of truth.
	// appPublicAuthMode is consulted via the per-app cache
	// populated by the same hydration path Handler.PublicAuthConfig
	// reads; nil-safe (nil lookup = every app treated as "open").
	deps.synth.WithInternalSvcVerifier(deps.internalSvcVerifier)
	deps.synth.WithMetrics(deps.metrics).
		WithAudit(deps.requireAuthnAudit.Emit).
		WithAppModeLookup(func(ctx context.Context, appID string) string {
			// ADR-119 — per-app mode lookup for the synth-side
			// gate. Reads from the per-app cache hydrated by
			// the same path Handler.PublicAuthConfig reads.
			// A cache miss returns "" which the gate treats
			// as "open" (no JWT required). Returns "" on error
			// so a transient pg failure doesn't 500 every
			// internal_only cron fire. Round-3 golangci-lint
			// contextcheck: now uses the inbound request's ctx
			// (with timeout / cancel chain) instead of a
			// fresh context.Background().
			app, err := pgStore.AppByID(ctx, appID)
			if err != nil {
				return ""
			}
			return app.PublicAuthMode
		})
	// ADR-123 — wire the synth-side members_only gate. Same
	// checker + same cookie principal resolver the Handler
	// uses; the appOrgIDLookup closure reads the per-app
	// org_id the same way the per-host LRU cache on the
	// HTTP-front-door side does (via Store.AppOrgID).
	// Returns "" on error so a transient pg failure doesn't
	// 500 every members_only cron fire (the gate's
	// empty-orgid misconfig branch is reserved for the
	// "we know the app is in members_only mode AND the row
	// is broken" case — that's an operator-misconfig
	// surface, not a transient-failure surface).
	// F1 (review fix): inline the checker + adapter at the
	// synth wiring site so the SynthServer fields are NEVER
	// nil in production. The previous code assigned to
	// deps.membersOnlyChecker AFTER the synth wiring (at
	// the bottom of run()), so the WithMembersOnlyChecker
	// call captured the zero value. Every cron-fired
	// /v1/synthesize against a members_only app would have
	// 500'd with operator_error. Now: inlined.
	deps.synth.WithMembersOnlyChecker(authz.PoolOrgMemberChecker(pool))
	deps.synth.WithMembersOnlyPrincipalExtractor(newAuthPrincipalAdapter())
	deps.synth.WithAppOrgIDLookup(func(ctx context.Context, appID string) string {
		orgID, err := pgStore.AppOrgID(ctx, appID)
		if err != nil {
			return ""
		}
		return orgID
	})
	// Process-local Prometheus registry (spec §12). Constructed here so
	// every downstream consumer — handler, warm-hint consumer, top-N
	// sampler — shares the same registry. The registry is exposed via
	// :9090/metrics by gateway.RunControlServer; the gate/handler pick
	// it up via deps.metrics in runWithDeps.
	//
	// ADR-068 / Tier A7 split: TLS termination moved to gatewayd-public
	// (certmagic, httpsec, :443/:80 ACME mux). This daemon stays
	// plain HTTP on :8080; the resolved-TLS branch was removed in PR-A.
	deps.metrics = gateway.NewMetrics()

	// ADR-100 / issue #879: re-arm the per-surface cert-remint
	// engine with the now-built metrics registry so
	// gateway_tenant_surface_cert_total{result,kind} ticks from
	// boot. The earlier WithCertIssuer call (in the chained
	// builder above) ran with a nil metrics pointer because
	// deps.metrics didn't exist yet; ObserveTenantSurfaceCert
	// guards on nil but skipping the increment in production
	// made the dashboard panel observability-dead. The feature
	// flag is the same env var (PR-C dark-launch switch).
	backend.WithCertIssuer(certIssuerFor(pgStore, deps.metrics, api.TenantSurfacesEnabled(), dnsTokenLookupFromEnv, log))

	// PR-D commit 3: cert-renewer goroutine. Periodically
	// re-mints tenant surfaces whose cert_not_after < now +
	// CertRenewBeforeNotAfterDays. The renewer rides the
	// existing pg_notify pipeline (TouchTenantSurfaceForRenewal
	// bumps updated_at → trigger fires → subscriber dispatches
	// to CertIssuer.RequestCertForSurface), so the state
	// machine stays single-writer.
	//
	// Skipped when the tenant-surfaces feature flag is off —
	// the renewer would loop on a closed cartesian and emit
	// noisy "0 due" log lines every tick.
	if api.TenantSurfacesEnabled() {
		renewer := gateway.NewSurfaceCertRenewer(pgStore, log)
		go renewer.Run(ctx)
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
	// (vmmdTLS was loaded earlier in run() — see the scheddRouter
	// wiring above. The same TLS bundle is shared with the vmmd
	// node cache.)
	// ADR-052: load the server mTLS material the egress gRPC
	// listener uses when meterd dials it from a remote compute
	// node. Empty cluster returns (nil, nil); partial cluster is
	// rejected with the egress_tls_* field names.
	deps.egressTLS, err = cfg.LoadEgressTLS()
	if err != nil {
		return fmt.Errorf("gatewayd: load egress TLS: %w", err)
	}
	// PR scale-out readiness: thread *gateway.Metrics into the node
	// cache so the WatchEvictions heartbeat goroutine has a sink for
	// gateway_compute_node_changed_subscriber_alive. deps.metrics is
	// always non-nil after NewMetrics (cmd/gatewayd-internal/main.go:284).
	//
	// issue #517 / PR-C / ADR-064: thread the events Platform into
	// the node cache so the forwarder emits wake.proxy_first_byte
	// on the first downstream byte. The Platform writes the events
	// row + bumps wake_phase_emitted_total{wake.proxy_first_byte}.
	// We do NOT pass a Broadcaster — gatewayd is the public
	// listener (CLAUDE.md ownership), not an SSE fan-out source.
	gatewayOps := wire.NewOpsMetrics("gatewayd")
	wire.BootStamps(ctx, "gatewayd", gatewayOps)
	wire.RegisterDefaultOps(gatewayOps)
	eventsPlatform := events.NewPlatform("gatewayd", pgStore, log, gatewayOps, nil)
	deps.nodeCache = newNodeCache(pgStore, vmmdTLS, log, deps.metrics).WithEvents(eventsPlatform)
	// Synthetic invocations share the same per-node HTTP→vmmd bridge as
	// public requests. This assignment happens after nodeCache creation so
	// the cache has its production mTLS/overlay wiring before schedd can
	// dispatch a cron, async, queue, or delayed-task envelope.
	synth.forward = deps.nodeCache.Forwarding()
	go deps.nodeCache.WatchEvictions(ctx, pool)
	// Issue #587 / PR-A: the raw-stream forwarder needs the drain
	// tracker so the hijacked Upgrade pump is held in the in-flight
	// count. Wired BEFORE the handler sets up WithRawForwarding
	// below so the factory closure captures the tracker.
	// nodeCache.WithDrainTracker is a no-op when deps.drain is
	// nil; see cmd/gatewayd-internal/nodecache.go for the seam.
	deps.pgStore = pgStore
	// Tier A9 / ADR-084 (PR-B sub-task B7): expose the
	// daemon-wide *wire.OpsMetrics to runWithDeps so the
	// standby write-redirect gate can increment the
	// gatewayd_internal_write_redirect_total counter
	// alongside the other daemons' bundles. The pool is
	// likewise needed by the gate's LeaderURLPublisher
	// (B4) — subscribe to compute_node_changed.
	deps.opsMetrics = gatewayOps
	deps.pool = pool
	// ADR-104 amendment 5 / issue #881 Phase 4 C3: hand the
	// parsed TOML config to runDeps so runWithDeps can read
	// the [ratelimit] mode knob and wire the production
	// CentralBackend iff cfg.RateLimit.Mode = "central".
	deps.config = cfg
	// Issue #471 / ADR-047 (PR-A): merge the per-process streaming
	// opt-in from TOML + env. Either source flips the bit. Production
	// default is false (no streaming — the legacy buffered path).
	// Tests override by setting one of the two knobs.
	deps.streamingEnabled = cfg.StreamingEnabled || streamingEnabledFromEnv()
	// ADR-093: operator kill-switch for the per-route observability
	// surface. Mirrors the streamingEnabled merge above — TOML or
	// FAAS_GATEWAY_ROUTE_METRICS flips the bit, either is sufficient.
	// The per-app flag (apps.route_metrics_enabled) is AND-gated against
	// this in Handler.routeSetFor, so flipping the kill-switch at process
	// level (via env or systemd drop-in) takes the surface offline
	// without a Postgres round-trip or a pg_notify fan-out — the
	// "operator can disable wholesale on a hot day" property the
	// two-level design promises.
	deps.routeMetricsEnabled = cfg.RouteMetricsEnabled || routeMetricsEnabledFromEnv()
	// Issue #471 / ADR-047 (PR-A): resolve the http.Server.WriteTimeout.
	// Spec §4.1 baseline is 300 s; the TOML [response_write_timeout]
	// key overrides per-cluster. PR-B lifts the per-plan cap on
	// Hobby+ to 900 s; that path flips deps.writeTimeout to the
	// per-app app.Plan.ResponseWriteTimeout() at request-init time
	// (http.Server.WriteTimeout is global, so the per-app lift needs
	// per-request bookkeeping via http.ResponseController — out of
	// scope here). 0 means "spec default"; runWithDeps fills it in.
	if cfg.ResponseWriteTimeout > 0 {
		deps.writeTimeout = cfg.ResponseWriteTimeout
	}
	// Issue #995 Phase 3 / ADR-121: resolve the http.Server.ReadTimeout
	// override (matches the ResponseWriteTimeout resolver above).
	// 0 means "use api.GatewaydInternalReadTimeoutSecondsDefault".
	if cfg.RequestReadTimeout > 0 {
		deps.readTimeout = cfg.RequestReadTimeout
	}
	// Issue #254 / Move 4 PR-2: pkg/auth.Middleware construction.
	// AppLogsHandler (cmd/gatewayd-internal/app_logs.go) shares the auth chain
	// with cmd/apid via ADR-046 — same bearer / session / MFA / scope
	// / IDOR-safe LoadApp, same per-IP AuthLimit bucket family. The
	// session manager is loaded from FAAS_SESSION_KEY (see
	// session_key.go); the auditor is the same pkg/audit.Auditor
	// gatewayd already uses for githubd replay-dedupe audit rows
	// (cmd/gatewayd-internal/audit.go). nil-safe when the env is unset so
	// the e2e harness + these dev-mode paths still boot.
	sessions := loadSessionManager(osGetenv, log)
	if sessions == nil {
		return fmt.Errorf("gatewayd: session manager init failed")
	}
	deps.sessions = sessions
	deps.apiAuthLimiter = middleware.NewLimiter(middleware.AuthLimitConfig{Log: log})
	deps.audit = audit.New(pgStore, log, nil, "gatewayd")
	deps.authMw = authmw.New(
		storeAsAuthenticator(pgStore),
		sessions,
		storeAsSessionLookup(pgStore),
		auditorAsAuthAuditor(deps.audit),
		log,
		deps.apiAuthLimiter,
		// gatewayd-internal has no need for the binding-hash
		// cross-check (it's a Unix-socket-only daemon in the
		// ADR-070 split; the cookie branch it serves — public
		// requests that come back through gatewayd-public — has
		// already been validated upstream at the public edge).
		// nil ⇒ the cross-check is a no-op.
		nil,
	)
	// Issue #560: per-deployment require_authn (pro/Scale opt-in).
	// Build the narrow gateway.RequireAuthnAuthenticator
	// adapter around the production middleware and the
	// gatewayd-scope audit emitter used by every other gated
	// path. Both are nil-safe on the pkg/gateway side so a
	// future operator env-override can disable the chain
	// without code changes — for now production always wires
	// them when deps.authMw is non-nil (which it always is
	// outside unit tests).
	deps.requireAuthnAdapter = newRequireAuthnAdapter(deps.authMw)
	deps.requireAuthnAudit = newGatewaydAuditor(deps.pgStore, log)
	// ADR-089 / issue #561 PR 3 — build the edge-rule matcher
	// next to the requireAuthn chain so the per-host cache +
	// audit thin wrapper share the same auditor. The matcher
	// reads state.EdgeRule via the store; reset on
	// db.NotifyEdgeRuleChanged is wired via PGBackend.WithEdgeRules
	// below.
	deps.edgeRulesMatcher = newGatewaydEdgeRules(pgStore, log, deps.edgeValidateAdapter, deps.metrics)
	deps.edgeRulesAudit = newGatewaydEdgeRulesAud(newGatewaydAuditor(deps.pgStore, log))
	// ADR-091 D21 — build the pkg/geoip.Reader backed by the
	// DB-IP Lite .mmdb file at FAAS_GEOIP_DB_PATH. The Reader
	// is nil-safe: a missing file logs a WARN and the reader
	// stays nil so the daemon boots cleanly (the gate fail-opens
	// at request time). The watcher is optional and only
	// spawned when FAAS_GEOIP_AUTO_REFRESH=1 — the default is
	// operator-owned file (rsync / ConfigMap / volume mount).
	if geoipDBPath != "" {
		reader, gerr := geoip.Open(geoipDBPath, geoip.SourceDBIP, geoip.DBIPAttribution, log)
		if gerr != nil {
			log.Warn("geoip: open failed; geo kind disabled (fail-open)",
				"path", geoipDBPath,
				"err", gerr)
		} else {
			deps.geoReader = reader
			log.Info("geoip: loaded",
				"path", geoipDBPath,
				"source", string(geoip.SourceDBIP),
				"attribution", geoip.DBIPAttribution,
			)
			if geoipAutoRefresh == "1" {
				// 168h = 7 days. DB-IP publishes a monthly
				// snapshot; the weekly cadence is a safety
				// margin and tolerates a few days of drift.
				watcher, werr := geoip.NewWatcher(reader, 168*time.Hour, log)
				if werr != nil {
					log.Warn("geoip: watcher init failed; continuing without auto-refresh",
						"err", werr)
				} else {
					deps.geoWatcher = watcher
					// Best-effort fetch on boot so the
					// first request doesn't hit a stale
					// DB. Cancellable via the daemon's
					// shutdown context.
					if berr := watcher.WatcherOnce(ctx); berr != nil {
						log.Warn("geoip: boot refresh failed; continuing with the on-disk file",
							"err", berr)
					}
					watcher.Start(ctx)
				}
			}
		}
	}
	// Issue #561 / ADR-091 PR 5 — build the per-URL JWKS cache
	// + JWT verifier that applyEdgeRuleJWT consults. Lazy
	// registration on first match; the cache uses an HTTP client
	// with a 5s fetch timeout so an IdP outage can't block the
	// gateway hot path.
	deps.edgeJWKSAdapter = newEdgeJWKSAdapter(log)
	// PR-B — build the kind=validate adapter backed by
	// pkg/edgevalidate.NewManager (sha256-keyed LRU + Draft
	// 2020-12 compile + JSON-Schema validate). The loader
	// (loadHost) calls CompileSchema through this adapter for
	// every kind=validate rule; the applier (handler.go) calls
	// Validate through it on every matched rule.
	deps.edgeValidateAdapter = newEdgeValidateAdapter(log)
	// Issue #477 / ADR-079: build the unsealed basic-auth
	// credential cache + the secretbox unsealer closure.
	// The cache is shared between the Handler (read path)
	// and the PGBackend (invalidation path on
	// db.NotifyKeyChanged). The unsealer closes over the
	// loaded host identities (the same secretbox.OpenMulti
	// path cmd/apid uses) so the gateway hot path doesn't
	// import pkg/secretbox directly. hostKeyDir follows
	// the FAAS_HOST_KEY_PATH convention (mirrors
	// cmd/meterd/main.go:983) — the same identities slice
	// the apid + meterd daemons load.
	deps.publicAuthCache = gateway.NewPublicAuthCache()
	if deps.hostKeyDir != "" {
		if identities, loadErr := secretbox.LoadHostKeys(deps.hostKeyDir); loadErr != nil {
			log.Warn("gatewayd-internal: LoadHostKeys (rotation overlap) failed; basic-auth will be unseal-disabled until next boot",
				"dir", deps.hostKeyDir, "err", loadErr.Error())
		} else {
			loader := func() []*age.X25519Identity { return identities }
			deps.publicAuthUnsealer = newPublicAuthUnsealer(loader)
			if len(identities) > 1 {
				log.Info("gatewayd-internal: rotation overlap active — basic-auth unseals across current + previous host.age")
			}
		}
	}
// PR-3 / ADR-125 fleet-wide signing key: try the
	// cluster_signing_keys PG row FIRST so a JWT minted by
	// schedd on box A is verifiable by gatewayd-internal on
	// box B (audit F1+F20). The per-env path below is the
	// fallback for the operator-migration window (single-box
	// dev + legacy installs without `hostage-gen cluster-init`).
	rotatingVerifier := &rotatingVerifier{}
	clusterVerifier, clusterErr := loadClusterInternalSvcVerifier(ctx, pgStore)
	switch {
	case clusterErr == nil:
		if initErr := rotatingVerifier.initial(clusterVerifier); initErr != nil {
			return fmt.Errorf("gatewayd-internal: prime rotating verifier with cluster key: %w", initErr)
		}
		deps.internalSvcVerifier = rotatingVerifier
		log.Info("gatewayd-internal: internal_only verifier loaded (cluster_signing_keys)",
			"source", "cluster_signing_keys")
		// Subscribe to rotations so a cluster-init refresh lands
		// without daemon restart. Best-effort — if subscribe
		// fails the boot-time key keeps working.
		if subErr := SubscribeClusterVerifierChanges(ctx, pool, pgStore, rotatingVerifier, log); subErr != nil {
			log.Warn("gatewayd-internal: cluster verifier rotation subscribe failed; verifier frozen at boot key",
				"err", subErr.Error())
		}
	case errors.Is(clusterErr, ErrClusterVerifierUnavailable):
		log.Info("gatewayd-internal: cluster_signing_keys row missing; falling back to FAAS_INTERNAL_SVC_PUBKEYS env path")
	default:
		// Hard error — DB unreachable, parse error on
		// public_key_pem. Fall back to env path; log loudly so
		// the operator can investigate. We do NOT fail boot —
		// the env fallback is the operator-migration path and
		// must keep the box reachable.
		log.Warn("gatewayd-internal: cluster verifier load failed; falling back to FAAS_INTERNAL_SVC_PUBKEYS env path",
			"err", clusterErr.Error())
	}

	// ADR-119 — env-path fallback: load FAAS_INTERNAL_SVC_PUBKEYS
	// (JSON document mapping svcName → PEM-encoded Ed25519 public
	// key). Read once at boot; runtime rotation is a follow-up
	// (ADR-120 candidate — see plan). nil = no apps in
	// internal_only mode should be reachable, and the gate 500s
	// loudly rather than fail-open. Production wires schedd at
	// minimum; meterd / future daemons add keys to the same JSON
	// map.
	//
	// PR-3 / ADR-125 — this path runs ONLY when the cluster path
	// above returned ErrClusterVerifierUnavailable or a hard
	// error. In the cluster-OK case deps.internalSvcVerifier is
	// already the rotatingVerifier backed by cluster_signing_keys.
	if deps.internalSvcVerifier == nil {
		if rawPubkeys, ok := os.LookupEnv("FAAS_INTERNAL_SVC_PUBKEYS"); ok && rawPubkeys != "" {
			var perSvc map[string]string
			if err := json.Unmarshal([]byte(rawPubkeys), &perSvc); err != nil {
				log.Warn("gatewayd-internal: FAAS_INTERNAL_SVC_PUBKEYS is not valid JSON; internal_only mode will 500 until corrected",
					"err", err.Error())
			} else if len(perSvc) == 0 {
				log.Warn("gatewayd-internal: FAAS_INTERNAL_SVC_PUBKEYS is empty; internal_only mode will 500 until corrected")
			} else {
				deps.internalSvcVerifier = newInternalSvcVerifierFromPEMs(perSvc)
				log.Info("gatewayd-internal: internal_only verifier loaded (env)",
					"svc_count", len(perSvc),
					"source", "FAAS_INTERNAL_SVC_PUBKEYS")
			}
		}
	}
	// The scheddClient reference is needed by AppLogsHandler (PR-2).
	// It outlives `run` because we want the AppLogsHandler to keep a
	// pointer to the same client; defers Close.
	deps.scheddClient = sched
	// Issue #562 PR-B: build the S3 client the bucket-proxy
	// read-back handler uses to fetch archived .jsonl.gz
	// objects. We re-use the same FAAS_LOG_ARCHIVE_* env vars
	// apid's shipper reads (single source of truth on the
	// box — same endpoint, region, key, secret, bucket). When
	// the bucket is unset (the disabled-path branch), the
	// handler surfaces 503 log_archive_unconfigured rather
	// than booting a half-configured client. apid is the
	// canonical shipper owner; gatewayd-internal only reads.
	// The wire-up happens AFTER the public-auth unseal so an
	// operator who rotates host.age picks up the new envelope
	// on the same boot — no separate restart needed.
	archiveCfg, archiveCfgErr := logarchive.ConfigFromEnv(os.Getenv, log)
	if archiveCfgErr == nil {
		archiveCredsPath := envOrGateway(logarchive.EnvCredentialsPath, logarchive.DefaultCredentialsPath)
		if creds, err := logarchive.ReadCredentials(archiveCredsPath); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				log.Warn("gatewayd-internal: log archive credentials unavailable; archive read-back disabled", "path", archiveCredsPath, "err", err)
			}
		} else {
			archiveCfg = archiveCfg.WithCredentials(creds)
		}
	}
	switch {
	case archiveCfgErr != nil:
		log.Warn("gatewayd-internal: log archive config parse failed; archive read-back disabled", "err", archiveCfgErr)
	case !archiveCfg.Enabled():
		log.Info("gatewayd-internal: log archive disabled (FAAS_LOG_ARCHIVE_BUCKET unset); archive read-back will return 503")
	default:
		s3c, s3Err := logarchive.NewS3Client(
			archiveCfg.Endpoint, archiveCfg.Region, archiveCfg.Bucket,
			archiveCfg.KeyID, archiveCfg.Secret,
		)
		if s3Err != nil {
			log.Warn("gatewayd-internal: S3 client build failed; archive read-back disabled", "err", s3Err)
		} else {
			deps.archiveS3 = s3c
			deps.archiveBucket = archiveCfg.Bucket
			log.Info("gatewayd-internal: log archive read-back armed",
				"endpoint", archiveCfg.Endpoint,
				"region", archiveCfg.Region,
				"bucket", archiveCfg.Bucket)
		}
	}
	// ADR-025 axis 4: StreamWarmHints consumer. Long-lived
	// goroutine under the same ctx as the rest of the daemon —
	// drains the sticky-warm affinity stream from schedd into
	// the picker's hint cache. Reconnects with backoff on
	// transient errors; freezes the cache on persistent errors
	// (Phase 3 review policy). nil in tests (the e2e harness
	// doesn't drive the stream; the picker falls through to
	// per-node healthyCount as it always did).
	deps.warmHints = newWarmHintConsumer(sched, warmHintCache, log, nil)
	go deps.warmHints.Run(ctx)
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
	cfg := deps.config
	if cfg == nil {
		cfg = &Config{}
	}
	// DEPLOY-1 / ADR-075 capdecl gate. gatewayd-internal is
	// unprivileged — no Allow, no Deny. The HTTP/1.1 listener,
	// the gRPC egress sink, the schedd dial, the vmmd dial and
	// every Postgres read/write in this daemon run inside the
	// unit's systemd hardening (NoNewPrivileges, ProtectSystem,
	// PrivateTmp, etc.). Any future cap_ add lands here, not
	// in the unit file, and the gate stops the daemon on boot
	// if the new cap isn't bound. The capCheck seam (review
	// finding M2) lets tests stub the live /proc/self/status
	// check.
	capCheck := deps.capCheck
	if capCheck == nil {
		capCheck = func() error { return runtimecheck.MustCheckOnBoot(capsDecl, log, nil) }
	}
	if err := capCheck(); err != nil {
		return err
	}

	// ADR-068 / Tier A7 split: TLS termination moved to gatewayd-public.
	// The legacy daemon always serves plain HTTP on :8080 (the e2e
	// harness path); production traffic terminates TLS at gatewayd-public
	// and proxies to the unix socket bound in cmd/gatewayd-internal/.

	handler := gateway.NewHandlerWith(deps.backend, deps.metrics, log)
	// ADR-104 amendment 5 / issue #881 Phase 4 C3: opt-in
	// central-mode rate-limit counter (the [ratelimit] mode TOML
	// knob added in C2). mode = "local" (default) leaves every
	// Limiter on its noop backend — behaviour unchanged from
	// pre-Phase-4. mode = "central" wires the production
	// PGRateLimitBackend + the LISTEN-side invalidator (C4).
	if deps.config != nil && deps.config.RateLimit.Mode == "central" {
		backend, rps := buildCentralRateLimitBackend(deps.pool, log)
		if backend != nil {
			handler.WithCentralBackend(backend)
			log.Info("gatewayd-internal: rate-limit central mode armed",
				"rps_resolver", rpsKind(rps))
			// ADR-104 amendment 5 / issue #881 Phase 4 C4:
			// the LISTEN-side invalidator. Closes the cross-
			// replica drift by dropping the local bucket when a
			// peer writes to the central counter. Started as a
			// long-lived goroutine; cancellation rides on the
			// daemon's main ctx.
			invalidator := wire.NewPGRateLimitInvalidator(deps.pool, handler, log)
			go func() {
				if err := invalidator.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
					log.Warn("gatewayd-internal: rate-limit invalidator exited", "err", err)
				}
			}()
		} else {
			log.Warn("gatewayd-internal: [ratelimit] mode = \"central\" but Postgres pool unavailable; falling back to in-process buckets (degraded posture)")
		}
	}
	// Issue #587 / PR-A: construct the per-request drain tracker
	// the graceful-shutdown path waits on. ONE tracker per daemon
	// shared with the nodeCache raw forwarder + the control mux.
	// See pkg/gateway/drain for the WaitGroup/atomic contract.
	deps.drain = drain.NewTracker()
	handler.WithInFlightTracker(deps.drain)
	handler.SetWakeGateHook()
	// Issue #471 / ADR-047: per-process streaming opt-in. The Handler
	// buffers every response unless the flag is on; when off, a
	// misconfigured SSE-emitting app surfaces the once-per-process
	// deprecation log instead of a silent buffered blob. The flag
	// here is the merged (cfg.StreamingEnabled || env(FAAS_GATEWAY_STREAMING))
	// — run()
	// populates deps.streamingEnabled; tests inject the bit directly.
	handler.WithStreamingEnabled(deps.streamingEnabled)
	// ADR-093: arm the per-process routeMetricsEnabled kill-switch on
	// the Handler so routeSetFor can AND the operator flag against the
	// per-app flag (apps.route_metrics_enabled). Same merge point as
	// WithStreamingEnabled above; both flags ride the same plumbing.
	handler.WithRouteMetricsEnabled(deps.routeMetricsEnabled)
	// Issue #560: per-deployment require_authn. The adapter
	// + auditor are nil-safe; the pre-issue public-by-default
	// behaviour is preserved for unit tests + dev boxes that
	// leave deps.requireAuthnAdapter / deps.requireAuthnAudit
	// nil. Production wires both unconditionally after
	// deps.authMw is built in run().
	handler.WithRequireAuthn(deps.requireAuthnAdapter, deps.requireAuthnAudit)
	// ADR-089 / issue #561 PR 3: arm the per-host edge-rule
	// matcher so ServeHTTP consults it BEFORE Backend.Lookup
	// (handler.go:1599-1618). The resolve closure wraps
	// AppBySlug so a matched route re-targets the gateway.App
	// end-to-end (handler.go:matchAndSubstituteRoute does the
	// same-account check).
	//
	// Field copy (review fix R1): every auth/forwarder-relevant
	// field on state.App MUST be copied onto the gateway.App —
	// enforceRequireAuthn reads App.RequireAuthn,
	// enforcePublicAuth reads App.PublicAuth.Mode, the streaming/
	// WS detectors read StreamingEnabled/WebSocketEnabled, per-node
	// schedd routing reads NodeID. A zero value on any of these
	// would silently disable the per-app gate ("auth remains
	// per-app on the target" promise would be broken — a target
	// with require_authn=true would suddenly appear open to the
	// inbound host's anonymous traffic after the route fires).
	// Sidecars is intentionally NOT copied: state.App doesn't
	// carry it (it lives on the deployment join), and a kind=route
	// hit never references the inbound host's sidecar roster —
	// the target's own routing resolves its own sidecars on the
	// next request to its own hostname.
	//
	// Plan: state.App does not carry Plan (the apps table
	// doesn't denormalize it; Plan lives on accounts). The
	// routed App returned here has Plan="", which surfaces as
	// an empty `plan` label on gateway_requests_total for
	// routed requests — bounded cardinality, distinguishable
	// from non-routed (plan=Free|Hobby|Pro|Scale). Populating
	// Plan properly is PR 8 (denormalize on apps row OR
	// back-to-back AccountByID join — both deferred; the
	// metrics label gap is acceptable for a v1 surface).
	//
	// Error classification (review fix R2): state.ErrNotFound
	// is a clean miss (the target app row was deleted) — silent
	// fall-through. Anything else is a real error (DB outage,
	// pool exhaustion, ctx cancel) and gets logged at WARN +
	// audited so a dashboard operator can distinguish a noisy
	// transient from a sustained backend failure.
	handler.WithEdgeRules(deps.edgeRulesMatcher, func(ctx context.Context, slug string) (gateway.App, bool) {
		app, err := deps.pgStore.AppBySlug(ctx, slug)
		if err != nil {
			if !errors.Is(err, state.ErrNotFound) {
				if log != nil {
					log.Warn("edge rule target AppBySlug failed", "slug", slug, "err", err)
				}
				if deps.edgeRulesAudit != nil {
					subject := slug
					deps.edgeRulesAudit.Emit(ctx, "edge_rule.route_loader_error", &subject, map[string]any{
						"slug": slug,
						"err":  err.Error(),
					})
				}
			}
			return gateway.App{}, false
		}
		// ADR-123: hydrate app.OrgID so applyIngressMembersOnly
		// can gate edge-routed traffic to a members_only app
		// against the cookie principal's membership in the
		// owning org. AppBySlug does NOT inflate state.App with
		// OrgID (deliberate — only the per-host LRU hydration
		// path at toApp consults it); we read the narrow
		// AppOrgID accessor instead. An empty OrgID on a
		// members_only app routes through the gate's misconfig
		// 500 branch — the loud-posture contract.
		orgID, orgErr := deps.pgStore.AppOrgID(ctx, app.ID)
		if orgErr != nil {
			if log != nil {
				log.Warn("edge rule target AppOrgID failed", "slug", slug, "app_id", app.ID, "err", orgErr)
			}
			// Fall through with empty OrgID — the gate's
			// empty-OrgID misconfig branch 500s the request,
			// which is the correct posture for a transient
			// store read (the next request hits the same
			// accessor; a sustained failure will surface in
			// the operator dashboard via the gate's metric
			// increment).
		}
		return gateway.App{
			ID:               app.ID,
			AccountID:        app.AccountID,
			Slug:             app.Slug,
			RequireAuthn:     app.RequireAuthn,
			PublicAuth:       gateway.PublicAuthConfig{Mode: app.PublicAuthMode, BasicSealed: app.PublicAuthBasicSealed, IPAllowlist: app.PublicAuthIPAllowlist},
			StreamingEnabled: app.StreamingEnabled,
			WebSocketEnabled: app.WebSocketEnabled,
			// ADR-093: per-route observability opt-in. Mirrors
			// the WebSocketEnabled plumbing above — the same
			// routeSetFor gate in Handler.ServeHTTP reads this
			// alongside the operator kill-switch.
			RouteMetricsEnabled: app.RouteMetricsEnabled,
			// ADR-091 amendment / §4.1.2.0: coarse-gate per-app
			// maintenance flag (apps.maintenance_mode).
			MaintenanceMode: app.MaintenanceMode,
			NodeID:          app.NodeID,
			// ADR-123: see hydration comment above.
			OrgID: orgID,
		}, true
	}, deps.edgeRulesAudit)
	// Issue #561 / ADR-091 PR 5 — arm the per-rule JWT verifier.
	// nil-safe: deps.edgeJWKSAdapter nil falls through
	// (applyEdgeRuleJWT short-circuits, matching pre-PR-5 + dev
	// posture). Production wires a real adapter backed by
	// pkg/edgejwks.NewCache + pkg/edgejwks.NewVerifier.
	if deps.edgeJWKSAdapter != nil {
		handler.WithJWTVerifier(deps.edgeJWKSAdapter)
	}
	// ADR-091 D21 — arm the geoip reader that applyEdgeRuleGeo
	// consults. nil-safe: deps.geoReader nil (file missing or
	// FAAS_GEOIP_DB_PATH empty) keeps the gate disabled; the
	// matcher still surfaces a geo rule, but the lookup is a
	// no-op and the gate fail-opens.
	if deps.geoReader != nil {
		handler.WithGeoReader(deps.geoReader)
	}
	// PR-B — arm the per-rule JSON-Schema validator that
	// applyEdgeRuleValidate consults. nil-safe:
	// deps.edgeValidateAdapter nil falls through
	// (applyEdgeRuleValidate short-circuits, matching pre-PR-B +
	// dev posture). Production wires a real adapter backed by
	// pkg/edgevalidate.NewManager.
	if deps.edgeValidateAdapter != nil {
		handler.WithValidator(deps.edgeValidateAdapter)
	}
	// Issue #477 / ADR-079: per-app public_auth (open|bearer|basic).
	// The 60s cache lives on the Handler (production wires
	// deps.publicAuthCache below); the secretbox unseal goes
	// through deps.publicAuthUnsealer, which closes over the
	// loaded host identities (the same secretbox.OpenMulti path
	// cmd/apid uses). nil-safe: tests + dev boxes that don't
	// wire either pass through the per-request unseal path.
	handler.WithPublicAuth(deps.publicAuthCache, deps.publicAuthUnsealer)
	// ADR-119 — wire the per-service public-key allowlist into
	// the Handler so applyIngressInternalSvc (handler.go:~4586
	// area) can validate Authorization: Bearer JWTs on apps
	// whose public_auth_mode='internal_only'. The same verifier
	// is consulted by the synth-side gate
	// (pkg/gateway/synth.go::SynthServer.handleSynthesize) via
	// the SynthServer dependency wiring — single source of
	// truth on the verifier.
	handler.WithInternalSvcVerifier(deps.internalSvcVerifier)
	// ADR-123 — wire the Handler-side members_only gate.
	// Same checker + same cookie principal resolver the
	// SynthServer uses; the Handler reads app.OrgID
	// directly (pgRouter.toApp hydrates it from
	// Store.AppOrgID at backend.go:218), so the
	// WithAppOrgIDLookup call is not needed on the
	// Handler side. The cmd-side adapter at
	// auth_principal_adapter.go is the only NEW
	// cmd-side file. Inlined values (F1 review fix):
	// the deps fields are NOT pre-populated at this
	// point — they were originally assigned AFTER the
	// synth wiring (which itself runs BEFORE the
	// Handler wiring), so the Handler captured the
	// zero value. Inlining makes the values explicit
	// and removes the ordering dependency.
	handler.WithMembersOnlyChecker(authz.PoolOrgMemberChecker(deps.pool))
	handler.WithMembersOnlyPrincipalExtractor(newAuthPrincipalAdapter())
	// ADR-046 PR-2: per-instance egress ring buffer + the gRPC
	// producer channel. The sink is shared between Handler.recordEgress
	// (writer) and egressgrpc.Server (drainer-on-cadence reader).
	// The gRPC server runs on a second unix socket (FAAS_GATEWAY_EGRESS_SOCKET)
	// because the existing synth socket serves an HTTP/1.1 listener
	// (pkg/gateway/synth.go) and a single unix socket cannot host both
	// HTTP and gRPC simultaneously; the DAC auth posture (ADR-015) is
	// identical — group `faas` ownership + 0660 mode. netns dialer is
	// unchanged.
	egressSink := egresssink.NewEgressSink()
	handler.WithEgressSink(egressSink)
	// Issue #676 / ADR-080 PR-C: the raw-stream forwarder writes
	// raw-bytes Upgrade chunks into the same per-instance egress
	// ring as the plain HTTP forwarder. They share the *EgressSink
	// pointer so both paths land in the same usage_minutes.tx_bytes
	// bucket — see cmd/gatewayd-internal/nodecache.go:WithEgressSink.
	if deps.nodeCache != nil {
		deps.nodeCache.WithEgressSink(egressSink)
		// Issue #587 / PR-A: raw-stream Upgrade pump drain tracking.
		// Captured by the ForwardingRawReverseProxyWithEventsAndDrain
		// closure in nodeCache.RawForwarding() — every hijacked
		// raw-stream conn holds a Begin("upgrade") slot for its
		// pump's lifetime, so the graceful-shutdown drain sees it
		// in-flight instead of force-closing on TimeoutStopSec=30s.
		deps.nodeCache.WithDrainTracker(deps.drain)
	}
	egressGRPCSocket := egressGRPCSocketPath()
	// Reject the silent-no-TLS path: a non-unix target without TLS
	// would build an insecure server (ADR-052). deps.egressTLS is
	// loaded in run() — see TomlTLSLoad order below.
	if !isUnixSocketPath(egressGRPCSocket) && deps.egressTLS == nil {
		return fmt.Errorf("gatewayd: egress target %q is non-unix but egress_tls_* is empty (set egress_tls_cert_path / key_path / ca_path or point the target at a unix socket for single-box mode)", egressGRPCSocket)
	}
	egressGRPCSrv := egressgrpc.NewServer(egressSink, log)
	deps.egressGRPC = newEgressGRPCListener(egressGRPCSocket, deps.egressTLS, egressGRPCSrv, log)
	// Best-effort start, mirroring the synth listener pattern
	// (runWithDeps internal RPC). If the unix socket can't bind
	// (e.g. /run/faas doesn't exist on a dev/test box), log + continue
	// — the public + control listeners are still up, and the
	// per-instance egress sink continues to accumulate in memory
	// even though no consumer can dial the stream. The meterd side
	// reports ok=false on every EgressBytes read during the gap;
	// AppendUsage writes 0 to tx_bytes for that minute. Once the
	// socket becomes bindable (deploy-time /run/faas exists), the
	// next daemon restart picks up the stream automatically.
	if err := deps.egressGRPC.start(ctx); err != nil {
		log.Warn("gatewayd egress listen failed; tx_bytes will stay 0 until restart",
			"socket", egressGRPCSocket, "err", err)
		deps.egressGRPC = nil
	}
	//nolint:contextcheck // shutdown ctx must outlive the cancelled caller ctx per net/http + gRPC GracefulStop contract.
	defer func() {
		if deps.egressGRPC == nil {
			return
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = deps.egressGRPC.stop(shutdownCtx)
	}()
	// Issue #300: topNSampler drives the gateway_top_tenant_rps
	// gauge from the rolling per-app count fed by Handler.observe
	// (pkg/gateway/handler.go:observe). 5s tick; runs for the
	// daemon's lifetime; stops cleanly on ctx cancel. Local
	// topAccountSet mirrors pkg/wire/topn.go so pkg/gateway stays
	// free of any dependency on pkg/wire (avoids an import cycle
	// through cmd/gatewayd-internal → pkg/gateway → pkg/wire → cmd/gatewayd-internal).
	gatewayTopNSampler := newTopNSampler(handler.Metrics(), log)
	go gatewayTopNSampler.run(ctx)

	// Issue #98 / ADR-028 + ADR-047: install the per-node HTTP→gRPC
	// forwarder. Backend.Target returns a compute_node.id (string-typed
	// for backwards compat); the forwarder dereferences it via the
	// per-node vmmd client cache and bridges HTTP bytes to the
	// instance netns through vmmd's bidi ForwardHTTPStream RPC. nil
	// cache = legacy addr-based path (tests + e2e harness without
	// vmmd overlay).
	if deps.nodeCache != nil {
		handler.WithForwarding(deps.nodeCache.Forwarding())
		// Issue #676 / ADR-080: install the raw-bytes Upgrade
		// bridge alongside the plain forwarder. WithRawForwarding
		// takes the same NodeClientLookup + logger so the per-node
		// gRPC channel is reused; only the RPC method differs
		// (ForwardRawStream vs ForwardHTTPStream). The handler's
		// three-input gate routes Connection: Upgrade requests
		// here BEFORE falling through to WithForwarding's
		// ForwardHTTPStream bridge.
		//
		// Operator kill switch (issue #676 follow-up): when
		// FAAS_GATEWAY_RAW_STREAM_ENABLED=false, the call is
		// skipped — h.rawByNode stays nil, and the three-input
		// gate at pkg/gateway/handler.go:2899 falls through to
		// writeWebSocketNotAllowed(forwarderMissing=true),
		// returning a deterministic 501 + x-faas-error-reason:
		// websocket_not_on_plan. Default is true; see
		// rawStreamEnabledFromEnv above.
		if rawStreamEnabledFromEnv() {
			handler.WithRawForwarding(deps.nodeCache.RawForwarding())
		} else {
			slog.Info("gatewayd-internal: raw-bytes Upgrade bridge disabled by FAAS_GATEWAY_RAW_STREAM_ENABLED=false",
				"fallthrough", "writeWebSocketNotAllowed(forwarderMissing=true)")
		}
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

	// ADR-127 production debugger — request_telemetry data plane.
	// Wires the recorder into Handler.observe + launches the
	// publisher goroutine. The ShipFn is a sampled log stub in
	// PR-A: rows drain from the recorder on FlushInterval and
	// are sampled at 1/N to slog.Debug so operators can see the
	// data plane is running without flooding the log. PR-B
	// replaces the log stub with a real IncrementRequestTelemetry
	// streaming RPC against apid's unix socket — the recorder +
	// publisher plumbing stays unchanged.
	requestTelemetryEnabled := osGetenv("FAAS_REQUEST_TELEMETRY_ENABLED") != "false"
	if requestTelemetryEnabled {
		recorder := gateway.NewRequestTelemetryRecorder(gateway.RequestTelemetryConfig{
			Enabled:  true,
			RingSize: 4096,
		}, log)
		// PR-B (ADR-127 §PR-B): dial apid's RequestTelemetry
		// service over the unix socket and stream the collapsed
		// buckets through IncrementRequestTelemetry. PR-A's
		// log-only stub at this site is replaced — every batch
		// now produces real INSERTs in apid's request_telemetry
		// table. The dial is lazy: a transient apid outage
		// doesn't block gateway boot, and the publisher's
		// retry-with-backoff handles a cold apid start.
		apidRTSock := osGetenv("FAAS_APID_REQUEST_TELEMETRY_SOCKET")
		if apidRTSock == "" {
			apidRTSock = "/run/faas/request_telemetry.sock"
		}
		rtCli, dialErr := apidgrpc.DialRequestTelemetry(ctx, apidRTSock, nil)
		var rtShippedTotal int64
		publisher := gateway.NewRequestTelemetryPublisher(gateway.RequestTelemetryPublisherConfig{
			Enabled:        true,
			FlushInterval:  5 * time.Second,
			FlushBatchSize: 256,
			MaxRetries:     3,
		}, recorder, func(ctx context.Context, rows []gateway.RequestTelemetryRow) error {
			// Dial failed at boot — log-only fallback mirrors
			// PR-A's stub. Operators see the dial failure once
			// at startup + a per-tick "drained batch" debug.
			if rtCli == nil {
				rtShippedTotal += int64(len(rows))
				if len(rows) > 0 {
					log.Debug("request_telemetry: drained batch (no apid client)",
						"batch_size", len(rows),
						"shipped_total", rtShippedTotal)
				}
				return nil
			}
			stream, err := rtCli.IncrementRequestTelemetry(ctx)
			if err != nil {
				return fmt.Errorf("open request_telemetry stream: %w", err)
			}
			for i := range rows {
				row := rows[i]
				req := &apidpb.IncrementRequestTelemetryRequest{
					AccountId:        row.AccountID.String(),
					AppId:            row.AppID.String(),
					DeploymentId:     row.DeploymentID.String(),
					RouteTemplate:    row.Route,
					Method:           row.Method,
					HttpStatus:       int32(row.Status),
					LatencyMs:        int32(row.LatencyMS),
					ColdBoot:         row.ColdBoot,
					TraceId:          row.TraceID,
					ReceivedAtUnixMs: row.ReceivedAt.UnixMilli(),
					Count:            int32(row.Count),
				}
				if row.Count < 1 {
					req.Count = 1
				}
				if err := stream.Send(req); err != nil {
					_ = stream.CloseSend()
					return fmt.Errorf("send request_telemetry row: %w", err)
				}
			}
			if err := stream.CloseSend(); err != nil {
				log.Warn("request_telemetry: stream CloseSend failed",
					"batch_size", len(rows), "err", err)
			}
			// Drain responses to detect per-row failures. The
			// publisher's retry-with-backoff covers transient
			// errors here; rate-limit + db_error outcomes are
			// surfaced via Prometheus counters in the apid
			// receiver (PR-B stage 4).
			for {
				resp, rerr := stream.Recv()
				if rerr != nil {
					// io.EOF is the canonical end-of-stream.
					if errors.Is(rerr, io.EOF) {
						break
					}
					return fmt.Errorf("recv request_telemetry response: %w", rerr)
				}
				if resp == nil {
					break
				}
				if resp.GetOutcome() == "rate_limited" {
					log.Debug("request_telemetry: row rate_limited",
						"retry_after_ms", resp.GetRetryAfterMs())
				}
			}
			rtShippedTotal += int64(len(rows))
			if len(rows) > 0 {
				log.Debug("request_telemetry: drained batch",
					"batch_size", len(rows),
					"shipped_total", rtShippedTotal)
			}
			return nil
		}, log)
		// Surface dial failures back into the ShipFn via rtCli.
		// (Defer the close until after publisher.Stop so the
		// publisher's final-drain batch can still ship.)
		if dialErr != nil {
			log.Warn("request_telemetry: apid socket dial failed; recorder ships to log only",
				"err", dialErr)
		} else {
			defer func() {
				if err := rtCli.Close(); err != nil {
					log.Warn("request_telemetry: close apid client", "err", err)
				}
			}()
		}
		publisher.Start(ctx)
		handler.WithRequestTelemetryRecorder(recorder)
		// Stop() drains the final batch synchronously on shutdown.
		defer publisher.Stop()
		// Expose counters for the dashboard via /metrics; read by
		// the existing Prometheus scrape.
		log.Info("request_telemetry recorder enabled",
			"ring_size", 4096,
			"flush_interval", 5*time.Second,
			"note", "PR-A: log-only ship stub; PR-B adds apid gRPC receiver")
	} else {
		log.Info("request_telemetry recorder disabled (FAAS_REQUEST_TELEMETRY_ENABLED == \"false\")")
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
				// Reset both per-app and per-account buckets (ADR-040).
				// appDropped = per-app buckets cleared; acctDropped =
				// per-account buckets cleared. Operators see the sum.
				appDropped := handler.Limiter().ForgetAll()
				acctDropped := handler.AccountLimiter().ForgetAll()
				log.Info("gatewayd sighup reload",
					"action", "rate_limit_buckets_dropped",
					"app_count", appDropped,
					"account_count", acctDropped,
					"count", appDropped+acctDropped)
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
	// Issue #254 / Move 4 PR-2: the AppLogsHandler claims the
	// customer-facing `GET /v1/apps/{slug}/logs` route. gatewayd
	// already imports pkg/scheddgrpc (ADR-018) so the schedd dial
	// stays in one daemon (depguard apid-control-plane-only blocks
	// cmd/apid from importing pkg/scheddgrpc). The auth chain
	// (bearer / session / MFA / scope / IDOR-safe LoadApp) is
	// shared with cmd/apid via pkg/auth.Middleware (ADR-046), so
	// the spec §11 10/min/IP rule covers both gateways.
	//
	// The handler is mounted on a tiny *http.ServeMux so the
	// `{slug}` path parameter binds (the AppLogsHandler reads
	// r.PathValue("slug")). The mux is wrapped by the apidProxy
	// carve-out (cmd/gatewayd-internal/proxy.go::isApidLogsPath) so the
	// public listener dispatches the path straight to the handler
	// without consulting pkg/gateway.Handler's wake/proxy logic
	// (the route is hostname-agnostic — it owns /v1/apps/{slug}/logs
	// regardless of the Host header).
	//
	// The handler is wired only when deps.authMw is non-nil — tests
	// default deps.authMw to nil and the carve-out is silently
	// disabled (the path falls through to apid, matching the
	// pre-PR-2 behaviour).
	var logsHandler http.Handler
	if deps.authMw != nil && deps.pgStore != nil && deps.scheddRouter != nil {
		logsMux := http.NewServeMux()
		// PR-B (issue #562): mount a tiny dispatcher that routes
		// ?archive=1 to the bucket-proxy read-back handler
		// (cmd/gatewayd-internal/app_logs_archive.go) and the
		// bare /v1/apps/{slug}/logs path to the live handler.
		// The mux is mounted at GET /v1/apps/{slug}/logs and the
		// dispatcher reads r.URL.Query().Get("archive"); query
		// params don't appear in r.URL.Path, so the mux still
		// routes to the dispatcher regardless of whether
		// ?archive=1 is present.
		logsMux.Handle("GET /v1/apps/{slug}/logs", &appLogsDispatcher{
			live: &AppLogsHandler{
				Auth: deps.authMw,
				// Phase 2 / Gate A: dial the owner schedd via the
				// per-node router. The legacy scheddClient field is
				// retained for the warm-hint stream (single-stream
				// fallback); the log stream is the second fan-in
				// consumer and resolves per-app.
				ScheddFor: appLogsScheddResolver{store: deps.pgStore, router: deps.scheddRouter},
				Store:     deps.pgStore,
				Log:       log,
				Ops:       nil,
			},
			archive: &ArchiveLogsHandler{
				Auth:   deps.authMw,
				S3:     deps.archiveS3,
				Bucket: deps.archiveBucket,
				Store:  deps.pgStore,
				Log:    log,
				Ops:    nil,
			},
		})
		logsHandler = logsMux
	}

	// Tier A9 / ADR-084: standby write-redirect gate. The gate
	// sits in front of every apid-bound mutating request; on a
	// two-node fleet, standby writes are either relayed to the
	// leader over mTLS (bearer / anonymous) or 307-redirected to
	// the leader's public URL (cookie). When the resolver has
	// no leader, the gate emits 503 with a 60-second Retry-After.
	//
	// We construct the resolver + client + gate ONLY when:
	//   - pgStore is available (the resolver needs the leader
	//     store + the publisher needs a pool for pg_notify),
	//   - mTLS material is on disk (the certs are operator-
	//     deployed; missing material means the standby can
	//     never relay successfully anyway).
	// Single-node builds (the macOS dev loop, Lima unit tests)
	// omit both, so the gate is silently bypassed and writes
	// land on the local apid as before.
	var writeGate http.Handler
	if deps.pgStore != nil && osGetenv("FAAS_LEADER_REDIRECT_TLS_CERT") != "" {
		refresh := make(chan struct{}, 1)
		resolver := writegate.NewCachedLeaderResolver(
			newLeaderStoreAdapter(deps.pgStore),
			osGetenv("FAAS_NODE_NAME"),
			time.Duration(api.StandbyWriteLeaderURLCacheTTLSeconds)*time.Second,
			refresh,
		)
		client, err := writegate.NewMTLSLeaderClient(
			osGetenv("FAAS_LEADER_REDIRECT_TLS_CERT"),
			osGetenv("FAAS_LEADER_REDIRECT_TLS_KEY"),
			osGetenv("FAAS_LEADER_REDIRECT_TLS_CA"),
			time.Duration(api.StandbyWriteRedirectTimeoutMS)*time.Millisecond,
		)
		if err != nil {
			return fmt.Errorf("gatewayd-internal: build writeGate mTLS client: %w", err)
		}
		writeGate = newWriteGate(
			nil, // next is set by apidProxy; the gate's bypass forwards to it
			resolver, client,
			apid.IsApidPath,
			osGetenv("FAAS_NODE_NAME"),
			deps.opsMetrics, log,
		)
		go func() {
			if err := runLeaderURLPublisher(ctx, log, deps.pool, refresh, osGetenv("FAAS_NODE_NAME")); err != nil {
				log.Error("gatewayd-internal: leader url publisher exited", "err", err.Error())
			}
		}()
		log.Info("gatewayd-internal: standby write-redirect gate armed",
			"node", osGetenv("FAAS_NODE_NAME"),
			"cache_ttl_s", api.StandbyWriteLeaderURLCacheTTLSeconds,
			"timeout_ms", api.StandbyWriteRedirectTimeoutMS,
		)
	}

	apidHandler := newApidProxyWithGate(apidTarget, handler, logsHandler, writeGate, log)

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
	// Issue #294: wire the githubd proxy with the dedupe check and
	// the audit emitter. The replay interface is satisfied by
	// *state.PgStore; the auditStore interface is also satisfied by
	// *state.PgStore (compile-time checked in audit.go). nil
	// `deps.pgStore` (tests) skips the replay check, matching the
	// pre-#294 behaviour.
	publicHandler := newGithubdProxy(githubdTarget, githubdSecret, apidHandler, log, newGatewaydAuditor(deps.pgStore, log))

	// ADR-096: customer-facing automatic error grouping writer
	// path. gatewayd-internal records every 4xx/5xx response on
	// the public edge and ships batches to apid via the AppErrors gRPC
	// IncrementAppError streaming RPC. The target is a local Unix socket
	// on one-box deployments and a private mTLS endpoint in split-box mode.
	// apid is
	// the sole writer to app_errors / app_error_requests
	// (CLAUDE.md ownership); the gateway never opens a direct
	// Postgres connection for this store.
	//
	// Kill-switch: FAAS_APP_ERRORS_ENABLED defaults to true
	// (PR-B's stable point — PR-A shipped it OFF so the schema
	// populated only by hand; PR-B flips the default to ON now
	// that the customer-facing surface is in place). When false,
	// the recorder middleware is a pass-through (no per-request
	// cost beyond a single int compare) and the publisher
	// goroutine is NOT started.
	//
	// The legacy AppErrors socket defaults to /run/faas/app_errors.sock
	// (the ADR-015 unix-socket DAC convention shared with schedd + vmmd).
	// Split-box manifests provide app_errors_target and its client mTLS
	// paths in gatewayd.toml.
	appErrorsEnabled := osGetenv("FAAS_APP_ERRORS_ENABLED") != "false"
	apidAppErrorsTarget := cfg.GetAppErrorsTarget(osGetenv)
	appErrorsTLS, appErrorsTLSErr := cfg.LoadAppErrorsTLS()
	if appErrorsTLSErr != nil {
		return fmt.Errorf("gatewayd: load app errors TLS: %w", appErrorsTLSErr)
	}
	if appErrorsEnabled && deps.opsMetrics != nil {
		// Build the recorder + publisher; wire the publisher
		// goroutine; wrap the public handler with the
		// recorder's middleware.
		cli, dialErr := apidgrpc.DialContext(ctx, apidAppErrorsTarget, appErrorsTLS)
		if dialErr != nil {
			log.Warn("app_errors: apid target dial failed; recorder disabled", "target", apidAppErrorsTarget, "err", dialErr)
		} else {
			recorder := newAppErrorsRecorder(appErrorsRecorderConfig{
				Enabled:               true,
				DedupeWindowSeconds:   3600,
				CardinalityLimit:      10000,
				SampleMessageCapBytes: 512,
				HeadersSampleMaxKeys:  8,
			}, nil, deps.opsMetrics, log)
			publisher := newAppErrorsPublisher(recorder, cli, deps.opsMetrics, log)
			recorder.pub = publisher
			go publisher.Run(ctx)
			publicHandler = recorder.Middleware(publicHandler)
			log.Info("app_errors recorder enabled", "apid_target", apidAppErrorsTarget)
		}
	} else {
		log.Info("app_errors recorder disabled (FAAS_APP_ERRORS_ENABLED == \"false\")")
	}

	// Issue #249 / spec §11: mount security response headers at the
	// outermost wrapper of the public listener.
	//
	// httpsec.Static emits the five static headers (HSTS, X-Frame-
	// Options, X-Content-Type-Options, Referrer-Policy, Permissions-
	// Policy) on every response — customer apps, dashboard, API, even
	// githubd webhooks. Static headers are universally safe; the
	// customer's own CSP just sits on top.
	//
	// httpsec.Nonce gates Content-Security-Policy on isApidPath so it
	// only fires on URLs that hit apid. Customer-app responses are
	// proxied through our wake/proxy path — those apps govern their
	// own CSP, and injecting a nonce-locked policy here would break
	// every customer HTML page (issue #249 risk callout).
	// isApidPath lives in cmd/gatewayd-internal/proxy.go (path-string
	// predicate); the httpsec.Nonce gate takes a *http.Request, so
	// the adapter below extracts r.URL.Path and forwards.
	//
	// The order is httpsec.Nonce outer / httpsec.Static inner so the
	// CSP can be set before Static runs (it doesn't matter for the
	// static headers, but keeps the middleware chain readable).
	publicHandler = httpsec.Static(httpsec.Nonce(
		func(r *http.Request) bool { return isApidPath(r.URL.Path) },
		publicHandler,
	))

	// Issue #249: HSTS gate. RFC 6797 §7.2 says UAs ignore HSTS on
	// plain HTTP, so on a dev plaintext listener this is cosmetic.
	// Production TLS listener always emits it (the default).
	httpsec.SetHSTSEnabled(httpsec.HSTSEnabledFromEnv(osGetenv))

	// Private listener: control plane only — never authenticated (it's on a
	// private bind), never reachable from the public-internet path.
	//
	// Issue #568 / ADR-070 (Tier A7 edge split): the pre-split daemon
	// wired `nil` here, which made /readyz return 200 unconditionally.
	// That was acceptable for single-box (no LB to drain) but fails
	// closed wrong after the split: a partial-boot daemon would
	// happily accept traffic even though the routing cache, the cert-
	// bundle, and the warm-hint subscription are not yet ready.
	//
	// The pre-split daemon's readiness contract is the loosest of the
	// three flavours (gatewayd-public, gatewayd-internal, legacy
	// gatewayd): "we have a Postgres connection OR we're a unit test
	// that intentionally skips one". The split daemons (tasks #17,
	// #18) wire the full ReadyzProbe from pkg/gateway/readiness.go
	// with hydration tracking, schedd-router readiness, and PG ping.
	//
	// PR-B1 (issue #250 tier-1 ship-blocker): the gatewayd-internal
	// split daemon flips from legacyReady (only "pgStore != nil") to
	// the full ReadyzProbe. The tighter contract surfaces a partial-
	// boot daemon to the LB drain instead of letting it accept traffic
	// with a half-hydrated cache.
	//
	// Wiring strategy: each component-driven signal is constructed
	// via the appropriate helper (PG ping, staleness) or Register()
	// (manual), and is either Set(true, "") immediately (its
	// component is non-nil → already wired above) or left Set(false)
	// (nil → a test/dev path that intentionally skips the component).
	// Production paths with deps.pool != nil, deps.scheddRouter != nil,
	// deps.warmHints != nil, deps.nodeCache != nil flip true here.
	readyProbe := &gateway.ReadyzProbe{}
	// PG ping — only constructed if a pool is wired. The helper
	// goroutine pings every 5s and flips true on success; it also
	// kicks one ping immediately so the bit flips to ready as fast
	// as Postgres can answer. The 5s cadence matches
	// api.ReplicaHeartbeatIntervalSeconds so a /readyz scrape
	// observes a coherent "all-replicas-healthy" surface.
	if deps.pool != nil {
		pgSig, _ := gateway.NewPGPingSignal(ctx, deps.pool, 5*time.Second)
		readyProbe.RegisterSignal(pgSig)
	}
	// Schedd-router readiness: a manual signal flipped true the
	// moment WatchNodeChanges has been started (called at runDeps
	// init time around deps.scheddRouter construction above).
	if deps.scheddRouter != nil {
		s := readyProbe.Register()
		s.Set(true, "")
	}
	// Per-node schedd dial cache (nodeCache) readiness: a manual
	// signal flipped true after nodeCache construction above. The
	// first dial lazy-fills; today the first dial happens on the
	// first /v1/apps/{slug} request, so this signal is "we've
	// constructed the cache and at least the first ClientFor can
	// proceed" — not "we've already dialed". The lazy semantic is
	// load-bearing for cold-boot latency; a stricter "cache primed"
	// signal would force a synchronous Dial on every boot, which is
	// what the legacy daemon deliberately avoided.
	if deps.nodeCache != nil {
		s := readyProbe.Register()
		s.Set(true, "")
	}
	// Warm-hint subscriber freshness: a staleness signal Touched by
	// warmHints on each delivery. 2-minute staleness catches "the
	// daemon thinks it's subscribed but actually the goroutine
	// exited" rather than "the stream is momentarily idle" (the
	// schedd stream cadence under steady-state is sub-second).
	// Skipped when deps.warmHints is nil so a test that doesn't
	// construct the consumer doesn't fail /readyz permanently.
	//
	// FIX-4 (PR #880 code review): the touch callback returned
	// by NewStalenessSignal MUST be wired back to the consumer,
	// otherwise the helper goroutine observes lastTouch == 0 on
	// every tick and flips the signal false with reason "no
	// touch yet" — /readyz would be 503 forever and the LB
	// would never route traffic to this daemon. SetOnTouch
	// invokes touch() once immediately so the signal's first
	// goroutine tick already sees touched > 0.
	if deps.warmHints != nil {
		signal, touch, _ := gateway.NewStalenessSignal(2 * time.Minute)
		readyProbe.RegisterSignal(signal)
		deps.warmHints.SetOnTouch(touch)
	}
	controlMux := gateway.ControlMux(handler.Metrics(), readyProbe.ReadyFunc(), deps.drain)
	// Finding 6 (issue #314): mount the dashboard quota endpoint on the
	// control mux so an in-box caller (operator's curl today, future
	// apid-side dial) can read per-app bucket state without going through
	// the public :443 listener — that path self-rate-limits. The handler
	// reads from the same *Limiter the public edge uses (handler.Limiter()
	// is the seam) so the snapshot agrees with what Allow consumed.
	controlMux.HandleFunc("/v1/internal/quota", internalQuotaHandler(handler, log))
	// ADR-093: mount the per-route observability reader on the
	// control mux so apid can reverse-proxy
	// /v1/apps/{slug}/routes → /v1/internal/apps/{slug}/routes
	// without going through the public :443 listener (which would
	// self-rate-limit and expose the internal route-label state
	// to a customer-scoped probe). The handler resolves slug via
	// pgStore.AppBySlug — the control listener does NOT open its
	// own Postgres connection (ADR-070 single-purpose control
	// mux), so the closure shares the daemon's existing pool.
	// nil pgStore (tests / single-box dev without a DB) renders
	// the lookup_unavailable problem so the dashboard can
	// distinguish "DB missing" from "unknown slug".
	if deps.pgStore != nil {
		pgStore := deps.pgStore
		controlMux.HandleFunc("/v1/internal/apps/", func(w http.ResponseWriter, r *http.Request) {
			// Path-keyed: ServeMux's HandleFunc uses prefix
			// match, so /v1/internal/apps/foo/routes and
			// /v1/internal/apps/bar/routes both reach here.
			// The handler itself trims the prefix and reads
			// the slug from r.URL.Path.
			resolve := gateway.ResolveSlugFn(func(slug string) (string, bool) { //nolint:contextcheck // ADR-093 ResolveSlugFn signature is fixed; ctx captured from per-request r.Context().
				a, err := pgStore.AppBySlug(r.Context(), slug)
				if err != nil || a.ID == "" {
					return "", false
				}
				return string(a.ID), true
			})
			internalRoutesHandler(handler, resolve, log).ServeHTTP(w, r)
		})
	}

	// Track every *http.Server we spin up so the shutdown path can drain
	// them in parallel. sslib guidance is "call Shutdown on each" rather
	// than Close: Shutdown lets in-flight requests finish; Close does not.
	errc := make(chan error, 4)
	var servers []*http.Server
	addSrv := func(s *http.Server) { servers = append(servers, s) }

	// Issue #675: build the unified mux that the unix-socket server
	// (`deps.synth`) serves. The mux routes:
	//   /v1/synthesize           → synth handler (existing, M7)
	//   /v1/invocations:dispatch → synth handler (existing, Move 1)
	//   /healthz                 → synth handler
	//   everything else          → customer publicHandler (NEW — issue #675)
	//
	// Production (FAAS_GATEWAY_LISTEN=off) routes ALL customer traffic
	// through this mux via gatewayd-public's reverse proxy. On a split
	// compute box the TCP listener is also the schedd→compute synth
	// transport, so it must use this same mux; otherwise the cross-box
	// /v1/invocations:dispatch request falls through to the customer
	// handler and returns 404 while the VM wake itself succeeds.
	//
	// ADR-123: wrap publicHandler with the soft-session-attach
	// middleware so the cookie envelope stamped by the upstream
	// gatewayd-public proxy lands in r.Context() before
	// applyIngressMembersOnly runs. Hard RequireSession would
	// 401 on missing cookie and break open/bearer/basic/ip_allowlist
	// traffic; AttachSessionIfPresent stamps on success and
	// passes through silently on miss/invalid. The per-app
	// members_only gate reads the principal via
	// middleware.PrincipalFrom and 401s itself when the cookie
	// is absent — the authn gate stays per-app, not
	// per-listener. Wrap order: soft-session-attach sits
	// INSIDE httpsec (so response headers don't see the
	// stamped principal) and OUTSIDE publicHandler (so the
	// downstream chain sees the principal). Production
	// always has deps.authMw non-nil at this point (the
	// daemon panics at line ~1259 if it isn't); unit tests
	// + dev boxes with a nil deps.authMw get a pass-through
	// wrapper (no stamp, no 401 — exactly the pre-ADR-123
	// behaviour).
	publicListenerHandler := http.Handler(softSessionAttach(deps.authMw, publicHandler))
	if deps.synth != nil {
		unifiedMux := http.NewServeMux()
		// LOAD-BEARING ORDER: register Handle("/", publicHandler) FIRST,
		// then the more-specific synth routes AFTER. Go's
		// http.ServeMux uses longest-prefix matching, NOT
		// registration order — the catch-all written first does
		// NOT shadow the more-specific patterns written after.
		// Re-ordering this block "for readability" still works
		// in Go, but readers expect registration order to match
		// routing order and may "fix" it in a way that does NOT
		// work (e.g. moving the catch-all last while keeping
		// longest-prefix semantics). DO NOT change the order
		// without also updating the unified-mux test in
		// pkg/gateway/synth_test.go (TestSynthServer_UnifiedMux_RoutesPathsCorrectly).
		unifiedMux.Handle("/", publicHandler)
		// Pull the synth mux out of the SynthServer via a small
		// accessor; the server exposes SetHandler so the caller
		// owns the unified mux. The synth mux itself carries
		// the three routes registered in NewSynthServer.
		unifiedMux.Handle("/v1/synthesize", deps.synth.Mux())
		unifiedMux.Handle("/v1/invocations:dispatch", deps.synth.Mux())
		// Issue #757 / ADR-100 (commit #13): schedd's dispatch tick
		// posts trigger-driven batches here. SynthServer.handleInvocationDispatchBatch
		// fans each record through the same Invoke path and aggregates
		// the AWS Lambda ReportBatchItemFailures response back to the
		// schedd. The "POST /..." method-routed Handle below is the
		// route the spec-check AST scanner walks; the inner
		// unifiedMux.Handle above is the runtime prefix that
		// actually serves traffic on the unix socket.
		//
		// POST-only at the unified-mux level — a future GET surface
		// for a status read MUST go in the inner sub-mux
		// (synth.go:99), not here. Registering a bare
		// "/v1/invocations:dispatch_batch" pattern on top of the
		// POST-specific one was a copy-paste hazard: a future
		// maintainer adding GET for a status read would
		// silently fail because the bare pattern dispatches into
		// the POST handler's mux, which returns 405, not the
		// intended GET handler. Audit round 2 finding #3 (PR
		// #910).
		unifiedMux.Handle("POST /v1/invocations:dispatch_batch", deps.synth.Mux())
		unifiedMux.Handle("/healthz", deps.synth.Mux())
		// Wrap with h2c so the in-process unix-socket hop negotiates
		// H2C prior knowledge (no TLS). The customer publicHandler
		// speaks HTTP/1.1 downstream, but the gatewayd-public ← →
		// gatewayd-internal hop now carries H2 frames.
		//
		// Streaming responses (app.StreamingEnabled=true) still go
		// through the existing HTTP/1.1 chunked path inside the
		// handler — H2C is transparent to streaming because the
		// handler writes chunked on its own writer.
		//
		// Go 1.24+: the SynthServer's http.Server has Protocols set
		// to {HTTP/1.1, UnencryptedHTTP2} (see NewSynthServer), so
		// H2C is negotiated natively on the listener. No wrapper
		// needed — the deprecated golang.org/x/net/http2/h2c package
		// is gone.
		deps.synth.SetHandler(unifiedMux)
		publicListenerHandler = unifiedMux
	}
	// addSrv is the closure for the public :8080 + control listeners
	// below; declared above so the unified-mux block above can run
	// before the public-listener gate without depending on it.
	// Plain-:8080 path. ADR-068 / Tier A7 split: TLS termination
	// moved to gatewayd-public (certmagic, httpsec, :443/:80 ACME
	// mux); this daemon no longer builds a *gateway.TLSBundle. The
	// e2e harness hits this listener over plain HTTP.
	//
	// When FAAS_GATEWAY_LISTEN is the publicListenOffSentinel ("off"),
	// skip the public listener entirely: the daemon serves only on the
	// unix socket at /run/faas/gatewayd-internal.sock (forwarded to by
	// gatewayd-public) and the control plane (controlAddr). This is the
	// production shape per ADR-068 / Tier A7 split;
	// faas-gatewayd-internal.service ships with FAAS_GATEWAY_LISTEN=off
	// in Environment=.
	if listenAddr != publicListenOffSentinel {
		srv := deps.newSrv(listenAddr, publicListenerHandler)
		public := srv
		public.Addr = listenAddr
		if public.ReadTimeout == 0 {
			// Issue #995 Phase 3 / ADR-121: honour the TOML
			// override (cfg.RequestReadTimeout, propagated via
			// runDeps). 0 means "use api.GatewaydInternalRead
			// TimeoutSecondsDefault" (60 s — matches the legacy
			// default that lived here pre-PR).
			public.ReadTimeout = readTimeoutOrDefault(deps.readTimeout)
		}
		if public.WriteTimeout == 0 {
			// Issue #471 / ADR-047 (PR-A): honour the TOML override
			// (cfg.ResponseWriteTimeout, propagated via runDeps). 0
			// means "use the spec §4.1 baseline" — see run() for the
			// precedence. PR-B lifts the Hobby+ per-plan cap to 900 s
			// via http.ResponseController and a per-request timeout
			// (http.Server.WriteTimeout is global, so the per-app
			// override can never land at this layer).
			public.WriteTimeout = writeTimeoutOrDefault(deps.writeTimeout)
		}
		if public.MaxHeaderBytes == 0 {
			// Issue #995 Phase 3 / ADR-121: cap the header block on
			// the public listener too. gatewayd-public already
			// enforces 1 MiB, but defence in depth — a
			// misconfigured upstream that bypasses gatewayd-public
			// (loopback-only tests, the legacy single-box path)
			// still hits this cap.
			public.MaxHeaderBytes = int(api.DefaultMaxHeaderBytes)
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
	} else {
		log.Info("gatewayd public listener disabled (FAAS_GATEWAY_LISTEN=off); serving on unix socket only")
	}
	ctrlAddr := controlAddr
	if deps.controlAddr != "" {
		ctrlAddr = deps.controlAddr
	}
	// PR #218 review finding: refuse non-loopback binds for the control
	// listener when the env knob was used. The control mux is
	// unauthenticated by design (operator-prometheus scrape), so an
	// accidental public bind would expose /metrics. The default address
	// is already loopback so this only fires for misconfigured overrides
	// (and silently no-ops for in-process tests that inject via
	// deps.controlAddr on a loopback listener).
	if os.Getenv("FAAS_GATEWAY_CONTROL_LISTEN") != "" {
		if err := assertLoopbackBind(ctrlAddr); err != nil {
			return fmt.Errorf("gatewayd: FAAS_GATEWAY_CONTROL_LISTEN must bind loopback: %w", err)
		}
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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), drain.DrainGrace)
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
		// ADR-068 / Tier A7 split: tlsBundle.Close + tlsCertExpiryCancel
		// moved to gatewayd-public's shutdown path (cmd/gatewayd-public/main.go).
		if deps.synth != nil {
			//nolint:contextcheck // same shutdown-ctx contract as public.Shutdown above.
			_ = deps.synth.Stop(shutdownCtx)
		}
		if deps.nodeCache != nil {
			// Closing every cached *grpc.ClientConn here means
			// in-flight ForwardHTTPStream RPCs see a "transport
			// closing" error → handler maps it to 502; the
			// listener is already draining so no new requests
			// land.
			_ = deps.nodeCache.Close()
		}
		// Issue #587 / PR-A: wait for the per-request drain
		// tracker to flush before exiting. shutdownCtx has
		// already been wired to drain.DrainGrace (25s) — that
		// sits inside systemd's TimeoutStopSec=30s with 5s
		// headroom. The drain sets its own internal `draining`
		// flag so any post-Shutdown stragglers become no-op
		// Begin closures (pkg/gateway/drain.Drain doc).
		//
		// Exit-code discipline (systemd Restart=on-failure
		// contract, pkg/deploycontroller/controller.go:43-115):
		//   clean drain → return nil (no restart)
		//   deadline_exceeded / ctx_cancelled → return ctx.Err()
		//     so systemd restarts the daemon
		// Pre-PR-A this branch returned nil unconditionally,
		// which hid ctx-cancellation bugs from operators. The
		// PR-A fix surfaces them so a hung drain doesn't quietly
		// recycle on the next deploy.
		if deps.drain != nil {
			drainStart := time.Now()
			//nolint:contextcheck // shutdownCtx is intentionally
			// derived from context.Background() (line 1834)
			// because it must outlive the cancelled caller
			// ctx — the deadline budget for the drain comes
			// from drain.DrainGrace, not from the caller's
			// already-cancelled ctx. The Drain contract is to
			// honour shutdownCtx's deadline + cancellation;
			// the `deadline` param on Drain is the
			// upper-bound knob.
			outcome, drainErr := deps.drain.Drain(shutdownCtx, drain.DrainGrace)
			drainElapsed := time.Since(drainStart).Seconds()
			// Issue #587 / PR-A: record the drain histogram on
			// every shutdown so an operator can spot a
			// pattern of forced exits from the dashboard
			// without re-reading the daemon log. Recorded
			// against deps.metrics (gateway.Metrics) so the
			// existing /metrics scrape picks it up alongside
			// the rest of the gateway_* series.
			deps.metrics.ObserveDrainWait("gatewayd-internal", string(outcome), drainElapsed)
			if drainErr != nil {
				log.Warn("gatewayd-internal drain exited non-clean",
					"outcome", string(outcome),
					"max_inflight", deps.drain.MaxInflight(),
					"err", drainErr)
				return drainErr
			}
			if outcome != drain.OutcomeClean {
				log.Warn("gatewayd-internal drain exited non-clean",
					"outcome", string(outcome),
					"max_inflight", deps.drain.MaxInflight())
				return ctx.Err()
			}
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
func (unwiredBackend) Pick(string) gateway.PickResult { return gateway.PickResult{} }
func (unwiredBackend) HealthyCount(string) int        { return 0 }
func (unwiredBackend) Admit(context.Context, string, string, string, string, int) (string, gateway.WakeMethod, bool, error) {
	return "", gateway.WakeMethodUnspecified, false, nil
}

// envOrGateway returns the value of env key, or fallback when unset/empty.
// Named with the daemon prefix to avoid a collision if two daemons are ever
// linked into the same test binary.
func envOrGateway(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// writeTimeoutOrDefault resolves the http.Server.WriteTimeout the
// gatewayd public listener binds to (issue #471 / ADR-047 PR-A).
// The precedence is:
//
//  1. cfg.ResponseWriteTimeout (TOML)        — wire via runDeps.writeTimeout
//  2. api.ResponseWriteTimeoutDefault        — spec §4.1 baseline (300 s)
//  3. 0 (the go interface default)           — never observed by callers
//
// Steps 1+2 collapse to the same constant when the TOML key is missing,
// so the call sites read "use the runtime value or fall back to spec";
// the helper is the single seam that picks the spec baseline so a
// future drift between pkg/api and the gatewayd default surfaces in
// one place rather than at every WriteTimeout literal.
func writeTimeoutOrDefault(d time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return time.Duration(api.ResponseWriteTimeoutDefault) * time.Second
}

// readTimeoutOrDefault mirrors writeTimeoutOrDefault for the
// http.Server.ReadTimeout field (issue #995 Phase 3 / ADR-121).
func readTimeoutOrDefault(d time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return time.Duration(api.GatewaydInternalReadTimeoutSecondsDefault) * time.Second
}

// assertLoopbackBind rejects non-loopback control-listener addresses.
// The check accepts explicit 127.0.0.0/8 and ::1 forms plus the
// "localhost" hostname. Bare ":port" (empty host) is rejected —
// Listen on a bare ":port" binds 0.0.0.0, which is exactly what this
// guard exists to prevent. The PR #218 controlAddr env override only
// lands in the harness today, but a future operator footgun (env in a
// dev systemd unit) would otherwise be a silent /metrics exposure.
func assertLoopbackBind(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("parse %q: %w", addr, err)
	}
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("control listener %q is not loopback; bind 127.0.0.1:9090 (or ::1) only", addr)
}

// weightsStoreAdapter (issue #556 / PR-B) adapts pkg/state.PgStore to
// the gateway.deploymentWeightsStore seam. The gateway package
// deliberately does NOT import pkg/state (same shape as Router above)
// so the hot-path picker has no Postgres dependency surface in tests;
// this adapter lives on the gatewayd-internal side where the
// pkg/state import already exists. It translates state.Deployment to
// gateway.DeploymentWeightsRow (only fields the picker reads).
type weightsStoreAdapter struct {
	store *state.PgStore
}

func (a weightsStoreAdapter) LiveDeployments(ctx context.Context, appID string) ([]gateway.DeploymentWeightsRow, error) {
	deps, err := a.store.LiveDeployments(ctx, appID)
	if err != nil {
		return nil, err
	}
	out := make([]gateway.DeploymentWeightsRow, 0, len(deps))
	for _, d := range deps {
		out = append(out, gateway.DeploymentWeightsRow{
			ID:             d.ID,
			TrafficPercent: d.TrafficPercent,
		})
	}
	return out, nil
}

// buildCentralRateLimitBackend wires the production
// CentralBackend iff deps.pool is non-nil (Postgres reachable)
// (ADR-104 amendment 5, issue #881 Phase 4 C3). Returns
// (nil, nil) when the pool is missing — runWithDeps logs a
// warning and falls back to the noop backend (degraded posture,
// single-box dev with no Postgres still works).
//
// The rps closure looks up the per-plan refill rate via
// api.LimitsFor; an unknown plan (defensive — e.g. a future
// plan addition that hasn't shipped everywhere) degrades to
// (0, false) which the backend treats as "infinite tokens"
// (admit-soft). The closure avoids an explicit pkg/api import
// in this file (state.PGRateLimitBackend accepts the closure
// directly).
func buildCentralRateLimitBackend(pool *pgxpool.Pool, log *slog.Logger) (*state.PGRateLimitBackend, func(plan string) (float64, bool)) {
	if pool == nil {
		return nil, nil
	}
	rps := func(plan string) (float64, bool) {
		p := api.Plan(plan)
		if !p.Valid() {
			return 0, false
		}
		limits, ok := api.LimitsFor(p)
		if !ok {
			return 0, false
		}
		return float64(limits.RateLimitRPS), true
	}
	return state.NewPGRateLimitBackend(pool, rps), rps
}

// rpsKind is a tiny stringifier for the rps closure kind. Used
// in the "rate-limit central mode armed" log line so an
// operator can confirm the closure compiled to the api.LimitsFor
// lookup at boot. Purely cosmetic.
func rpsKind(fn func(plan string) (float64, bool)) string {
	if fn == nil {
		return "noop"
	}
	return "api.LimitsFor"
}
