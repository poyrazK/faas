package main

import (
	"context"
	"time"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
		coverage.AppGaps = append(coverage.AppGaps, state.TelemetryAppGap{AppID: gap.GetAppId(), DroppedCount: gap.GetDroppedCount(), PendingCount: gap.GetPendingCount()})
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
