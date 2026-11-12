// apid-side receiver for in-guest OTLP metric exports (ADR-20261108160000
// slice B, ADR-958 bridge).
//
// Direction: vmmd → apid IngestGuestMetrics on the SpansWriter service, with
// the same host-owned identity as trace and log exports, for apps with
// tracing.metrics. apid owns plan admission, the per-account export cap and
// decoding; storing the export as the app's custom metrics is apidserver's
// (GuestMetricsSink), which translates and caps it exactly like the public
// OTLP metrics endpoint.

package receivers

import (
	"context"
	"errors"
	"fmt"
	"mime"

	"github.com/google/uuid"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/ratelimit/peraccount"
	"github.com/onebox-faas/faas/pkg/state"
)

// ErrGuestMetricsDisabled is returned by a GuestMetricsSink when the
// deployment or the app's plan does not take custom metrics; the receiver
// answers "disabled", which the guest SDK does not retry.
var ErrGuestMetricsDisabled = errors.New("receivers: custom metrics disabled")

// GuestMetricsSink stores one decoded in-guest metric export as the app's
// custom metrics and reports how many data points were stored and rejected.
type GuestMetricsSink interface {
	StoreGuestMetrics(ctx context.Context, accountID, appID string, export *colmetricspb.ExportMetricsServiceRequest) (accepted, rejected int64, err error)
}

// guestMetricsIngester has its own limiter, so metric exports never consume
// the trace or log budget.
type guestMetricsIngester struct {
	sink    GuestMetricsSink
	limiter *peraccount.Limiter
}

// WithMetrics enables guest metric ingestion through sink.
func (g *GuestSpansIngester) WithMetrics(sink GuestMetricsSink) *GuestSpansIngester {
	g.metrics = &guestMetricsIngester{sink: sink, limiter: peraccount.NewLimiter()}
	return g
}

func (g *GuestSpansIngester) ingestMetrics(ctx context.Context, req *apidpb.IngestGuestSpansRequest) *apidpb.IngestGuestMetricsResponse {
	outcome := func(value string) *apidpb.IngestGuestMetricsResponse {
		return &apidpb.IngestGuestMetricsResponse{Outcome: value}
	}
	if g.metrics == nil {
		return outcome(guestSpansUnavailable)
	}
	accountID, err := uuid.Parse(req.GetAccountId())
	if err != nil || req.GetAppId() == "" || req.GetDeploymentId() == "" || req.GetCodec() > 0xff {
		return outcome(guestSpansInvalid)
	}
	contentType, contentEncoding, ok := api.TraceCodecMediaType(byte(req.GetCodec()))
	if !ok || len(req.GetPayload()) == 0 || len(req.GetPayload()) > api.TraceMaxFrameBytes {
		return outcome(guestSpansInvalid)
	}
	plan, err := g.planFor(ctx, accountID, req.GetAppId())
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return outcome(guestSpansDisabled)
		}
		g.log.Warn("guest metrics: plan lookup failed", "err", err)
		return outcome(guestSpansUnavailable)
	}
	limits := plan.Limits
	// The per-minute export cap is the log export cap: both come from the
	// same SDK on the same export interval.
	if !limits.CustomMetricsAllowed || limits.DebugGuestLogExportsPerMinute <= 0 {
		return outcome(guestSpansDisabled)
	}
	if taken, retryMs := g.metrics.limiter.Take(plan.Subject, limits.DebugGuestLogExportsPerMinute); !taken {
		return &apidpb.IngestGuestMetricsResponse{Outcome: guestSpansRateLimited, RetryAfterMs: retryMs}
	}
	export, err := decodeGuestMetricsExport(contentType, contentEncoding, req.GetPayload())
	if err != nil {
		return outcome(guestSpansInvalid)
	}
	accepted, rejected, err := g.metrics.sink.StoreGuestMetrics(ctx, accountID.String(), req.GetAppId(), export)
	switch {
	case errors.Is(err, ErrGuestMetricsDisabled):
		return outcome(guestSpansDisabled)
	case err != nil:
		g.log.Warn("guest metrics: store failed", "err", err)
		return outcome(guestSpansUnavailable)
	}
	return &apidpb.IngestGuestMetricsResponse{Outcome: guestSpansAccepted, AcceptedPoints: accepted, RejectedPoints: rejected}
}

// IngestGuestMetrics implements the SpansWriter RPC.
func (r *spansWriterReceiver) IngestGuestMetrics(ctx context.Context, req *apidpb.IngestGuestSpansRequest) (*apidpb.IngestGuestMetricsResponse, error) {
	if !r.enabled || r.guest == nil {
		return &apidpb.IngestGuestMetricsResponse{Outcome: guestSpansUnavailable}, nil
	}
	refused, err := r.guest.bindToPeerNode(ctx, req)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "guest metrics: %v", err)
	}
	if refused != "" {
		return &apidpb.IngestGuestMetricsResponse{Outcome: refused}, nil
	}
	return r.guest.ingestMetrics(ctx, req), nil
}

// decodeGuestMetricsExport decodes one export within the public OTLP
// metrics endpoint's decoded-size bound. OTLP/JSON metrics carry no IDs, so
// protojson reads them correctly.
func decodeGuestMetricsExport(contentType, contentEncoding string, payload []byte) (*colmetricspb.ExportMetricsServiceRequest, error) {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, err
	}
	raw, err := gateway.DecodeOTLPContentEncoding(contentEncoding, payload, api.OTLPMetricsMaxBodyBytes)
	if err != nil {
		return nil, err
	}
	var export colmetricspb.ExportMetricsServiceRequest
	switch mediaType {
	case "application/x-protobuf":
		err = proto.Unmarshal(raw, &export)
	case "application/json":
		err = protojson.Unmarshal(raw, &export)
	default:
		err = fmt.Errorf("unsupported content type %q", mediaType)
	}
	if err != nil {
		return nil, err
	}
	return &export, nil
}
