package main

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type analyticsRollupClient interface {
	AddRequestAnalyticsRollups(ctx context.Context, req *apidpb.AddRequestAnalyticsRollupsRequest) (*apidpb.AddRequestAnalyticsRollupsResponse, error)
}

// shipAnalyticsRollups sends one gateway rollup batch and maps apid's answer
// onto the gateway's contract (ADR-20261103113952 amendment 5): Unimplemented
// means apid takes no batches, InvalidArgument that this batch never will be
// taken; anything else is resent with the same batch_id.
func shipAnalyticsRollups(ctx context.Context, cli analyticsRollupClient, batchID uuid.UUID, nodeID string, rows []state.RequestAnalyticsRollup) error {
	req := &apidpb.AddRequestAnalyticsRollupsRequest{BatchId: batchID.String(), NodeId: nodeID, Rows: make([]*apidpb.RequestAnalyticsRollupRow, 0, len(rows))}
	for _, r := range rows {
		req.Rows = append(req.Rows, &apidpb.RequestAnalyticsRollupRow{
			AccountId: r.AccountID.String(), AppId: r.AppID.String(), DeploymentId: r.DeploymentID.String(),
			BucketStartUnix: r.BucketStart.Unix(), DimensionKind: r.DimensionKind, Dimension: r.Dimension,
			Method: r.Method, Status: int32(r.Status), Requests: r.Requests, ErrorRequests: r.ErrorRequests,
			ColdBoots: r.ColdBoots, RequestTimeMs: r.RequestTimeMS, LatencyMaxMs: r.LatencyMaxMS,
			LatencyHist: append([]int64(nil), r.LatencyHist[:]...),
		})
	}
	_, err := cli.AddRequestAnalyticsRollups(ctx, req)
	switch status.Code(err) {
	case codes.OK:
		return nil
	case codes.Unimplemented:
		return fmt.Errorf("%w: %w", gateway.ErrRequestAnalyticsRollupsUnsupported, err)
	case codes.InvalidArgument:
		return fmt.Errorf("%w: %w", gateway.ErrRequestAnalyticsRollupBatchRefused, err)
	}
	return fmt.Errorf("ship analytics rollup batch: %w", err)
}
