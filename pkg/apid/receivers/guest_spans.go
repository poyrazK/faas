// apid-side receiver for in-guest trace exports (ADR-958).
//
// Direction: vmmd → apid IngestGuestSpans on the SpansWriter service. vmmd
// bounds the frame and supplies the host-owned principal; it never decodes
// the payload. apid owns plan admission, the per-account rate cap, OTLP
// decoding and the merge into request_telemetry.spans_summary. Coalesced
// summaries are flushed back through WriteSpansSummary so they share its
// validation, rate limiting and outcome metrics with the other producers.

package receivers

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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

// GuestSpansIngester is process-wide: both SpansWriter listeners (single-box
// Unix socket and split-box mTLS) share one accumulator and flush loop.
type GuestSpansIngester struct {
	acc     *gateway.SpansAccumulator
	limiter *peraccount.Limiter
	// plans resolves the app's resource owner's plan and the bucket its
	// exports draw from (tracing tenancy T6).
	plans *telemetryPlans
	// released, when set by WithTracingRelease, refuses exports while the
	// tracing capability is off for the app's resource owner.
	released TracingReleased
	log      *slog.Logger
	// logs, when set by WithLogs, ingests guest OTLP log exports (ADR-213
	// runtime adapter); nil reports log exports unavailable.
	logs *guestLogsIngester
	// binding, when set by WithPeerNodes, accepts a mutual-TLS caller's
	// frames only for instances on its node.
	binding *guestBinding
}

func NewGuestSpansIngester(accounts guestSpansAccounts, limiter *peraccount.Limiter, log *slog.Logger) *GuestSpansIngester {
	if limiter == nil {
		limiter = peraccount.NewLimiter()
	}
	if log == nil {
		log = slog.Default()
	}
	return &GuestSpansIngester{acc: gateway.NewSpansAccumulator(), limiter: limiter, plans: newTelemetryPlans(accounts, limiter), log: log}
}

// WithTracingRelease gates exports on the tracing capability's platform flag
// for the app's resource owner, as apid gates the tracing routes and settings
// (ADR-20261023115741). An export for an account the capability is off for is
// refused as disabled.
func (g *GuestSpansIngester) WithTracingRelease(released TracingReleased) *GuestSpansIngester {
	g.released = released
	return g
}

// planFor returns the plan of the app's resource owner (tracing tenancy T6):
// a company app is admitted, bounded and rate limited by its organization's
// plan, as request-telemetry retention keeps its rows, not by the plan of
// whoever created it. Steady-state exports read it from cache.
func (g *GuestSpansIngester) planFor(ctx context.Context, accountID uuid.UUID, appID string) (telemetryPlan, error) {
	app, err := uuid.Parse(appID)
	if err != nil {
		return g.plans.forAccount(ctx, accountID)
	}
	return g.plans.forApp(ctx, accountID, app)
}

func (g *GuestSpansIngester) ingest(ctx context.Context, req *apidpb.IngestGuestSpansRequest) *apidpb.IngestGuestSpansResponse {
	accountID, err := uuid.Parse(req.GetAccountId())
	if err != nil || req.GetDeploymentId() == "" {
		return &apidpb.IngestGuestSpansResponse{Outcome: guestSpansInvalid}
	}
	// The app is host-owned (vmmd resolves it from the instance); the
	// spans merge only into its own request rows (tracing tenancy T4).
	appID, err := uuid.Parse(req.GetAppId())
	if err != nil {
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
	plan, err := g.planFor(ctx, accountID, req.GetAppId())
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return &apidpb.IngestGuestSpansResponse{Outcome: guestSpansDisabled}
		}
		g.log.Warn("guest spans: plan lookup failed", "err", err)
		return &apidpb.IngestGuestSpansResponse{Outcome: guestSpansUnavailable}
	}
	limits := plan.Limits
	if !limits.DebugTelemetryEnabled || (g.released != nil && !g.released(plan.Flag)) {
		return &apidpb.IngestGuestSpansResponse{Outcome: guestSpansDisabled}
	}
	if g.acc.Len() >= guestSpansMaxBuckets {
		return &apidpb.IngestGuestSpansResponse{Outcome: guestSpansRateLimited, RetryAfterMs: int64(time.Second / time.Millisecond)}
	}
	if taken, retryMs := g.limiter.TakeForApp(plan.Subject, appID, limits.DebugTelemetryRequestsPerMinute); !taken {
		return &apidpb.IngestGuestSpansResponse{Outcome: guestSpansRateLimited, RetryAfterMs: retryMs}
	}
	target := gateway.SpanTarget{AccountID: accountID, AppID: appID}
	accepted, rejected, err := g.acc.AddOTLPTraceExportFor(target, string(limits.Plan), contentType, contentEncoding, req.GetPayload(), api.TraceMaxDecodedBytes)
	if err != nil {
		return &apidpb.IngestGuestSpansResponse{Outcome: guestSpansInvalid}
	}
	return &apidpb.IngestGuestSpansResponse{Outcome: guestSpansAccepted, AcceptedSpans: int64(accepted), RejectedSpans: int64(rejected)}
}

// run flushes coalesced summaries through write until ctx ends.
func (g *GuestSpansIngester) Run(ctx context.Context, interval time.Duration, write func(context.Context, *apidpb.WriteSpansSummaryRequest) (*apidpb.WriteSpansSummaryResponse, error)) error {
	return g.acc.RunFlushLoop(ctx, gateway.FlushLoopConfig{
		Interval: interval,
		WriteTargetFn: func(writeCtx context.Context, traceID string, summaryJSON []byte, target gateway.SpanTarget) (string, int64, error) {
			resp, err := write(writeCtx, target.Request(traceID, summaryJSON))
			if err != nil {
				return "", 0, err
			}
			return resp.GetOutcome(), resp.GetRetryAfterMs(), nil
		},
		Log: g.log,
		// Each trace is bounded by its account's plan (ingest passes it).
		MaxSpansPerTrace: gateway.MaxSpansPerTraceForPlan,
	})
}

// IngestGuestSpans implements the SpansWriter RPC. A receiver without an
// ingester (tests, or a listener registered before startup wiring) reports
// unavailable so the guest SDK retries later.
func (r *spansWriterReceiver) IngestGuestSpans(ctx context.Context, req *apidpb.IngestGuestSpansRequest) (*apidpb.IngestGuestSpansResponse, error) {
	if !r.enabled || r.guest == nil {
		return &apidpb.IngestGuestSpansResponse{Outcome: guestSpansUnavailable}, nil
	}
	refused, err := r.guest.bindToPeerNode(ctx, req)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "guest spans: %v", err)
	}
	if refused != "" {
		return &apidpb.IngestGuestSpansResponse{Outcome: refused}, nil
	}
	return r.guest.ingest(ctx, req), nil
}

// OtelFlushInterval mirrors the gateway producers' FAAS_OTEL_FLUSH_INTERVAL
// knob (default 30s).
func OtelFlushInterval(getenv func(string) string) time.Duration {
	if value := getenv("FAAS_OTEL_FLUSH_INTERVAL"); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
			return parsed
		}
	}
	return 30 * time.Second
}
