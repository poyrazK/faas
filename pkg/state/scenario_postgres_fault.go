// adr: 20261018000200
package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalfault"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	faultsql "github.com/onebox-faas/faas/pkg/state/scenariopgfault/sqlc"
)

func armScenarioPostgresFault(ctx context.Context, tx pgx.Tx, req api.ScenarioSimulationRequest, d *api.ScenarioSimulationDecision) error {
	q := faultsql.New()
	p, err := q.Enrollment(ctx, tx, req.AppID)
	if err != nil {
		return mapErr(err)
	}
	plan := rehearsalwork.Decode(string(p.ManagedWorkPlan))
	names := []string{}
	for _, v := range plan.Participants {
		names = append(names, v.Workload)
	}
	_, planHash, hashErr := rehearsalwork.Normalize(plan, names)
	f := plan.PostgresFault
	if f == nil || f.Workload != p.WorkloadName || f.Checkpoint != d.Attempt.Fixture {
		return nil
	}
	if hashErr != nil {
		return ErrConflict
	}
	if d.Attempt.Attempt != 1 {
		return ErrConflict
	}
	c, err := q.Checkpoint(ctx, tx, faultsql.CheckpointParams{AppID: req.AppID, InstanceID: req.InstanceID, AttemptID: d.Attempt.ID, RequestID: req.RecorderRequestID})
	if err != nil {
		return mapErr(err)
	}
	now := time.Now().UTC()
	r := rehearsalfault.Row{Receipt: rehearsalfault.Receipt{ID: uuid.NewString(), AccountID: req.AccountID, RunID: req.RunID, AppID: req.AppID,
		ResourceID: c.ResourceID, OwnerID: c.OwnerID, BackendSHA256: c.BackendSha256, PlanSHA256: planHash,
		DeploymentID: c.DeploymentID, InstanceID: req.InstanceID, RecorderSessionID: c.RecorderSessionID, InvocationID: c.InvocationID,
		CheckpointAttemptID: d.Attempt.ID, Spec: *f, State: "requested"}, CreatedAt: now, DeadlineAt: now.Add(30 * time.Second)}
	b, _ := json.Marshal(r)
	err = q.Insert(ctx, tx, faultsql.InsertParams{ID: r.ID, ResourceID: r.ResourceID, AccountID: r.AccountID, RunID: r.RunID, AppID: r.AppID, OwnerID: r.OwnerID, Receipt: b})
	if err == nil {
		d.PostgresFaultID = r.ID
	}
	return mapErr(err)
}

func postgresFaultRow(b []byte) (rehearsalfault.Row, error) {
	var r rehearsalfault.Row
	if json.Unmarshal(b, &r) != nil || r.ID == "" {
		return r, ErrConflict
	}
	return r, nil
}
func (s *PgStore) ScenarioPostgresFault(ctx context.Context, id string) (rehearsalfault.Row, error) {
	b, err := faultsql.New().Get(ctx, s.pool, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return rehearsalfault.Row{}, rehearsalfault.ErrNotFound
		}
		return rehearsalfault.Row{}, err
	}
	return postgresFaultRow(b)
}
func (s *PgStore) PendingScenarioPostgresFaults(ctx context.Context) ([]rehearsalfault.Row, error) {
	rows, err := faultsql.New().Pending(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	out := make([]rehearsalfault.Row, 0, len(rows))
	for _, b := range rows {
		r, err := postgresFaultRow(b)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
func (s *PgStore) ScenarioPostgresFaultForRun(ctx context.Context, account, run string) (rehearsalfault.Row, error) {
	b, err := faultsql.New().ForRun(ctx, s.pool, faultsql.ForRunParams{AccountID: account, RunID: run})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return rehearsalfault.Row{}, rehearsalfault.ErrNotFound
		}
		return rehearsalfault.Row{}, err
	}
	return postgresFaultRow(b)
}
func (s *PgStore) TransitionScenarioPostgresFault(ctx context.Context, r rehearsalfault.Row, expected string) error {
	old, err := s.ScenarioPostgresFault(ctx, r.ID)
	if err != nil {
		return err
	}
	if old.State != expected || !rehearsalfault.Transition(old, r) {
		return ErrConflict
	}
	b, _ := json.Marshal(r)
	n, err := faultsql.New().Transition(ctx, s.pool, faultsql.TransitionParams{ID: r.ID, OwnerID: r.OwnerID, Expected: expected, Phase: r.State, Receipt: b})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (m *MemStore) armScenarioPostgresFaultLocked(p scenarioActionPolicy, req api.ScenarioSimulationRequest, d *api.ScenarioSimulationDecision) error {
	plan := rehearsalwork.Decode(p.Member.ManagedWorkJSON)
	f := plan.PostgresFault
	if f == nil || f.Workload != p.Member.Workload || f.Checkpoint != d.Attempt.Fixture || d.Attempt.Status != "pending" {
		return nil
	}
	if d.Attempt.Attempt != 1 {
		return ErrConflict
	}
	for _, r := range m.scenarioPostgresFaults {
		if r.AccountID == req.AccountID && r.RunID == req.RunID {
			return ErrConflict
		}
	}
	resource, ok := m.scenarioPostgres[req.AccountID+":"+req.RunID]
	if !ok || resource.Status != "ready" || !resource.ExpiresAt.After(time.Now()) {
		return ErrConflict
	}
	var job Invocation
	count := 0
	for _, j := range m.invocations {
		w := m.scenarioActionPolicies[j.AppID].Member
		if w.AccountID == req.AccountID && w.RunID == req.RunID {
			job = j
			count++
		}
	}
	rc, _, found := m.recorderRequestLocked(p, req)
	if !found || count != 1 || job.AppID != req.AppID || job.InstanceID != req.InstanceID || job.State != InvocationDispatching || job.Source != InvocationAsyncInvoke || job.Attempts != 1 || job.ReplayGeneration != 0 || job.LeaseExpiresAt == nil || !job.LeaseExpiresAt.After(time.Now()) || m.instances[req.InstanceID].State != "running" {
		return ErrConflict
	}
	_, deployments := m.managedDeploymentLocked(req.AppID)
	if deployments != 1 {
		return ErrConflict
	}
	names := []string{}
	for _, v := range plan.Participants {
		names = append(names, v.Workload)
	}
	_, hash, err := rehearsalwork.Normalize(plan, names)
	if err != nil {
		return ErrConflict
	}
	now := time.Now().UTC()
	r := rehearsalfault.Row{Receipt: rehearsalfault.Receipt{ID: uuid.NewString(), AccountID: req.AccountID, RunID: req.RunID, AppID: req.AppID,
		ResourceID: resource.ID, OwnerID: resource.OwnerID, BackendSHA256: resource.BackendSHA256, PlanSHA256: hash, DeploymentID: rc.Identity.DeploymentID,
		InstanceID: req.InstanceID, RecorderSessionID: rc.ID, InvocationID: job.ID, CheckpointAttemptID: d.Attempt.ID, Spec: *f, State: "requested"}, CreatedAt: now, DeadlineAt: now.Add(30 * time.Second)}
	if m.scenarioPostgresFaults == nil {
		m.scenarioPostgresFaults = map[string]rehearsalfault.Row{}
	}
	m.scenarioPostgresFaults[r.ID] = r
	d.PostgresFaultID = r.ID
	return nil
}
func (m *MemStore) ScenarioPostgresFault(ctx context.Context, id string) (rehearsalfault.Row, error) {
	if err := ctx.Err(); err != nil {
		return rehearsalfault.Row{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.scenarioPostgresFaults[id]
	if !ok {
		return r, rehearsalfault.ErrNotFound
	}
	return rehearsalfault.Clone(r), nil
}
func (m *MemStore) PendingScenarioPostgresFaults(ctx context.Context) ([]rehearsalfault.Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []rehearsalfault.Row{}
	for _, r := range m.scenarioPostgresFaults {
		if r.State != "recovered" && (r.State != "failed" || !r.Recovered) {
			out = append(out, rehearsalfault.Clone(r))
		}
	}
	return out, nil
}
func (m *MemStore) ScenarioPostgresFaultForRun(ctx context.Context, account, run string) (rehearsalfault.Row, error) {
	if err := ctx.Err(); err != nil {
		return rehearsalfault.Row{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.scenarioPostgresFaults {
		if r.AccountID == account && r.RunID == run {
			return rehearsalfault.Clone(r), nil
		}
	}
	return rehearsalfault.Row{}, rehearsalfault.ErrNotFound
}
func (m *MemStore) TransitionScenarioPostgresFault(ctx context.Context, r rehearsalfault.Row, expected string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.scenarioPostgresFaults[r.ID]
	if !ok || old.State != expected || !rehearsalfault.Transition(old, r) {
		return ErrConflict
	}
	m.scenarioPostgresFaults[r.ID] = rehearsalfault.Clone(r)
	return nil
}

func (m *MemStore) checkScenarioPostgresFaultDispatchLocked(app, instance string) error {
	for _, f := range m.scenarioPostgresFaults {
		if f.AppID == app && (f.State == "requested" || f.State == "cutting" || instance != "" && instance != f.InstanceID) {
			return ErrConflict
		}
	}
	return nil
}

func addScenarioPostgresFaultSnapshot(ctx context.Context, tx pgx.Tx, e *rehearsalwork.Evidence, account string) error {
	if e.Plan.PostgresFault == nil {
		return nil
	}
	b, err := faultsql.New().ForRun(ctx, tx, faultsql.ForRunParams{AccountID: account, RunID: e.RunID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	r, err := postgresFaultRow(b)
	if err == nil {
		e.PostgresFault = &r.Receipt
	}
	return err
}

func interruptedPostgresFault(r rehearsalfault.Row) rehearsalfault.Row {
	if r.Error != "" || r.State == "recovering" || r.State == "failed" {
		return r
	}
	r.Error = "worker checkpoint was interrupted"
	if r.State == "recovered" {
		r.State = "failed"
	} else {
		r.State = "recovering"
	}
	return r
}
func interruptScenarioPostgresFault(ctx context.Context, tx pgx.Tx, req api.ScenarioSimulationRequest, id string) error {
	q := faultsql.New()
	p, err := q.Enrollment(ctx, tx, req.AppID)
	if err != nil {
		return err
	}
	if rehearsalwork.Decode(string(p.ManagedWorkPlan)).PostgresFault == nil {
		return nil
	}
	b, err := q.ForRun(ctx, tx, faultsql.ForRunParams{AccountID: req.AccountID, RunID: req.RunID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	r, err := postgresFaultRow(b)
	if err != nil {
		return err
	}
	if r.CheckpointAttemptID != id {
		return nil
	}
	next := interruptedPostgresFault(r)
	if next.State == r.State {
		return nil
	}
	body, _ := json.Marshal(next)
	n, err := q.Transition(ctx, tx, faultsql.TransitionParams{ID: r.ID, OwnerID: r.OwnerID, Expected: r.State, Phase: next.State, Receipt: body})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func (m *MemStore) interruptScenarioPostgresFaultLocked(req api.ScenarioSimulationRequest, id string) {
	for key, r := range m.scenarioPostgresFaults {
		if r.AccountID == req.AccountID && r.RunID == req.RunID && r.CheckpointAttemptID == id {
			m.scenarioPostgresFaults[key] = interruptedPostgresFault(r)
		}
	}
}
