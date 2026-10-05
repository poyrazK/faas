package state

// adr: 618
import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimequota"
)

type RegionalRuntimeServingSeatReceipt struct {
	Seat                 runtimequota.ServingSeat
	CreatedAt            time.Time
	IssuedAt, ReleasedAt *time.Time
	FenceID              string
	Stop                 *runtimequota.ServingSeatStop
	StopEnvelope         *runtimequota.Envelope
	ClosedByPhysicalStop bool
}

type RegionalRuntimeServingSeatUsage struct{ TotalHeld, IssuedHeld, SteadyHeld, RolloutHeld int64 }

type RegionalRuntimeServingSeatStore interface {
	ReserveRegionalRuntimeServingSeat(context.Context, runtimequota.ServingSeat) (RegionalRuntimeServingSeatReceipt, error)
	IssueRegionalRuntimeServingSeat(context.Context, string, string) (RegionalRuntimeServingSeatReceipt, error)
	FenceRegionalRuntimeServingSeat(context.Context, string, string, string) (RegionalRuntimeServingSeatReceipt, error)
	RegionalRuntimeServingSeat(context.Context, string, string) (RegionalRuntimeServingSeatReceipt, error)
	ListRegionalRuntimeServingSeats(context.Context, string, string, string, int) ([]RegionalRuntimeServingSeatReceipt, error)
	RegionalRuntimeServingSeatUsage(context.Context, string, string) (RegionalRuntimeServingSeatUsage, error)
	applyRegionalRuntimeServingSeatStop(context.Context, runtimequota.ServingSeatStop, runtimequota.Envelope) (RegionalRuntimeServingSeatReceipt, error)
}

type RegionalRuntimeCapacityLedger interface {
	RegionalRuntimeCapacityStore
	RegionalRuntimeServingSeatStore
	RegionalRuntimeIssuedPlacementReader
	RegionalRuntimePlacementWriter
}

func (a RegionalRuntimeCapacityAuthority) AcceptServingSeatStop(ctx context.Context, e runtimequota.Envelope) (RegionalRuntimeServingSeatReceipt, error) {
	if a.Store == nil {
		return RegionalRuntimeServingSeatReceipt{}, runtimequota.ErrTrust
	}
	stop, err := a.Cells.VerifyServingSeatStop(e)
	if err != nil {
		return RegionalRuntimeServingSeatReceipt{}, err
	}
	return a.Store.applyRegionalRuntimeServingSeatStop(ctx, stop, e)
}

func cloneServingSeat(r RegionalRuntimeServingSeatReceipt) RegionalRuntimeServingSeatReceipt {
	if r.IssuedAt != nil {
		v := *r.IssuedAt
		r.IssuedAt = &v
	}
	if r.ReleasedAt != nil {
		v := *r.ReleasedAt
		r.ReleasedAt = &v
	}
	if r.Stop != nil {
		v := *r.Stop
		r.Stop = &v
	}
	if r.StopEnvelope != nil {
		v := *r.StopEnvelope
		r.StopEnvelope = &v
	}
	return r
}

func fenceServingSeat(r RegionalRuntimeServingSeatReceipt, fenceID string) (RegionalRuntimeServingSeatReceipt, error) {
	if !validRegionalReleaseIDs(fenceID, fenceID, fenceID) {
		return RegionalRuntimeServingSeatReceipt{}, ErrInvalidArgument
	}
	if r.FenceID != "" && r.FenceID != fenceID {
		return RegionalRuntimeServingSeatReceipt{}, ErrConflict
	}
	r.FenceID = fenceID
	if r.IssuedAt == nil && r.ReleasedAt == nil {
		now := regionalCapacityNow()
		r.ReleasedAt = &now
	}
	return r, nil
}

func stopServingSeat(r RegionalRuntimeServingSeatReceipt, stop runtimequota.ServingSeatStop, e runtimequota.Envelope) (RegionalRuntimeServingSeatReceipt, error) {
	if stop.Validate() != nil || r.Seat != stop.Seat || r.FenceID != stop.FenceID || r.IssuedAt == nil {
		return RegionalRuntimeServingSeatReceipt{}, ErrConflict
	}
	if r.Stop != nil {
		if *r.Stop != stop {
			return RegionalRuntimeServingSeatReceipt{}, ErrConflict
		}
		return r, nil
	}
	if r.ClosedByPhysicalStop && r.ReleasedAt != nil {
		return r, nil
	}
	if r.ReleasedAt != nil {
		return RegionalRuntimeServingSeatReceipt{}, ErrConflict
	}
	now := regionalCapacityNow()
	r.ReleasedAt = &now
	r.Stop = &stop
	r.StopEnvelope = &e
	return r, nil
}
