package apidgrpc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
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
	NodeName string                             `json:"node_name,omitempty"`
	Accounts []state.RealtimeAccountConnections `json:"accounts"`
}

// SyncRealtimeConnections reports this node's per-account connection counts
// and returns the other nodes' fresh counts (ADR-1323 fair use).
func (c *RealtimeHistoryClientImpl) SyncRealtimeConnections(ctx context.Context, counts []state.RealtimeAccountConnections) ([]state.RealtimeAccountConnections, error) {
	if c == nil || c.conn == nil || c.nodeName == "" {
		return nil, fmt.Errorf("apidgrpc: realtime connection sync requires a connection and node name")
	}
	data, err := json.Marshal(realtimeConnectionSyncMessage{NodeName: c.nodeName, Accounts: counts})
	if err != nil {
		return nil, err
	}
	response := new(wrapperspb.BytesValue)
	if err := c.conn.Invoke(ctx, realtimeFleetConnectionsMethod, wrapperspb.Bytes(data), response); err != nil {
		return nil, fmt.Errorf("apidgrpc: sync realtime connections: %w", err)
	}
	var out realtimeConnectionSyncMessage
	if err := json.Unmarshal(response.GetValue(), &out); err != nil {
		return nil, fmt.Errorf("apidgrpc: decode realtime connection counts: %w", err)
	}
	return out.Accounts, nil
}

var _ realtime.ConnectionSyncer = (*RealtimeHistoryClientImpl)(nil)
