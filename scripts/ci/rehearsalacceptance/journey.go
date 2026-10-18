package main

import (
	"errors"
	"reflect"

	"github.com/onebox-faas/faas/pkg/rehearsalfault"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
)

type checkpointIdentity struct {
	ID, InstanceID, RecorderSessionID, InvocationID, CheckpointAttemptID string
}

// Called after validating the sealed work journal. Each journey proves its own
// physical operation; identities are shared only for the common effect checks.
func journeyCheckpoint(r run, account, journey string) (checkpointIdentity, error) {
	var id checkpointIdentity
	s := r.Actions.Evidence.Snapshot
	work := s.ManagedWork
	switch journey {
	case "running-job":
		e := r.SuspendResume
		if e == nil || e.Validate([]string{"import-api", "worker"}) != nil || e.RunID != r.RunID || !reflect.DeepEqual(s.SuspendResume, e) || r.Actions.SuspendResumeSHA256 != e.PlanSHA256 || work.Plan.PostgresFault != nil || r.Actions.PostgresFaultSHA256 != "" {
			return id, errors.New("missing independent suspend/resume evidence")
		}
		if !reflect.DeepEqual(e.Spec, rehearsalresume.Spec{Version: 1, Checkpoint: "managed_job", Workloads: []string{"worker"}, WorkerCheckpoint: "after-commit", PauseMS: 1000, WithinMS: 5000}) {
			return id, errors.New("wrong running-job checkpoint contract")
		}
		p := e.Receipts[0]
		if p.AccountID != account || p.AppID != r.WorkloadAppIDs["worker"] || p.DeploymentID != r.WorkloadDeploymentIDs["worker"] || p.ResumeMS > e.Spec.WithinMS {
			return id, errors.New("checkpoint identity or resume budget differs")
		}
		return checkpointIdentity{p.ID, p.InstanceID, p.RecorderSessionID, p.InvocationID, p.CheckpointAttemptID}, nil
	case "postgres-recovery":
		f := work.PostgresFault
		spec := rehearsalfault.Postgres{Workload: "worker", Checkpoint: "after-commit", UnavailableMS: 1000, RecoverWithinMS: 5000}
		if r.SuspendResume != nil || s.SuspendResume != nil || r.Actions.SuspendResumeSHA256 != "" || f == nil || f.Validate() != nil || f.Spec != spec || r.Actions.PostgresFaultSHA256 != rehearsalpg.Hash(spec) || f.AccountID != account || f.RunID != r.RunID || f.AppID != r.WorkloadAppIDs["worker"] || f.DeploymentID != r.WorkloadDeploymentIDs["worker"] || r.Postgres == nil || f.ResourceID != r.Postgres.ResourceID || f.ClosedConnections < 1 || f.RecoveryMS > spec.RecoverWithinMS {
			return id, errors.New("missing independently activated and recovered PostgreSQL fault")
		}
		return checkpointIdentity{f.ID, f.InstanceID, f.RecorderSessionID, f.InvocationID, f.CheckpointAttemptID}, nil
	default:
		return id, errors.New("unsupported qualification journey")
	}
}
