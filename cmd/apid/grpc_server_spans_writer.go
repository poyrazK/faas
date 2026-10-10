// apid-side gRPC handler for the SpansWriter service
// (ADR-127 PR-D). Wired by registerSpansWriterReceiver in
// main.go onto /run/faas/otel_spans_writer.sock.
//
// Direction: gatewayd-public → apid. gatewayd-public is the
// ONLY caller (CLAUDE.md ownership: apid is the sole writer to
// request_telemetry; gatewayd-public never opens a direct
// Postgres connection for this path).
//
// Wire discipline mirrors grpc_server_auth.go: errors map to
// gRPC codes (InvalidArgument / Unauthenticated / Internal),
// rate-limit outcomes ride on the response stream (NOT gRPC
// errors — gateway drops + retries next window).

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ratelimit/peraccount"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Outcome strings for the WriteSpansSummaryResponse stream.
// The §12 metrics (`otel_spans_writes_total` keys off the
// apid-side `inserted/rate_limited/validation_error/db_error`
// outcomes) match these literals — goconst-flagged so drift
// breaks the lint gate.
//
// PR-D code-review #7: validation errors (bad trace_id regex,
// invalid JSON, malformed account_id, CHECK violation) and
// real Postgres failures used to share the `db_error` label.
// Operators chasing a Postgres failover drill would see the
// counter climb because customers were sending upper-case hex
// trace_ids that pass the gateway regex but trip the DB CHECK.
// Split the labels so the dashboard distinguishes.
const (
	swOutcomeInserted        = "inserted"
	swOutcomeRateLimited     = "rate_limited"
	swOutcomeValidationError = "validation_error"
	swOutcomeDBError         = "db_error"
	// swOutcomeNoRow: the request_telemetry row is not written yet.
	swOutcomeNoRow = "no_row"
)

// traceIDPattern is the W3C trace-id regex — 32 lowercase hex
// chars. The request_telemetry trace_id CHECK constraint
// mirrors this regex, so matching it here matches what the DB
// would accept. Built once at package init via MustCompile.
var traceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// spansWriterStore is the Store subset the receiver needs.
//
// PR-D code-review #1: accountID is REQUIRED in the interface
// signature. The SQL UPDATE pins (trace_id, account_id) so a
// cross-customer overwrite is impossible even if a buggy upstream
// caller forwards the wrong trace_id. The apid gRPC handler
// already extracted the account_id from req.GetAccountId() and
// validated it parses as a UUID (step 3), so the value here is
// trusted.
type spansWriterStore interface {
	UpdateSpansSummary(ctx context.Context, traceID string, accountID uuid.UUID, summary []byte) error
}

// spansWriterReceiver is the in-package server implementation
// of apidpb.SpansWriterServer. Wired by registerSpansWriterReceiver
// onto a *grpc.Server.
//
// ops is the apid daemon's *wire.OpsMetrics; nil-safe.
//
// limiter is the per-account token bucket pool shared with the
// PR-B IncrementRequestTelemetry receiver; nil disables rate
// limiting (testing seam).
//
// enabled is the kill-switch (FAAS_OTEL_SPANS_WRITER_ENABLED).
// When false, WriteSpansSummary returns codes.Unavailable so
// the gateway stops sending.
type spansWriterReceiver struct {
	apidpb.UnimplementedSpansWriterServer
	store   spansWriterStore
	ops     spansWriterMonitor
	limiter *peraccount.Limiter
	enabled bool
	// guest is the ADR-934 in-guest export ingester; nil disables it.
	guest *guestSpansIngester
}

// spansWriterMonitor is the metric surface the receiver uses.
// Decoupled from *wire.OpsMetrics so unit tests can substitute
// a fake without spinning a Prometheus registry.
type spansWriterMonitor interface {
	IncrementSpansWriteOutcome(outcome string)
}

// newSpansWriterReceiver wires a production receiver.
func newSpansWriterReceiver(store spansWriterStore, ops spansWriterMonitor, limiter *peraccount.Limiter, enabled bool) *spansWriterReceiver {
	if limiter == nil {
		limiter = peraccount.NewLimiter()
	}
	return &spansWriterReceiver{store: store, ops: ops, limiter: limiter, enabled: enabled}
}

// WriteSpansSummary updates the matching request_telemetry
// row's spans_summary jsonb column. The flow:
//  1. Validate trace_id against the W3C regex (matches the
//     DB CHECK constraint).
//  2. Validate summary_json parses as JSON.
//  3. Resolve account_id UUID — defensive check (the gateway
//     already validated when accepting the OTLP POST).
//  4. Per-account token bucket (1 token per flush — the
//     gateway's coalesce window is 30s, so this caps a
//     chatty customer at 2 flushes/min, well below the
//     DebugTelemetryRequestsPerMinute OTLP-ingest cap).
//  5. UPDATE request_telemetry SET spans_summary = $2::jsonb
//     WHERE trace_id = $1 AND received_at >= now() - interval
//     '24 hours'.
//
// Outcome ∈ {inserted, rate_limited, db_error}. Per-record
// outcomes ride on the response — gRPC codes map to terminal
// failures only.
func (r *spansWriterReceiver) WriteSpansSummary(ctx context.Context, req *apidpb.WriteSpansSummaryRequest) (*apidpb.WriteSpansSummaryResponse, error) {
	if !r.enabled {
		return nil, status.Error(codes.Unavailable, "otel_spans_writer disabled by FAAS_OTEL_SPANS_WRITER_ENABLED")
	}

	// ---- 1. trace_id regex ----
	traceID := req.GetTraceId()
	if !traceIDPattern.MatchString(traceID) {
		r.observe(swOutcomeValidationError)
		return nil, status.Errorf(codes.InvalidArgument, "trace_id must match ^[0-9a-f]{32}$ (got len=%d)", len(traceID))
	}

	// ---- 2. summary_json parses as JSON ----
	summary := req.GetSummaryJson()
	if !json.Valid(summary) {
		r.observe(swOutcomeValidationError)
		return nil, status.Error(codes.InvalidArgument, "summary_json is not valid JSON")
	}

	// ---- 3. account_id UUID ----
	accountID, err := uuid.Parse(req.GetAccountId())
	if err != nil {
		r.observe(swOutcomeValidationError)
		return nil, status.Errorf(codes.Unauthenticated, "account_id malformed: %v", err)
	}

	// ---- 4. per-account token bucket ----
	// We don't have per-plan caps cached here; the limiter
	// still rate-limits the write path so a runaway customer
	// can't DOS the DB. Cap = DebugTelemetryRequestsPerMinute
	// (same pool the OTLP ingest uses). PR-B's caps cache is
	// queryable via the limiter so a sustained-overflow
	// customer's plan cap is honored without an extra DB hit.
	limits, ok := r.limiter.CachedLimits(accountID)
	if !ok {
		// No cached limits — let the write through with a
		// permissive cap. The cache will populate on the next
		// IncrementRequestTelemetry call for this account.
		limits = api.MustLimitsFor(api.PlanScale) // most permissive
	}
	taken, retryMs := r.limiter.Take(accountID, limits.DebugTelemetryRequestsPerMinute)
	if !taken {
		r.observe(swOutcomeRateLimited)
		return &apidpb.WriteSpansSummaryResponse{
			Outcome:      swOutcomeRateLimited,
			RetryAfterMs: retryMs,
		}, nil
	}

	// ---- 5. UPDATE ----
	// PR-D code-review #1: forward the already-validated
	// accountID so the SQL predicate
	// `and account_id = $3::uuid` can match. The bucket mutex
	// was the upstream guard; this is defense in depth.
	if err := r.store.UpdateSpansSummary(ctx, traceID, accountID, summary); err != nil {
		// PR-D code-review #7: a CHECK violation (trace_id
		// format drift between client + server) is a
		// VALIDATION failure, not a DB failure. The §12
		// dashboard distinguishes validation_error from
		// db_error so an operator chasing a Postgres
		// failover drill doesn't get misled by client-side
		// shape drift.
		// ADR-934: spans that arrive before their request row are
		// retryable, not delivered; producers keep them for the next flush.
		if errors.Is(err, state.ErrRequestTelemetryRowNotFound) {
			r.observe(swOutcomeNoRow)
			return &apidpb.WriteSpansSummaryResponse{Outcome: swOutcomeNoRow}, nil
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23514" {
			r.observe(swOutcomeValidationError)
			return nil, status.Errorf(codes.InvalidArgument, "constraint violation: %v", err)
		}
		r.observe(swOutcomeDBError)
		return nil, status.Errorf(codes.Internal, "update spans_summary: %v", err)
	}

	r.observe(swOutcomeInserted)
	return &apidpb.WriteSpansSummaryResponse{Outcome: swOutcomeInserted}, nil
}

// observe is the nil-safe wrapper around the spans-write
// outcome counter. The metric lives on the apid OpsMetrics
// (PR-D adds IncrementSpansWriteOutcome there in the metrics
// amendment).
func (r *spansWriterReceiver) observe(outcome string) {
	if r.ops == nil {
		return
	}
	r.ops.IncrementSpansWriteOutcome(outcome)
}

// registerSpansWriterReceiver binds the SpansWriterServer onto
// a gRPC server. Called from runSpansWriterServer in main.go.
func registerSpansWriterReceiver(s *grpc.Server, store spansWriterStore, ops spansWriterMonitor, limiter *peraccount.Limiter, enabled bool, guest *guestSpansIngester) {
	receiver := newSpansWriterReceiver(store, ops, limiter, enabled)
	receiver.guest = guest
	apidpb.RegisterSpansWriterServer(s, receiver)
}

// Compile-time guards. The bytes import is preserved for the
// future gzip-compressed summary path (PR-D.1 follow-on — see
// docs/adr/adr-127-pr-d.md open follow-ons).
var _ = bytes.NewReader
var _ = state.NewPgtypeUUID
