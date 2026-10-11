package state

// adr: 800
import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimequota"
)

// One original restoring/running lease attempt. LeaseOwner is the scheduler
// owner, not a native node identity. Native ownership comes only from issuance.
type RegionalAppTaskBillingInterval struct {
	ID                                             int64
	AppID, TaskID, DeploymentID, RunID, LeaseOwner string
	StartedAt, RecordedAt                          time.Time
	CompleteStart                                  bool
	EndedAt, UnknownSince                          *time.Time
	SourceMatches                                  int64
	Permit                                         *runtimequota.CapacityPermit
	Launch                                         *RegionalRuntimeLaunchSource
	IssuedAt                                       *time.Time
}

type RegionalAppTaskBillingHistoryStore interface {
	RegionalAppTaskBillingHistoryStart(context.Context) (time.Time, error)
	// Pages are retained evidence; a future sealing transaction must establish
	// complete coverage under the app journal lock, including native sources.
	ListRegionalAppTaskBillingIntervals(context.Context, string, time.Time, int64, int) ([]RegionalAppTaskBillingInterval, error)
}

func regionalAppTaskBillable(task AppTask) bool {
	return regionalQuotaCanonicalID(task.AppID) != "" && (task.Status == AppTaskRestoring || task.Status == AppTaskRunning)
}

func regionalAppTaskBillingPermitMatches(r RegionalAppTaskBillingInterval, p runtimequota.CapacityPermit) bool {
	instance, err := runtimequota.AppTaskInstanceID(r.TaskID, r.RunID)
	return err == nil && p.Kind == runtimequota.CapacityAppTask && p.InstanceID == instance &&
		p.Grant.Binding.RegionalAppID == r.AppID && p.DeploymentID == r.DeploymentID && p.RunID == r.RunID
}

func regionalAppTaskBillingLaunchMatches(r RegionalAppTaskBillingInterval, launch RegionalRuntimeLaunchSource) bool {
	// Missing historical task provenance stays readable and explicitly missing.
	return launch.Source.TaskID == "" || launch.Source.TaskID == r.TaskID
}
