package sched

import (
	"sort"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// NodeRegistry is the schedd-side cache of active compute nodes. The
// bootstrap snapshot is replaced incrementally by compute_node_changed
// notifications, so placement and observers do not have to enumerate the
// database on every hot-path operation.
type NodeRegistry struct {
	mu    sync.RWMutex
	nodes map[string]state.ComputeNode
}

// NewNodeRegistry seeds a registry from the active-node bootstrap snapshot.
func NewNodeRegistry(nodes []state.ComputeNode) *NodeRegistry {
	r := &NodeRegistry{nodes: make(map[string]state.ComputeNode, len(nodes))}
	for _, node := range nodes {
		if node.ID != "" && node.Active {
			r.nodes[node.ID] = node
		}
	}
	return r
}

// Refresh applies one compute-node notification. Inactive or empty rows are
// removed so a drain becomes visible to placement immediately after the
// notification is processed.
func (r *NodeRegistry) Refresh(node state.ComputeNode) {
	if r == nil || node.ID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !node.Active {
		delete(r.nodes, node.ID)
		return
	}
	r.nodes[node.ID] = node
}

// Remove evicts a node whose row disappeared between notification and
// refresh, or whose active flag changed to false.
func (r *NodeRegistry) Remove(nodeID string) {
	if r == nil || nodeID == "" {
		return
	}
	r.mu.Lock()
	delete(r.nodes, nodeID)
	r.mu.Unlock()
}

// Snapshot returns a stable copy suitable for a placement or observer sweep.
func (r *NodeRegistry) Snapshot() []state.ComputeNode {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]state.ComputeNode, 0, len(r.nodes))
	for _, node := range r.nodes {
		out = append(out, node)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// NodeTelemetry is one vmmd Stats row transported inside a node's persistent
// capacity stream. The outer stream frame supplies NodeID and SampledAt.
// Pointer fields preserve the existing absent-versus-zero semantics.
type NodeTelemetry struct {
	InstanceID          string
	ResidentBytes       *int64
	DiskUsedBytes       *int64
	DiskCapacityBytes   *int64
	CPUPct              *float64
	CPUSeconds          *float64
	CPUThrottledSeconds *float64
	InflightRequests    int64
	RequestCountTotal   *int64
	LastRequestAt       time.Time
	NetTxBytes          *int64
	NetRxBytes          *int64
	OpenConns           int64
}

// NodeTelemetryCache holds the most recent batched report from each node.
// It is intentionally independent from nodeCapacityTable: capacity and
// per-instance observability have different consumers and can age out
// independently without coupling their locks.
type NodeTelemetryCache struct {
	mu    sync.RWMutex
	nodes map[string]telemetryEntry
}

type telemetryEntry struct {
	sampledAt time.Time
	lastSeen  time.Time
	rows      []NodeTelemetry
}

// TelemetryFreshness is the maximum age accepted by the local observer
// projection. The vmmd publisher reports every second, so this tolerates a
// few transient missed ticks while preventing old node data from persisting.
const TelemetryFreshness = 5 * time.Second

// NewNodeTelemetryCache constructs an empty cache.
func NewNodeTelemetryCache() *NodeTelemetryCache {
	return &NodeTelemetryCache{nodes: make(map[string]telemetryEntry)}
}

// Replace atomically replaces one node's complete telemetry batch. A copied
// slice prevents a caller reusing its protobuf-conversion buffer from racing
// readers.
func (c *NodeTelemetryCache) Replace(nodeID string, sampledAt, receivedAt time.Time, rows []NodeTelemetry) {
	if c == nil || nodeID == "" {
		return
	}
	copyRows := append([]NodeTelemetry(nil), rows...)
	c.mu.Lock()
	if c.nodes == nil {
		c.nodes = make(map[string]telemetryEntry)
	}
	c.nodes[nodeID] = telemetryEntry{sampledAt: sampledAt, lastSeen: receivedAt, rows: copyRows}
	c.mu.Unlock()
}

// Snapshot flattens fresh node batches into one copy for the schedd-side
// metrics reader. The caller supplies the clock so tests can exercise expiry.
func (c *NodeTelemetryCache) Snapshot(now time.Time) []NodeTelemetryWithNode {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []NodeTelemetryWithNode
	for nodeID, entry := range c.nodes {
		if now.Sub(entry.lastSeen) > TelemetryFreshness {
			continue
		}
		for _, row := range entry.rows {
			out = append(out, NodeTelemetryWithNode{NodeID: nodeID, SampledAt: entry.sampledAt, Telemetry: row})
		}
	}
	return out
}

// LookupOpenConns returns the freshest compute-side conntrack count for an
// instance. A miss means the node report is absent or stale; callers should
// use their safe fallback rather than treating a stale zero as authoritative.
func (c *NodeTelemetryCache) LookupOpenConns(instanceID string, now time.Time) (int64, bool) {
	if c == nil || instanceID == "" {
		return 0, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, entry := range c.nodes {
		if now.Sub(entry.lastSeen) > TelemetryFreshness {
			continue
		}
		for _, row := range entry.rows {
			if row.InstanceID == instanceID {
				return row.OpenConns, true
			}
		}
	}
	return 0, false
}

// NodeTelemetryWithNode is the flattened cache view used by the stats
// projection; keeping node identity here avoids duplicating it per wire row.
type NodeTelemetryWithNode struct {
	NodeID    string
	SampledAt time.Time
	Telemetry NodeTelemetry
}

// TelemetrySink is the schedd-side callback used by ReportCapacity. It keeps
// the gRPC package independent of the cache implementation.
type TelemetrySink func(nodeID string, sampledAt, receivedAt time.Time, rows []NodeTelemetry) error

// NodeUsageCache keeps the bulk database fallback bounded when vmmd has not
// published a fresh capacity sample yet. It is deliberately short-lived: the
// live capacity stream is authoritative during steady state, while this cache
// only bridges startup and transient publisher gaps.
type NodeUsageCache struct {
	mu          sync.RWMutex
	refreshedAt time.Time
	used        map[string]int64
}

const NodeUsageFreshness = 1 * time.Second

func NewNodeUsageCache() *NodeUsageCache {
	return &NodeUsageCache{used: make(map[string]int64)}
}

// Lookup returns a complete cached answer only when every requested node is
// represented. Zero-valued nodes are stored explicitly, so an aggregate query
// that returns no row still has a cache entry.
func (c *NodeUsageCache) Lookup(nodeIDs []string, now time.Time) (map[string]int64, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if now.Sub(c.refreshedAt) > NodeUsageFreshness {
		return nil, false
	}
	out := make(map[string]int64, len(nodeIDs))
	for _, id := range nodeIDs {
		used, ok := c.used[id]
		if !ok {
			return nil, false
		}
		out[id] = used
	}
	return out, true
}

// Replace records a bulk answer, including explicit zeros for nodes omitted
// by SQL's GROUP BY aggregate.
func (c *NodeUsageCache) Replace(nodeIDs []string, used map[string]int64, now time.Time) {
	if c == nil {
		return
	}
	next := make(map[string]int64, len(nodeIDs))
	for _, id := range nodeIDs {
		next[id] = used[id]
	}
	c.mu.Lock()
	c.used = next
	c.refreshedAt = now
	c.mu.Unlock()
}
