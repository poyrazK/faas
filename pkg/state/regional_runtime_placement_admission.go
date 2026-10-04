package state

// adr: 496
import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/runtimequota"
)

var ErrRegionalRuntimePlacementUnavailable = errors.New("regional runtime placement is not issued and unfenced")

// RegionalRuntimePlacementWriter atomically reserves and issues one launch.
// Success is bookkeeping only; no scheduler/host execution authority is granted.
type RegionalRuntimePlacementWriter interface {
	IssueRegionalRuntimePlacement(context.Context, runtimequota.Placement) error
}

// RegionalRuntimeIssuedPlacementReader checks one coherent cell journal view.
// Success never activates quota admission or replaces host operation fences.
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

// Retry checks deliberately allow planned receipts, but never reopen a fence.
func checkRegionalPhysicalForIssue(p runtimequota.CapacityPermit, r RegionalRuntimeCapacityReceipt) error {
	if r.Permit != p {
		return ErrConflict
	}
	if r.ReleasedAt != nil || r.FenceID != "" || r.Stop != nil || r.StopEnvelope != nil {
		return ErrRegionalRuntimePlacementUnavailable
	}
	return nil
}

func checkRegionalSeatForIssue(s runtimequota.ServingSeat, r RegionalRuntimeServingSeatReceipt) error {
	if r.Seat != s {
		return ErrConflict
	}
	if r.ReleasedAt != nil || r.FenceID != "" || r.ClosedByPhysicalStop || r.Stop != nil || r.StopEnvelope != nil {
		return ErrRegionalRuntimePlacementUnavailable
	}
	return nil
}
