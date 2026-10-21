// adr: 20261017201006
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type ScenarioSimulationStore interface {
	BeginScenarioSimulation(context.Context, api.ScenarioSimulationRequest) (api.ScenarioSimulationDecision, error)
	FinishScenarioSimulation(context.Context, api.ScenarioSimulationRequest, string, string) error
}

func scenarioSimulationJSON(text string) []byte {
	if text == "" {
		return nil
	}
	return []byte(text)
}

func scenarioSimulationText(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	plan, err := api.DecodeScenarioSimulationPlan(string(body))
	if err != nil {
		return "invalid"
	}
	names := make([]string, 0, len(plan.Fixtures))
	for _, f := range plan.Fixtures {
		names = append(names, f.Workload)
	}
	normalized, _, err := api.NormalizeScenarioSimulationPlan(plan, names)
	if err != nil {
		return "invalid"
	}
	return string(normalized)
}

func simulationOrdinal(decision api.ScenarioSimulationDecision, appID string, attempts []simulationStoredAttempt) int {
	n := 1
	for _, row := range attempts {
		a := row.attempt
		if canonicalMemUUID(row.appID) == canonicalMemUUID(appID) && a.Fixture == decision.Attempt.Fixture && a.IdempotencySHA256 == decision.Attempt.IdempotencySHA256 {
			n++
		}
	}
	return n
}

type simulationStoredAttempt struct {
	appID   string
	attempt api.ScenarioSimulationAttempt
}

func (s *PgStore) BeginScenarioSimulation(ctx context.Context, req api.ScenarioSimulationRequest) (api.ScenarioSimulationDecision, error) {
	var empty api.ScenarioSimulationDecision
	if !scenarioTestRunPattern.MatchString(req.RunID) || req.AccountID == "" || req.AppID == "" || req.InstanceID == "" {
		return empty, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return empty, fmt.Errorf("begin simulation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err = q.LockScenarioActionRun(ctx, tx, sqlc.LockScenarioActionRunParams{AccountID: req.AccountID, RunID: req.RunID}); err != nil {
		return empty, err
	}
	if _, err = q.LockScenarioActionApp(ctx, tx, sqlc.LockScenarioActionAppParams{AccountID: req.AccountID, AppID: req.AppID}); err != nil {
		return empty, mapErr(err)
	}
	policy, err := q.ScenarioSimulationPolicy(ctx, tx, sqlc.ScenarioSimulationPolicyParams{AccountID: req.AccountID, AppID: req.AppID, InstanceID: req.InstanceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, err
	}
	if policy.RunID != req.RunID || !policy.Active.Valid || !policy.Active.Bool {
		return empty, ErrConflict
	}
	available, err := q.ScenarioRecorderRequestAvailable(ctx, tx, sqlc.ScenarioRecorderRequestAvailableParams{ID: req.RecorderRequestID, AccountID: req.AccountID, AppID: req.AppID, InstanceID: req.InstanceID, RunID: req.RunID})
	if err != nil {
		return empty, err
	}
	if !available {
		return empty, ErrConflict
	}
	plan, err := api.DecodeScenarioSimulationPlan(string(policy.SimulationPlan))
	if err != nil {
		return empty, err
	}
	rows, err := q.ScenarioSimulationRunAttempts(ctx, tx, sqlc.ScenarioSimulationRunAttemptsParams{AccountID: req.AccountID, RunID: req.RunID})
	if err != nil {
		return empty, err
	}
	prior := make([]simulationStoredAttempt, 0, len(rows))
	totalBytes := 0
	for _, row := range rows {
		var a api.ScenarioSimulationAttempt
		if err = json.Unmarshal(row.Evidence, &a); err != nil {
			return empty, err
		}
		prior = append(prior, simulationStoredAttempt{row.AppID, a})
		totalBytes += len(row.Evidence)
	}
	id := uuid.NewString()
	d := api.PrepareScenarioSimulation(plan, req, policy.WorkloadName, id, 1)
	d = api.PrepareScenarioSimulation(plan, req, policy.WorkloadName, id, simulationOrdinal(d, req.AppID, prior))
	body, err := json.Marshal(d.Attempt)
	if err != nil {
		return empty, err
	}
	if len(rows) >= api.MaxScenarioSimulationAttempts || totalBytes+len(body) > api.MaxScenarioActionPayloadBytes {
		if err = q.FailScenarioSimulation(ctx, tx, req.AppID); err != nil {
			return empty, err
		}
		if err = tx.Commit(ctx); err != nil {
			return empty, err
		}
		return empty, ErrConflict
	}
	if err = q.InsertScenarioSimulationAttempt(ctx, tx, sqlc.InsertScenarioSimulationAttemptParams{ID: id, AppID: req.AppID, Evidence: body}); err != nil {
		return empty, err
	}
	recorderStatus := "admitted"
	if d.Attempt.Status != "pending" {
		recorderStatus = "failed"
	}
	linked, err := q.AdmitScenarioRecorderRequest(ctx, tx, sqlc.AdmitScenarioRecorderRequestParams{ID: req.RecorderRequestID, AttemptID: id, Status: recorderStatus})
	if err != nil {
		return empty, err
	}
	if linked != 1 {
		return empty, ErrConflict
	}
	if d.Attempt.Status == "pending" {
		if err = armScenarioPostgresFault(ctx, tx, req, &d); err != nil {
			return empty, err
		}
		if err = armScenarioWorkerFault(ctx, tx, req, &d); err != nil {
			return empty, err
		}
		if err = armScenarioSuspendCheckpoint(ctx, tx, req, &d); err != nil {
			return empty, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	return d, nil
}

func (s *PgStore) FinishScenarioSimulation(ctx context.Context, req api.ScenarioSimulationRequest, id, status string) error {
	if status != "completed" && status != "interrupted" {
		return ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err = q.LockScenarioActionRun(ctx, tx, sqlc.LockScenarioActionRunParams{AccountID: req.AccountID, RunID: req.RunID}); err != nil {
		return err
	}
	if _, err = q.LockScenarioActionApp(ctx, tx, sqlc.LockScenarioActionAppParams{AccountID: req.AccountID, AppID: req.AppID}); err != nil {
		return mapErr(err)
	}
	policy, err := q.ScenarioSimulationPolicy(ctx, tx, sqlc.ScenarioSimulationPolicyParams{AccountID: req.AccountID, AppID: req.AppID, InstanceID: req.InstanceID})
	if err != nil {
		return mapErr(err)
	}
	if policy.RunID != req.RunID || policy.AccountID != req.AccountID || policy.SealedAt.Valid {
		return ErrConflict
	}
	owned, err := q.ScenarioRecorderAttemptOwner(ctx, tx, sqlc.ScenarioRecorderAttemptOwnerParams{ID: req.RecorderRequestID, AttemptID: id, AccountID: req.AccountID, AppID: req.AppID, InstanceID: req.InstanceID, RunID: req.RunID})
	if err != nil {
		return err
	}
	if !owned {
		return ErrConflict
	}
	n, err := q.FinishScenarioSimulationAttempt(ctx, tx, sqlc.FinishScenarioSimulationAttemptParams{ID: id, AppID: req.AppID, Status: status})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	recorderStatus := "completed"
	if status != "completed" {
		recorderStatus = "failed"
	}
	n, err = q.FinishScenarioRecorderRequest(ctx, tx, sqlc.FinishScenarioRecorderRequestParams{ID: req.RecorderRequestID, AttemptID: id, Status: recorderStatus})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	if status == "interrupted" {
		if err = interruptScenarioPostgresFault(ctx, tx, req, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (m *MemStore) BeginScenarioSimulation(ctx context.Context, req api.ScenarioSimulationRequest) (api.ScenarioSimulationDecision, error) {
	var empty api.ScenarioSimulationDecision
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.scenarioActionPolicyLocked(req.AppID)
	member, active := m.scenarioTestMembers[p.Member.AppID]
	identity := rehearsalrecorder.Identity{AccountID: req.AccountID, AppID: req.AppID, RunID: req.RunID, InstanceID: req.InstanceID}
	if !ok || !active || member.RunID != req.RunID || p.Member.AccountID != req.AccountID || p.Member.SimulationJSON == "" || !p.SealedAt.IsZero() || p.SimulationFailed || !eligibleScenarioTestApp(m.apps[p.Member.AppID], req.AccountID) || !m.rehearsalExecutionLocked(identity) {
		return empty, ErrConflict
	}
	plan, err := api.DecodeScenarioSimulationPlan(p.Member.SimulationJSON)
	if err != nil {
		return empty, err
	}
	recorder, request, found := m.recorderRequestLocked(p, req)
	if !found || recorder.Status != "recording" || request.Status != "pending" || !m.rehearsalExecutionLocked(recorder.Identity) {
		return empty, ErrConflict
	}
	prior := []simulationStoredAttempt{}
	totalBytes := 0
	for appID, policy := range m.scenarioActionPolicies {
		if policy.Member.AccountID != req.AccountID || policy.Member.RunID != req.RunID {
			continue
		}
		for _, a := range policy.SimulationAttempts {
			prior = append(prior, simulationStoredAttempt{appID, a})
			body, _ := json.Marshal(a)
			totalBytes += len(body)
		}
	}
	id := uuid.NewString()
	d := api.PrepareScenarioSimulation(plan, req, p.Member.Workload, id, 1)
	d = api.PrepareScenarioSimulation(plan, req, p.Member.Workload, id, simulationOrdinal(d, req.AppID, prior))
	body, _ := json.Marshal(d.Attempt)
	if len(prior) >= api.MaxScenarioSimulationAttempts || totalBytes+len(body) > api.MaxScenarioActionPayloadBytes {
		p.SimulationFailed = true
		m.scenarioActionPolicies[p.Member.AppID] = p
		return empty, ErrConflict
	}
	p.SimulationAttempts = append(p.SimulationAttempts, d.Attempt)
	request.AttemptID, request.Status = id, "admitted"
	if d.Attempt.Status != "pending" {
		request.Status = "failed"
	}
	recorder.Requests[req.RecorderRequestID] = request
	if err := m.armScenarioPostgresFaultLocked(p, req, &d); err != nil {
		return empty, err
	}
	if err := m.armScenarioWorkerFaultLocked(p, req, &d); err != nil {
		return empty, err
	}
	if err := m.armScenarioSuspendCheckpointLocked(p, req, &d); err != nil {
		return empty, err
	}
	m.scenarioActionPolicies[p.Member.AppID] = p
	// Do not expose storage-owned JSON to callers.
	d.Attempt.Payload = append(json.RawMessage(nil), d.Attempt.Payload...)
	return d, nil
}

func simulationLiveState(state string) bool {
	switch state {
	case string(StateWaking), string(StateColdBooting), string(StateRunning), string(StateDraining):
		return true
	}
	return false
}

func (m *MemStore) FinishScenarioSimulation(ctx context.Context, req api.ScenarioSimulationRequest, id, status string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if status != "completed" && status != "interrupted" {
		return ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.scenarioActionPolicyLocked(req.AppID)
	if !ok || p.Member.RunID != req.RunID || p.Member.AccountID != req.AccountID || !p.SealedAt.IsZero() {
		return ErrConflict
	}
	recorder, request, found := m.recorderRequestLocked(p, req)
	if !found || request.Status != "admitted" || request.AttemptID != id {
		return ErrConflict
	}
	for i, a := range p.SimulationAttempts {
		if a.ID == id && a.Status == "pending" {
			p.SimulationAttempts[i].Status = status
			request.Status = "completed"
			if status != "completed" {
				request.Status = "failed"
			}
			recorder.Requests[req.RecorderRequestID] = request
			m.scenarioActionPolicies[p.Member.AppID] = p
			if status == "interrupted" {
				m.interruptScenarioPostgresFaultLocked(req, id)
			}
			return nil
		}
	}
	return ErrNotFound
}

func applyScenarioSimulationSnapshot(snapshot *api.ScenarioActionSnapshot, planJSON string, failed bool, attempts []api.ScenarioSimulationAttempt) {
	if planJSON == "" {
		return
	}
	snapshot.Version, snapshot.Scope = api.ScenarioSimulationSnapshotVersion, api.ScenarioSimulationScope
	snapshot.SimulationVersion = api.ScenarioSimulationVersion
	snapshot.SimulationSHA256 = api.ScenarioSimulationDigest([]byte(planJSON))
	snapshot.SimulatedAttempts = make([]api.ScenarioSimulationAttempt, len(attempts))
	for i, a := range attempts {
		a.Payload = append(json.RawMessage(nil), a.Payload...)
		snapshot.SimulatedAttempts[i] = a
	}
	if failed {
		snapshot.IncompleteSimulationAttempts++
	}
	for _, a := range attempts {
		if a.Status == "pending" {
			snapshot.PendingSimulationAttempts++
		} else if a.Status != "completed" {
			snapshot.IncompleteSimulationAttempts++
		}
	}
	if len(attempts) > api.MaxScenarioSimulationAttempts {
		snapshot.IncompleteSimulationAttempts++
		snapshot.Truncated = true
		snapshot.SimulatedAttempts = nil
	}
}

func (s *PgStore) ScenarioRehearsalSimulationRun(ctx context.Context, appID string) (string, error) {
	run, err := sqlc.New().ScenarioRehearsalSimulationRun(ctx, s.pool, appID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return run, err
}

func (m *MemStore) ScenarioRehearsalSimulationRun(ctx context.Context, appID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.scenarioActionPolicyLocked(appID)
	if !ok || p.Member.SimulationJSON == "" {
		return "", nil
	}
	return p.Member.RunID, nil
}

func (s *PgStore) addScenarioSimulationSnapshot(ctx context.Context, tx sqlc.DBTX, snapshot *api.ScenarioActionSnapshot, accountID string, policies []sqlc.ScenarioActionPoliciesForRunRow) error {
	planJSON := ""
	failed := false
	for _, p := range policies {
		text := scenarioSimulationText(p.SimulationPlan)
		if text != "" {
			if planJSON != "" && planJSON != text {
				failed = true
			}
			planJSON = text
		}
		failed = failed || p.SimulationFailed || text == "invalid"
	}
	if planJSON == "" {
		return nil
	}
	for _, p := range policies {
		if len(p.SimulationPlan) == 0 {
			failed = true
		}
	}
	rows, err := sqlc.New().ScenarioSimulationRunAttempts(ctx, tx, sqlc.ScenarioSimulationRunAttemptsParams{AccountID: accountID, RunID: snapshot.RunID})
	if err != nil {
		return err
	}
	attempts := make([]api.ScenarioSimulationAttempt, 0, len(rows))
	for _, row := range rows {
		var a api.ScenarioSimulationAttempt
		if err := json.Unmarshal(row.Evidence, &a); err != nil {
			return err
		}
		attempts = append(attempts, a)
	}
	applyScenarioSimulationSnapshot(snapshot, planJSON, failed, attempts)
	return nil
}
