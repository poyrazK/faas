package apidgrpc

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/onebox-faas/faas/pkg/realtime"
)

const realtimeFleetOccupancyMethod = "/onebox.faas.apid.v1.RealtimeFleet/OccupancyOperation"

type realtimeOccupancyMessage struct {
	NodeName   string         `json:"node_name,omitempty"`
	EndpointID string         `json:"endpoint_id,omitempty"`
	Channels   []string       `json:"channels,omitempty"`
	Counts     map[string]int `json:"counts,omitempty"`
}

// ReadRemoteOccupancy returns the other realtime nodes' subscriber counts on
// an endpoint's channels for live occupancy frames (ADR-1324).
func (c *RealtimeHistoryClientImpl) ReadRemoteOccupancy(ctx context.Context, endpointID string, channels []string) (map[string]int, error) {
	if c == nil || c.conn == nil || c.nodeName == "" {
		return nil, fmt.Errorf("apidgrpc: realtime occupancy requires a connection and node name")
	}
	data, err := json.Marshal(realtimeOccupancyMessage{NodeName: c.nodeName, EndpointID: endpointID, Channels: channels})
	if err != nil {
		return nil, err
	}
	response := new(wrapperspb.BytesValue)
	if err := c.conn.Invoke(ctx, realtimeFleetOccupancyMethod, wrapperspb.Bytes(data), response); err != nil {
		return nil, fmt.Errorf("apidgrpc: read realtime occupancy: %w", err)
	}
	var out realtimeOccupancyMessage
	if err := json.Unmarshal(response.GetValue(), &out); err != nil {
		return nil, fmt.Errorf("apidgrpc: decode realtime occupancy: %w", err)
	}
	return out.Counts, nil
}

var _ realtime.ManagedRealtimeOccupancyClient = (*RealtimeHistoryClientImpl)(nil)
