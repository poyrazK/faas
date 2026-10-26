package state

import (
	"context"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// NodeAdmission binds an admission to the node generation selected by placement.
// It is independent of the release/configuration generation (ADR-1528).
type NodeAdmission struct {
	NodeID     string
	Generation int64
	// ReservationPercent is how far Σ(ram_mb + overhead) may reach on this
	// node, as a percent of its admission ceiling. A scheduler that admits by
	// a fresh measurement of the node sets the ADR-909 overcommit bound;
	// zero, and anything outside [100, api.MeasuredAdmissionMaxOvercommitPercent],
	// is clamped, so an unbound or reservation-only admission keeps 100.
	ReservationPercent int
}

type nodeAdmissionKey struct{}

// WithNodeAdmission preserves the original placement decision through the
// existing store APIs. The store validates it while holding the target lock.
func WithNodeAdmission(ctx context.Context, admission NodeAdmission) context.Context {
	return context.WithValue(ctx, nodeAdmissionKey{}, admission)
}

// ErrNodeAdmissionStale is a definite admission refusal, never a lost commit
// response. A scheduler may refresh placement and retry before any VM dispatch.
var ErrNodeAdmissionStale = fmt.Errorf("node placement is stale or not admitting: %w", ErrConflict)

func nodeAdmissionExpectation(ctx context.Context, nodeID string) (NodeAdmission, bool, error) {
	expected, present := ctx.Value(nodeAdmissionKey{}).(NodeAdmission)
	if present && (expected.NodeID == "" || expected.NodeID != nodeID || expected.Generation <= 0) {
		return NodeAdmission{}, false, ErrInvalidArgument
	}
	return expected, present, nil
}

// nodeReservationPercent is the reservation allowance ctx grants an admission
// to nodeID (ADR-909 amendment 1). Only a binding for this node counts.
func nodeReservationPercent(ctx context.Context, nodeID string) int {
	expected, present := ctx.Value(nodeAdmissionKey{}).(NodeAdmission)
	if !present || expected.NodeID != nodeID {
		return 100
	}
	return min(max(expected.ReservationPercent, 100), api.MeasuredAdmissionMaxOvercommitPercent)
}

// nodeReservationCapMB is the most Σ(ram_mb + overhead) a node with this
// ceiling may hold under the reservation allowance.
func nodeReservationCapMB(ceilingMB int64, percent int) int64 {
	return ceilingMB * int64(percent) / 100
}

func (m *MemStore) checkNodeAdmissionLocked(ctx context.Context, nodeID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	expected, bound, err := nodeAdmissionExpectation(ctx, nodeID)
	if err != nil {
		return err
	}
	node, exists := m.computeNodes[nodeID]
	if !exists && !bound {
		// Preserve the historical unregistered-node unit fixture seam. Real
		// PostgreSQL rows always require a node; bound admissions do too.
		return nil
	}
	if !exists || node.Lifecycle != "" && !node.Lifecycle.IsAdmitting() || !node.Active || bound && node.LifecycleGeneration != expected.Generation {
		return ErrNodeAdmissionStale
	}
	return nil
}

func (m *MemStore) checkInstanceNodeAdmissionLocked(ctx context.Context, old, next Instance) error {
	if IsLive(strings.ToLower(next.State)) && (old.ID == "" || next.NodeID != old.NodeID || !IsLive(strings.ToLower(old.State))) {
		return m.checkNodeAdmissionLocked(ctx, next.NodeID)
	}
	return nil
}
