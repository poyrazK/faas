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
//     gatewayd's edge-verifying proxy forwards here — never
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
	"time"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
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
}

// DefaultSocketPath is the ADR-015 / spec §11 location for the
// githubd gRPC socket.
const DefaultSocketPath = "/run/faas/githubd.sock"

// DefaultHTTPAddr is the loopback listener gatewayd reverse-proxies
// /webhooks/github to. Spec §11: githubd is loopback-only.
const DefaultHTTPAddr = "127.0.0.1:8083"

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
	gsrv := grpc.NewServer()
	s.GRPCServer.Register(gsrv)

	// HTTP loopback listener for /webhooks/github.
	httpHandler := s.WebhookLoopbackHandler()
	httpSrv := &http.Server{
		Addr:              httpAddr,
		Handler:           httpHandler,
		ReadHeaderTimeout: 10 * time.Second,
	}
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
// serves. The proxy in cmd/gatewayd HMAC-verifies the request
// before forwarding; this handler re-verifies (defense in depth)
// and then dispatches via Service.HandlePushRequest.
//
// On success: 200 with the deployment_id (or "ignored" if the
// push didn't match any binding). On verify failure: 401. On
// decode failure: 400. On internal error: 500.
func (s *Server) WebhookLoopbackHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := readBody(w, r)
		if err != nil {
			s.Log.Warn("githubd webhook body read", "err", err)
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
			return
		}
		// Re-verify the HMAC. The gatewayd proxy already did this,
		// but a misconfigured proxy (no secret) must NOT bypass the
		// daemon-side check.
		sig := r.Header.Get("X-Hub-Signature-256")
		secret := webhookSecretFromHeader(r)
		if secret == nil || !verifyOrLog(s, body, sig, secret) {
			http.Error(w, "signature verification failed", http.StatusUnauthorized)
			return
		}
		depID, err := s.Service.HandlePushRequest(r.Context(), body)
		if err != nil {
			if IsNoBinding(err) {
				// 200 + ignored payload — the push doesn't apply to
				// any of our apps. GitHub's webhook retry policy
				// respects a 2xx response, so this is the canonical
				// "not mine, do not retry" reply.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status":"ignored","reason":"no_binding"}`))
				return
			}
			s.Log.Error("githubd webhook handle", "err", err)
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Marshal the depID into JSON so the response body is
		// safely escaped (the depID is operator-controlled today
		// but a future caller might thread a tainted string
		// through this path).
		respBody, _ := json.Marshal(struct {
			Status       string `json:"status"`
			DeploymentID string `json:"deployment_id"`
		}{Status: statusQueued, DeploymentID: depID})
		_, _ = w.Write(respBody)
	})
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

// webhookSecretFromHeader is the slice-7 hook for an out-of-band
// secret. Today we trust the gatewayd proxy; slice 8 adds the
// per-tenant secret rotation that justifies this hook.
func webhookSecretFromHeader(_ *http.Request) []byte {
	// Placeholder — slice 8 reads from the per-account config table.
	// Returning nil short-circuits verify so an unconfigured
	// installation refuses all webhooks (closed by default).
	return nil
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
// (gatewayd → githubd → apid), so this RPC stays Unimplemented in
// the production daemon — the inbound path uses
// Service.HandlePushRequest, not gRPC. Tests still exercise the
// type satisfaction via the adapter.
func (a *grpcSvcAdapter) CreateDeploymentFromPush(repoFullName, ref, commitSHA, pusher string) (string, string, error) {
	a.svc.Log.Info("githubd grpc CreateDeploymentFromPush (no-op in slice 7; webhook path uses Service.HandlePushRequest)",
		"repo", repoFullName, "ref", ref, "sha", commitSHA, "pusher", pusher)
	return "", "", nil
}

// WriteCheck is the slice-8 seam — slice 7 leaves it as a log-
// only stub so the smoke test can still observe the call ordering.
func (a *grpcSvcAdapter) WriteCheck(repoFullName, commitSHA string, phase githubdgrpc.CheckPhase, _, summary string) error {
	a.svc.Log.Info("githubd grpc WriteCheck (slice-7 stub)",
		"repo", repoFullName, "sha", commitSHA, "phase", phase, "summary", summary)
	return nil
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
func (a *grpcSvcAdapter) VerifyInstallation(installationID int64) (bool, string, error) {
	a.svc.Log.Warn("githubd grpc VerifyInstallation via webhook svc (no OAuth)", "installation_id", installationID)
	return false, "", fmt.Errorf("githubd: VerifyInstallation requires the slice-8 OAuth path (install=%d)", installationID)
}
