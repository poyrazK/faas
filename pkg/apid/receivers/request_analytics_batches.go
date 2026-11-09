package receivers

import (
	"context"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/requestanalytics"
	"github.com/onebox-faas/faas/pkg/state"
)

// AddRequestAnalyticsRollups applies one gateway's per-minute analytics
// rollups, once per batch_id (ADR-20261103113952 amendment 5). Rows of apps
// whose plan keeps no request telemetry are left out, as apid's own
// counting leaves them out; rows that are not valid rollups are left out
// and counted as rejected. A storage failure answers Unavailable, and the
// gateway resends the batch with the same batch_id.
func (r *requestTelemetryReceiver) AddRequestAnalyticsRollups(ctx context.Context, req *apidpb.AddRequestAnalyticsRollupsRequest) (*apidpb.AddRequestAnalyticsRollupsResponse, error) {
	if !r.enabled {
		return nil, status.Error(codes.Unavailable, "request_telemetry disabled by FAAS_REQUEST_TELEMETRY_ENABLED")
	}
	store, ok := r.store.(state.RequestAnalyticsRollupBatchStore)
	if !ok || r.rollups == nil {
		// The gateway keeps leaving the counting to apid.
		return nil, status.Error(codes.Unimplemented, "this apid keeps no analytics rollups")
	}
	if len(req.GetRows()) > api.RequestAnalyticsRollupBatchRowsMax {
		return nil, status.Errorf(codes.InvalidArgument, "a batch carries at most %d rows, got %d", api.RequestAnalyticsRollupBatchRowsMax, len(req.GetRows()))
	}
	batchID, err := uuid.Parse(req.GetBatchId())
	if err != nil || batchID == uuid.Nil {
		return nil, status.Error(codes.InvalidArgument, "batch_id must be a UUID")
	}
	nodeID := req.GetNodeId()
	if len(nodeID) > 255 {
		return nil, status.Error(codes.InvalidArgument, "node_id is longer than 255 bytes")
	}
	out := &apidpb.AddRequestAnalyticsRollupsResponse{}
	agg := requestanalytics.NewAggregator(len(req.GetRows()) + 1)
	first := map[uuid.UUID]time.Time{}
	for _, p := range req.GetRows() {
		row, ok := rollupFromProto(p)
		if !ok {
			out.Rejected++
			continue
		}
		plan, err := r.plans.forApp(ctx, row.AccountID, row.AppID)
		if err != nil || !plan.Limits.DebugTelemetryEnabled {
			out.NotRetained++
			continue
		}
		// One statement cannot update a row twice: merge repeated keys.
		agg.Restore([]state.RequestAnalyticsRollup{row})
		if at, seen := first[row.AppID]; !seen || row.BucketStart.Before(at) {
			first[row.AppID] = row.BucketStart
		}
	}
	rows := agg.Take(0, true)
	coverage := make([]state.RequestAnalyticsCoverage, 0, len(first))
	for app, at := range first {
		coverage = append(coverage, state.RequestAnalyticsCoverage{AppID: app, CoveredSince: at})
	}
	release, err := r.gate.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	replayed, err := store.AddRequestAnalyticsRollupBatch(ctx, batchID, nodeID, rows, coverage)
	if err != nil {
		if state.IsRequestAnalyticsDataError(err) {
			// Resending the same rows fails again: the gateway drops the
			// batch and records its minutes as lost.
			return nil, status.Errorf(codes.InvalidArgument, "analytics rollup batch refused: %v", err)
		}
		return nil, status.Errorf(codes.Unavailable, "apply analytics rollup batch: %v", err)
	}
	out.Replayed = replayed
	return out, nil
}

// rollupFromProto validates one gateway rollup row against the
// request_analytics_minutes contract.
func rollupFromProto(p *apidpb.RequestAnalyticsRollupRow) (state.RequestAnalyticsRollup, bool) {
	var row state.RequestAnalyticsRollup
	accountID, err1 := uuid.Parse(p.GetAccountId())
	appID, err2 := uuid.Parse(p.GetAppId())
	deploymentID, err3 := uuid.Parse(p.GetDeploymentId())
	if err1 != nil || err2 != nil || err3 != nil || appID == uuid.Nil {
		return row, false
	}
	start := time.Unix(p.GetBucketStartUnix(), 0).UTC()
	if p.GetBucketStartUnix() <= 0 || !start.Equal(start.Truncate(time.Minute)) {
		return row, false
	}
	kind, dimension, method := p.GetDimensionKind(), p.GetDimension(), p.GetMethod()
	route := kind == requestanalytics.KindRoute
	if !route && !validFoldedKind(kind) {
		return row, false
	}
	if dimension == "" || len(dimension) > 512 || !utf8.ValidString(dimension) || len(method) > 16 || !utf8.ValidString(method) {
		return row, false
	}
	httpStatus := p.GetStatus()
	if route && (httpStatus < 100 || httpStatus > 599) || !route && (httpStatus != requestanalytics.FoldedStatus || method != "" || deploymentID != uuid.Nil) {
		return row, false
	}
	requests := p.GetRequests()
	if requests < 1 || p.GetErrorRequests() < 0 || p.GetErrorRequests() > requests ||
		p.GetColdBoots() < 0 || p.GetColdBoots() > requests || p.GetRequestTimeMs() < 0 || p.GetLatencyMaxMs() < 0 ||
		len(p.GetLatencyHist()) != api.RequestAnalyticsLatencyBuckets {
		return row, false
	}
	row = state.RequestAnalyticsRollup{
		AppID: appID, AccountID: accountID, DeploymentID: deploymentID, BucketStart: start,
		DimensionKind: kind, Dimension: dimension, Method: method, Status: int16(httpStatus),
		Requests: requests, ErrorRequests: p.GetErrorRequests(), ColdBoots: p.GetColdBoots(),
		RequestTimeMS: p.GetRequestTimeMs(), LatencyMaxMS: p.GetLatencyMaxMs(),
	}
	for i, n := range p.GetLatencyHist() {
		if n < 0 {
			return state.RequestAnalyticsRollup{}, false
		}
		row.LatencyHist[i] = n
	}
	return row, true
}

func validFoldedKind(kind string) bool {
	for _, k := range requestanalytics.FoldedKinds {
		if k == kind {
			return true
		}
	}
	return false
}
