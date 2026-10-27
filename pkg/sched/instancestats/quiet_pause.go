package instancestats

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// quietPausedSnapshot is the set of instances each node reported
// quiet-paused (ADR-1151) in one tick, and when that tick read them.
type quietPausedSnapshot struct {
	byNode map[string]quietPausedNode
	asOf   time.Time
}

// quietPausedNode is one node's report: the instances it saw quiet-paused
// and when vmmd sampled them. The poller projects a node's latest stream
// batch on every tick, so the sample can be older than the tick.
type quietPausedNode struct {
	instances map[string]struct{}
	sampledAt time.Time
}

// publishQuietPaused records which instances each node reported
// quiet-paused this tick, for schedd's quiet pause admission. A row without
// a sample time counts as sampled at asOf.
func (p *Poller) publishQuietPaused(rows []InstanceStat, asOf time.Time) {
	if p.Reader == nil {
		return
	}
	byNode := make(map[string]quietPausedNode)
	for _, row := range rows {
		if row.NodeID == "" {
			continue
		}
		sampled := row.SampledAt
		if sampled.IsZero() {
			sampled = asOf
		}
		node, seen := byNode[row.NodeID]
		if !seen || sampled.Before(node.sampledAt) {
			node.sampledAt = sampled
		}
		if row.QuietPaused {
			if node.instances == nil {
				node.instances = make(map[string]struct{})
			}
			node.instances[row.InstanceID] = struct{}{}
		}
		byNode[row.NodeID] = node
	}
	p.Reader.quiet.Store(&quietPausedSnapshot{byNode: byNode, asOf: asOf})
}

// QuietPaused returns the instances a node last reported quiet-paused and
// when vmmd sampled them, so admission never discounts a booking made after
// the sample. ok is false when no tick has run, or the sample is older than
// QuietPauseSampleMaxAgeMillis; admission then counts every booking. A node
// that reported nothing paused, or nothing at all this tick, returns an
// empty set with ok true.
func (r *Reader) QuietPaused(nodeID string, now time.Time) (map[string]struct{}, time.Time, bool) {
	maxAge := time.Duration(api.QuietPauseSampleMaxAgeMillis) * time.Millisecond
	snap := r.quiet.Load()
	if snap == nil || now.Sub(snap.asOf) > maxAge {
		return nil, time.Time{}, false
	}
	node, reported := snap.byNode[nodeID]
	if !reported {
		return nil, snap.asOf, true
	}
	if now.Sub(node.sampledAt) > maxAge {
		return nil, time.Time{}, false
	}
	return node.instances, node.sampledAt, true
}
