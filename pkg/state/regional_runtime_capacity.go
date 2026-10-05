package state

// adr: 618
import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

var ErrRegionalRuntimeCapacity = errors.New("regional runtime capacity is reserved")

// No lease expires these records. An issued permit may have escaped to a host
// even when the issuing RPC failed or its task/instance row was collected.
type RegionalRuntimeCapacityReceipt struct {
	Permit               runtimequota.CapacityPermit
	CreatedAt            time.Time
	IssuedAt, ReleasedAt *time.Time
	FenceID              string
	Stop                 *runtimequota.CapacityStop
	StopEnvelope         *runtimequota.Envelope
}

// Read receipts without granting admission or requiring current authority.
type RegionalRuntimeCapacityReader interface {
	RegionalRuntimeCapacity(context.Context, string, string) (RegionalRuntimeCapacityReceipt, error)
}

// Reservation and issuance track capacity without activating quota authority.
// Only the verified cell-stop receiver can close an issued physical attempt.
type RegionalRuntimeCapacityStore interface {
	RegionalRuntimeCapacityReader
	ReserveRegionalRuntimeCapacity(context.Context, runtimequota.CapacityPermit) (RegionalRuntimeCapacityReceipt, error)
	IssueRegionalRuntimeCapacity(context.Context, string, string) (RegionalRuntimeCapacityReceipt, error)
	FenceRegionalRuntimeCapacity(context.Context, string, string, string) (RegionalRuntimeCapacityReceipt, error)
	BeginRegionalRuntimeRetirement(context.Context, runtimequota.CapacityPermit, string) (RegionalRuntimeCapacityReceipt, error)
	ListRegionalRuntimeCapacity(context.Context, string, string, string, int) ([]RegionalRuntimeCapacityReceipt, error)
	RegionalRuntimeCapacityUsage(context.Context, string, string) (RegionalRuntimeCapacityUsage, error)
	applyRegionalRuntimeCapacityStop(context.Context, runtimequota.CapacityStop, runtimequota.Envelope) (RegionalRuntimeCapacityReceipt, error)
	completeRegionalRuntimeRetirement(context.Context, runtimequota.CapacityStop, runtimequota.Signer, runtimequota.Trust) (RegionalRuntimeCapacityReceipt, error)
}

// Usage counts durable holds only. Zero does not prove that every legacy or
// unreachable host has been fenced, and cannot authorize a global drain receipt.
type RegionalRuntimeCapacityUsage struct{ TotalHeld, IssuedHeld int64 }

func validCapacityPage(app, allocation, after string, limit int) bool {
	return validRegionalReleaseIDs(app, allocation, allocation) && (after == "" || validRegionalReleaseIDs(after, after, after)) && limit >= 1 && limit <= api.RegionalRuntimeCapacityPageMax
}

type RegionalRuntimeCapacityAuthority struct {
	Store RegionalRuntimeCapacityLedger
	Cells runtimequota.Trust
}

func (a RegionalRuntimeCapacityAuthority) AcceptStop(ctx context.Context, e runtimequota.Envelope) (RegionalRuntimeCapacityReceipt, error) {
	if a.Store == nil {
		return RegionalRuntimeCapacityReceipt{}, runtimequota.ErrTrust
	}
	stop, err := a.Cells.VerifyCapacityStop(e)
	if err != nil {
		return RegionalRuntimeCapacityReceipt{}, err
	}
	return a.Store.applyRegionalRuntimeCapacityStop(ctx, stop, e)
}

func cloneRegionalCapacity(r RegionalRuntimeCapacityReceipt) RegionalRuntimeCapacityReceipt {
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

func regionalCapacityNow() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

func reserveRegionalCapacityCheck(p runtimequota.CapacityPermit, receipt RegionalRuntimeQuotaReceipt, head RegionalRuntimeQuotaHead) error {
	if p.Validate() != nil || p.Grant.Validate() != nil {
		return ErrInvalidArgument
	}
	if receipt.Grant != p.Grant || head.Grant != p.Grant {
		return ErrConflict
	}
	if receipt.GrantEnvelope == nil || receipt.Retirement != nil {
		return ErrRegionalRuntimeQuotaFrozen
	}
	return nil
}

func checkServingSeatConservation(p runtimequota.ServingSeat, rows []RegionalRuntimeServingSeatReceipt) error {
	steady, rollout := 0, 0
	differentSteady := false
	for _, r := range rows {
		if r.ReleasedAt != nil || r.Seat.Permit.Grant.AllocationID != p.Permit.Grant.AllocationID {
			continue
		}
		if r.Seat.Rollout {
			rollout++
		} else {
			steady++
			if r.FenceID == "" && r.Seat.Permit.DeploymentID != p.Permit.DeploymentID {
				differentSteady = true
			}
		}
	}
	if p.Rollout {
		if rollout >= p.Permit.Grant.RolloutConcurrency || !differentSteady {
			return ErrRegionalRuntimeCapacity
		}
	} else if steady >= p.Permit.Grant.Concurrency {
		return ErrRegionalRuntimeCapacity
	}
	if steady+rollout >= p.Permit.Grant.Concurrency+p.Permit.Grant.RolloutConcurrency {
		return ErrRegionalRuntimeCapacity
	}
	return nil
}

func fenceRegionalCapacity(r RegionalRuntimeCapacityReceipt, fenceID string) (RegionalRuntimeCapacityReceipt, error) {
	if !validRegionalReleaseIDs(fenceID, fenceID, fenceID) {
		return RegionalRuntimeCapacityReceipt{}, ErrInvalidArgument
	}
	if r.FenceID != "" && r.FenceID != fenceID {
		return RegionalRuntimeCapacityReceipt{}, ErrConflict
	}
	r.FenceID = fenceID
	if r.IssuedAt == nil && r.ReleasedAt == nil {
		now := regionalCapacityNow()
		r.ReleasedAt = &now
	}
	return r, nil
}

func stopRegionalCapacity(r RegionalRuntimeCapacityReceipt, stop runtimequota.CapacityStop, e runtimequota.Envelope) (RegionalRuntimeCapacityReceipt, error) {
	if stop.Validate() != nil || r.Permit != stop.Permit || r.FenceID != stop.FenceID || r.IssuedAt == nil {
		return RegionalRuntimeCapacityReceipt{}, ErrConflict
	}
	if r.Stop != nil {
		if *r.Stop != stop {
			return RegionalRuntimeCapacityReceipt{}, ErrConflict
		}
		return r, nil // Retain the original receipt across signing-key rotation.
	}
	if r.ReleasedAt != nil {
		return RegionalRuntimeCapacityReceipt{}, ErrConflict
	}
	now := regionalCapacityNow()
	r.ReleasedAt = &now
	r.Stop = &stop
	r.StopEnvelope = &e
	return r, nil
}

func checkServingSeatIssue(seat runtimequota.ServingSeat, rows []RegionalRuntimeServingSeatReceipt) error {
	if !seat.Rollout {
		return nil
	}
	for _, r := range rows {
		if r.Seat.Permit.Grant == seat.Permit.Grant && !r.Seat.Rollout && r.IssuedAt != nil && r.ReleasedAt == nil && r.FenceID == "" && r.Seat.Permit.DeploymentID != seat.Permit.DeploymentID {
			return nil
		}
	}
	return ErrRegionalRuntimeCapacity
}
