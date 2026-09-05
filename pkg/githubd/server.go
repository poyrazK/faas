// githubd server wiring (spec §14 M7.5, ADR-012, ADR-015).
//
// Two listeners run inside cmd/githubd:
//
//  1. gRPC server on a unix socket at /run/faas/githubd.sock,
//     mode 0660, group `faas` (ADR-015). apid is the only caller
//     in v1.0. The gRPC surface is the slice 1 githubdgrpc.Server;
//     slices 7-8 swap Unimplemented for real handlers.
//
//  2. Plain HTTP webhook listener on 127.0.0.1:8083. Only
//     gatewayd-internal's edge-verifying proxy forwards here — never
//     reachable from the public internet (§11 single-public-
//     listener invariant). The handler is the bridge between
//     HTTP POSTs and Service.HandlePushRequest.
//
// The two listeners share ctx cancellation and live in the same
// goroutine fan-out used by every other daemon in the fleet.
package githubd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/trace"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
)

// Server bundles the gRPC + HTTP listeners. cmd/githubd builds it
// from runDeps and calls Start; the returned errors feed the
// shared errc fan-out.
type Server struct {
	// Service is the business-logic core (HandlePushRequest today;
	// the OAuth/install-token work lands in slice 8 via additional
	// methods on the same struct).
	Service *Service

	// GRPCServer is the registered Server; nil → no gRPC listener.
	GRPCServer *githubdgrpc.Server

	// Ops holds the per-daemon Prometheus registry. Wired via
	// WithOpsMetrics so callers (cmd/githubd) control the registry
	// lifecycle. WebhookLoopbackHandler mounts Ops.Handler() at
	// GET /metrics on the same loopback listener as
	// POST /webhooks/github.
	Ops *wire.OpsMetrics

	// SocketPath is the unix socket path when ListenAddr is empty
	// (default /run/faas/githubd.sock).
	SocketPath string

	// ListenAddr is the location-transparent gRPC listen target
	// (issue #95, ADR-025). Accepts unix:///path or tcp://host:port.
	// Empty falls back to unix://+SocketPath.
	ListenAddr string `toml:"listen_addr"`

	// Server-mTLS material (issue #95). All three paths empty =>
	// no TLS; all three set => mTLS. Partial cluster => startup error.
	TLSCertPath string `toml:"tls_cert_path"`
	TLSKeyPath  string `toml:"tls_key_path"`
	TLSCAPath   string `toml:"tls_ca_path"`

	// HTTPAddr is the loopback bind address (default 127.0.0.1:8083).
	HTTPAddr string

	// Log receives structured events from both listeners.
	Log *slog.Logger

	// SecretResolver supports legacy private senders that attach an
	// X-GitHub-Installation-id header. Normal GitHub App deliveries use the
	// App-level WebhookSecret below because GitHub cannot select a secret by
	// installation before signing the request.
	SecretResolver WebhookSecretResolver

	// WebhookSecret is the GitHub App-level webhook secret. GitHub signs every
	// installation delivery with this one secret and does not send an
	// installation-id header. SecretResolver remains as a compatibility
	// fallback for older private senders.
	WebhookSecret []byte

	// Deliveries is the durable delivery inbox. When wired, the HTTP handler
	// acknowledges after Enqueue and the worker performs fetch/scan/build work.
	Deliveries WebhookDeliveryStore

	// ReadyFunc + ReasonFunc are the /readyz hooks (issue #571
	// PR-A2). Wired by cmd/githubd/main.go to
	// wire.ReadyzProbe.ReadyFunc/ReasonFunc. Both nil = skip
	// /readyz registration (test-friendly default); both set =
	// register /healthz + /readyz via wire.ControlMuxLite on
	// the same loopback mux as /metrics + /webhooks/github.
	ReadyFunc  func() bool
	ReasonFunc func() string
}

// DefaultSocketPath is the ADR-015 / spec §11 location for the
// githubd gRPC socket.
const DefaultSocketPath = "/run/faas/githubd.sock"

// DefaultHTTPAddr is the loopback listener gatewayd-internal reverse-proxies
// /webhooks/github to. Spec §11: githubd is loopback-only.
const DefaultHTTPAddr = "127.0.0.1:8083"

// WithOpsMetrics attaches a per-daemon Prometheus registry. Required
// by Start: WebhookLoopbackHandler exposes the registry at GET /metrics
// on the loopback mux, and the inbound webhook observer records into the
// same registry. Mirrors pkg/builderd/builderd.go WithOpsMetrics (PR #124,
// ADR-030) and the setters on pkg/imaged/Handler and cmd/apid/server.
func (s *Server) WithOpsMetrics(ops *wire.OpsMetrics) *Server {
	s.Ops = ops
	return s
}

// NewWebhookHTTPServer returns a *http.Server configured with the
// ADR-122 canonical webhook-listener shape. Exported so the test
// suite can inspect the actual struct the production code
// constructs — a future edit that drops a knob (ReadTimeout,
// WriteTimeout, IdleTimeout, MaxHeaderBytes) is caught by
// TestWebhookServer_AppliesCanonicalShape instead of silently
// shipping a half-hardened listener.
//
// Knob set:
//   - ReadHeaderTimeout=10s (pre-existing Slowloris guard)
//   - ReadTimeout=30s (webhook variant — 10 MiB upload budget)
//   - WriteTimeout=30s (mirror)
//   - IdleTimeout=60s (keep-alive ceiling)
//   - MaxHeaderBytes=api.DefaultMaxHeaderBytes (1 MiB)
//
// The handler is whatever WebhookLoopbackHandler (or a stub in
// tests) returns. The server is not yet listening; callers must
// net.Listen + srv.Serve (or use Server.Start which does both).
func NewWebhookHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       time.Duration(api.WebhookReadTimeoutSecondsDefault) * time.Second,
		WriteTimeout:      time.Duration(api.WebhookWriteTimeoutSecondsDefault) * time.Second,
		IdleTimeout:       time.Duration(api.WebhookIdleTimeoutSecondsDefault) * time.Second,
		MaxHeaderBytes:    int(api.DefaultMaxHeaderBytes),
	}
}

// Start binds the gRPC + HTTP listeners, wires the handlers, and
// returns when both are serving. The returned cleanup func
// releases both; the returned errc channel reports listener errors
// so the caller's select can shut everything down on first failure.
//
// Issue #95: the gRPC listen target is now location-transparent.
// `ListenAddr` takes precedence; if empty, falls back to the
// `unix://` + `SocketPath` default. When the mTLS cluster (cert,
// key, CA) is fully populated, the listener is wrapped in a
// `tls.NewListener`. Partial clusters fail closed at wire.Listen.
func (s *Server) Start(ctx context.Context) (func(context.Context) error, <-chan error, error) {
	if s.Log == nil {
		s.Log = slog.Default()
	}
	if s.Service == nil {
		return nil, nil, errors.New("githubd: Service is required")
	}
	if s.GRPCServer == nil {
		s.GRPCServer = githubdgrpc.New(s.grpcAdapter(), wire.NewOpsMetrics("githubd"), s.Log)
	}

	socketPath := s.SocketPath
	if socketPath == "" {
		socketPath = DefaultSocketPath
	}
	httpAddr := s.HTTPAddr
	if httpAddr == "" {
		httpAddr = DefaultHTTPAddr
	}

	// gRPC listen target — ListenAddr wins, else unix://+SocketPath.
	listenTarget := s.ListenAddr
	if listenTarget == "" {
		listenTarget = "unix://" + socketPath
	}

	// Server-mTLS cluster (issue #95). Empty cluster → nil TLS;
	// full cluster → mTLS. Partial cluster fails closed inside
	// LoadServerTLSConfig.
	serverTLS, err := wire.LoadServerTLSConfig(s.TLSCertPath, s.TLSKeyPath, s.TLSCAPath)
	if err != nil {
		return nil, nil, fmt.Errorf("githubd: tls: %w", err)
	}

	gLis, err := wire.Listen(ctx, listenTarget, serverTLS)
	if err != nil {
		return nil, nil, fmt.Errorf("githubd: listen %q: %w", listenTarget, err)
	}
	gsrv := grpc.NewServer(append(
		wire.ServerCredsOrEmpty(serverTLS),
		wire.TraceServerOptions()...,
	)...)
	s.GRPCServer.Register(gsrv)

	// HTTP loopback listener for /webhooks/github. ADR-122: the
	// listener is built via the exported NewWebhookHTTPServer
	// helper so a future edit that drops a knob from the canonical
	// shape surfaces in TestWebhookServer_AppliesCanonicalShape
	// (pkg/githubd/server_test.go) rather than silently in
	// production. The webhook variant uses ReadTimeout=30s (not
	// 10s like the pure-metrics family) to budget a slow GitHub
	// webhook client uploading a 10 MiB body at the readBody cap
	// (pkg/githubd/server.go:323). Body cap stays; new server-
	// level knobs are defence-in-depth against header smuggling
	// + runaway scrapers holding a connection open.
	httpHandler := trace.HTTPHandler("githubd", s.WebhookLoopbackHandler())
	httpSrv := NewWebhookHTTPServer(httpAddr, httpHandler)
	hLis, err := net.Listen("tcp", httpAddr)
	if err != nil {
		_ = gLis.Close()
		return nil, nil, fmt.Errorf("githubd: http listen %q: %w", httpAddr, err)
	}

	// Fan out both Serve calls. Errors flow through errc so the
	// caller's select can shut everything down on first failure.
	errc := make(chan error, 2)
	go func() {
		s.Log.Info("githubd gRPC listening", "target", listenTarget)
		if err := gsrv.Serve(gLis); err != nil {
			errc <- fmt.Errorf("githubd gRPC serve: %w", err)
		}
	}()
	go func() {
		s.Log.Info("githubd HTTP listening", "addr", httpAddr)
		if err := httpSrv.Serve(hLis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- fmt.Errorf("githubd HTTP serve: %w", err)
		}
	}()
	if s.Deliveries != nil {
		go RunWebhookDeliveryWorker(ctx, s.Deliveries, s.Service, s.Log)
	}
	//nolint:gosec // shutdown ctx must outlive caller ctx (net/http Shutdown contract).
	go func() {
		<-ctx.Done()
		s.Log.Info("githubd shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		//nolint:contextcheck // shutdown ctx must outlive caller ctx (net/http contract).
		_ = httpSrv.Shutdown(shutdownCtx)
		gsrv.GracefulStop()
	}()

	cleanup := func(ctx context.Context) error {
		//nolint:contextcheck // see above.
		_ = httpSrv.Shutdown(ctx)
		gsrv.GracefulStop()
		return nil
	}
	return cleanup, errc, nil
}

// WebhookLoopbackHandler returns the http.Handler the HTTP listener
// serves. The proxy in cmd/gatewayd-internal/HMAC-verifies the request
// before forwarding; this handler re-verifies (defense in depth)
// and then dispatches via Service.HandlePushRequest.
//
// Loopback-only invariant (§11 single-public-listener): the listener
// binds to 127.0.0.1:8083; gatewayd-internal's edge-verifying proxy is the only
// reachable caller. Adding GET /metrics for ops scraping on the same
// mux is safe — there's no path from the public internet to it.
//
// On success: 200 with the deployment_id (or "ignored" if the
// push didn't match any binding). On verify failure: 401. On
// decode failure: 400. On internal error: 500.
func (s *Server) WebhookLoopbackHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/webhooks/github", s.handleWebhookPush)
	if s.Ops != nil {
		// Nil s.Ops = metrics not wired. Still serve the webhook path
		// (the daemon stays up); just skip /metrics so a partially
		// configured unit test doesn't expose a stray handler.
		mux.Handle("/metrics", s.Ops.Handler())
	}
	// Issue #571 / PR-A2: operator-side /healthz + /readyz on the
	// loopback mux. Both ReadyFunc + ReasonFunc must be wired
	// (single-nil = skip; the boot path is responsible for
	// passing both). ControlMuxLite is the canonical shape —
	// /readyz returns 503 with the failing reason when the
	// probe is degraded.
	if s.ReadyFunc != nil && s.ReasonFunc != nil {
		wire.ControlMuxLite(mux, s.ReadyFunc, s.ReasonFunc)
	}
	return mux
}

// handleWebhookPush is the POST /webhooks/github receiver — split out
// from the mux so a single named function shows up in profiles / Go's
// recovery trace rather than a closure.
func (s *Server) handleWebhookPush(w http.ResponseWriter, r *http.Request) {
	const op = "webhook_push"
	start := time.Now()
	// observe is the nil-safe observe closure: when Ops isn't wired
	// (a unit test that builds a Server directly), every exit below
	// becomes a no-op instead of a nil-deref panic. Captures `s` and
	// `start` by reference so the per-call start time stays correct.
	// Same pattern as apid's observeWrap nil-safety (review finding
	// #3 on PR #132).
	observe := func(err error) {
		if s.Ops != nil {
			s.Ops.Observe(op, time.Since(start), err)
		}
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		observe(errors.New("githubd: webhook method not allowed"))
		return
	}
	body, err := readBody(w, r)
	if err != nil {
		s.Log.Warn("githubd webhook body read", "err", err)
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		observe(err)
		return
	}
	// Re-verify the HMAC. The gateway proxy already did this, but a
	// misconfigured proxy must not bypass the daemon-side check. Standard
	// GitHub App deliveries use one App-level secret; the scoped resolver is
	// retained only for legacy private senders.
	sig := r.Header.Get("X-Hub-Signature-256")
	secret := s.WebhookSecret
	if len(secret) == 0 {
		secret = webhookSecretFromHeader(r.Context(), r, s.SecretResolver)
	}
	if secret == nil || !verifyOrLog(s, body, sig, secret) {
		http.Error(w, "signature verification failed", http.StatusUnauthorized)
		observe(errors.New("githubd: webhook signature invalid"))
		return
	}
	eventType := r.Header.Get("X-GitHub-Event")
	if eventType == "ping" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
		observe(nil)
		return
	}
	if eventType != "" && eventType != "push" && eventType != "pull_request" {
		http.Error(w, "unsupported event", http.StatusBadRequest)
		observe(errors.New("githubd: unsupported webhook event"))
		return
	}
	if s.Deliveries != nil {
		deliveryID := r.Header.Get("X-GitHub-Delivery")
		if eventType == "" || deliveryID == "" {
			http.Error(w, "missing GitHub event or delivery id", http.StatusBadRequest)
			observe(errors.New("githubd: missing webhook metadata"))
			return
		}
		inserted, enqueueErr := s.Deliveries.Enqueue(r.Context(), WebhookDelivery{
			DeliveryID: deliveryID,
			EventType:  eventType,
			Payload:    body,
		})
		if enqueueErr != nil {
			s.Log.Error("githubd webhook enqueue", "err", enqueueErr)
			http.Error(w, "internal", http.StatusInternalServerError)
			observe(enqueueErr)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		if inserted {
			_, _ = w.Write([]byte(`{"status":"accepted"}`))
		} else {
			_, _ = w.Write([]byte(`{"status":"duplicate"}`))
		}
		observe(nil)
		return
	}
	// Compatibility path for unit tests and deliberately store-less
	// embeddings. Production always wires Deliveries.
	if eventType == "pull_request" {
		result, err := s.Service.handlePullRequest(r.Context(), body)
		s.writeWebhookResult(w, result, err, observe)
		return
	}
	result, err := s.Service.HandlePushRequest(r.Context(), body)
	s.writeWebhookResult(w, result, err, observe)
}

func (s *Server) writeWebhookResult(w http.ResponseWriter, result reconcile.Result, err error, observe func(error)) {
	if err != nil {
		if IsNoBinding(err) {
			// 200 + ignored payload — the push doesn't apply to
			// any of our apps. GitHub's webhook retry policy
			// respects a 2xx response, so this is the canonical
			// "not mine, do not retry" reply. From githubd's POV
			// the call succeeded, so err=nil on observe (the §12
			// dashboard counts it as a no-op dispatch, not an error).
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ignored","reason":"no_binding"}`))
			observe(nil)
			return
		}
		if IsIgnored(err) {
			// Feature-branch push — the production-branch guard
			// tripped. Same 200-ignored shape as no_binding, but
			// with reason=feature_branch so dashboards can group
			// these together.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ignored","reason":"feature_branch"}`))
			observe(nil)
			return
		}
		s.Log.Error("githubd webhook handle", "err", err)
		http.Error(w, "internal", http.StatusInternalServerError)
		observe(err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// PR-GH.5: the response carries the reconcile Result + the
	// per-app build fan-out. The wire shape is:
	//   {status: "queued", added: N, changed: N, removed: N,
	//    build_ids: [...]} when the push reconciles + enqueues
	// builds. The previous PR-GH.4 response shape
	// ({status:queued, added:N, changed:N, removed:N}) is
	// forward-compatible — build_ids is additive.
	respBody, _ := json.Marshal(struct {
		Status   string   `json:"status"`
		Added    int      `json:"added"`
		Changed  int      `json:"changed"`
		Removed  int      `json:"removed"`
		BuildIDs []string `json:"build_ids"`
	}{Status: statusQueued, Added: len(result.Added), Changed: len(result.Changed), Removed: len(result.Removed), BuildIDs: result.BuildIDs})
	_, _ = w.Write(respBody)
	observe(nil)
}

// readBody is split out so the 413 path can fail fast without
// allocating the whole payload.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	const maxBytes = 10 << 20 // 10 MiB; pushes are <10 MB typically
	return readAllLimited(w, r.Body, maxBytes)
}

func readAllLimited(w http.ResponseWriter, rc interface {
	Read(p []byte) (int, error)
	Close() error
}, max int64) ([]byte, error) {
	// Local helper that mirrors http.MaxBytesReader but is testable
	// without the http.ResponseWriter coupling.
	limited := http.MaxBytesReader(w, readCloser{rc}, max)
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := limited.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			if errors.Is(err, errTooLarge) || bufErrTooLarge(err) {
				return nil, errTooLarge
			}
			return buf, nil // EOF
		}
	}
}

// readCloser adapts the body reader interface to io.ReadCloser for
// MaxBytesReader without pulling in io.ReadAll twice.
type readCloser struct {
	inner interface {
		Read(p []byte) (int, error)
		Close() error
	}
}

func (r readCloser) Read(p []byte) (int, error) { return r.inner.Read(p) }
func (r readCloser) Close() error               { return r.inner.Close() }

var errTooLarge = errors.New("githubd: payload too large")

func bufErrTooLarge(err error) bool {
	// http.MaxBytesReader returns *http.MaxBytesError; the error
	// message string is the only portable check across Go versions
	// without importing the unexported type.
	return err != nil && (err.Error() == "http: request body too large")
}

// webhookSecretFromHeader is the legacy installation-scoped secret
// seam. Looks up the bytea secret
// for the given installation via the configured WebhookSecretResolver
// (production: PGWebhookSecretResolver). When the resolver is nil,
// returns nil — the verify step short-circuits, the webhook is
// rejected, and the Prometheus counter
// `githubd_webhook_secret_total{status="resolver_nil"}` is incremented
// so the on-call sees the misconfigured-box signal.
//
// Returns nil (fail-closed) when:
//   - The X-GitHub-Installation-id header is missing or zero.
//   - The resolver returns errSecretNotFound (no row for this install).
//     Normal GitHub App traffic is authenticated by WebhookSecret before this
//     compatibility path is considered.
//   - The resolver returns any other error (DB outage). The on-call
//     sees `githubd_webhook_secret_total{status="db_error"}` spike.
func webhookSecretFromHeader(ctx context.Context, r *http.Request, resolver WebhookSecretResolver) []byte {
	if resolver == nil {
		return nil // fail-closed; the boot path must wire a resolver
	}
	header := r.Header.Get("X-GitHub-Installation-id")
	if header == "" {
		return nil
	}
	instID, perr := strconv.ParseInt(header, 10, 64)
	if perr != nil || instID == 0 {
		return nil
	}
	secret, err := resolver.Resolve(ctx, instID)
	if err != nil {
		return nil // fail-closed; caller logs the metric
	}
	return secret
}

func verifyOrLog(s *Server, body []byte, sig string, secret []byte) bool {
	// Reuse the package-level verifier so the proxy and the
	// listener cannot drift on the algorithm.
	return verifySHA256(body, sig, secret) == nil
}

// verifySHA256 is split out so the test can stub webhookSecretFromHeader.
func verifySHA256(body []byte, header string, secret []byte) error {
	// The proxy already verifies; this is defense in depth. We
	// re-import VerifyPushSignature via the package alias to avoid
	// a circular dep.
	return Verify(body, header, secret)
}

// Verify is the package-level re-export so server.go doesn't have
// to import githubd from inside the githubd package (would
// circular). Tests bypass this and call VerifyPushSignature
// directly.
func Verify(body []byte, header string, secret []byte) error {
	return VerifyPushSignature(body, header, secret)
}

// grpcAdapter bridges the githubd.Service business object onto the
// gRPC Service interface (pkg/githubdgrpc). Slice 7 only wires the
// two push-handling methods; slice 8 fills in the OAuth + binding
// RPCs (UnimplementedService covers those today).
type grpcSvcAdapter struct {
	githubdgrpc.UnimplementedService

	svc *Service
}

func (s *Server) grpcAdapter() githubdgrpc.Service {
	return &grpcSvcAdapter{svc: s.Service}
}

// CreateDeploymentFromPush is the gRPC entry that the apid webhook
// bridge (slice 8) calls. For slice 7 githubd is the inbound caller
// (gatewayd-internal → githubd → apid), so this RPC stays Unimplemented in
// the production daemon — the inbound path uses
// Service.HandlePushRequest, not gRPC. Tests still exercise the
// type satisfaction via the adapter.
func (a *grpcSvcAdapter) CreateDeploymentFromPush(repoFullName, ref, commitSHA, pusher string) (string, string, error) {
	a.svc.Log.Info("githubd grpc CreateDeploymentFromPush (no-op in slice 7; webhook path uses Service.HandlePushRequest)",
		"repo", repoFullName, "ref", ref, "sha", commitSHA, "pusher", pusher)
	return "", "", nil
}

// WriteCheck panics if reached — production wires
// *githubd.RealService directly as the gRPC Service (see
// cmd/githubd/main.go::gRPCImpl), bypassing this adapter. The
// slice-7 smoke test path that called this stub was retired in
// PR-D; panicking here makes a regression loud rather than
// silently dropping the check-run write. The slice-7 test
// pkg/githubdgrpc/bufconn_test.go uses RealService directly
// (see TestService_WriteCheck_RoundTrip), so the panic is
// unreachable from the production test surface.
func (a *grpcSvcAdapter) WriteCheck(repoFullName, commitSHA string, phase githubdgrpc.CheckPhase, _, summary string) error {
	panic(fmt.Sprintf("githubd: grpcSvcAdapter.WriteCheck called (repo=%s sha=%s phase=%v summary=%s) — production wires *RealService directly; this stub is retired per PR-D",
		repoFullName, commitSHA, phase, summary))
}

// VerifyInstallation forwards the install verification request to
// the githubd.Service. apid's /oauth/callback handler (cmd/apid/
// handlers_oauth.go) is the only caller; a real install_id
// confirms the customer actually finished the GitHub App install
// flow before we persist a (app → install_id) binding (review
// finding #1+#2 closure).
//
// Note: in production cmd/githubd wires RealService directly as the
// gRPC Service (bypassing this adapter), so this method is the
// slice-7 webhook-path fallback. It returns an error rather than
// a silent false because the webhook svc has no OAuth credentials
// and we don't want a misconfigured wiring to silently 200 an
// unverified install_id.
func (a *grpcSvcAdapter) VerifyInstallation(installationID int64, expectedLogin string) (bool, string, string, error) {
	a.svc.Log.Warn("githubd grpc VerifyInstallation via webhook svc (no OAuth)", "installation_id", installationID, "expected_login", expectedLogin)
	return false, "", "", fmt.Errorf("githubd: VerifyInstallation requires the slice-8 OAuth path (install=%d)", installationID)
}
