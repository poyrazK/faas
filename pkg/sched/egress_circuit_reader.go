package sched

// The production EgressCircuitCandidateReader (ADR-201 §3): the seam between
// the store's row shape and the breaker loop's domain type.
//
// Kept separate from the loop so the loop stays free of pkg/state and remains
// testable with a plain closure — the same dependency posture pkg/gateway
// takes toward pkg/state.

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// EgressCircuitCandidateStore is the narrow slice of state.Store the reader
// needs.
type EgressCircuitCandidateStore interface {
	ListEgressCircuitCandidates(ctx context.Context, since time.Time) ([]state.EgressCircuitCandidate, error)
}

// NewStoreEgressCircuitCandidateReader returns a reader over the store.
//
// `window` bounds how far back the probe join looks. It must be at least a
// few probe intervals: too short and a single missed probe makes every
// upstream look unmeasured, which stalls the breaker; too long and the join
// scans more partitions than it needs. The loop applies its own freshness cut
// on top, so this is purely the SQL-side bound.
func NewStoreEgressCircuitCandidateReader(store EgressCircuitCandidateStore, window time.Duration, now func() time.Time) EgressCircuitCandidateReader {
	if window <= 0 {
		window = 10 * time.Minute
	}
	if now == nil {
		now = time.Now
	}
	return func(ctx context.Context) ([]EgressCircuitCandidate, error) {
		rows, err := store.ListEgressCircuitCandidates(ctx, now().Add(-window))
		if err != nil {
			return nil, err
		}
		out := make([]EgressCircuitCandidate, 0, len(rows))
		for _, r := range rows {
			out = append(out, EgressCircuitCandidate{
				Upstream: EgressUpstream{
					AppID: r.AppID,
					Hash:  r.HostRedactedHash,
					Host:  r.Host,
					Port:  r.Port,
				},
				OK:      r.OK,
				Sampled: r.Sampled,
			})
		}
		return out, nil
	}
}

// EgressCircuitInstanceStore is the narrow slice used to find which nodes an
// app is currently running on.
type EgressCircuitInstanceStore interface {
	ListInstancesForApp(ctx context.Context, appID string) ([]state.Instance, error)
}

// NewStoreEgressCircuitNodeLister returns the node lister the routed applier
// fans out over.
//
// Only LIVE states count. A parked or failed instance has no netns to hold a
// rule, so pushing to its node would be a wasted RPC — and, worse, would make
// a node that is merely remembering a dead instance look like a delivery
// target, so a genuine delivery failure there would be indistinguishable from
// this no-op.
func NewStoreEgressCircuitNodeLister(store EgressCircuitInstanceStore) EgressCircuitNodeLister {
	return func(ctx context.Context, appID string) ([]string, error) {
		instances, err := store.ListInstancesForApp(ctx, appID)
		if err != nil {
			return nil, err
		}
		seen := make(map[string]struct{}, len(instances))
		var out []string
		for _, ins := range instances {
			if ins.NodeID == "" || !egressCircuitLiveState(ins.State) {
				continue
			}
			if _, dup := seen[ins.NodeID]; dup {
				continue
			}
			seen[ins.NodeID] = struct{}{}
			out = append(out, ins.NodeID)
		}
		return out, nil
	}
}

// egressCircuitLiveState reports whether an instance state can hold a netns.
//
// WAKING and COLD_BOOTING are included alongside RUNNING: an instance coming
// up already has its netns rendered, and skipping it would leave a window
// where a newly-woken instance reaches a dependency the breaker considers
// broken.
func egressCircuitLiveState(s string) bool {
	switch s {
	case string(state.StateRunning), string(state.StateWaking), string(state.StateColdBooting):
		return true
	default:
		return false
	}
}
