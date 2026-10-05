package state

// adr: 626
import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimequota"
)

type RegionalRuntimeServingResumeReceipt struct {
	Intent    runtimequota.ServingResumeIntent
	CreatedAt time.Time
}

// Issuance coalesces competing requests onto the existing held resume intent.
// Callers must dispatch the returned intent, never the proposed one. Neither
// an RPC error nor an acknowledgement releases a seat or a physical hold.
type RegionalRuntimeServingResumeStore interface {
	IssueRegionalRuntimeServingResume(context.Context, runtimequota.ServingResumeIntent) (RegionalRuntimeServingResumeReceipt, error)
	RegionalRuntimeServingResume(context.Context, string, string) (RegionalRuntimeServingResumeReceipt, error)
	// Held lookup counts fenced and retired authorizations until release. More
	// than one host incarnation for the row requires inventory reconciliation.
	HeldRegionalRuntimeServingResumeForInstance(context.Context, string, string, string, string) (RegionalRuntimeServingResumeReceipt, error)
}

func validResumeInstanceIDs(app, instance, node, run string) bool {
	return validRegionalReleaseIDs(app, instance, node) && validRegionalReleaseIDs(run, run, run)
}

func checkResumeRetry(proposed, retained runtimequota.ServingResumeIntent) error {
	if proposed.Seat.Permit != retained.Seat.Permit || proposed.Seat.Rollout != retained.Seat.Rollout || (proposed.ID == retained.ID && proposed != retained) {
		return ErrConflict
	}
	return nil
}
