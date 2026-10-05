package state

// adr: 635
import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

// Resources describe the actual native launch request, not today's app or plan.
// CPU includes its temporary startup peak. An orphan retains that conservative
// peak until native inventory or a verified physical stop resolves it.
type RegionalRuntimeResources struct {
	RAMMB, VCPU, CPUMillicores int
}

func (r RegionalRuntimeResources) Valid() bool {
	return r.RAMMB > 0 && r.RAMMB <= api.RegionalRuntimeResourceValueMax &&
		r.VCPU > 0 && r.VCPU <= api.RegionalRuntimeResourceValueMax &&
		r.CPUMillicores > 0 && r.CPUMillicores <= api.RegionalRuntimeResourceValueMax
}

// Source may be absent for historical bookkeeping-only issuance. Do not
// fabricate its native lookup key or backfill resource claims from current rows.
type RegionalRuntimeInventoryEntry struct {
	Receipt          RegionalRuntimeCapacityReceipt
	Source           *runtimequota.LaunchSource
	Resources        RegionalRuntimeResources
	ServingSeatsHeld int64
}

func regionalResourceRetry(old RegionalRuntimeLaunchSource, requested RegionalRuntimeResources) error {
	if old.Source.Permit.ID != "" && old.Resources != requested {
		return ErrConflict // Unknown historical provenance cannot be upgraded on retry.
	}
	return nil
}
