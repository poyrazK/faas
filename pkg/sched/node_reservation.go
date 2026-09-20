// node_reservation.go — schedd's adapter for the durable per-node RAM
// reservation (ADR-193, implemented in pkg/state/node_reservation.go).
//
// Two tiers now enforce invariant §6.2-2:
//
//  1. NodeLedger (admission.go) — in-memory, per-process, fast, and the only
//     tier that also owns per-app concurrency, vCPU, and CPU millicores. It
//     sees only the apps this schedd owns.
//  2. The state-layer reservation — a per-node advisory lock around the
//     headroom check and the instances INSERT. It sees every schedd.
//
// Tier 1 runs after tier 2 because the INSERT is what publishes the
// reservation fleet-wide; there is nothing to serialize on before the row
// exists. When tier 2 refuses, no row was written and no ledger entry was
// taken, so the unwind is a plain return.
package sched

import (
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// nodeCapacityProblem translates state.ErrNodeCapacity into the same typed
// *api.Problem the ledger returns from its own per-node ceiling check.
//
// Both tiers enforce the same invariant, so they must be indistinguishable
// above schedd: the gateway's capacity handling, the CLI exit code, and the
// 503 the customer sees are all keyed on api.CodeCapacity. A caller that had
// to tell the tiers apart would be a caller that could regress one of them.
//
// The node id and the MB figures stay in the operator log rather than the
// customer-facing detail. Every other error passes through untouched.
func (e *Engine) nodeCapacityProblem(err error) error {
	if err == nil || !errors.Is(err, state.ErrNodeCapacity) {
		return err
	}
	e.log.Warn("sched: admit refused by the durable per-node reservation (ADR-193)",
		"err", err)
	return api.ErrCapacity(
		"RAM headroom: the selected compute node is at its per-node admission ceiling")
}
