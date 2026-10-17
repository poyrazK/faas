// adr: 20261017201015
package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func armScenarioSuspendCheckpoint(ctx context.Context, tx pgx.Tx, req api.ScenarioSimulationRequest, d *api.ScenarioSimulationDecision) error {
	q := sqlc.New()
	c, err := q.ScenarioSuspendCheckpoint(ctx, tx, sqlc.ScenarioSuspendCheckpointParams{AppID: req.AppID, InstanceID: req.InstanceID, AttemptID: d.Attempt.ID, Checkpoint: d.Attempt.Fixture})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	id := uuid.NewString()
	err = q.InsertScenarioSuspendCheckpoint(ctx, tx, sqlc.InsertScenarioSuspendCheckpointParams{ID: id, AccountID: c.AccountID, AppID: req.AppID, RunID: req.RunID, DeploymentID: c.DeploymentID, InstanceID: req.InstanceID, SessionID: c.RecorderSessionID, InvocationID: c.InvocationID, AttemptID: d.Attempt.ID})
	if err == nil {
		d.SuspendResumeID = id
	}
	return mapErr(err)
}

func (m *MemStore) armScenarioSuspendCheckpointLocked(p scenarioActionPolicy, req api.ScenarioSimulationRequest, d *api.ScenarioSimulationDecision) error {
	spec := rehearsalresume.Decode(p.Member.SuspendResumeJSON)
	if !spec.ManagedJob() || len(spec.Workloads) != 1 || spec.Workloads[0] != p.Member.Workload || spec.WorkerCheckpoint != d.Attempt.Fixture || d.Attempt.Status != "pending" {
		return nil
	}
	var job Invocation
	count := 0
	for _, v := range m.invocations {
		member := m.scenarioActionPolicies[v.AppID].Member
		if member.AccountID == req.AccountID && member.RunID == req.RunID {
			job = v
			count++
		}
	}
	if job.Attempts > 1 {
		return nil
	}
	if count != 1 || job.AppID != req.AppID || job.State != InvocationDispatching || job.Attempts != 1 || job.InstanceID != req.InstanceID || job.Source != InvocationAsyncInvoke || job.ReplayGeneration != 0 || job.LeaseExpiresAt == nil || !job.LeaseExpiresAt.After(time.Now()) {
		return ErrConflict
	}
	for _, r := range m.scenarioSuspendResumes {
		if r.AppID == req.AppID {
			return ErrConflict
		}
	}
	rc, _, found := m.recorderRequestLocked(p, req)
	if !found || m.instances[req.InstanceID].State != "running" {
		return ErrConflict
	}
	r := rehearsalresume.Receipt{ID: uuid.NewString(), AccountID: req.AccountID, AppID: req.AppID, RunID: req.RunID, Workload: p.Member.Workload, DeploymentID: rc.Identity.DeploymentID, InstanceID: req.InstanceID, RecorderSessionID: rc.ID, State: "waiting", InvocationID: job.ID, InvocationAttempt: 1, CheckpointAttemptID: d.Attempt.ID}
	if m.suspendCheckpointBusyLocked(r) {
		return ErrConflict
	}
	for id, q := range rc.Requests {
		if id != req.RecorderRequestID && q.Status != "completed" {
			return ErrConflict
		}
	}
	if m.scenarioSuspendResumes == nil {
		m.scenarioSuspendResumes = map[string]rehearsalresume.Receipt{}
	}
	m.scenarioSuspendResumes[r.ID] = r
	d.SuspendResumeID = r.ID
	return nil
}

func (m *MemStore) suspendJobActiveLocked(r rehearsalresume.Receipt) bool {
	j, ok := m.invocations[r.InvocationID]
	return ok && j.AppID == r.AppID && j.InstanceID == r.InstanceID && j.Source == InvocationAsyncInvoke && j.State == InvocationDispatching && j.Attempts == r.InvocationAttempt && j.Attempts == 1 && j.ReplayGeneration == 0 && j.LeaseExpiresAt != nil && j.LeaseExpiresAt.After(time.Now())
}

func (m *MemStore) suspendCheckpointCompletedLocked(r rehearsalresume.Receipt) bool {
	p := m.scenarioActionPolicies[r.AppID]
	spec := rehearsalresume.Decode(p.Member.SuspendResumeJSON)
	for _, a := range p.SimulationAttempts {
		if a.ID == r.CheckpointAttemptID && a.Fixture == spec.WorkerCheckpoint && a.Status == "completed" {
			return true
		}
	}
	return false
}

func (m *MemStore) suspendNewJobBlockedLocked(app string) bool {
	p := m.scenarioActionPolicies[app]
	spec := rehearsalresume.Decode(p.Member.SuspendResumeJSON)
	if !spec.ManagedJob() {
		return false
	}
	if len(spec.Workloads) != 1 || spec.Workloads[0] != p.Member.Workload {
		return true
	}
	for _, v := range m.invocations {
		w := m.scenarioActionPolicies[v.AppID].Member
		if w.AccountID == p.Member.AccountID && w.RunID == p.Member.RunID {
			return true
		}
	}
	return false
}
