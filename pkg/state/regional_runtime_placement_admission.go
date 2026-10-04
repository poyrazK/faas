package state

// adr: 496
import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/runtimequota"
)

var ErrRegionalRuntimePlacementUnavailable = errors.New("regional runtime placement is not issued and unfenced")

// RegionalRuntimeIssuedPlacementReader checks one coherent cell journal view.
// A successful bookkeeping check never activates quota admission or replaces
// the host's epoch, immutable identity and irreversible operation fences.
type RegionalRuntimeIssuedPlacementReader interface {
	CheckRegionalRuntimeIssuedPlacement(context.Context, runtimequota.Placement) error
}

func checkIssuedRegionalPhysical(p runtimequota.CapacityPermit, r RegionalRuntimeCapacityReceipt) error {
	if r.Permit.ID == "" {
		return ErrRegionalRuntimePlacementUnavailable
	}
	if r.Permit != p {
		return ErrConflict
	}
	if r.IssuedAt == nil || r.ReleasedAt != nil || r.FenceID != "" || r.Stop != nil || r.StopEnvelope != nil {
		return ErrRegionalRuntimePlacementUnavailable
	}
	return nil
}

func checkIssuedRegionalSeat(s runtimequota.ServingSeat, r RegionalRuntimeServingSeatReceipt) error {
	if r.Seat.ID == "" {
		return ErrRegionalRuntimePlacementUnavailable
	}
	if r.Seat != s {
		return ErrConflict
	}
	if r.IssuedAt == nil || r.ReleasedAt != nil || r.FenceID != "" || r.ClosedByPhysicalStop || r.Stop != nil || r.StopEnvelope != nil {
		return ErrRegionalRuntimePlacementUnavailable
	}
	return nil
}

func validIssuedRegionalPlacement(p runtimequota.Placement) bool {
	return p.Validate() == nil && (p.Permit.Kind != runtimequota.CapacityServing || p.Seat != nil)
}
