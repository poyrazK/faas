package realtime

import (
	"sync"
)

// Channel occupancy (ADR-1324) is the number of open subscriptions to a
// channel. Every transport counts: legacy and resumable WebSocket
// subscriptions, read-only ticket subscriptions and SSE streams. The index is
// exact on each node; apid sums the nodes for a fleet-wide count.

// ChannelOccupancy is one node's subscriber count for a channel.
type ChannelOccupancy struct {
	Channel     string `json:"channel"`
	Subscribers int    `json:"subscribers"`
}

// OccupancyInventory is a fleet-wide occupancy read. Nodes maps a node ID to
// its counts by channel; an unreachable node is counted, not guessed.
type OccupancyInventory struct {
	Nodes            map[string]map[string]int
	NodesQueried     int
	NodesUnavailable int
}

// Subscribers sums a channel's count across the queried nodes, leaving out
// excludeNode (the asking node, which knows its own count) when set.
func (i OccupancyInventory) Subscribers(channel, excludeNode string) int {
	total := 0
	for node, counts := range i.Nodes {
		if node != excludeNode {
			total += counts[channel]
		}
	}
	return total
}

// occupancyIndex counts subscriptions per endpoint channel. Its mutex is a
// leaf: callers may hold m.mu or c.mu, and nothing is locked while it is held.
type occupancyIndex struct {
	mu     sync.Mutex
	counts map[channelKey]int
	// changed is told about every change after mu is released, so live
	// occupancy can mark the channel for its next frame.
	changed func(channelKey)
}

func (o *occupancyIndex) add(key channelKey, delta int) {
	o.mu.Lock()
	if o.counts == nil {
		o.counts = make(map[channelKey]int)
	}
	if next := o.counts[key] + delta; next > 0 {
		o.counts[key] = next
	} else {
		delete(o.counts, key)
	}
	o.mu.Unlock()
	if o.changed != nil {
		o.changed(key)
	}
}

func (o *occupancyIndex) count(key channelKey) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.counts[key]
}

// ChannelOccupancy returns this node's subscriber count for each channel of
// an endpoint, in the order asked.
func (m *Manager) ChannelOccupancy(endpointID string, channels []string) []ChannelOccupancy {
	out := make([]ChannelOccupancy, 0, len(channels))
	for _, channel := range channels {
		out = append(out, ChannelOccupancy{Channel: channel, Subscribers: m.occupancy.count(channelKey{endpointID: endpointID, channel: channel})})
	}
	return out
}

// addChannelLocked records a channel subscription. c.mu must be held.
func (c *connection) addChannelLocked(channel string) {
	if _, ok := c.channels[channel]; ok {
		return
	}
	c.channels[channel] = struct{}{}
	if !c.channelsReleased {
		c.m.occupancy.add(channelKey{endpointID: c.info.EndpointID, channel: channel}, 1)
	}
}

// removeChannelLocked drops a channel subscription. c.mu must be held.
func (c *connection) removeChannelLocked(channel string) {
	if _, ok := c.channels[channel]; !ok {
		return
	}
	delete(c.channels, channel)
	if !c.channelsReleased {
		c.m.occupancy.add(channelKey{endpointID: c.info.EndpointID, channel: channel}, -1)
	}
}

// releaseChannelsLocked stops counting a removed connection's channels, once;
// a subscription that races the removal is then never counted. c.mu must be
// held.
func (c *connection) releaseChannelsLocked() {
	if c.channelsReleased {
		return
	}
	c.channelsReleased = true
	for channel := range c.channels {
		c.m.occupancy.add(channelKey{endpointID: c.info.EndpointID, channel: channel}, -1)
	}
}
