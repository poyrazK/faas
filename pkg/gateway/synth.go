package gateway

import (
	"context"
	"encoding/base64"
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
	"github.com/onebox-faas/faas/pkg/state"
)

// SynthDispatcher is the slice of the gateway the internal schedd
// RPC needs. Going through Wake/Invoke (rather than reimplementing
// routing + proxying) ensures capacity + plan-quota admission apply
// identically to cron / async / queue-pull / delayed-task traffic,
// and the per-minute sampler picks up the live instance for metering.
//
// Wake is the no-payload path (legacy cron wake-only; back-pressure
// probe). Invoke carries a payload through the wake gate so the
// synthetic HTTP envelope (method + path + body + headers) reaches
// the runner envelope unchanged — the cron rewriting bug fixed by
// Move 1 was the Wake-only path never reaching the runner at all.
type SynthDispatcher interface {
	Wake(ctx context.Context, appID string) error
	Invoke(ctx context.Context, appID string, inv state.Invocation) (state.Invocation, error)
}

// SynthServer is the unix-socket HTTP listener that exposes
// /v1/synthesize (legacy no-payload path) and /v1/invocations:dispatch
// (Move 1 event-shaped path). Both routes share the unix-socket DAC
// auth (ADR-015) — only schedd is in the `faas` group, so the socket
// IS the auth.
//
// The Move 1 follow-up split /v1/synthesize and /v1/invocations:dispatch
// into two routes (rather than one body-discriminated POST) so the
// dispatcher-surface widening above stays load-bearing: a future Move
// 2 surface can extend SynthDispatcher without rewriting the wire.
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
	// Move 1: schedd's drain posts here for event-shaped invocations
	// (async_invoke / queue / delayed_task / cron). The response is
	// the post-dispatch Invocation row (state + result envelope),
	// which schedd's drain stores via Store.CompleteInvocation.
	mux.HandleFunc("/v1/invocations:dispatch", s.handleInvocationDispatch)
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

// invocationDispatchRequest is the body schedd's drain posts.
// InvocationID / Source carry through to the runner envelope as
// x-faas-invocation-id + x-faas-invocation-source so the user's
// function can branch on shape without re-parsing the dispatch
// response.
type invocationDispatchRequest struct {
	InvocationID string            `json:"invocation_id"`
	AppID        string            `json:"app_id"`
	Source       string            `json:"source"` // async_invoke|queue|delayed_task|cron
	Method       string            `json:"method"`
	Path         string            `json:"path"`
	Headers      map[string]string `json:"headers,omitempty"`
	// BodyB64 is base64-encoded so JSON encoding stays trivial and
	// the cron path (no body) ships an empty string by default.
	BodyB64 string `json:"body_b64,omitempty"`
}

func (s *SynthServer) handleInvocationDispatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req invocationDispatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.AppID == "" || req.InvocationID == "" {
		http.Error(w, "app_id + invocation_id required", http.StatusBadRequest)
		return
	}
	method := req.Method
	if method == "" {
		method = http.MethodPost
	}
	path := req.Path
	if path == "" {
		path = "/"
	}
	// body is decoded base64-ish (we accept the literal bytes); keep
	// the field small so the drain's per-tick JSON stays bounded.
	var payload []byte
	if req.BodyB64 != "" {
		// base64.StdEncoding is the default the platform uses for
		// every other envelope (e.g. gateway request bodies, secret
		// ciphertext). Match.
		dec, err := base64Decode(req.BodyB64)
		if err != nil {
			http.Error(w, "body_b64 invalid", http.StatusBadRequest)
			return
		}
		payload = dec
	}
	inv := state.Invocation{
		ID:      req.InvocationID,
		AppID:   req.AppID,
		Source:  state.InvocationSource(req.Source),
		Method:  method,
		Path:    path,
		Payload: payload,
		Headers: jsonOrEmpty(req.Headers),
	}
	// Pre-flush logsanitised fields so a malicious /invocations:dispatch
	// caller cannot forge lines.
	s.log.Debug("gateway synth: invocation dispatched",
		"inv", logsanitize.Field(req.InvocationID),
		"app_id", logsanitize.Field(req.AppID),
		"source", logsanitize.Field(req.Source),
		"method", logsanitize.Field(method),
		"path", logsanitize.Field(path))
	out, err := s.dispatcher.Invoke(r.Context(), req.AppID, inv)
	if err != nil {
		// Transient vs permanent split: any error here means the
		// runner never received the body. schedd retries transient
		// (5s); permanent shapes (no such app) end the row.
		s.log.Warn("gateway synth: invoke", "inv", logsanitize.Field(req.InvocationID), "err", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	s.calls.Add(1)
	w.Header().Set("Content-Type", "application/json")
	// Echo the post-dispatch state + result back so the drain can
	// call CompleteInvocation(result) on the same transaction.
	_ = json.NewEncoder(w).Encode(struct {
		State  string          `json:"state"`
		Result json.RawMessage `json:"result,omitempty"`
	}{string(out.State), out.Result})
}

func jsonOrEmpty(m map[string]string) json.RawMessage {
	if len(m) == 0 {
		return json.RawMessage("{}")
	}
	b, _ := json.Marshal(m)
	return b
}

// base64Decode wraps base64.StdEncoding with an explicit error so the
// synth handler stays readable. Callers MUST reject malformed bodies
// — a forged body_b64 could otherwise smuggle arbitrary bytes into
// the runner envelope (the runner's JSON parser only sees the body,
// not the encoding we used).
func base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
