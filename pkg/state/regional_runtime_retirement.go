package state

// adr: 631
import (
	"context"

	"github.com/onebox-faas/faas/pkg/runtimequota"
)

// CompleteHostRetirement is the cell owner's signing boundary, after it has
// authenticated the exact native assertion. The ledger signs and closes under
// the same app journal lock as issuance and retirement, using historical pins.
func (a RegionalRuntimeCapacityAuthority) CompleteHostRetirement(ctx context.Context, stop runtimequota.CapacityStop, signer runtimequota.Signer) (RegionalRuntimeCapacityReceipt, error) {
	if a.Store == nil || a.Cells.CheckSigner(signer, runtimequota.Cell) != nil || signer.IssuerID() != stop.Permit.Grant.Binding.CellID {
		return RegionalRuntimeCapacityReceipt{}, runtimequota.ErrTrust
	}
	if stop.Validate() != nil {
		return RegionalRuntimeCapacityReceipt{}, ErrInvalidArgument
	}
	return a.Store.completeRegionalRuntimeRetirement(ctx, stop, signer, a.Cells)
}

func beginRegionalRetirement(r RegionalRuntimeCapacityReceipt, p runtimequota.CapacityPermit, fence string) (RegionalRuntimeCapacityReceipt, error) {
	if r.Permit != p {
		return RegionalRuntimeCapacityReceipt{}, ErrConflict
	}
	if r.FenceID != "" {
		return r, nil // Retain the winner of retirement/parallel cleanup races.
	}
	return fenceRegionalCapacity(r, fence)
}

// Call only while holding the app's durable quota journal lock. A first
// accepted envelope survives retries and cell-key rotation without re-signing.
func completeRegionalRetirement(old RegionalRuntimeCapacityReceipt, stop runtimequota.CapacityStop, signer runtimequota.Signer, cells runtimequota.Trust) (RegionalRuntimeCapacityReceipt, error) {
	if stop.Validate() != nil || old.Permit != stop.Permit || old.FenceID != stop.FenceID || old.IssuedAt == nil {
		return RegionalRuntimeCapacityReceipt{}, ErrConflict
	}
	if old.Stop != nil {
		if *old.Stop != stop || old.StopEnvelope == nil || old.ReleasedAt == nil {
			return RegionalRuntimeCapacityReceipt{}, ErrConflict
		}
		return old, nil
	}
	if old.ReleasedAt != nil {
		return RegionalRuntimeCapacityReceipt{}, ErrConflict
	}
	e, err := signer.SignCapacityStop(stop)
	if err != nil {
		return RegionalRuntimeCapacityReceipt{}, err
	}
	verified, err := cells.VerifyCapacityStop(e)
	if err != nil || verified != stop {
		return RegionalRuntimeCapacityReceipt{}, runtimequota.ErrTrust
	}
	return stopRegionalCapacity(old, stop, e)
}
