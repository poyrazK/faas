package state

// adr: 799
import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

// History is retained independently of current scheduler/customer rows. Missing
// or ambiguous source provenance is explicit; it never means zero consumption.
type RegionalRuntimeBillingInterval struct {
	ID                                             int64
	AppID, InstanceID, DeploymentID, NodeID, RunID string
	StartedAt, RecordedAt                          time.Time
	CompleteStart                                  bool
	EndedAt, UnknownSince                          *time.Time
	SourceMatches                                  int64
	Permit                                         *runtimequota.CapacityPermit
	Launch                                         *RegionalRuntimeLaunchSource
	IssuedAt                                       *time.Time
}

type RegionalRuntimeBillingHistoryStore interface {
	RegionalRuntimeBillingHistoryStart(context.Context) (time.Time, error)
	// Pages are retained evidence, not a completeness assertion or a snapshot
	// across pages. A sealing transaction must read under its journal lock.
	ListRegionalRuntimeBillingIntervals(context.Context, string, time.Time, int64, int) ([]RegionalRuntimeBillingInterval, error)
}

func validRegionalBillingHistoryPage(app string, minute time.Time, after int64, limit int) bool {
	return validRegionalReleaseIDs(app, app, app) && minute.UnixMicro() > 0 &&
		minute.UnixMicro() <= api.GlobalRuntimeQuotaGenerationMax-time.Minute.Microseconds() &&
		minute.Equal(minute.Truncate(time.Minute)) && after >= 0 && limit >= 1 && limit <= api.RegionalRuntimeCapacityPageMax
}

func regionalBillingIntervalMatches(r RegionalRuntimeBillingInterval, p runtimequota.CapacityPermit) bool {
	return r.AppID == p.Grant.Binding.RegionalAppID && r.InstanceID == p.InstanceID &&
		r.DeploymentID == p.DeploymentID && r.NodeID == p.NodeID && r.RunID == p.RunID
}

func regionalBillingInstanceBillable(ins Instance) bool {
	if ins.Mode == "mirror" || regionalQuotaCanonicalID(ins.AppID) == "" {
		return false
	}
	switch State(ins.State) {
	case StateWaking, StateColdBooting, StateRunning, StateDraining, StateSnapshotting, StateMigrating, StateWarm:
		return true
	}
	return false
}
