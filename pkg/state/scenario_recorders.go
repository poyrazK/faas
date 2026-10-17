// adr: 20261017201007
package state

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalcommand"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func recorderTransaction(ctx context.Context, s *PgStore, identity rehearsalrecorder.Identity) (pgx.Tx, sqlc.ScenarioSimulationPolicyRow, error) {
	var empty sqlc.ScenarioSimulationPolicyRow
	if !scenarioTestRunPattern.MatchString(identity.RunID) {
		return nil, empty, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, empty, err
	}
	q := sqlc.New()
	err = q.LockScenarioActionRun(ctx, tx, sqlc.LockScenarioActionRunParams{AccountID: identity.AccountID, RunID: identity.RunID})
	if err == nil {
		_, err = q.LockScenarioActionApp(ctx, tx, sqlc.LockScenarioActionAppParams{AccountID: identity.AccountID, AppID: identity.AppID})
	}
	var policy sqlc.ScenarioSimulationPolicyRow
	if err == nil {
		policy, err = q.ScenarioSimulationPolicy(ctx, tx, sqlc.ScenarioSimulationPolicyParams{AccountID: identity.AccountID, AppID: identity.AppID, InstanceID: identity.InstanceID})
	}
	if err == nil && (policy.AccountID != identity.AccountID || policy.RunID != identity.RunID || policy.SealedAt.Valid || len(policy.SimulationPlan) == 0) {
		err = ErrConflict
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, empty, fmt.Errorf("recorder authority: %w", mapErr(err))
	}
	return tx, policy, nil
}

func recorderSessionParams(identity rehearsalrecorder.Identity, id string) sqlc.ScenarioRecorderSessionParams {
	return sqlc.ScenarioRecorderSessionParams{ID: id, AppID: identity.AppID, InstanceID: identity.InstanceID, DeploymentID: identity.DeploymentID, AccountID: identity.AccountID, RunID: identity.RunID}
}

func (s *PgStore) StartScenarioRecorder(ctx context.Context, identity rehearsalrecorder.Identity) (string, error) {
	tx, policy, err := recorderTransaction(ctx, s, identity)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if policy.RecorderClosing {
		return "", rehearsalrecorder.ErrClosing
	}
	if !policy.Active.Valid || !policy.Active.Bool || policy.DeploymentID != identity.DeploymentID {
		return "", ErrConflict
	}
	q := sqlc.New()
	counts, err := q.ScenarioRecorderRunCounts(ctx, tx, sqlc.ScenarioRecorderRunCountsParams{AccountID: identity.AccountID, RunID: identity.RunID})
	if err != nil {
		return "", err
	}
	if counts.Sessions >= rehearsalrecorder.MaxSessions {
		if err = q.FailScenarioSimulation(ctx, tx, identity.AppID); err != nil {
			return "", err
		}
		if err = tx.Commit(ctx); err != nil {
			return "", err
		}
		return "", ErrConflict
	}
	id := uuid.NewString()
	if err = q.StartScenarioRecorder(ctx, tx, sqlc.StartScenarioRecorderParams{ID: id, AppID: identity.AppID, InstanceID: identity.InstanceID, DeploymentID: identity.DeploymentID}); err != nil {
		return "", mapErr(err)
	}
	return id, tx.Commit(ctx)
}

func (s *PgStore) BeginScenarioRecorderRequest(ctx context.Context, identity rehearsalrecorder.Identity, sessionID string) (string, error) {
	tx, policy, err := recorderTransaction(ctx, s, identity)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if policy.RecorderClosing {
		return "", rehearsalrecorder.ErrClosing
	}
	if !policy.Active.Valid || !policy.Active.Bool || policy.DeploymentID != identity.DeploymentID {
		return "", ErrConflict
	}
	q := sqlc.New()
	session, err := q.ScenarioRecorderSession(ctx, tx, recorderSessionParams(identity, sessionID))
	if err != nil {
		return "", mapErr(err)
	}
	if session.Status != "recording" {
		return "", rehearsalrecorder.ErrClosing
	}
	counts, err := q.ScenarioRecorderRunCounts(ctx, tx, sqlc.ScenarioRecorderRunCountsParams{AccountID: identity.AccountID, RunID: identity.RunID})
	if err != nil {
		return "", err
	}
	if counts.Requests >= rehearsalrecorder.MaxRequests {
		if err = q.FailScenarioSimulation(ctx, tx, identity.AppID); err != nil {
			return "", err
		}
		if err = tx.Commit(ctx); err != nil {
			return "", err
		}
		return "", ErrConflict
	}
	id := uuid.NewString()
	if err = q.InsertScenarioRecorderRequest(ctx, tx, sqlc.InsertScenarioRecorderRequestParams{ID: id, SessionID: sessionID}); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func (s *PgStore) SettleScenarioRecorder(ctx context.Context, identity rehearsalrecorder.Identity, sessionID string, failed, acknowledgement bool) (string, error) {
	tx, _, err := recorderTransaction(ctx, s, identity)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.ScenarioRecorderSession(ctx, tx, recorderSessionParams(identity, sessionID))
	if err != nil {
		return "", mapErr(err)
	}
	status := row.Status
	if status == "recording" {
		switch {
		case failed || row.Failed > 0:
			status = "failed"
		case row.RecorderClosing && acknowledgement && row.Pending == 0:
			status = "closed"
		}
		if status != "recording" {
			if err = q.SetScenarioRecorderStatus(ctx, tx, sqlc.SetScenarioRecorderStatusParams{ID: sessionID, Status: status}); err != nil {
				return "", err
			}
		} else if row.RecorderClosing {
			status = "closing"
		}
	}
	return status, tx.Commit(ctx)
}

type scenarioRecorderSession struct {
	CommandResult                string
	NetworkNodeID, NetworkHostIP string
	NetworkOpen                  bool
	ID                           string
	Identity                     rehearsalrecorder.Identity
	Status                       string
	Requests                     map[string]scenarioRecorderRequest
}
type scenarioRecorderRequest struct{ Status, AttemptID string }

func (m *MemStore) recorderPolicyLocked(identity rehearsalrecorder.Identity) (scenarioActionPolicy, error) {
	p, ok := m.scenarioActionPolicyLocked(identity.AppID)
	if !ok || p.Member.AccountID != identity.AccountID || p.Member.RunID != identity.RunID || p.Member.SimulationJSON == "" || !p.SealedAt.IsZero() {
		return p, ErrConflict
	}
	return p, nil
}

func (m *MemStore) StartScenarioRecorder(ctx context.Context, identity rehearsalrecorder.Identity) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.recorderPolicyLocked(identity)
	if err != nil {
		return "", err
	}
	if p.RecorderClosing {
		return "", rehearsalrecorder.ErrClosing
	}
	if !eligibleScenarioTestApp(m.apps[identity.AppID], identity.AccountID) || !m.rehearsalExecutionLocked(identity) || p.SimulationFailed {
		return "", ErrConflict
	}
	sessions, _ := m.recorderCountsLocked(identity)
	if sessions >= rehearsalrecorder.MaxSessions {
		p.SimulationFailed = true
		m.scenarioActionPolicies[identity.AppID] = p
		return "", ErrConflict
	}
	for _, r := range p.Recorders {
		if r.Identity.InstanceID == identity.InstanceID {
			return "", ErrConflict
		}
	}
	if p.Recorders == nil {
		p.Recorders = make(map[string]scenarioRecorderSession)
	}
	id := uuid.NewString()
	p.Recorders[id] = scenarioRecorderSession{ID: id, Identity: identity, Status: "recording", Requests: make(map[string]scenarioRecorderRequest)}
	m.scenarioActionPolicies[identity.AppID] = p
	return id, nil
}

func (m *MemStore) recorderCountsLocked(identity rehearsalrecorder.Identity) (int, int) {
	sessions, requests := 0, 0
	for _, p := range m.scenarioActionPolicies {
		if p.Member.AccountID != identity.AccountID || p.Member.RunID != identity.RunID {
			continue
		}
		sessions += len(p.Recorders)
		for _, r := range p.Recorders {
			requests += len(r.Requests)
		}
	}
	return sessions, requests
}

func (m *MemStore) BeginScenarioRecorderRequest(ctx context.Context, identity rehearsalrecorder.Identity, sessionID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.recorderPolicyLocked(identity)
	if err != nil {
		return "", err
	}
	if p.RecorderClosing {
		return "", rehearsalrecorder.ErrClosing
	}
	r, ok := p.Recorders[sessionID]
	if !ok || r.Identity != identity || r.Status != "recording" || !eligibleScenarioTestApp(m.apps[identity.AppID], identity.AccountID) || p.SimulationFailed || !m.rehearsalExecutionLocked(identity) {
		return "", ErrConflict
	}
	_, requests := m.recorderCountsLocked(identity)
	if requests >= rehearsalrecorder.MaxRequests {
		p.SimulationFailed = true
		m.scenarioActionPolicies[identity.AppID] = p
		return "", ErrConflict
	}
	id := uuid.NewString()
	r.Requests[id] = scenarioRecorderRequest{Status: "pending"}
	return id, nil
}

func (m *MemStore) SettleScenarioRecorder(ctx context.Context, identity rehearsalrecorder.Identity, sessionID string, failed, acknowledgement bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.recorderPolicyLocked(identity)
	if err != nil {
		return "", err
	}
	r, ok := p.Recorders[sessionID]
	if !ok || r.Identity != identity {
		return "", ErrNotFound
	}
	pending := false
	for _, request := range r.Requests {
		failed = failed || request.Status == "failed"
		pending = pending || request.Status == "pending" || request.Status == "admitted"
	}
	if r.Status == "recording" {
		if failed {
			r.Status = "failed"
		} else if p.RecorderClosing && acknowledgement && !pending {
			r.Status = "closed"
		}
		p.Recorders[sessionID] = r
		m.scenarioActionPolicies[identity.AppID] = p
	}
	if r.Status == "recording" && p.RecorderClosing {
		return "closing", nil
	}
	return r.Status, nil
}

var _ rehearsalrecorder.Store = (*PgStore)(nil)
var _ rehearsalrecorder.Store = (*MemStore)(nil)

func (m *MemStore) recorderRequestLocked(p scenarioActionPolicy, req api.ScenarioSimulationRequest) (scenarioRecorderSession, scenarioRecorderRequest, bool) {
	for _, r := range p.Recorders {
		if r.Identity.AccountID != req.AccountID || r.Identity.RunID != req.RunID || r.Identity.InstanceID != req.InstanceID || r.Identity.AppID != req.AppID {
			continue
		}
		c, ok := r.Requests[req.RecorderRequestID]
		if ok {
			return r, c, true
		}
	}
	return scenarioRecorderSession{}, scenarioRecorderRequest{}, false
}

func (m *MemStore) addScenarioRecorderSnapshotLocked(snapshot *api.ScenarioActionSnapshot, appIDs []string, closing bool) {
	if snapshot.SimulationVersion == 0 {
		return
	}
	snapshot.RecorderVersion = rehearsalrecorder.Version
	// The caller's pending-work summary already includes commands from these
	// account-scoped members. Another account may reuse the same run string.
	for _, id := range appIDs {
		p := m.scenarioActionPolicies[id]
		if closing {
			p.RecorderClosing = true
			m.scenarioActionPolicies[id] = p
		}
		snapshot.RecorderSessions += int64(len(p.Recorders))
		hasInstance := false
		for _, inst := range m.instances {
			hasInstance = hasInstance || inst.AppID == id
		}
		if len(p.Recorders) == 0 && !hasInstance {
			snapshot.IncompleteRecorderSessions++
		}
		for _, r := range p.Recorders {
			if r.Status == "recording" {
				snapshot.PendingRecorderSessions++
			}
			if r.Status == "failed" {
				snapshot.IncompleteRecorderSessions++
			}
		}
		for _, inst := range m.instances {
			if inst.AppID != id {
				continue
			}
			found := false
			for _, r := range p.Recorders {
				found = found || r.Identity.InstanceID == inst.ID
			}
			if !found {
				snapshot.IncompleteRecorderSessions++
			}
		}
		for _, task := range m.appTasks {
			if task.AppID != id || !rehearsalcommand.IsCommand(task.Command) {
				continue
			}
			if !task.Status.Terminal() {
				continue
			}
			found := false
			for _, r := range p.Recorders {
				found = found || r.Identity.InstanceID == task.ID && r.Status == "closed" && r.CommandResult == task.StdoutTail
			}
			if task.Status != AppTaskSucceeded || task.OutputTruncated || !found {
				snapshot.IncompleteRecorderSessions++
			}
		}
	}
}

func addScenarioRecorderSnapshot(ctx context.Context, tx pgx.Tx, snapshot *api.ScenarioActionSnapshot, accountID string, closing bool) error {
	if snapshot.SimulationVersion == 0 {
		return nil
	}
	q := sqlc.New()
	if closing {
		pending, err := q.PendingScenarioCommands(ctx, tx, sqlc.PendingScenarioCommandsParams{AccountID: accountID, RunID: snapshot.RunID})
		if err != nil {
			return err
		}
		closing = pending == 0
	}
	if closing {
		if err := q.CloseScenarioRecorderAdmission(ctx, tx, sqlc.CloseScenarioRecorderAdmissionParams{AccountID: accountID, RunID: snapshot.RunID}); err != nil {
			return err
		}
	}
	counts, err := q.ScenarioRecorderSnapshot(ctx, tx, sqlc.ScenarioRecorderSnapshotParams{AccountID: accountID, RunID: snapshot.RunID})
	if err != nil {
		return err
	}
	snapshot.RecorderVersion, snapshot.RecorderSessions, snapshot.PendingRecorderSessions, snapshot.IncompleteRecorderSessions = rehearsalrecorder.Version, counts.Sessions, counts.Pending, counts.Incomplete
	return nil
}
