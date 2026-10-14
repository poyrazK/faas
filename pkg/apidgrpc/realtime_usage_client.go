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
// (ADR-1238). The node name comes from the client, not the caller.
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
