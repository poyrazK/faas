package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/rehearsalobj"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	objsql "github.com/onebox-faas/faas/pkg/state/scenarioobj/sqlc"
)

func scenarioObjectDocument(body []byte, err error) (rehearsalobj.Resource, error) {
	var r rehearsalobj.Resource
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if json.Unmarshal(body, &r) != nil {
		return r, ErrConflict
	}
	return r, nil
}
func (s *PgStore) ReserveScenarioObjects(ctx context.Context, r rehearsalobj.Resource) (rehearsalobj.Resource, error) {
	if !r.ValidReservation() {
		return r, ErrConflict
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return r, e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := objsql.New()
	if e = q.LockRun(ctx, tx, objsql.LockRunParams{AccountID: r.AccountID, RunID: r.RunID}); e != nil {
		return r, e
	}
	b, _ := json.Marshal(r)
	out, e := scenarioObjectDocument(q.Reserve(ctx, tx, objsql.ReserveParams{ID: scenarioPGUUID(r.ID), AccountID: scenarioPGUUID(r.AccountID), RunID: r.RunID, AppID: scenarioPGUUID(r.AppID), ExpiresAt: pgtype.Timestamptz{Time: r.ExpiresAt, Valid: true}, Document: b}))
	if errors.Is(e, ErrNotFound) {
		e = ErrConflict
	}
	if e != nil {
		return r, e
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) GetScenarioObjects(ctx context.Context, account, run string) (rehearsalobj.Resource, error) {
	return scenarioObjectDocument(objsql.New().Get(ctx, s.pool, objsql.GetParams{AccountID: scenarioPGUUID(account), RunID: run}))
}
func (s *PgStore) ScenarioObjectsForApp(ctx context.Context, app string) (rehearsalobj.Resource, error) {
	return scenarioObjectCaller(objsql.New().GetForApp(ctx, s.pool, scenarioPGUUID(app)))
}
func (s *PgStore) UpdateScenarioObjects(ctx context.Context, account, run, admissionApp string, update func(*rehearsalobj.Resource) error) (rehearsalobj.Resource, error) {
	var r rehearsalobj.Resource
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return r, e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := objsql.New()
	// Share the action closing barrier. The later read sees a fresh statement
	// snapshot after a concurrent action sealer releases the advisory lock.
	if admissionApp != "" {
		if e = q.LockRun(ctx, tx, objsql.LockRunParams{AccountID: account, RunID: run}); e != nil {
			return r, e
		}
	}
	r, e = scenarioObjectDocument(q.GetLocked(ctx, tx, objsql.GetLockedParams{AccountID: scenarioPGUUID(account), RunID: run}))
	if e != nil {
		return r, e
	}
	if admissionApp != "" {
		active, err := scenarioObjectCaller(q.GetForApp(ctx, tx, scenarioPGUUID(admissionApp)))
		if err != nil || active.ID != r.ID {
			return r, ErrConflict
		}
	}
	old := rehearsalobj.Clone(r)
	if e = update(&r); e != nil {
		return old, e
	}
	if e = rehearsalobj.ValidateUpdate(old, r); e != nil {
		return old, e
	}
	b, _ := json.Marshal(r)
	if e = q.Update(ctx, tx, objsql.UpdateParams{ID: scenarioPGUUID(r.ID), Status: r.Status, Document: b}); e != nil {
		return old, e
	}
	return r, tx.Commit(ctx)
}
func (s *PgStore) ExpiredScenarioObjects(ctx context.Context) ([]rehearsalobj.Resource, error) {
	rows, e := objsql.New().Expired(ctx, s.pool)
	if e != nil {
		return nil, e
	}
	out := make([]rehearsalobj.Resource, 0, len(rows))
	for _, b := range rows {
		r, e := scenarioObjectDocument(b, nil)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, nil
}

func (m *MemStore) eligibleScenarioObjectsLocked(app, account, run string) bool {
	p, ok := m.scenarioActionPolicyLocked(app)
	a, found := m.apps[app]
	return ok && found && eligibleScenarioTestApp(a, account) && p.Member.AccountID == account && p.Member.RunID == run && p.Member.NetworkMode == "contained" && p.Member.SimulationJSON != "" && !p.RecorderClosing && !p.SimulationFailed && p.SealedAt.IsZero()
}
func (m *MemStore) ReserveScenarioObjects(ctx context.Context, r rehearsalobj.Resource) (rehearsalobj.Resource, error) {
	if e := ctx.Err(); e != nil {
		return r, e
	}
	if !r.ValidReservation() {
		return r, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.eligibleScenarioObjectsLocked(r.AppID, r.AccountID, r.RunID) {
		return r, ErrConflict
	}
	for _, d := range m.deployments {
		if d.AppID == r.AppID {
			return r, ErrConflict
		}
	}
	if m.scenarioObjects == nil {
		m.scenarioObjects = map[string]rehearsalobj.Resource{}
	}
	key := r.AccountID + ":" + r.RunID
	if _, ok := m.scenarioObjects[key]; ok {
		return r, ErrConflict
	}
	p := m.scenarioActionPolicies[r.AppID]
	if p.Member.ManagedWorkJSON != "" {
		plan := rehearsalwork.Decode(p.Member.ManagedWorkJSON)
		part, _ := plan.Participant(p.Member.Workload)
		if plan.Primary != p.Member.Workload || part.Objects != "read_write" {
			return r, ErrConflict
		}
	}
	m.scenarioObjects[key] = rehearsalobj.Clone(r)
	return rehearsalobj.Clone(r), nil
}
func (m *MemStore) GetScenarioObjects(ctx context.Context, account, run string) (rehearsalobj.Resource, error) {
	if e := ctx.Err(); e != nil {
		return rehearsalobj.Resource{}, e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.scenarioObjects[account+":"+run]
	if !ok {
		return r, ErrNotFound
	}
	return rehearsalobj.Clone(r), nil
}
func (m *MemStore) ScenarioObjectsForApp(ctx context.Context, app string) (rehearsalobj.Resource, error) {
	if e := ctx.Err(); e != nil {
		return rehearsalobj.Resource{}, e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.scenarioObjects {
		if m.scenarioObjectPermissionLocked(r, app) != "" && r.Status == "ready" && r.CollectorID != "" && !r.CollectorClosed && r.ExpiresAt.After(time.Now()) && m.eligibleScenarioObjectsLocked(app, r.AccountID, r.RunID) {
			out := rehearsalobj.Clone(r)
			out.CallerPermission = m.scenarioObjectPermissionLocked(r, app)
			p := m.scenarioActionPolicies[app]
			if p.Member.ManagedWorkJSON != "" {
				out.CallerWorkload = p.Member.Workload
				out.CallerDeploymentID, _ = m.managedDeploymentLocked(app)
			}
			return out, nil
		}
	}
	return rehearsalobj.Resource{}, ErrNotFound
}
func (m *MemStore) UpdateScenarioObjects(ctx context.Context, account, run, admissionApp string, update func(*rehearsalobj.Resource) error) (rehearsalobj.Resource, error) {
	if e := ctx.Err(); e != nil {
		return rehearsalobj.Resource{}, e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := account + ":" + run
	old, ok := m.scenarioObjects[key]
	if !ok {
		return old, ErrNotFound
	}
	if admissionApp != "" && (m.scenarioObjectPermissionLocked(old, admissionApp) == "" || old.Status != "ready" || old.CollectorID == "" || old.CollectorClosed || !old.ExpiresAt.After(time.Now()) || !m.eligibleScenarioObjectsLocked(admissionApp, account, run)) {
		return old, ErrConflict
	}
	r := rehearsalobj.Clone(old)
	if e := update(&r); e != nil {
		return old, e
	}
	if e := rehearsalobj.ValidateUpdate(old, r); e != nil {
		return old, e
	}
	m.scenarioObjects[key] = rehearsalobj.Clone(r)
	return r, nil
}
func (m *MemStore) ExpiredScenarioObjects(ctx context.Context) ([]rehearsalobj.Resource, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []rehearsalobj.Resource{}
	for _, r := range m.scenarioObjects {
		if (r.Status == "ready" || r.Status == "sealing") && r.ExpiresAt.Before(time.Now()) && len(out) < 16 {
			out = append(out, rehearsalobj.Clone(r))
		}
	}
	return out, nil
}

func (s *PgStore) ActiveScenarioObjects(ctx context.Context, after string) ([]rehearsalobj.Resource, error) {
	rows, e := objsql.New().Active(ctx, s.pool, scenarioPGUUID(after))
	if e != nil {
		return nil, e
	}
	out := make([]rehearsalobj.Resource, 0, len(rows))
	for _, b := range rows {
		r, e := scenarioObjectDocument(b, nil)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, nil
}
func (m *MemStore) ActiveScenarioObjects(ctx context.Context, after string) ([]rehearsalobj.Resource, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []rehearsalobj.Resource{}
	for _, r := range m.scenarioObjects {
		if r.ID > after && (r.Status == "ready" || r.Status == "sealing") && !r.CollectorClosed && r.ExpiresAt.After(time.Now()) {
			out = append(out, rehearsalobj.Clone(r))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > 16 {
		out = out[:16]
	}
	return out, nil
}
