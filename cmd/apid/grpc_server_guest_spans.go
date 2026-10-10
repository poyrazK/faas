// apid-side receiver for in-guest trace exports (ADR-957).
//
// Direction: vmmd → apid IngestGuestSpans on the SpansWriter service. vmmd
// bounds the frame and supplies the host-owned principal; it never decodes
// the payload. apid owns plan admission, the per-account rate cap, OTLP
// decoding and the merge into request_telemetry.spans_summary. Coalesced
// summaries are flushed back through WriteSpansSummary so they share its
// validation, rate limiting and outcome metrics with the other producers.

package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/ratelimit/peraccount"
	"github.com/onebox-faas/faas/pkg/state"
)

// Outcome strings for IngestGuestSpansResponse (shared with vmmd via pkg/api).
const (
	guestSpansAccepted    = api.TraceIngestAccepted
	guestSpansRateLimited = api.TraceIngestRateLimited
	guestSpansDisabled    = api.TraceIngestDisabled
	guestSpansInvalid     = api.TraceIngestInvalid
	guestSpansUnavailable = api.TraceIngestUnavailable
)

// guestSpansMaxBuckets bounds traces buffered between flushes so a burst of
// distinct trace IDs cannot grow apid memory without limit.
const guestSpansMaxBuckets = 50000

type guestSpansAccounts interface {
	AccountByID(ctx context.Context, id string) (state.Account, error)
}

// guestSpansIngester is process-wide: both SpansWriter listeners (single-box
// Unix socket and split-box mTLS) share one accumulator and flush loop.
type guestSpansIngester struct {
	acc      *gateway.SpansAccumulator
	accounts guestSpansAccounts
	limiter  *peraccount.Limiter
	log      *slog.Logger
}

func newGuestSpansIngester(accounts guestSpansAccounts, limiter *peraccount.Limiter, log *slog.Logger) *guestSpansIngester {
	if limiter == nil {
		limiter = peraccount.NewLimiter()
	}
	if log == nil {
		log = slog.Default()
	}
	return &guestSpansIngester{acc: gateway.NewSpansAccumulator(), accounts: accounts, limiter: limiter, log: log}
}

// limitsFor returns the account's plan limits, caching them in the shared
// limiter so steady-state exports avoid a database read.
func (g *guestSpansIngester) limitsFor(ctx context.Context, accountID uuid.UUID) (api.Limits, error) {
	if limits, ok := g.limiter.CachedLimits(accountID); ok {
		return limits, nil
	}
	acct, err := g.accounts.AccountByID(ctx, accountID.String())
	if err != nil {
		return api.Limits{}, err
	}
	limits := api.MustLimitsFor(acct.Plan)
	g.limiter.CacheLimits(accountID, limits)
	return limits, nil
}

func (g *guestSpansIngester) ingest(ctx context.Context, req *apidpb.IngestGuestSpansRequest) *apidpb.IngestGuestSpansResponse {
	accountID, err := uuid.Parse(req.GetAccountId())
	if err != nil || req.GetAppId() == "" || req.GetDeploymentId() == "" {
		return &apidpb.IngestGuestSpansResponse{Outcome: guestSpansInvalid}
	}
	codec := req.GetCodec()
	if codec > 0xff {
		return &apidpb.IngestGuestSpansResponse{Outcome: guestSpansInvalid}
	}
	contentType, contentEncoding, ok := api.TraceCodecMediaType(byte(codec))
	if !ok || len(req.GetPayload()) == 0 || len(req.GetPayload()) > api.TraceMaxFrameBytes {
		return &apidpb.IngestGuestSpansResponse{Outcome: guestSpansInvalid}
	}
	limits, err := g.limitsFor(ctx, accountID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return &apidpb.IngestGuestSpansResponse{Outcome: guestSpansDisabled}
		}
		g.log.Warn("guest spans: account lookup failed", "err", err)
		return &apidpb.IngestGuestSpansResponse{Outcome: guestSpansUnavailable}
	}
	if !limits.DebugTelemetryEnabled {
		return &apidpb.IngestGuestSpansResponse{Outcome: guestSpansDisabled}
	}
	if g.acc.Len() >= guestSpansMaxBuckets {
		return &apidpb.IngestGuestSpansResponse{Outcome: guestSpansRateLimited, RetryAfterMs: int64(time.Second / time.Millisecond)}
	}
	if taken, retryMs := g.limiter.Take(accountID, limits.DebugTelemetryRequestsPerMinute); !taken {
		return &apidpb.IngestGuestSpansResponse{Outcome: guestSpansRateLimited, RetryAfterMs: retryMs}
	}
	accepted, rejected, err := g.acc.AddOTLPTraceExport(accountID, contentType, contentEncoding, req.GetPayload(), api.TraceMaxDecodedBytes)
	if err != nil {
		return &apidpb.IngestGuestSpansResponse{Outcome: guestSpansInvalid}
	}
	return &apidpb.IngestGuestSpansResponse{Outcome: guestSpansAccepted, AcceptedSpans: int64(accepted), RejectedSpans: int64(rejected)}
}

// run flushes coalesced summaries through write until ctx ends.
func (g *guestSpansIngester) run(ctx context.Context, interval time.Duration, write func(context.Context, *apidpb.WriteSpansSummaryRequest) (*apidpb.WriteSpansSummaryResponse, error)) error {
	return g.acc.RunFlushLoop(ctx, gateway.FlushLoopConfig{
		Interval: interval,
		WriteFn: func(writeCtx context.Context, traceID string, summaryJSON []byte, accountID string) (string, int64, error) {
			resp, err := write(writeCtx, &apidpb.WriteSpansSummaryRequest{TraceId: traceID, SummaryJson: summaryJSON, AccountId: accountID})
			if err != nil {
				return "", 0, err
			}
			return resp.GetOutcome(), resp.GetRetryAfterMs(), nil
		},
		Log: g.log,
		// UpdateSpansSummary keeps the slowest Scale-tier maximum across all
		// producers; per-trace volume is bounded upstream by the rate cap.
		MaxSpansPerTrace: func(string) int {
			return api.MustLimitsFor(api.PlanScale).DebugTelemetrySpansPerTrace
		},
	})
}

// IngestGuestSpans implements the SpansWriter RPC. A receiver without an
// ingester (tests, or a listener registered before startup wiring) reports
// unavailable so the guest SDK retries later.
func (r *spansWriterReceiver) IngestGuestSpans(ctx context.Context, req *apidpb.IngestGuestSpansRequest) (*apidpb.IngestGuestSpansResponse, error) {
	if !r.enabled || r.guest == nil {
		return &apidpb.IngestGuestSpansResponse{Outcome: guestSpansUnavailable}, nil
	}
	return r.guest.ingest(ctx, req), nil
}

// otelFlushInterval mirrors the gateway producers' FAAS_OTEL_FLUSH_INTERVAL
// knob (default 30s).
func otelFlushInterval(getenv func(string) string) time.Duration {
	if value := getenv("FAAS_OTEL_FLUSH_INTERVAL"); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
			return parsed
		}
	}
	return 30 * time.Second
}
