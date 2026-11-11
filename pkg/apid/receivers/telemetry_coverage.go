package receivers

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/state"
)

// Coverage bypasses debugger rate/plan gates, including when collection is disabled.
func (r *requestTelemetryReceiver) RecordTelemetryCoverage(ctx context.Context, req *apidpb.TelemetryCoverage) (*apidpb.TelemetryCoverageReceipt, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "telemetry coverage is required")
	}
	coverage := state.TelemetryCoverage{NodeName: req.GetNodeName(), BootID: req.GetBootId(), Sequence: req.GetSequence(),
		Enabled: req.GetEnabled() && r.enabled, SamplingBasisPoints: req.GetSamplingBasisPoints(), DroppedTotal: req.GetDroppedTotal(),
		PendingCount: req.GetPendingCount(), SourceAt: time.UnixMilli(req.GetSourceAtUnixMs()).UTC(),
		AppScoped: req.GetAppScoped(), UnattributedDroppedTotal: req.GetUnattributedDroppedTotal()}
	for _, gap := range req.GetAppGaps() {
		coverage.AppGaps = append(coverage.AppGaps, state.TelemetryAppGap{AppID: gap.GetAppId(), DroppedCount: gap.GetDroppedCount(), PendingCount: gap.GetPendingCount(),
			AnalyticsDroppedCount: gap.GetAnalyticsDroppedCount(), AnalyticsPendingCount: gap.GetAnalyticsPendingCount()})
	}
	if a := req.GetAnalytics(); a != nil {
		// A gateway counting its own analytics (ADR-20261103113952
		// amendment 6). Counting only matters while apid keeps rollups.
		coverage.Analytics = &state.AnalyticsCoverage{Counting: a.GetCounting() && r.rollups != nil, UnattributedDroppedTotal: a.GetUnattributedDroppedTotal()}
	}
	if err := state.ValidateTelemetryCoverage(coverage); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid telemetry coverage")
	}
	store, ok := r.store.(state.TelemetryCoverageStore)
	if !ok {
		return nil, status.Error(codes.Unavailable, "telemetry coverage store unavailable")
	}
	if err := store.RecordTelemetryCoverage(ctx, coverage); err != nil {
		return nil, status.Error(codes.Internal, "record telemetry coverage failed")
	}
	return &apidpb.TelemetryCoverageReceipt{Recorded: true}, nil
}
