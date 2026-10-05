package state

// adr: 633
import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

type RegionalRuntimeLaunchSource struct {
	Source    runtimequota.LaunchSource
	CreatedAt time.Time
}

// The cursor is composite: permit IDs are unique only within an app journal.
type RegionalRuntimeRetirementCursor struct{ AppID, PermitID string }

type RegionalRuntimeRetirementCandidate struct {
	Source  runtimequota.LaunchSource
	FenceID string
}

type RegionalRuntimeLaunchStore interface {
	// Persist the exact source before issuance in the same app journal transaction.
	IssueRegionalRuntimeLaunch(context.Context, runtimequota.Placement, runtimequota.LaunchSource) error
	RegionalRuntimeLaunchSource(context.Context, string, string) (RegionalRuntimeLaunchSource, error)
	// Read original issued, fenced, unreleased sources, independent of current rows.
	ListRegionalRuntimeRetirements(context.Context, string, RegionalRuntimeRetirementCursor, int) ([]RegionalRuntimeRetirementCandidate, error)
}

func validRegionalRetirementPage(cell string, after RegionalRuntimeRetirementCursor, limit int) bool {
	return validRegionalReleaseIDs(cell, cell, cell) && limit >= 1 && limit <= api.RegionalRuntimeCapacityPageMax &&
		((after.AppID == "" && after.PermitID == "") || validRegionalReleaseIDs(after.AppID, after.PermitID, after.PermitID))
}

func checkRegionalLaunchSource(source runtimequota.LaunchSource, old RegionalRuntimeLaunchSource, physical RegionalRuntimeCapacityReceipt) error {
	if source.Validate() != nil || physical.Permit != source.Permit {
		return ErrInvalidArgument
	}
	if old.Source.Permit.ID != "" {
		if old.Source != source {
			return ErrConflict
		}
		return nil
	}
	// Never fabricate an actual lookup spelling for an already escaped permit.
	if physical.IssuedAt != nil || physical.FenceID != "" || physical.ReleasedAt != nil {
		return ErrRegionalRuntimePlacementUnavailable
	}
	return nil
}
