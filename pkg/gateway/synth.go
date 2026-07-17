package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// SynthDispatcher is the slice of the gateway the internal cron RPC
// needs. Going through Wake (rather than reimplementing routing +
// proxying) ensures capacity + plan-quota admission apply identically
// to cron traffic, and the per-minute sampler picks up the live
// instance for metering. lastSeen is intentionally out of scope here:
// schedd's ReportActivity is instance-scoped, and exposing an
// app-scoped touch is its own PR — for now, cron-fired apps park on
// the next idle boundary, which is fine for v1.
type SynthDispatcher interface {
	Wake(ctx context.Context, appID string) error
}

// SynthServer is the unix-socket HTTP listener that exposes
// POST /v1/synthesize. It exists so schedd can fire synthetic cron
// requests through gatewayd (spec §4.4, M7) without touching the
// public listener.
//
// Auth: the unix-socket DAC model (ADR-015). Listeners run as mode 0660
// group `faas`; only schedd is in that group. No header auth, no
// token — the socket IS the auth.
type SynthServer struct {
	socketPath string
	dispatcher SynthDispatcher
	log        *slog.Logger
	srv        *http.Server
	calls      atomic.Int64
}

// NewSynthServer wires the listener. socketPath is the unix-domain
// path (e.g. /run/faas/gatewayd-internal.sock). dispatcher is the
// gateway's wake/proxy path.
func NewSynthServer(socketPath string, dispatcher SynthDispatcher, log *slog.Logger) *SynthServer {
	if log == nil {
		log = slog.Default()
	}
	mux := http.NewServeMux()
	s := &SynthServer{socketPath: socketPath, dispatcher: dispatcher, log: log}
	mux.HandleFunc("/v1/synthesize", s.handleSynthesize)
	mux.HandleFunc("/healthz", s.handleHealthz)
	// ReadHeaderTimeout pins the Slowloris attack surface (gosec G112).
	// The unix socket is DAC-gated (ADR-015), but we set the timeout
	// anyway — defense in depth + uniform config.
	s.srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

// Start binds the unix socket and starts serving. Returns when the
// listener is ready; subsequent Serve blocks until the server stops.
// Caller is responsible for the goroutine.
func (s *SynthServer) Start() error {
	// Remove any stale socket from a previous run (server crashed, etc).
	// The platform treats the socket as owned by this process; recreate
	// is safer than failing on EADDRINUSE.
	if err := os.Remove(s.socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("gateway synth: remove stale socket: %w", err)
	}
	lis, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("gateway synth: listen %s: %w", s.socketPath, err)
	}
	// Mode 0660 group `faas` — only schedd in that group can dial.
	// The wire package's ListenOrRecreateByName handles chmod in
	// production; this server keeps the lock tight regardless of who
	// ran cmd/gatewayd.
	if err := os.Chmod(s.socketPath, 0o660); err != nil {
		_ = lis.Close()
		return fmt.Errorf("gateway synth: chmod: %w", err)
	}
	s.log.Info("gateway synth: listening", "socket", s.socketPath)
	go func() {
		if err := s.srv.Serve(lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Warn("gateway synth: serve", "err", err)
		}
	}()
	return nil
}

// Stop tears down the listener. Idempotent.
func (s *SynthServer) Stop(ctx context.Context) error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}

// SocketPath returns the bound socket. Wired in cmd/gatewayd so
// schedd knows where to dial.
func (s *SynthServer) SocketPath() string { return s.socketPath }

// Calls returns the number of synthesize requests served. Metric-only;
// tests assert on it to confirm a fake-clock advance produced exactly
// one cron fire.
func (s *SynthServer) Calls() int64 { return s.calls.Load() }

func (s *SynthServer) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// synthesizeRequest is the JSON body schedd posts.
type synthesizeRequest struct {
	AppID  string `json:"app_id"`
	Method string `json:"method"`
	Path   string `json:"path"`
}

func (s *SynthServer) handleSynthesize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req synthesizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.AppID == "" || req.Path == "" {
		http.Error(w, "app_id + path required", http.StatusBadRequest)
		return
	}
	method := req.Method
	if method == "" {
		method = http.MethodPost
	}
	// method is recorded in the synth call log so the dashboard can
	// distinguish cron-fired POSTs from GETs once the proxy-side
	// follow-up routes a real request through gatewayd.
	//
	// The values flow from the JSON request body — CodeQL's
	// go/log-injection (CWE-117) flags them as attacker-controlled
	// regardless of the unix-socket DAC check (ADR-015). Even though
	// slog's JSON encoder escapes \n / \r, defense-in-depth is to
	// strip the control characters before logging so a compromised
	// schedd (or anything else in the `faas` group) cannot forge
	// lines or hide activity in the log stream.
	logAppID := logsanitize.Field(req.AppID)
	logMethod := logsanitize.Field(method)
	logPath := logsanitize.Field(req.Path)
	s.log.Debug("gateway synth: dispatched", "app_id", logAppID, "method", logMethod, "path", logPath)
	if err := s.dispatcher.Wake(r.Context(), req.AppID); err != nil {
		s.log.Warn("gateway synth: wake", "app_id", logAppID, "path", logPath, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.calls.Add(1)
	w.WriteHeader(http.StatusOK)
}
