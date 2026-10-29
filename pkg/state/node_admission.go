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
	// Compute is what the admitted instance books against the node's vCPU
	// and CPU budgets, checked under the same lock as the RAM reservation
	// (ADR-20261027220445). The zero value skips that check.
	Compute NodeCompute
}

// NodeCompute is an admission's guest vCPUs and sustained CPU millicores, and
// the quiet-pause discounts (ADR-1151) the scheduler applied to the node's
// bookings when it chose the node. The guard bounds each discount by
// api.QuietPauseMaxVCPUOvercommitPercent of the budget.
type NodeCompute struct {
	VCPU, CPUMillicores           int
	QuietVCPU, QuietCPUMillicores int
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

// nodeComputeAdmission is the compute ctx's placement binding books on
// nodeID (ADR-20261027220445). Only a binding for this node counts, and
// negative values count as zero.
func nodeComputeAdmission(ctx context.Context, nodeID string) NodeCompute {
	expected, present := ctx.Value(nodeAdmissionKey{}).(NodeAdmission)
	if !present || expected.NodeID != nodeID {
		return NodeCompute{}
	}
	c := expected.Compute
	return NodeCompute{
		VCPU: max(c.VCPU, 0), CPUMillicores: max(c.CPUMillicores, 0),
		QuietVCPU: max(c.QuietVCPU, 0), QuietCPUMillicores: max(c.QuietCPUMillicores, 0),
	}
}

// nodeComputeRefusal is the durable vCPU and CPU check: the live instances'
// bookings, less the scheduler's quiet-pause discount, plus the admission
// must fit each budget. A zero request or budget is not checked, as in
// placement.
func nodeComputeRefusal(nodeID string, c NodeCompute, vcpuBudget, usedVCPU, cpuBudget, usedCPU int64) error {
	if c.VCPU > 0 && vcpuBudget > 0 {
		quiet := min(int64(c.QuietVCPU), quietDiscountBound(vcpuBudget))
		if usedVCPU-quiet+int64(c.VCPU) > vcpuBudget {
			return fmt.Errorf("state: %w: node=%s resource=vcpu used_vcpu=%d quiet_vcpu=%d admit_vcpu=%d budget_vcpu=%d",
				ErrNodeCapacity, nodeID, usedVCPU, quiet, c.VCPU, vcpuBudget)
		}
	}
	if c.CPUMillicores > 0 && cpuBudget > 0 {
		quiet := min(int64(c.QuietCPUMillicores), quietDiscountBound(cpuBudget))
		if usedCPU-quiet+int64(c.CPUMillicores) > cpuBudget {
			return fmt.Errorf("state: %w: node=%s resource=cpu used_cpu_millicores=%d quiet_cpu_millicores=%d admit_cpu_millicores=%d budget_cpu_millicores=%d",
				ErrNodeCapacity, nodeID, usedCPU, quiet, c.CPUMillicores, cpuBudget)
		}
	}
	return nil
}

// quietDiscountBound is the largest quiet-pause discount the guard accepts:
// bookings may reach api.QuietPauseMaxVCPUOvercommitPercent of the budget.
func quietDiscountBound(budget int64) int64 {
	return budget * (api.QuietPauseMaxVCPUOvercommitPercent - 100) / 100
}

// nodeCPUBudgetMillicores is placement's physical CPU budget for a node with
// vpcpus host CPUs.
func nodeCPUBudgetMillicores(vpcpus int64) int64 {
	return vpcpus * 1000 * api.CPUOvercommit
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
