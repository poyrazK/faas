package receivers

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/realtime"
)

// Live channel occupancy for realtime nodes (ADR-1324). A node asks for the
// other nodes' subscriber counts on the channels its clients watch. apid
// reads the fleet through the realtime owner and keeps each channel's
// per-node counts for api.RealtimeOccupancyCacheTTL, so nodes watching the
// same channel share one fleet read.

// RealtimeOccupancyReader is the realtime owner's fleet occupancy read.
type RealtimeOccupancyReader interface {
	ChannelOccupancy(context.Context, string, []string) (realtime.OccupancyInventory, error)
}

// realtimeOccupancyMaxBytes bounds a request: 20 channels of at most 256 bytes.
const realtimeOccupancyMaxBytes = 16 << 10

// realtimeOccupancyCachePrune is the entry count past which a read drops
// expired entries.
const realtimeOccupancyCachePrune = 4096

type realtimeOccupancyMessage struct {
	NodeName   string         `json:"node_name,omitempty"`
	EndpointID string         `json:"endpoint_id,omitempty"`
	Channels   []string       `json:"channels,omitempty"`
	Counts     map[string]int `json:"counts,omitempty"`
}

type realtimeOccupancyEntry struct {
	nodes map[string]int // node ID → subscribers
	at    time.Time
}

type realtimeOccupancyCache struct {
	mu      sync.Mutex
	entries map[string]realtimeOccupancyEntry // endpoint ID + "\x00" + channel
}

func newRealtimeOccupancyCache() *realtimeOccupancyCache {
	return &realtimeOccupancyCache{entries: make(map[string]realtimeOccupancyEntry)}
}

// read returns each channel's per-node counts, reading the fleet only for
// channels whose cached counts are missing or expired.
func (c *realtimeOccupancyCache) read(ctx context.Context, owner RealtimeOccupancyReader, endpointID string, channels []string, now time.Time) (map[string]map[string]int, error) {
	out := make(map[string]map[string]int, len(channels))
	var stale []string
	c.mu.Lock()
	for _, channel := range channels {
		if entry, ok := c.entries[endpointID+"\x00"+channel]; ok && now.Sub(entry.at) < api.RealtimeOccupancyCacheTTL {
			out[channel] = entry.nodes
		} else {
			stale = append(stale, channel)
		}
	}
	c.mu.Unlock()
	if len(stale) == 0 {
		return out, nil
	}
	inventory, err := owner.ChannelOccupancy(ctx, endpointID, stale)
	if err != nil && inventory.NodesQueried == 0 {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) > realtimeOccupancyCachePrune {
		for key, entry := range c.entries {
			if now.Sub(entry.at) >= api.RealtimeOccupancyCacheTTL {
				delete(c.entries, key)
			}
		}
	}
	for _, channel := range stale {
		nodes := make(map[string]int, len(inventory.Nodes))
		for node, counts := range inventory.Nodes {
			if n := counts[channel]; n > 0 {
				nodes[node] = n
			}
		}
		c.entries[endpointID+"\x00"+channel] = realtimeOccupancyEntry{nodes: nodes, at: now}
		out[channel] = nodes
	}
	return out, nil
}

// OccupancyOperation answers a node with the subscriber counts of every other
// active node on the requested channels.
func (r *realtimeFleetReceiver) OccupancyOperation(ctx context.Context, envelope *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error) {
	if r.occupancy == nil || r.store == nil {
		return nil, status.Error(codes.Unavailable, "realtime occupancy unavailable")
	}
	if envelope == nil || len(envelope.GetValue()) == 0 || len(envelope.GetValue()) > realtimeOccupancyMaxBytes {
		return nil, status.Error(codes.InvalidArgument, "invalid realtime occupancy request")
	}
	var req realtimeOccupancyMessage
	if err := decodeRealtimeFleetJSON(envelope.GetValue(), &req); err != nil || req.EndpointID == "" ||
		len(req.Channels) == 0 || len(req.Channels) > api.RealtimeOccupancyChannelsMax {
		return nil, status.Error(codes.InvalidArgument, "invalid realtime occupancy request")
	}
	for _, channel := range req.Channels {
		if !realtime.ValidateChannel(channel) {
			return nil, status.Error(codes.InvalidArgument, "invalid realtime occupancy request")
		}
	}
	node, err := r.lookupNode(ctx, req.NodeName)
	if err != nil {
		return nil, err
	}
	if err := r.requireEnabledEndpoint(ctx, req.EndpointID); err != nil {
		return nil, err
	}
	counts, err := r.occupancyCache.read(ctx, r.occupancy, req.EndpointID, req.Channels, time.Now())
	if err != nil {
		return nil, status.Error(codes.Unavailable, "realtime occupancy unavailable")
	}
	remote := make(map[string]int, len(req.Channels))
	for _, channel := range req.Channels {
		for nodeID, n := range counts[channel] {
			if nodeID != node.ID {
				remote[channel] += n
			}
		}
	}
	data, err := json.Marshal(realtimeOccupancyMessage{Counts: remote})
	if err != nil {
		return nil, status.Error(codes.Internal, "encode realtime occupancy")
	}
	return wrapperspb.Bytes(data), nil
}
