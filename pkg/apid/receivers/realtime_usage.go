package receivers

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// ReportUsage records one realtimed node's usage for one hour (ADR-1323). The
// node must be registered; account and app come from the endpoint row, never
// from the report.
func (r *realtimeFleetReceiver) ReportUsage(ctx context.Context, envelope *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error) {
	if r.usage == nil {
		return nil, status.Error(codes.Unavailable, "realtime usage store unavailable")
	}
	if envelope == nil || len(envelope.GetValue()) == 0 || len(envelope.GetValue()) > api.RealtimeUsageReportMaxBytes {
		return nil, status.Error(codes.InvalidArgument, "invalid realtime usage report")
	}
	var report state.RealtimeUsageReport
	if err := decodeRealtimeFleetJSON(envelope.GetValue(), &report); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid realtime usage report")
	}
	if _, err := r.lookupNode(ctx, report.NodeName); err != nil {
		return nil, err
	}
	recorded, err := r.usage.RecordRealtimeUsage(ctx, report)
	switch {
	case errors.Is(err, state.ErrRealtimeUsageInvalid):
		return nil, status.Error(codes.InvalidArgument, err.Error())
	case err != nil:
		return nil, status.Error(codes.Unavailable, "realtime usage store unavailable")
	}
	data, err := json.Marshal(map[string]int{"recorded": recorded})
	if err != nil {
		return nil, status.Error(codes.Internal, "encode realtime usage response")
	}
	return wrapperspb.Bytes(data), nil
}
