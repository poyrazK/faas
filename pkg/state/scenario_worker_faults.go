// adr: 20261017201012
package state

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func workerFaultRow(r sqlc.ScenarioWorkerFaultRow) rehearsalwork.WorkerFault {
	return rehearsalwork.WorkerFault{ID: r.ID, AccountID: r.AccountID, AppID: r.AppID, RunID: r.RunID, Workload: r.WorkloadName, InstanceID: r.InstanceID, DeploymentID: r.DeploymentID, InvocationID: r.InvocationID, CheckpointAttemptID: r.CheckpointAttemptID, RecorderSessionID: r.RecorderSessionID, State: r.State}
}

func armScenarioWorkerFault(ctx context.Context, tx pgx.Tx, req api.ScenarioSimulationRequest, d *api.ScenarioSimulationDecision) error {
	q := sqlc.New()
	checkpoint, err := q.ScenarioWorkerCheckpoint(ctx, tx, sqlc.ScenarioWorkerCheckpointParams{AppID: req.AppID, InstanceID: req.InstanceID, AttemptID: d.Attempt.ID, Checkpoint: d.Attempt.Fixture})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = q.ScenarioWorkerFaultForApp(ctx, tx, req.AppID); err == nil {
		return ErrConflict
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	id := uuid.NewString()
	err = q.ArmScenarioWorkerFault(ctx, tx, sqlc.ArmScenarioWorkerFaultParams{ID: id, AccountID: checkpoint.AccountID, AppID: req.AppID, RunID: req.RunID, InstanceID: req.InstanceID, DeploymentID: checkpoint.DeploymentID, InvocationID: checkpoint.InvocationID, CheckpointAttemptID: d.Attempt.ID, RecorderSessionID: checkpoint.RecorderSessionID})
	if err == nil {
		d.WorkerFaultID = id
	}
	return err
}

func (s *PgStore) ScenarioWorkerFault(ctx context.Context, id string) (rehearsalwork.WorkerFault, error) {
	r, err := sqlc.New().ScenarioWorkerFault(ctx, s.pool, id)
	return workerFaultRow(r), mapErr(err)
}
func (s *PgStore) PendingScenarioWorkerFaults(ctx context.Context) ([]rehearsalwork.WorkerFault, error) {
	rows, err := sqlc.New().PendingScenarioWorkerFaults(ctx, s.pool)
	out := make([]rehearsalwork.WorkerFault, 0, len(rows))
	for _, r := range rows {
		out = append(out, workerFaultRow(sqlc.ScenarioWorkerFaultRow(r)))
	}
	return out, err
}
func (s *PgStore) ClaimScenarioWorkerFault(ctx context.Context, id string) (rehearsalwork.WorkerFault, error) {
	n, err := sqlc.New().ClaimScenarioWorkerFault(ctx, s.pool, id)
	if err != nil {
		return rehearsalwork.WorkerFault{}, err
	}
	if n != 1 {
		return rehearsalwork.WorkerFault{}, ErrConflict
	}
	return s.ScenarioWorkerFault(ctx, id)
}
func (s *PgStore) FailScenarioWorkerFault(ctx context.Context, id string) error {
	return sqlc.New().FailScenarioWorkerFault(ctx, s.pool, id)
}
func (s *PgStore) ScenarioWorkerDispatchBlocked(ctx context.Context, app string) (bool, error) {
	return sqlc.New().ScenarioWorkerDispatchBlocked(ctx, s.pool, app)
}

func faultIdentity(f rehearsalwork.WorkerFault, id rehearsalrecorder.Identity, session string) bool {
	return f.AccountID == id.AccountID && f.RunID == id.RunID && f.AppID == id.AppID && f.InstanceID == id.InstanceID && f.DeploymentID == id.DeploymentID && f.RecorderSessionID == session
}

func (s *PgStore) workerFaultTransition(ctx context.Context, id rehearsalrecorder.Identity, fault, session, transition string) error {
	tx, policy, err := recorderTransaction(ctx, s, id)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	r, err := q.ScenarioWorkerFault(ctx, tx, fault)
	if err != nil {
		return mapErr(err)
	}
	f := workerFaultRow(r)
	if !faultIdentity(f, id, session) || policy.SimulationPlan == nil || policy.RecorderClosing {
		return ErrConflict
	}
	row, err := q.ScenarioRecorderSession(ctx, tx, recorderSessionParams(id, session))
	if err != nil {
		return mapErr(err)
	}
	if row.Status != "recording" || row.Failed != 0 || row.Pending != 0 {
		return ErrConflict
	}
	if transition != "terminated" && (!policy.Active.Valid || !policy.Active.Bool) {
		return ErrConflict
	}
	var n int64
	switch transition {
	case "ready":
		n, err = q.ReadyScenarioWorkerFault(ctx, tx, sqlc.ReadyScenarioWorkerFaultParams{ID: fault, SessionID: session})
	case "fenced":
		n, err = q.FenceScenarioWorkerFault(ctx, tx, sqlc.FenceScenarioWorkerFaultParams{ID: fault, SessionID: session})
	case "terminated":
		n, err = q.CompleteScenarioWorkerFault(ctx, tx, sqlc.CompleteScenarioWorkerFaultParams{ID: fault, SessionID: session})
		if err == nil && n == 1 {
			err = q.SetScenarioRecorderStatus(ctx, tx, sqlc.SetScenarioRecorderStatusParams{ID: session, Status: "closed"})
		}
	default:
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}
func (s *PgStore) ReadyScenarioWorkerCheckpoint(ctx context.Context, id rehearsalrecorder.Identity, fault, session string) error {
	return s.workerFaultTransition(ctx, id, fault, session, "ready")
}
func (s *PgStore) FenceScenarioWorkerTermination(ctx context.Context, id rehearsalrecorder.Identity, fault, session string) error {
	return s.workerFaultTransition(ctx, id, fault, session, "fenced")
}
func (s *PgStore) CompleteScenarioWorkerTermination(ctx context.Context, id rehearsalrecorder.Identity, fault, session string) error {
	return s.workerFaultTransition(ctx, id, fault, session, "terminated")
}

func (m *MemStore) armScenarioWorkerFaultLocked(p scenarioActionPolicy, req api.ScenarioSimulationRequest, d *api.ScenarioSimulationDecision) error {
	spec := rehearsalwork.Decode(p.Member.ManagedWorkJSON).WorkerTermination
	if spec == nil || spec.Workload != p.Member.Workload || spec.Checkpoint != d.Attempt.Fixture || d.Attempt.Status != "pending" {
		return nil
	}
	var job Invocation
	count := 0
	for _, inv := range m.invocations {
		if inv.AppID == req.AppID {
			job = inv
			count++
		}
	}
	// A retry receives the ordinary fixture response; it cannot arm a second kill.
	if job.Attempts > 1 {
		return nil
	}
	if count != 1 || job.State != InvocationDispatching || job.Attempts != 1 || job.InstanceID != req.InstanceID || job.LeaseExpiresAt == nil || !job.LeaseExpiresAt.After(time.Now()) {
		return ErrConflict
	}
	for _, f := range m.scenarioWorkerFaults {
		if f.AppID == req.AppID {
			return ErrConflict
		}
	}
	r, _, found := m.recorderRequestLocked(p, req)
	if !found {
		return ErrConflict
	}
	if m.scenarioWorkerFaults == nil {
		m.scenarioWorkerFaults = map[string]rehearsalwork.WorkerFault{}
	}
	id := uuid.NewString()
	m.scenarioWorkerFaults[id] = rehearsalwork.WorkerFault{ID: id, AccountID: req.AccountID, AppID: req.AppID, RunID: req.RunID, Workload: p.Member.Workload, InstanceID: req.InstanceID, DeploymentID: r.Identity.DeploymentID, InvocationID: job.ID, CheckpointAttemptID: d.Attempt.ID, RecorderSessionID: r.ID, State: "armed"}
	d.WorkerFaultID = id
	return nil
}
func (m *MemStore) ScenarioWorkerFault(ctx context.Context, id string) (rehearsalwork.WorkerFault, error) {
	if err := ctx.Err(); err != nil {
		return rehearsalwork.WorkerFault{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.scenarioWorkerFaults[id]
	if !ok {
		return f, ErrNotFound
	}
	return f, nil
}
func (m *MemStore) PendingScenarioWorkerFaults(ctx context.Context) ([]rehearsalwork.WorkerFault, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []rehearsalwork.WorkerFault{}
	for _, f := range m.scenarioWorkerFaults {
		p := m.scenarioActionPolicies[f.AppID]
		if p.SealedAt.IsZero() && (f.State == "ready" || (f.State == "terminated" && simulationLiveState(m.instances[f.InstanceID].State))) {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > 64 {
		out = out[:64]
	}
	return out, nil
}
func (m *MemStore) ClaimScenarioWorkerFault(ctx context.Context, id string) (rehearsalwork.WorkerFault, error) {
	if err := ctx.Err(); err != nil {
		return rehearsalwork.WorkerFault{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.scenarioWorkerFaults[id]
	if !ok || f.State != "ready" || !m.scenarioActionPolicies[f.AppID].SealedAt.IsZero() {
		return f, ErrConflict
	}
	f.State = "claimed"
	m.scenarioWorkerFaults[id] = f
	return f, nil
}

func (m *MemStore) deleteScenarioWorkerFaultsForAppLocked(app string) {
	delete(m.scenarioColdPreparations, app)
	for id, r := range m.scenarioSuspendResumes {
		if r.AppID == app {
			delete(m.scenarioSuspendResumes, id)
		}
	}
	for id, f := range m.scenarioWorkerFaults {
		if f.AppID == app {
			delete(m.scenarioWorkerFaults, id)
		}
	}
}
func (m *MemStore) FailScenarioWorkerFault(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if f, ok := m.scenarioWorkerFaults[id]; ok && f.State != "terminated" {
		f.State = "failed"
		m.scenarioWorkerFaults[id] = f
	}
	return nil
}
func (m *MemStore) scenarioWorkerDispatchBlockedLocked(app string) bool {
	for _, f := range m.scenarioPostgresFaults {
		if f.AppID == app && (f.State == "requested" || f.State == "cutting") {
			return true
		}
	}
	for _, r := range m.scenarioSuspendResumes {
		if r.AppID == app && r.InvocationID != "" && r.State != "complete" {
			return true
		}
	}
	for _, f := range m.scenarioWorkerFaults {
		if f.AppID == app && (f.State != "terminated" || simulationLiveState(m.instances[f.InstanceID].State)) {
			return true
		}
	}
	return false
}
func (m *MemStore) ScenarioWorkerDispatchBlocked(ctx context.Context, app string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.scenarioWorkerDispatchBlockedLocked(app), nil
}
func (m *MemStore) workerFaultTransition(ctx context.Context, id rehearsalrecorder.Identity, fault, session, transition string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.recorderPolicyLocked(id)
	if err != nil {
		return err
	}
	f, ok := m.scenarioWorkerFaults[fault]
	r, found := p.Recorders[session]
	if !ok || !found || !faultIdentity(f, id, session) || r.Identity != id || r.Status != "recording" || p.RecorderClosing || p.SimulationFailed {
		return ErrConflict
	}
	for _, c := range r.Requests {
		if c.Status != "completed" {
			return ErrConflict
		}
	}
	expected := map[string]string{"ready": "armed", "fenced": "claimed", "terminated": "fenced"}[transition]
	if expected == "" || f.State != expected {
		return ErrConflict
	}
	if transition != "terminated" && !m.rehearsalExecutionLocked(id) {
		return ErrConflict
	}
	if transition == "ready" {
		completed := false
		for _, a := range p.SimulationAttempts {
			completed = completed || (a.ID == f.CheckpointAttemptID && a.Status == "completed")
		}
		if !completed {
			return ErrConflict
		}
	}
	if transition == "fenced" {
		inv := m.invocations[f.InvocationID]
		count := 0
		for _, v := range m.invocations {
			if v.AppID == id.AppID {
				count++
			}
		}
		if count != 1 || inv.State != InvocationDispatching || inv.Attempts != 1 || inv.InstanceID != id.InstanceID || inv.LeaseExpiresAt == nil || !inv.LeaseExpiresAt.After(time.Now()) {
			return ErrConflict
		}
	}
	f.State = transition
	m.scenarioWorkerFaults[fault] = f
	if transition == "terminated" {
		r.Status = "closed"
		p.Recorders[session] = r
		m.scenarioActionPolicies[id.AppID] = p
	}
	return nil
}
func (m *MemStore) ReadyScenarioWorkerCheckpoint(ctx context.Context, id rehearsalrecorder.Identity, fault, session string) error {
	return m.workerFaultTransition(ctx, id, fault, session, "ready")
}
func (m *MemStore) FenceScenarioWorkerTermination(ctx context.Context, id rehearsalrecorder.Identity, fault, session string) error {
	return m.workerFaultTransition(ctx, id, fault, session, "fenced")
}
func (m *MemStore) CompleteScenarioWorkerTermination(ctx context.Context, id rehearsalrecorder.Identity, fault, session string) error {
	return m.workerFaultTransition(ctx, id, fault, session, "terminated")
}
