package tasktoolrehearsal

import (
	"time"

	"github.com/google/uuid"
)

const MigrationWorkload = "migration"

// WorkloadLease is the lifetime of a disposable run from its reservation.
const WorkloadLease = 15 * time.Minute

// WorkerStopDeadline is the maximum lifetime of a freshly observed lease
// before the scheduler must decide whether to interrupt its worker.
const WorkerStopDeadline = 5 * time.Second

type WorkerInterruption struct {
	AccountID, RunID, PlanSHA256, TaskID            string
	AppID, DeploymentID, InstanceID, NodeID, WakeID string
	ObservedAt                                      time.Time
}

// WorkloadID derives an app identity from the native run reservation.
func WorkloadID(run uuid.UUID, role string) string {
	return uuid.NewSHA1(run, []byte("hosted-task-rehearsal-v1:"+role)).String()
}
