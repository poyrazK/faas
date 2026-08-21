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

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

// batchDispatchStatus values are the per-record terminal states the
// batch dispatch handler writes back to trigger_records. They are
// package-level constants so goconst doesn't flag the literal three
// times across handleInvocationDispatchBatch and its sibling
// response-parsing helpers.
const (
	batchDispatchStatusSucceeded = "succeeded"
	batchDispatchStatusRetry     = "retry"
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
// Issue #675: in the Tier A7 production shape (`FAAS_GATEWAY_LISTEN=off`)
// the customer publicHandler also rides this socket — gatewayd-public
// forwards every inbound request here, and the mux routes synth paths
// to the synth handlers and everything else to publicHandler. The
// unified mux is built by the caller after SetHandler is invoked
// (the publicHandler depends on apid + handler wiring that isn't
// ready when synth is constructed at boot).
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
	mux        *http.ServeMux
	calls      atomic.Int64
	// internalSvcVerifier (ADR-119) is the per-service
	// public-key allowlist consulted by the synth-side gate
	// (synth_internal_only.go). nil = gate disabled; an
	// internal_only cron request with no verifier wired 500s
	// (operator_error). Wired by
	// SynthServer.WithInternalSvcVerifier (cmd/gatewayd-internal/
	// run.go) — same bridge as the HTTP-side gate.
	internalSvcVerifier InternalSvcVerifier
	// metrics (ADR-119) is the gateway-wide Metrics instance.
	// The synth-side gate increments
	// gateway_internal_auth_match_total{outcome} via the
	// same ObserveInternalAuthMatch the HTTP-side gate uses
	// — single counter, both gates write to it. nil = silent
	// (unit tests). Wired by SynthServer.WithMetrics.
	metrics *Metrics
	// synthAuditEmit (ADR-119) emits the
	// instances.public_auth_internal_* audit rows for cron-
	// fired wake attempts. nil = audit-disabled (unit tests);
	// the gate still increments the metric and writes the
	// 403 problem. Production wires the same auditor the
	// Handler uses (cmd/gatewayd-internal/edge_rules.go
	// constructs the gatewaydAuditor and passes it to both
	// Handler.WithEdgeRules and SynthServer.WithAudit).
	synthAuditEmit func(ctx context.Context, kind string, subject *string, data map[string]any)
	// appPublicAuthMode (ADR-119) returns the public_auth_mode
	// for a given appID. nil = every app treated as "open"
	// (no gate, no JWT check). Wired by SynthServer.WithAppModeLookup;
	// production wires the same per-app cache the Handler
	// consults (cmd/gatewayd-internal/run.go). The lookup
	// takes a context so the request's ctx (with timeout /
	// cancel chain) flows into the per-app store call.
	appPublicAuthMode func(ctx context.Context, appID string) string
	// membersOnlyChecker (ADR-123) is the DB-side bridge for
	// the public_auth_mode='members_only' synth-side gate
	// (synth_members_only.go). Same shape as
	// internalSvcVerifier above; nil = gate disabled, the
	// gate 500s rather than silently letting every
	// members_only cron request through.
	membersOnlyChecker authz.OrgMemberChecker
	// membersOnlyPrincipal (ADR-123) is the cookie-side
	// bridge. Cron has no cookie, so the dominant case
	// (every /v1/synthesize from the schedd cron driver) is
	// denied at this gate. A dashboard-fired
	// /v1/invocations:dispatch with a faas_sid cookie
	// would pass this check and reach the org-membership
	// verification.
	membersOnlyPrincipal CookiePrincipalExtractor
	// appOrgID (ADR-123) returns the org_id for a given
	// appID via the per-app cache. nil = gate disabled
	// (same misconfig posture as appPublicAuthMode above).
	appOrgID func(ctx context.Context, appID string) string
}

// NewSynthServer wires the unix-socket listener on socketPath with the
// synth dispatcher. The internal http.ServeMux is constructed here with
// the three synth routes registered; callers that want to also serve
// customer traffic on the same socket call SetHandler with a wrapping
// mux that includes the customer publicHandler (issue #675).
//
// Envelope (ADR-122 / post-merge audit, issue #995 closure): the
// listener installs the canonical metrics variant — RHT=5s (pre-existing
// H2C negotiation guard) + RT=10s + WT=10s + IT=60s + MHB=1 MiB. The
// RHT=5s is one knob tighter than the 10s canonical metrics variant
// because the unix socket is DAC-gated (ADR-015) and the only
// legitimate clients (schedd + the unified-mux Tier A7 caller) finish
// their headers in milliseconds. The remaining four knobs are the
// canonical metrics shape from pkg/api/limits.go::Metrics*SecondsDefault.
//
// The unix socket is DAC-gated (ADR-015), but we set the timeouts
// anyway — defense in depth + uniform config so a future caller of
// synth.New() standalone (outside the unified Tier A7 mux) inherits
// the same envelope.
//
// Issue #675 / H2C: the server enables unencrypted HTTP/2 (H2C) on the
// listener via srv.Protocols.SetUnencryptedHTTP2(true). This is the
// Go 1.24+ replacement for the deprecated golang.org/x/net/http2/h2c
// package — the stdlib now negotiates H2C + HTTP/1.1 on the same
// listener with no wrapper. The customer publicHandler still speaks
// HTTP/1.1 downstream; H2C is contained to the public→internal hop.
func NewSynthServer(socketPath string, dispatcher SynthDispatcher, log *slog.Logger) *SynthServer {
	if log == nil {
		log = slog.Default()
	}
	s := &SynthServer{socketPath: socketPath, dispatcher: dispatcher, log: log}
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("/v1/synthesize", s.handleSynthesize)
	// Move 1: schedd's drain posts here for event-shaped invocations
	// (async_invoke / queue / delayed_task / cron). The response is
	// the post-dispatch Invocation row (state + result envelope),
	// which schedd's drain stores via Store.CompleteInvocation.
	s.mux.HandleFunc("/v1/invocations:dispatch", s.handleInvocationDispatch)
	// Commit #13 (issue #757 / ADR-0NN): schedd's dispatch tick
	// posts the trigger batch envelope here. The gateway calls
	// Invoke() per record (the VM lifecycle is one-wake-per-batch,
	// matching AWS Lambda ESM semantics), aggregates the
	// ReportBatchItemFailures response, and returns a per-record
	// status array so the schedd can drive trigger_records state
	// transitions. Same DAC-group auth as the single-record
	// dispatch — the unix socket IS the auth.
	s.mux.HandleFunc("/v1/invocations:dispatch_batch", s.handleInvocationDispatchBatch)
	s.mux.HandleFunc("/healthz", s.handleHealthz)
	s.srv = &http.Server{
		Handler:           s.mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       time.Duration(api.MetricsReadTimeoutSecondsDefault) * time.Second,
		WriteTimeout:      time.Duration(api.MetricsWriteTimeoutSecondsDefault) * time.Second,
		IdleTimeout:       time.Duration(api.MetricsIdleTimeoutSecondsDefault) * time.Second,
		MaxHeaderBytes:    int(api.DefaultMaxHeaderBytes),
	}
	// Enable H2C + HTTP/1.1 on this listener (issue #675). SetHTTP1
	// is true by default but we set it explicitly so the intent is
	// unambiguous for future readers. SetUnencryptedHTTP2 opts in to
	// the in-process H2 prior-knowledge negotiation; the deprecated
	// x/net/http2/h2c wrapper is no longer needed.
	s.srv.Protocols = new(http.Protocols)
	s.srv.Protocols.SetHTTP1(true)
	s.srv.Protocols.SetUnencryptedHTTP2(true)
	return s
}

// SetHandler replaces the http.Server's handler with `h`. Used by the
// Tier A7 unified mux path (issue #675) — runWithDeps builds the
// unified mux (synth routes mounted as a sub-mux + customer
// publicHandler at the catch-all) and hands it here before Start.
// Must be called before Start; calling after Start has no effect on
// the already-serving server.
func (s *SynthServer) SetHandler(h http.Handler) {
	s.srv.Handler = h
}

// Mux returns the synth-only http.ServeMux registered in NewSynthServer.
// Used by the Tier A7 unified-mux builder (issue #675) to mount the
// three synth routes (`/v1/synthesize`, `/v1/invocations:dispatch`,
// `/healthz`) as sub-handlers on the unified mux. Returns nil if the
// server was constructed via the legacy fallback path.
func (s *SynthServer) Mux() *http.ServeMux {
	return s.mux
}

// Start binds the unix socket and starts serving. Returns when the
// listener is ready; subsequent Serve blocks until the server stops.
// Caller is responsible for the goroutine.
//
// Issue #675: the http.Server.Handler is whatever was passed to
// NewSynthServer (or the synth-only fallback mux if nil was passed).
// The unified mux carrying both the synth routes AND the customer
// publicHandler is built by runWithDeps after both deps exist; the
// caller hands the fully-built mux to NewSynthServer before Start.
func (s *SynthServer) Start() error {
	if s.srv == nil {
		return fmt.Errorf("gateway synth: server not configured (call NewSynthServer first)")
	}
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
	// ran cmd/gatewayd-internal/
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

// SocketPath returns the bound socket. Wired in cmd/gatewayd-internal/so
// schedd knows where to dial.
func (s *SynthServer) SocketPath() string { return s.socketPath }

// WithMetrics (ADR-119) arms the synth-side gate's metric
// increments. Same Metrics instance the Handler uses (single
// gateway_internal_auth_match_total counter, two writers).
func (s *SynthServer) WithMetrics(m *Metrics) *SynthServer {
	s.metrics = m
	return s
}

// WithAudit (ADR-119) arms the synth-side gate's audit emit.
// Closure signature matches RequireAuthnAuditor.Emit so
// production can wire the same auditor the Handler uses.
func (s *SynthServer) WithAudit(emit func(ctx context.Context, kind string, subject *string, data map[string]any)) *SynthServer {
	s.synthAuditEmit = emit
	return s
}

// WithAppModeLookup (ADR-119) arms the per-app mode lookup
// the synth-side gate consults on every /v1/synthesize
// request. nil = no mode lookup, every app treated as
// "open" (the gate is a no-op for non-internal_only apps).
//
// The lookup signature takes a context.Context so the gate
// can pass the inbound request's ctx (with the canonical
// timeout / cancel chain) into the per-app store call. Round-3
// golangci-lint contextcheck hooked the closure in
// cmd/gatewayd-internal/run.go::WithAppModeLookup — the lint
// flagged that the closure builds a fresh context.Background()
// instead of receiving the request's context. The fix surfaces
// here: the wired callback now receives ctx.
//
// A cache miss (or transient pg error) returns "" which the
// gate treats as "open" (no JWT required). Returning "" on
// error is the same posture as the HTTP-front-door gate's
// fail-closed-on-Verify-error behavior — but at the lookup
// layer we treat "I don't know" as "open" so a transient pg
// blip doesn't 500 every internal_only cron fire.
func (s *SynthServer) WithAppModeLookup(lookup func(ctx context.Context, appID string) string) *SynthServer {
	s.appPublicAuthMode = lookup
	return s
}

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
	// follow-up routes a real request through gatewayd-internal.
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
	// ADR-119 — per-app 'internal_only' gate runs BEFORE
	// dispatcher.Wake so a forged schedd (or anything else in
	// the faas group) cannot wake an internal_only app. Same
	// verifier + same metric + same audit vocabulary as the
	// HTTP-front-door side; the only difference is the
	// "from" field is "synth" instead of a from_host string.
	// The mode lookup consults the per-app cache populated by
	// the same hydration path Handler.PublicAuthConfig reads.
	if s.appPublicAuthMode != nil {
		if s.applyIngressInternalSvc(w, r, req.AppID, s.appPublicAuthMode(r.Context(), req.AppID), "synth") {
			return
		}
	}
	// ADR-123: members_only synth-side gate mirrors
	// applyIngressInternalSvc above for the cron-fired path.
	// Cron has no human session, so the dominant 403 path
	// here is no_cookie_principal (every /v1/synthesize from
	// the schedd cron driver fires into this branch). The
	// gate runs AFTER applyIngressInternalSvc so an
	// internal_only cron request fails on the cheaper
	// JWT-verify first and never reaches the membership
	// predicate. Mirrors the Handler-side chain order at
	// handler.go:4648.
	if s.appPublicAuthMode != nil {
		if s.applyIngressMembersOnly(w, r, req.AppID, s.appPublicAuthMode(r.Context(), req.AppID), "synth") {
			return
		}
	}
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
	// ADR-119 — per-app 'internal_only' gate runs BEFORE
	// dispatcher.Invoke so a forged schedd (or anything else in
	// the faas group) cannot invoke an internal_only app via
	// /v1/invocations:dispatch. The same gate lives on
	// handleSynthesize (the legacy wake-only path) and on
	// handleInvocationDispatchBatch (the trigger batch path);
	// closing one surface without the others leaves the gate
	// bypassable. The mode lookup consults the per-app cache
	// populated by the same hydration path Handler.PublicAuthConfig
	// reads. The handler returns 403 + audit + metric; the
	// dispatcher is never reached.
	if s.appPublicAuthMode != nil {
		if s.applyIngressInternalSvc(w, r, req.AppID, s.appPublicAuthMode(r.Context(), req.AppID), "synth_dispatch") {
			return
		}
	}
	// ADR-123: members_only synth-side gate for the
	// /v1/invocations:dispatch path (Move 1 single
	// invocation envelope). Mirrors the chain at
	// handleSynthesize above. Dashboard-fired dispatch
	// (a human "trigger wake" click) carries a
	// faas_sid cookie; cron-fired dispatch carries
	// none. Both reach this gate; only the human
	// case passes the membership predicate.
	if s.appPublicAuthMode != nil {
		if s.applyIngressMembersOnly(w, r, req.AppID, s.appPublicAuthMode(r.Context(), req.AppID), "synth_dispatch") {
			return
		}
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

// batchDispatchRequest is the wire shape schedd posts to
// /v1/invocations:dispatch_batch. One app, one source tag, N
// records; the gateway walks each record and calls Invoke().
//
// Source tag: the dispatch tick sets source='esm' for trigger-driven
// batches so downstream metering + audit distinguish them from
// async_invoke / queue / cron.
type batchDispatchRequest struct {
	InvocationID string                `json:"invocation_id"`
	AppID        string                `json:"app_id"`
	Source       string                `json:"source"`
	TriggerID    string                `json:"trigger_id"`
	Records      []batchDispatchRecord `json:"records"`
}

// batchDispatchRecord is one trigger-delivered record's envelope.
// ItemIdentifier is the broker-side handle the dispatcher will
// pass to poller.Ack/Nack. PayloadB64 is base64 to keep the
// envelope JSON-safe (binary payloads are valid).
type batchDispatchRecord struct {
	ItemIdentifier string            `json:"item_identifier"`
	PayloadB64     string            `json:"payload_b64"`
	Headers        map[string]string `json:"headers"`
	Metadata       map[string]any    `json:"metadata"`
}

// batchDispatchResponse is the per-record outcome array the
// schedd's dispatch tick uses to drive trigger_records state
// transitions (commit #14 + #15).
//
// Status:
//
//	"succeeded"     function returned 2xx; no batchItemFailures
//	                entry for this id
//	"retry"         function returned non-2xx OR
//	                ReportBatchItemFailures listed this id;
//	                attempts < max_attempts
//	"dead_letter"   attempts >= max_attempts OR poison_record
//	"broker_error"  invoke itself failed (no result to parse)
//
// The schedd uses this array verbatim — no extra server-side state.
type batchDispatchResponse struct {
	Results []batchDispatchResult `json:"results"`
}

// batchDispatchResult mirrors one Records[i] entry with the
// terminal status. Error is omitted on success.
//
// Code (audit #8) is a stable machine-readable string the schedd
// can switch on instead of substring-matching Error. Mirrors
// api.Problem.Code; the values below are the dispatch-specific
// subset:
//
//	"payload_b64_invalid"   payload base64 decode failed
//	"invoke_error"          dispatcher.Invoke returned err
//	"response_malformed"    function response parse failed
//	"function_failed"       function reported batchItemFailure
//	"function_state_<X>"    function returned non-succeeded state
//
// schedd's classifyDLQReason prefers Code and falls back to
// Error substring matching only when Code is empty (older gateway
// versions).
type batchDispatchResult struct {
	ItemIdentifier string `json:"item_identifier"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
	Code           string `json:"code,omitempty"`
}

// batchDispatchPerRecordTimeout bounds each per-record Invoke
// call inside handleInvocationDispatchBatch. Audit round 2
// finding #4 (PR #910): a single stuck Invoke (e.g. function
// hangs forever, dispatcher blocks waiting on a wake that never
// completes) used to block the loop indefinitely; we now wrap
// each Invoke in a 30s context.WithTimeout. On per-record
// timeout the record is marked Status="retry",
// Code="invoke_timeout" so the schedd's retry FSM can advance
// attempts and only escalate to dead_letter when the
// per-trigger max_attempts budget is exhausted.
//
// Implemented as `var` (not `const`) so tests in the same
// package can shrink the timeout via a package-local override.
// Production code reads the var; tests write it.
var batchDispatchPerRecordTimeout = 30 * time.Second

// batchDispatchTotalTimeout bounds the entire
// handleInvocationDispatchBatch handler. Audit round 2 finding
// #4 (PR #910): a 1000-record batch held the gateway HTTP
// request for the whole loop duration. We now wrap the loop in
// a 5-minute context.WithTimeout so a stuck Invoke can't pin
// the gateway for hours. On total timeout, the remaining
// unprocessed records are marked Status="retry",
// Code="batch_timeout" so the schedd Nacks them and re-delivers
// them on the next tick.
//
// 5 minutes is well above the hot-path latency (sub-second per
// record for warm apps) but well below the schedd's
// gatewayHTTPClient timeout — if a batch needs > 5min the
// dispatch is broken in a way that retrying is the right move,
// not waiting forever.
//
// Implemented as `var` (not `const`) so tests in the same
// package can shrink the timeout via a package-local override.
// Production code reads the var; tests write it.
var batchDispatchTotalTimeout = 5 * time.Minute

// handleInvocationDispatchBatch is the trigger-driven batch path.
//
// Wire flow:
//
//  1. schedd posts the batch envelope to /v1/invocations:dispatch_batch
//     (one HTTP request per dispatch tick).
//  2. gateway decodes the envelope, walks each record, calls
//     s.dispatcher.Invoke() per record (NOT the AWS Lambda ESM
//     semantic of one invocation handling N records — see
//     dispatchBatchRecord below for why).
//  3. After each Invoke returns, parseBatchFailures() reads the
//     function's response body for `batchItemFailures[]` and
//     computes per-record status (succeeded / retry / dead_letter).
//  4. gateway returns the per-record status array to schedd.
//
// Per-record semantics (NOT Lambda ESM): the handler is
// single-threaded — N sequential function invocations per
// batch, each paying its own wake gate + admission cost. The
// schedd's HTTP client is held for the entire batch duration.
// Audit round 2 finding #4 (PR #910) documents this explicitly
// because a future maintainer reading the dispatch path expects
// Lambda ESM "one VM serves N records" — that is NOT what this
// code does today. A future PR can batch-encode and short-circuit
// Invoke() if a target VM is already RUNNING; that change is
// out of scope here.
//
// Safety rails (audit round 2 finding #4):
//
//   - Per-record 30s timeout (batchDispatchPerRecordTimeout):
//     wraps each Invoke call. On timeout, the record is marked
//     Status="retry", Code="invoke_timeout".
//   - Total-batch 5min timeout (batchDispatchTotalTimeout):
//     wraps the entire loop. On timeout, unprocessed records
//     are marked Status="retry", Code="batch_timeout" and the
//     gateway returns a partial 200 with the marked results —
//     schedd Nacks the unprocessed items and re-delivers on
//     the next tick.
//
// Why we don't add InvokeBatch to SynthDispatcher: the VM lifecycle
// is single-wake per record, and Invoke() is already rate-limited
// + plan-quota'd per app. Walking each record through Invoke
// reuses the existing admission gate (the per-app concurrency cap
// stays load-bearing) without doubling the dispatcher contract.
func (s *SynthServer) handleInvocationDispatchBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req batchDispatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.AppID == "" || req.InvocationID == "" {
		http.Error(w, "app_id + invocation_id required", http.StatusBadRequest)
		return
	}
	// ADR-119 — per-app 'internal_only' gate runs BEFORE the
	// per-record loop so a forged schedd (or anything else in
	// the faas group) cannot invoke an internal_only app via
	// /v1/invocations:dispatch_batch. The batch envelope carries
	// ONE appID across all records, so the gate is per-batch
	// (not per-record). Same verifier + same metric + same audit
	// vocabulary as the HTTP-front-door side; the only
	// difference is the "from" field is "synth_batch" instead
	// of "synth" so dashboards can split the two surfaces.
	if s.appPublicAuthMode != nil {
		if s.applyIngressInternalSvc(w, r, req.AppID, s.appPublicAuthMode(r.Context(), req.AppID), "synth_batch") {
			return
		}
	}
	// ADR-123: members_only synth-side gate for the
	// /v1/invocations:dispatch_batch path (Move 1
	// batch). Mirrors the chain at handleSynthesize
	// and handleInvocationDispatch above. The batch
	// path can carry a faas_sid cookie only when
	// fired from the dashboard's "trigger cron sweep"
	// button — cron fires from the schedd cron
	// driver with no cookie, so the dominant 403
	// path is no_cookie_principal.
	if s.appPublicAuthMode != nil {
		if s.applyIngressMembersOnly(w, r, req.AppID, s.appPublicAuthMode(r.Context(), req.AppID), "synth_batch") {
			return
		}
	}
	if len(req.Records) == 0 {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(batchDispatchResponse{Results: []batchDispatchResult{}})
		return
	}
	s.log.Debug("gateway synth: batch dispatched",
		"inv", logsanitize.Field(req.InvocationID),
		"app_id", logsanitize.Field(req.AppID),
		"source", logsanitize.Field(req.Source),
		"trigger_id", logsanitize.Field(req.TriggerID),
		"records", len(req.Records))

	// Audit round 2 finding #4: wrap the whole loop in a
	// 5-minute timeout so a stuck Invoke can't pin the gateway
	// for hours. The ctx is derived from r.Context() so
	// client-side disconnects still cancel the loop.
	ctx, cancel := context.WithTimeout(r.Context(), batchDispatchTotalTimeout)
	defer cancel()

	results := make([]batchDispatchResult, 0, len(req.Records))
	for _, rec := range req.Records {
		if ctx.Err() != nil {
			// Total-batch timeout fired (or client disconnected).
			// Mark every remaining record with batch_timeout so
			// schedd Nacks them and re-delivers on the next tick.
			// Without this, the loop would just stop and the
			// schedd would have no idea which records were
			// processed vs dropped.
			results = append(results, batchDispatchResult{
				ItemIdentifier: rec.ItemIdentifier,
				Status:         batchDispatchStatusRetry,
				Error:          "batch_timeout: handler exceeded " + batchDispatchTotalTimeout.String(),
				Code:           "batch_timeout",
			})
			continue
		}
		results = append(results, s.dispatchBatchRecord(ctx, req, rec))
	}
	s.calls.Add(1)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(batchDispatchResponse{Results: results})
}

// dispatchBatchRecord handles one record inside the batch loop.
// Extracted from handleInvocationDispatchBatch so the per-record
// processing reads cleanly and the timeout wrappers stay
// localised. The handler itself stays under the 50-line cap
// CLAUDE.md enforces.
//
// Per-record 30s timeout (batchDispatchPerRecordTimeout): on
// timeout, the record is marked Status="retry",
// Code="invoke_timeout" — the schedd's retry FSM advances the
// attempts counter and only escalates to dead_letter when the
// per-trigger max_attempts budget is exhausted.
//
// Note: the per-record context is derived from the total-batch
// context (ctx), so a total-batch timeout ALSO trips the
// per-record timeout for the next iteration; ctx.Err() check
// above catches the next iteration. The two timeouts compose
// cleanly because context.WithTimeout is short-circuiting.
func (s *SynthServer) dispatchBatchRecord(ctx context.Context, req batchDispatchRequest, rec batchDispatchRecord) batchDispatchResult {
	// Per-record timeout (audit round 2 finding #4). Wraps the
	// Invoke call so a stuck function or stuck dispatcher can't
	// pin a single record.
	recCtx, recCancel := context.WithTimeout(ctx, batchDispatchPerRecordTimeout)
	defer recCancel()
	var payload []byte
	if rec.PayloadB64 != "" {
		dec, err := base64Decode(rec.PayloadB64)
		if err != nil {
			return batchDispatchResult{
				ItemIdentifier: rec.ItemIdentifier,
				Status:         "dead_letter",
				Error:          "payload_b64 invalid",
				Code:           "payload_b64_invalid",
			}
		}
		payload = dec
	}
	// Synthesize a single-record invocation envelope. We
	// route via the /v1/invocations:dispatch code path in
	// spirit (same dispatcher.Invoke) but bypass the JSON
	// decoder so the headers / metadata land on the runner
	// envelope unchanged.
	inv := state.Invocation{
		ID:      req.InvocationID + "-" + rec.ItemIdentifier,
		AppID:   req.AppID,
		Source:  state.InvocationSource(req.Source),
		Method:  http.MethodPost,
		Path:    "/_triggers/" + req.Source + "/" + req.TriggerID,
		Payload: payload,
		Headers: jsonOrEmpty(rec.Headers),
	}
	out, err := s.dispatcher.Invoke(recCtx, req.AppID, inv)
	if err != nil {
		s.log.Warn("gateway synth: invoke (batch)",
			"inv", logsanitize.Field(inv.ID),
			"item", logsanitize.Field(rec.ItemIdentifier),
			"err", err)
		// Per-record timeout: recCtx.Err() returns
		// context.DeadlineExceeded when the per-record timeout
		// fired (and NOT when only the total-batch timeout
		// fired — the next-iteration check above catches that).
		// Map it to a stable invoke_timeout Code so the schedd's
		// classifyDLQReason can switch on it instead of
		// substring-matching err.Error().
		code := "invoke_error"
		if errors.Is(recCtx.Err(), context.DeadlineExceeded) {
			code = "invoke_timeout"
		}
		return batchDispatchResult{
			ItemIdentifier: rec.ItemIdentifier,
			Status:         batchDispatchStatusRetry,
			Error:          err.Error(),
			Code:           code,
		}
	}
	// Parse the function's response body for
	// ReportBatchItemFailures. The convention is a JSON
	// envelope: {"batchItemFailures":[{"itemIdentifier":"..."}]}
	// — stolen verbatim from AWS Lambda.
	failed, parseErr := parseBatchFailures(out.Result)
	if parseErr != nil {
		s.log.Warn("gateway synth: batch failures parse",
			"item", logsanitize.Field(rec.ItemIdentifier),
			"err", parseErr)
		// Malformed function response — emit Status='retry'
		// so the schedd's retry FSM can advance attempts and
		// only escalate to dead_letter when the per-trigger
		// max_attempts budget is exhausted. review finding #5:
		// the prior code skipped straight to dead_letter on
		// the first transient 5xx, bypassing the customer's
		// retry budget entirely.
		return batchDispatchResult{
			ItemIdentifier: rec.ItemIdentifier,
			Status:         batchDispatchStatusRetry,
			Error:          "function response malformed: " + parseErr.Error(),
			Code:           "response_malformed",
		}
	}
	// Per-record status: succeeded unless the record's id is
	// in the failure list.
	if containsString(failed, rec.ItemIdentifier) {
		return batchDispatchResult{
			ItemIdentifier: rec.ItemIdentifier,
			Status:         batchDispatchStatusRetry,
			Error:          "reported in batchItemFailures",
			Code:           "function_failed",
		}
	}
	// If state==succeeded from the dispatcher, success.
	// Otherwise (e.g. function returned 5xx, dispatcher
	// captured it) it's a retry.
	if string(out.State) == batchDispatchStatusSucceeded {
		return batchDispatchResult{
			ItemIdentifier: rec.ItemIdentifier,
			Status:         batchDispatchStatusSucceeded,
		}
	}
	return batchDispatchResult{
		ItemIdentifier: rec.ItemIdentifier,
		Status:         batchDispatchStatusRetry,
		Error:          fmt.Sprintf("function state=%s", out.State),
		Code:           "function_state_" + string(out.State),
	}
}

// batchFailuresEnvelope is the function's response shape for
// partial-failure reporting. Stolen verbatim from AWS Lambda's
// ReportBatchItemFailures contract:
//
//	{
//	  "batchItemFailures": [
//	    {"itemIdentifier": "id-1"},
//	    {"itemIdentifier": "id-2"}
//	  ]
//	}
//
// Empty/missing batchItemFailures ⇒ all records succeeded. We
// accept any extra top-level fields the function returns (SDKs
// sometimes add metadata) and ignore them.
type batchFailuresEnvelope struct {
	BatchItemFailures []batchFailureItem `json:"batchItemFailures"`
}

// batchFailureItem is one entry in batchFailuresEnvelope.
// ItemIdentifier is the SourceRecord.ItemIdentifier the dispatcher
// passed to the function (so the function needs to echo it back).
type batchFailureItem struct {
	ItemIdentifier string `json:"itemIdentifier"`
}

// parseBatchFailures decodes the function's response body and
// returns the list of item identifiers the function reported as
// failed.
//
// Acceptable shapes:
//
//   - {"batchItemFailures":[{"itemIdentifier":"..."}]}
//   - []byte("null")            → empty slice, no error
//   - []byte("")                 → empty slice, no error
//   - {}                         → empty slice, no error
//   - any other valid JSON value → empty slice, no error
//
// Errors:
//
//   - malformed JSON              → wrapped error
//   - batchItemFailures not array → error (poison_record)
//
// Idempotency: callers treat empty slice as "all succeeded" so
// the empty-body case is the success path.
func parseBatchFailures(body []byte) ([]string, error) {
	if len(body) == 0 {
		return []string{}, nil
	}
	var env batchFailuresEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("parseBatchFailures: %w", err)
	}
	out := make([]string, 0, len(env.BatchItemFailures))
	for _, it := range env.BatchItemFailures {
		if it.ItemIdentifier != "" {
			out = append(out, it.ItemIdentifier)
		}
	}
	return out, nil
}

// containsString is a tiny helper for the per-record failure
// check. Linear scan is fine — batches cap at the per-plan
// TriggerBatchSizeMax (5000 max, 500 typical for Pro).
func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
