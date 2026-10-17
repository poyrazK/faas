// adr: 1356
package rehearsalwork

import (
	"context"
	"errors"
)

// WorkerTermination names a cooperative HTTP simulation checkpoint. V1 permits
// exactly one job for this worker, terminated during its first dispatch.
type WorkerTermination struct {
	Workload   string `json:"workload" yaml:"workload"`
	Checkpoint string `json:"checkpoint" yaml:"checkpoint"`
}

// WorkerFault is retained platform evidence. A terminated receipt is written
// only by the original vmmd recorder owner after confirmed physical cleanup.
type WorkerFault struct {
	ID                  string `json:"id"`
	AccountID           string `json:"account_id"`
	AppID               string `json:"app_id"`
	RunID               string `json:"run_id"`
	Workload            string `json:"workload"`
	DeploymentID        string `json:"deployment_id"`
	InstanceID          string `json:"instance_id"`
	InvocationID        string `json:"invocation_id"`
	CheckpointAttemptID string `json:"checkpoint_attempt_id"`
	RecorderSessionID   string `json:"recorder_session_id"`
	State               string `json:"state"`
	RecoveryInstanceID  string `json:"recovery_instance_id,omitempty"`
}

// FaultStore is used by schedd. Admission is gated separately; already enrolled
// faults remain drainable when operators disable new rehearsal admission.
type FaultStore interface {
	PendingScenarioWorkerFaults(context.Context) ([]WorkerFault, error)
	ClaimScenarioWorkerFault(context.Context, string) (WorkerFault, error)
	ScenarioWorkerFault(context.Context, string) (WorkerFault, error)
	FailScenarioWorkerFault(context.Context, string) error
	ScenarioWorkerDispatchBlocked(context.Context, string) (bool, error)
}

func (e Evidence) validateWorkerFault() error {
	spec, f := e.Plan.WorkerTermination, e.WorkerFault
	if spec == nil {
		if f != nil {
			return errors.New("unexpected worker fault evidence")
		}
		return nil
	}
	if f == nil || f.State != "terminated" || f.RunID != e.RunID || f.Workload != spec.Workload || !validID(f.ID) || !validID(f.AccountID) || !validID(f.CheckpointAttemptID) || !validID(f.RecorderSessionID) {
		return errors.New("worker termination was not acknowledged by its collector owner")
	}
	count := 0
	for _, j := range e.Jobs {
		if j.Workload != spec.Workload {
			continue
		}
		count++
		if j.ID != f.InvocationID || j.AppID != f.AppID || j.DeploymentID != f.DeploymentID || len(j.Attempts) < 2 || j.Attempts[0].InstanceID != f.InstanceID || j.Attempts[1].InstanceID == f.InstanceID || j.Attempts[1].InstanceID != f.RecoveryInstanceID {
			return errors.New("terminated job did not retry on a fresh VM")
		}
		for _, attempt := range j.Attempts[1:] {
			if attempt.InstanceID == f.InstanceID {
				return errors.New("terminated VM reappeared in a later retry")
			}
		}
	}
	if count != 1 {
		return errors.New("worker termination requires exactly one job for its target")
	}
	return nil
}

// BindRecovery keeps generated identities out of recipe identity while requiring
// the independently recorded second dispatch in completed public evidence.
func (e *Evidence) BindRecovery() {
	if e.WorkerFault == nil {
		return
	}
	for _, j := range e.Jobs {
		if j.ID == e.WorkerFault.InvocationID && len(j.Attempts) >= 2 {
			e.WorkerFault.RecoveryInstanceID = j.Attempts[1].InstanceID
		}
	}
}
