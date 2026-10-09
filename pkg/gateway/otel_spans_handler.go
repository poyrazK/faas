// Package gateway — otel_spans_handler.go (ADR-127 PR-D).
//
// gatewayd-public's POST /v1/otel/v1/traces handler (OTLP/HTTP
// JSON-protobuf, the standard sidecar protocol). Decodes the
// inbound ExportTraceServiceRequest, validates per-account auth
// via the apid Auth gRPC service, enforces the per-plan telemetry
// rate cap (DebugTelemetryRequestsPerMinute) + span ceiling
// (DebugTelemetrySpansPerTrace), and feeds the truncated spans
// into a per-trace accumulator (Stage 4 flushes the accumulator
// to apid's WriteSpansSummary gRPC RPC).
//
// Auth model: loopback gRPC, single-box mode. The unix socket
// /run/faas/auth.sock is DAC 0660 group `faas`; the gateway's
// only credential is socket membership. Cross-box the loopback
// is replaced by the FAAS_AUTH_RPC target (PR-D's v1 ships with
// the loopback-only posture — see ADR-070 cross-box HA).
//
// Failure posture:
//   - 400 on shape-invalid OTLP body (malformed body, invalid IDs).
//   - 401 on bearer auth failure (apid Unauthenticated).
//   - 402 on plan-disabled (DebugTelemetryEnabled=false).
//   - 429 on per-account rate cap exhaustion.
//   - 200 on accepted (the truncated summary is staged in the
//     accumulator; the customer's app doesn't wait on the DB
//     write — that's the flush loop's job).

package gateway

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"github.com/onebox-faas/faas/pkg/gateway/drain"
	"github.com/onebox-faas/faas/pkg/ratelimit/peraccount"
	"github.com/onebox-faas/faas/pkg/wire"
)

// OTelSpansHandlerConfig bundles the handler's dependencies.
type OTelSpansHandlerConfig struct {
	// AuthClient is the apid Auth service client. Required.
	AuthClient apidgrpc.AuthClient
	// Limiter is the per-account token-bucket pool shared with
	// the apid PR-B IncrementRequestTelemetry receiver (same
	// pkg/ratelimit/peraccount instance in production).
	// Required.
	//
	// Known limitation (PR-D code-review #3): the gateway runs
	// in a separate process from apid, so the gateway's limiter
	// is in-process memory and cannot share buckets with the
	// apid-side limiter. The customer's effective cap is
	// therefore 2x the plan's DebugTelemetryRequestsPerMinute:
	// once on the gateway's pre-flight gate, once on apid's
	// writer. The bucket cap is frozen on first Take (PR-D
	// code-review #2), so this 2x ceiling is stable across the
	// customer's session — not oscillating with caller order.
	// A PR-D.1 follow-on folds the rate-limit decision into
	// the apid AuthenticateKey RPC so the gateway stops holding
	// a local bucket; PR-D ships with the documented 2x
	// ceiling as a known acceptable trade-off for v1.0.
	Limiter *peraccount.Limiter
	// Acc is the per-trace coalesce buffer. Required.
	Acc *SpansAccumulator
	// Ops is the daemon's *wire.OpsMetrics. nil-safe.
	Ops *wire.OpsMetrics
	// Log is the structured logger. nil = slog.Default().
	Log *slog.Logger
	// Drain is the per-request WaitGroup-backed drain tracker
	// shared with Handler + the trace handler. nil = disabled.
	Drain *drain.Tracker
	// LimitsFor resolves an api.Plan into its api.Limits. Pulled
	// via a closure so tests can stub the plan table.
	LimitsFor func(plan api.Plan) api.Limits
}

// OTelSpansHandler is the http.Handler for POST /v1/otel/v1/traces.
type OTelSpansHandler struct {
	cfg OTelSpansHandlerConfig
}

// NewOTelSpansHandler returns a handler ready to mount on the
// public mux. The handler is safe for concurrent use; the
// accumulator + limiter own all mutable state.
func NewOTelSpansHandler(cfg OTelSpansHandlerConfig) *OTelSpansHandler {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.LimitsFor == nil {
		cfg.LimitsFor = api.MustLimitsFor
	}
	return &OTelSpansHandler{cfg: cfg}
}

// ServeHTTP handles POST /v1/otel/v1/traces. Drain-tracked for
// graceful shutdown parity with the trace handler.
//
// PR-D code-review #6: auth runs BEFORE the body read. The
// previous order decoded up to 4 MiB + OTLP-decoded
// every request before peeking the Authorization header, so
// unauthenticated attackers could amplify their CPU/bandwidth
// spend ~1000x against the gateway by sending valid-shaped
// OTLP bodies with bogus bearer tokens. New order:
//
//  1. Method check.
//  2. Bearer parse (header only — no body read).
//  3. apid AuthenticateKey RPC (the sha256 lookup; loopback
//     unix socket, sub-ms).
//  4. Plan gate.
//  5. Per-account token bucket.
//  6. Body read (4 MiB cap; only authed customers reach
//     this point).
//  7. OTLP decode + shape validation.
//  8. Accumulator Add.
//
// Unauthenticated 401s now do ~header-parse work; the
// 4 MiB + decode cost only applies to legitimate traffic.
func (h *OTelSpansHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	codec, codecErr := traceCodec(r)
	// Drain tracker (parity with trace_handler.go:74-78).
	done := func() {}
	if h.cfg.Drain != nil {
		done = h.cfg.Drain.Begin("http")
	}
	defer done()

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		codec.problem(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	accountID, authorized := h.authorizeOTLP(w, r, codec)
	if !authorized {
		return
	}

	if codecErr != nil {
		codec.problem(w, http.StatusUnsupportedMediaType, codecErr.Error())
		return
	}
	if encoding := strings.TrimSpace(r.Header.Get("Content-Encoding")); encoding != "" && !strings.EqualFold(encoding, "gzip") {
		codec.problem(w, http.StatusUnsupportedMediaType, "unsupported Content-Encoding")
		return
	}

	batches, err := readTraceBatches(w, r, codec)
	if err != nil {
		h.observeIngest("shape_invalid")
		codec.problem(w, http.StatusBadRequest, "invalid OTLP body")
		return
	}
	h.acceptTraceBatches(w, codec, accountID, batches)
}

// authorizeOTLP completes authentication and quota checks without reading
// customer-controlled payloads or initializing a decompressor.
func (h *OTelSpansHandler) authorizeOTLP(w http.ResponseWriter, r *http.Request, codec otlpTraceCodec) (uuid.UUID, bool) {
	// ---- Step 2: Bearer parse (no body read) ----
	tok := bearerToken(r)
	if tok == "" {
		h.observeAuthFailure("unauthenticated")
		codec.problem(w, http.StatusUnauthorized, "missing bearer token")
		return uuid.Nil, false
	}

	// ---- Step 3: apid Auth RPC (no body read) ----
	accountIDStr, plan, err := h.cfg.AuthClient.AuthenticateKey(r.Context(), tok)
	if err != nil {
		if status.Code(err) == codes.Unauthenticated {
			h.observeAuthFailure("unauthenticated")
			codec.problem(w, http.StatusUnauthorized, "unauthenticated")
			return uuid.Nil, false
		}
		h.observeAuthFailure("internal")
		h.cfg.Log.Warn("apid auth RPC failed", "err", err)
		codec.problem(w, http.StatusInternalServerError, "auth unavailable")
		return uuid.Nil, false
	}
	accountID, err := uuid.Parse(accountIDStr)
	if err != nil {
		h.observeAuthFailure("unauthenticated")
		codec.problem(w, http.StatusUnauthorized, "malformed account_id")
		return uuid.Nil, false
	}

	// ---- Step 4: Plan gate ----
	limits := h.cfg.LimitsFor(api.Plan(plan))
	if !limits.DebugTelemetryEnabled {
		h.observeAuthFailure("plan_disabled")
		w.Header().Set("Retry-After", "0")
		codec.problem(w, http.StatusPaymentRequired, "plan does not include telemetry")
		return uuid.Nil, false
	}

	// ---- Step 5: Per-account token bucket ----
	taken, retryMs := h.cfg.Limiter.Take(accountID, limits.DebugTelemetryRequestsPerMinute)
	if !taken {
		h.observeIngest("rate_limited")
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retryMs/1000))
		codec.problem(w, http.StatusTooManyRequests, "rate limited")
		return uuid.Nil, false
	}

	return accountID, true
}

func readTraceBatches(w http.ResponseWriter, r *http.Request, codec otlpTraceCodec) ([]traceSpanBatch, error) {
	raw, err := readOTLPTraceBody(w, r)
	if err != nil {
		return nil, err
	}
	request, err := codec.decode(raw)
	if err != nil {
		return nil, err
	}
	return extractTraceBatches(request)
}

// acceptTraceBatches keeps per-trace account isolation while allowing standard
// exporter batches. Contested traces are rejected without discarding the other
// traces in the request or inviting retries of data already accepted.
func (h *OTelSpansHandler) acceptTraceBatches(w http.ResponseWriter, codec otlpTraceCodec, accountID uuid.UUID, batches []traceSpanBatch) {
	var rejected int64
	accepted := 0
	for _, batch := range batches {
		if _, err := h.cfg.Acc.Add(batch.traceID, accountID, batch.spans); err != nil {
			rejected += int64(len(batch.spans))
			h.observeAuthFailure("unauthenticated")
			continue
		}
		accepted += len(batch.spans)
	}
	response := &collectortracepb.ExportTraceServiceResponse{}
	if rejected > 0 {
		response.PartialSuccess = &collectortracepb.ExportTracePartialSuccess{
			RejectedSpans: rejected,
			ErrorMessage:  "some spans could not be accepted",
		}
	}
	h.observeIngest("inserted")
	w.Header().Set("X-Gregale-Accepted-Spans", fmt.Sprint(accepted))
	codec.write(w, http.StatusOK, response)
}

type traceSpanBatch struct {
	traceID string
	spans   []summarizedSpan
}

// extractTraceBatches validates the whole request before buffering any spans,
// and preserves the first-seen trace order. Empty exports are successful.
func extractTraceBatches(req *collectortracepb.ExportTraceServiceRequest) ([]traceSpanBatch, error) {
	var batches []traceSpanBatch
	index := make(map[string]int)
	for _, rs := range req.GetResourceSpans() {
		for _, ss := range rs.GetScopeSpans() {
			for _, span := range ss.GetSpans() {
				if !validOTLPID(span.GetTraceId(), 16) || !validOTLPID(span.GetSpanId(), 8) {
					return nil, errors.New("invalid trace or span ID")
				}
				if parent := span.GetParentSpanId(); len(parent) != 0 && !validOTLPID(parent, 8) {
					return nil, errors.New("invalid parent span ID")
				}
				traceID := formatTraceID(span.GetTraceId())
				position, found := index[traceID]
				if !found {
					position = len(batches)
					index[traceID] = position
					batches = append(batches, traceSpanBatch{traceID: traceID})
				}
				batches[position].spans = append(batches[position].spans, summarizeSpan(span))
			}
		}
	}
	return batches, nil
}

func validOTLPID(id []byte, length int) bool {
	if len(id) != length {
		return false
	}
	for _, value := range id {
		if value != 0 {
			return true
		}
	}
	return false
}

// summarizeSpan flattens one OTLP Span into the summarizedSpan
// shape the accumulator + flush loop work with. The
// db.statement.* attributes are pulled to a top-level field so
// PR-C's prose synthesis can quote SQL without parsing the
// attributes map.
func summarizeSpan(sp *tracepb.Span) summarizedSpan {
	start := sp.GetStartTimeUnixNano()
	end := sp.GetEndTimeUnixNano()
	var dur uint64
	if end > start {
		dur = end - start
	}
	attrs := stripPlatformAttributes(flattenAttributes(sp.GetAttributes()))
	return summarizedSpan{
		TraceID:           formatTraceID(sp.GetTraceId()),
		SpanID:            formatSpanID(sp.GetSpanId()),
		ParentSpanID:      formatSpanID(sp.GetParentSpanId()),
		Name:              sp.GetName(),
		Kind:              sp.GetKind().String(),
		StartTimeUnixNano: start,
		EndTimeUnixNano:   end,
		DurationNanos:     dur,
		Status:            sp.GetStatus().GetCode().String(),
		StatusMessage:     sp.GetStatus().GetMessage(),
		Attributes:        attrs,
		DBStatement:       extractDBStatement(attrs),
	}
}

// formatTraceID hex-encodes an OTLP trace_id (16 bytes) into the
// 32-char lowercase string the database CHECK constraint
// enforces. Empty bytes → empty string (caller validates).
// stripPlatformAttributes removes the platform-reserved gregale.* namespace
// from customer-submitted spans. Only platform producers (the retained
// service-spans exporter) may classify a span as a managed binding,
// outbound integration or guest transport; a customer span claiming that
// identity would otherwise be presented as platform evidence (ADR-829).
// This path serves only customer ingest: the public OTLP endpoint and the
// in-guest bridge.
func stripPlatformAttributes(attrs map[string]string) map[string]string {
	for key := range attrs {
		if strings.HasPrefix(key, "gregale.") {
			delete(attrs, key)
		}
	}
	return attrs
}

func formatTraceID(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return hex.EncodeToString(b)
}

// formatSpanID hex-encodes an OTLP span_id (8 bytes). The
// summarizedSpan.ParentSpanID omits the empty field for
// root spans (handled at the JSON marshaller via omitempty).
func formatSpanID(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return hex.EncodeToString(b)
}

// flattenAttributes walks an OTLP KeyValueList into a flat
// string map. OTLP attribute values may be string/int/double/bool
// arrays — for the debugger we collapse to their canonical
// fmt.Sprintf("%v") form (load-bearing: PR-C reads Attributes
// to render the prose). Non-string scalar types come through as
// fmt-formatted.
func flattenAttributes(kvs []*commonpb.KeyValue) map[string]string {
	if len(kvs) == 0 {
		return nil
	}
	out := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		out[kv.GetKey()] = attrAnyToString(kv.GetValue())
	}
	return out
}

func attrAnyToString(v *commonpb.AnyValue) string {
	if v == nil {
		return ""
	}
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return x.StringValue
	case *commonpb.AnyValue_BoolValue:
		return fmt.Sprintf("%v", x.BoolValue)
	case *commonpb.AnyValue_IntValue:
		return fmt.Sprintf("%d", x.IntValue)
	case *commonpb.AnyValue_DoubleValue:
		return fmt.Sprintf("%v", x.DoubleValue)
	case *commonpb.AnyValue_ArrayValue:
		return fmt.Sprintf("%v", x.ArrayValue)
	case *commonpb.AnyValue_KvlistValue:
		return fmt.Sprintf("%v", x.KvlistValue)
	case *commonpb.AnyValue_BytesValue:
		return hex.EncodeToString(x.BytesValue)
	}
	return ""
}

// extractDBStatement reads the standard OTEL_SEMCONV_DB_STATEMENT
// attribute. PR-C quotes this verbatim in the prose synthesis;
// lifting it to a top-level field keeps the rest of the
// attributes map free of semantic load.
func extractDBStatement(attrs map[string]string) string {
	if attrs == nil {
		return ""
	}
	if v := attrs["db.statement"]; v != "" {
		return v
	}
	if v := attrs["db.query.text"]; v != "" {
		return v
	}
	return ""
}

// bearerToken extracts the raw Bearer token from the
// Authorization header. Mirrors pkg/auth/middleware/
// middleware.go:1180 (the PR-B IncrementRequestTelemetry
// receiver uses the same helper, but lives in cmd/apid so
// we duplicate the 4 lines here rather than widening that
// package's exported surface).
//
// PR-D code-review #8: the prefix match is case-insensitive
// per RFC 7235 §2.1. The previous strings.HasPrefix was
// case-sensitive, so a customer SDK sending
// 'Authorization: bearer faas_live_hobby' (lowercase scheme)
// would 401 against the OTLP handler even though the
// canonical apid REST surface accepts it. Match the canonical
// middleware: strings.EqualFold on the first 7 bytes.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) <= len(bearerPrefix) {
		return ""
	}
	if !strings.EqualFold(h[:len(bearerPrefix)], bearerPrefix) {
		return ""
	}
	return strings.TrimSpace(h[len(bearerPrefix):])
}

// bearerPrefix is "Bearer " — the RFC 7235 §2.1 scheme. The
// comparison is case-insensitive (handled by strings.EqualFold
// in bearerToken).
const bearerPrefix = "Bearer "

// observeIngest is the nil-safe wrapper for the ingested-counter
// metric. Centralized so the call sites stay 1-liners.
func (h *OTelSpansHandler) observeIngest(outcome string) {
	if h.cfg.Ops == nil {
		return
	}
	h.cfg.Ops.IncrementGatewaydPublicOtelSpansIngested(outcome)
}

// observeAuthFailure is the nil-safe wrapper for the
// auth-failures metric.
func (h *OTelSpansHandler) observeAuthFailure(reason string) {
	if h.cfg.Ops == nil {
		return
	}
	h.cfg.Ops.IncrementGatewaydPublicOtelAuthFailures(reason)
}

// Compile-time guard so a future gateway refactor that forgets
// to mount the handler on a mux doesn't silently break the
// OTLP endpoint.
var _ http.Handler = (*OTelSpansHandler)(nil)

// context-time pin: the handler doesn't use ctx for anything
// beyond passing it to the apid Auth RPC. This time import is
// pulled in by the time.Time tripwire below — keep both.
var _ = time.Time{}
var _ = context.Background
