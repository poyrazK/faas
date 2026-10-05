package sched

// adr: 631
import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

// RegionalRuntimeRetirement owns the cell's explicit source-bound physical
// cleanup. Operator-provisioned keys and an authenticated router are required.
// It neither activates quota heads nor interprets missing rows as native stops.
type RegionalRuntimeRetirement struct {
	Authority state.RegionalRuntimeCapacityAuthority
	Signer    runtimequota.Signer
	Router    *VMMRouter
}

func (r RegionalRuntimeRetirement) Retire(ctx context.Context, instance string, p runtimequota.CapacityPermit, fence string) (state.RegionalRuntimeCapacityReceipt, error) {
	if r.Authority.Store == nil || r.Router == nil || r.Router.regionalRuntimePeers == nil ||
		r.Authority.Cells.CheckSigner(r.Signer, runtimequota.Cell) != nil || r.Signer.IssuerID() != p.Grant.Binding.CellID {
		return state.RegionalRuntimeCapacityReceipt{}, runtimequota.ErrTrust
	}
	if p.Validate() != nil || !runtimequota.MatchesID(p.InstanceID, instance) ||
		(runtimequota.CapacityStop{Permit: p, FenceID: fence, AdmissionFrozen: true}).Validate() != nil {
		return state.RegionalRuntimeCapacityReceipt{}, runtimequota.ErrBinding
	}
	if err := ctx.Err(); err != nil {
		return state.RegionalRuntimeCapacityReceipt{}, err
	}
	receipt, err := r.Authority.Store.BeginRegionalRuntimeRetirement(ctx, p, fence)
	if err != nil {
		return state.RegionalRuntimeCapacityReceipt{}, fmt.Errorf("sched: fence original regional permit: %w", err)
	}
	if receipt.Permit != p || receipt.FenceID == "" {
		return state.RegionalRuntimeCapacityReceipt{}, runtimequota.ErrBinding
	}
	if receipt.ReleasedAt != nil {
		if receipt.IssuedAt != nil && (receipt.Stop == nil || receipt.StopEnvelope == nil || receipt.Stop.Validate() != nil || receipt.Stop.Permit != p || receipt.Stop.FenceID != receipt.FenceID) {
			return state.RegionalRuntimeCapacityReceipt{}, runtimequota.ErrBinding
		}
		return receipt, nil // Durable cancellation or first accepted signed stop.
	}
	if receipt.IssuedAt == nil {
		return state.RegionalRuntimeCapacityReceipt{}, runtimequota.ErrBinding
	}
	stop, err := r.Router.RetireRegionalRuntime(ctx, instance, p, receipt.FenceID)
	if err != nil {
		return state.RegionalRuntimeCapacityReceipt{}, fmt.Errorf("sched: native regional retirement: %w", err)
	}
	if stop.Validate() != nil || stop.Permit != p || stop.FenceID != receipt.FenceID {
		// Host and cell first fences are immutable independent journals. A
		// conflict requires reconciliation; silently replacing either is unsafe.
		return state.RegionalRuntimeCapacityReceipt{}, runtimequota.ErrBinding
	}
	return r.Authority.CompleteHostRetirement(ctx, stop, r.Signer)
}
