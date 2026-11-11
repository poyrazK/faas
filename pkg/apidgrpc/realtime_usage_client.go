package apidgrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const realtimeFleetUsageMethod = "/onebox.faas.apid.v1.RealtimeFleet/ReportUsage"

// ReportRealtimeUsage sends one hour of this node's realtime usage to apid
// (ADR-1323). The node name comes from the client, not the caller.
func (c *RealtimeHistoryClientImpl) ReportRealtimeUsage(ctx context.Context, report state.RealtimeUsageReport) error {
	if c == nil || c.conn == nil || c.nodeName == "" {
		return fmt.Errorf("apidgrpc: realtime usage requires a connection and node name")
	}
	report.NodeName = c.nodeName
	data, err := json.Marshal(report)
	if err != nil {
		return err
	}
	if err := c.conn.Invoke(ctx, realtimeFleetUsageMethod, wrapperspb.Bytes(data), new(wrapperspb.BytesValue)); err != nil {
		return fmt.Errorf("apidgrpc: report realtime usage: %w", err)
	}
	return nil
}

var _ realtime.UsageReporter = (*RealtimeHistoryClientImpl)(nil)

const realtimeFleetConnectionsMethod = "/onebox.faas.apid.v1.RealtimeFleet/SyncConnections"

type realtimeConnectionSyncMessage struct {
	NodeName  string                              `json:"node_name,omitempty"`
	Accounts  []state.RealtimeAccountConnections  `json:"accounts"`
	Endpoints []state.RealtimeEndpointConnections `json:"endpoints,omitempty"`
}

// realtimeEndpointSyncRetryAfter is how long a node leaves endpoint counts
// out after an apid that predates them refused the field.
const realtimeEndpointSyncRetryAfter = 10 * time.Minute

// SyncRealtimeConnections reports this node's per-account connection counts
// and returns the other nodes' fresh counts (ADR-1323 fair use).
func (c *RealtimeHistoryClientImpl) SyncRealtimeConnections(ctx context.Context, counts []state.RealtimeAccountConnections) ([]state.RealtimeAccountConnections, error) {
	accounts, _, err := c.SyncRealtimeConnectionsWithEndpoints(ctx, counts, nil)
	return accounts, err
}

// SyncRealtimeConnectionsWithEndpoints also reports capped endpoints' counts
// and returns the other nodes' counts for them. An apid that predates
// endpoint counts refuses the field; the node then syncs accounts alone, so
// its endpoint caps stay per node, and tries again after a while.
func (c *RealtimeHistoryClientImpl) SyncRealtimeConnectionsWithEndpoints(ctx context.Context, counts []state.RealtimeAccountConnections, endpoints []state.RealtimeEndpointConnections) ([]state.RealtimeAccountConnections, []state.RealtimeEndpointConnections, error) {
	if c == nil || c.conn == nil || c.nodeName == "" {
		return nil, nil, fmt.Errorf("apidgrpc: realtime connection sync requires a connection and node name")
	}
	if time.Now().UnixNano() < c.endpointSyncRefusedUntil.Load() {
		endpoints = nil
	}
	out, err := c.syncConnections(ctx, realtimeConnectionSyncMessage{NodeName: c.nodeName, Accounts: counts, Endpoints: endpoints})
	if len(endpoints) > 0 && status.Code(err) == codes.InvalidArgument {
		c.endpointSyncRefusedUntil.Store(time.Now().Add(realtimeEndpointSyncRetryAfter).UnixNano())
		out, err = c.syncConnections(ctx, realtimeConnectionSyncMessage{NodeName: c.nodeName, Accounts: counts})
	}
	if err != nil {
		return nil, nil, err
	}
	return out.Accounts, out.Endpoints, nil
}

func (c *RealtimeHistoryClientImpl) syncConnections(ctx context.Context, message realtimeConnectionSyncMessage) (realtimeConnectionSyncMessage, error) {
	data, err := json.Marshal(message)
	if err != nil {
		return realtimeConnectionSyncMessage{}, err
	}
	response := new(wrapperspb.BytesValue)
	if err := c.conn.Invoke(ctx, realtimeFleetConnectionsMethod, wrapperspb.Bytes(data), response); err != nil {
		return realtimeConnectionSyncMessage{}, fmt.Errorf("apidgrpc: sync realtime connections: %w", err)
	}
	var out realtimeConnectionSyncMessage
	if err := json.Unmarshal(response.GetValue(), &out); err != nil {
		return realtimeConnectionSyncMessage{}, fmt.Errorf("apidgrpc: decode realtime connection counts: %w", err)
	}
	return out, nil
}

var (
	_ realtime.ConnectionSyncer         = (*RealtimeHistoryClientImpl)(nil)
	_ realtime.EndpointConnectionSyncer = (*RealtimeHistoryClientImpl)(nil)
)
