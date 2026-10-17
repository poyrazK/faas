package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	pgsql "github.com/onebox-faas/faas/pkg/state/scenariopg/sqlc"
)

func scenarioPGUUID(s string) pgtype.UUID {
	u, err := uuid.Parse(s)
	return pgtype.UUID{Bytes: u, Valid: err == nil}
}
func scenarioPGResource(v pgsql.ScenarioPostgresFixture) (rehearsalpg.Resource, error) {
	r := rehearsalpg.Resource{ID: uuid.UUID(v.ID.Bytes).String(), AccountID: uuid.UUID(v.AccountID.Bytes).String(), RunID: v.RunID, AppID: uuid.UUID(v.AppID.Bytes).String(), OwnerID: uuid.UUID(v.OwnerID.Bytes).String(), DatabaseName: v.DatabaseName, RoleName: v.RoleName, Status: v.Status, BackendSHA256: v.BackendSha256, ExpiresAt: v.ExpiresAt.Time}
	if json.Unmarshal(v.Spec, &r.Spec) != nil || json.Unmarshal(v.InitialSnapshot, &r.Initial) != nil {
		return r, ErrConflict
	}
	if string(v.Evidence) != "{}" {
		var e rehearsalpg.Evidence
		if json.Unmarshal(v.Evidence, &e) != nil {
			return r, ErrConflict
		}
		r.Evidence = &e
	}
	return r, nil
}
func (s *PgStore) ReserveScenarioPostgres(ctx context.Context, r rehearsalpg.Resource) (rehearsalpg.Resource, error) {
	if !r.ValidReservation() {
		return r, ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return r, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := pgsql.New()
	if err := q.LockRun(ctx, tx, pgsql.LockRunParams{AccountID: r.AccountID, RunID: r.RunID}); err != nil {
		return r, err
	}
	body, _ := json.Marshal(r.Spec)
	v, err := q.Reserve(ctx, tx, pgsql.ReserveParams{ID: scenarioPGUUID(r.ID), AccountID: scenarioPGUUID(r.AccountID), RunID: r.RunID, AppID: scenarioPGUUID(r.AppID), OwnerID: scenarioPGUUID(r.OwnerID), DatabaseName: r.DatabaseName, RoleName: r.RoleName, BackendSha256: r.BackendSHA256, Spec: body, ExpiresAt: pgtype.Timestamptz{Time: r.ExpiresAt, Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrConflict
	}
	if err != nil {
		return r, err
	}
	if err := tx.Commit(ctx); err != nil {
		return r, err
	}
	return scenarioPGResource(v)
}
func (s *PgStore) GetScenarioPostgres(ctx context.Context, account, run string) (rehearsalpg.Resource, error) {
	v, e := pgsql.New().Get(ctx, s.pool, pgsql.GetParams{AccountID: scenarioPGUUID(account), RunID: run})
	if errors.Is(e, pgx.ErrNoRows) {
		return rehearsalpg.Resource{}, ErrNotFound
	}
	if e != nil {
		return rehearsalpg.Resource{}, e
	}
	return scenarioPGResource(v)
}
func (s *PgStore) ScenarioPostgresForApp(ctx context.Context, app string) (rehearsalpg.Resource, error) {
	v, e := pgsql.New().GetForApp(ctx, s.pool, scenarioPGUUID(app))
	if errors.Is(e, pgx.ErrNoRows) {
		return rehearsalpg.Resource{}, ErrNotFound
	}
	if e != nil {
		return rehearsalpg.Resource{}, e
	}
	r, err := scenarioPGResource(v)
	if err == nil && r.AppID != app {
		r.RoleName = rehearsalwork.PostgresRole(r.ID, app)
	}
	return r, err
}
func (s *PgStore) TransitionScenarioPostgres(ctx context.Context, r rehearsalpg.Resource, expected string) (rehearsalpg.Resource, error) {
	initial, _ := json.Marshal(r.Initial)
	if r.Initial == nil {
		initial = []byte("[]")
	}
	evidence := []byte("{}")
	if r.Evidence != nil {
		evidence, _ = json.Marshal(r.Evidence)
	}
	v, e := pgsql.New().Transition(ctx, s.pool, pgsql.TransitionParams{ID: scenarioPGUUID(r.ID), OwnerID: scenarioPGUUID(r.OwnerID), ExpectedStatus: expected, Status: r.Status, InitialSnapshot: initial, Evidence: evidence})
	if errors.Is(e, pgx.ErrNoRows) {
		return r, ErrConflict
	}
	if e != nil {
		return r, e
	}
	return scenarioPGResource(v)
}
func (s *PgStore) ExpiredScenarioPostgres(ctx context.Context) ([]rehearsalpg.Resource, error) {
	rows, e := pgsql.New().Expired(ctx, s.pool)
	if e != nil {
		return nil, e
	}
	var out []rehearsalpg.Resource
	for _, v := range rows {
		r, e := scenarioPGResource(v)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, nil
}

func (m *MemStore) ReserveScenarioPostgres(ctx context.Context, r rehearsalpg.Resource) (rehearsalpg.Resource, error) {
	if e := ctx.Err(); e != nil {
		return r, e
	}
	if !r.ValidReservation() {
		return r, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.scenarioActionPolicyLocked(r.AppID)
	if !ok || p.Member.AccountID != r.AccountID || p.Member.RunID != r.RunID || p.Member.NetworkMode != "contained" || p.Member.SimulationJSON == "" || p.RecorderClosing || p.SimulationFailed || !p.SealedAt.IsZero() {
		return r, ErrConflict
	}
	a, ok := m.apps[r.AppID]
	if !ok || !eligibleScenarioTestApp(a, r.AccountID) {
		return r, ErrConflict
	}
	for _, d := range m.deployments {
		if d.AppID == r.AppID {
			return r, ErrConflict
		}
	}
	if m.scenarioPostgres == nil {
		m.scenarioPostgres = map[string]rehearsalpg.Resource{}
	}
	key := r.AccountID + ":" + r.RunID
	if _, ok := m.scenarioPostgres[key]; ok {
		return r, ErrConflict
	}
	if p.Member.ManagedWorkJSON != "" {
		plan := rehearsalwork.Decode(p.Member.ManagedWorkJSON)
		part, _ := plan.Participant(p.Member.Workload)
		if plan.Primary != p.Member.Workload || part.Postgres != "read_write" {
			return r, ErrConflict
		}
	}
	r.Status = "provisioning"
	m.scenarioPostgres[key] = rehearsalpg.CloneResource(r)
	return r, nil
}
func (m *MemStore) GetScenarioPostgres(ctx context.Context, account, run string) (rehearsalpg.Resource, error) {
	if e := ctx.Err(); e != nil {
		return rehearsalpg.Resource{}, e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.scenarioPostgres[account+":"+run]
	if !ok {
		return r, ErrNotFound
	}
	return rehearsalpg.CloneResource(r), nil
}
func (m *MemStore) ScenarioPostgresForApp(ctx context.Context, app string) (rehearsalpg.Resource, error) {
	if e := ctx.Err(); e != nil {
		return rehearsalpg.Resource{}, e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.scenarioActionPolicyLocked(app)
	if !ok || p.Member.NetworkMode != "contained" || p.Member.SimulationJSON == "" || p.RecorderClosing || p.SimulationFailed || !p.SealedAt.IsZero() {
		return rehearsalpg.Resource{}, ErrNotFound
	}
	if a, ok := m.apps[app]; !ok || !eligibleScenarioTestApp(a, p.Member.AccountID) {
		return rehearsalpg.Resource{}, ErrNotFound
	}
	r, ok := m.scenarioPostgres[p.Member.AccountID+":"+p.Member.RunID]
	if !ok || r.Status != "ready" || !r.ExpiresAt.After(time.Now()) {
		return rehearsalpg.Resource{}, ErrNotFound
	}
	if p.Member.ManagedWorkJSON == "" {
		if r.AppID != app {
			return rehearsalpg.Resource{}, ErrNotFound
		}
	} else {
		part, _ := rehearsalwork.Decode(p.Member.ManagedWorkJSON).Participant(p.Member.Workload)
		if part.Postgres != "read_write" {
			return rehearsalpg.Resource{}, ErrNotFound
		}
		if r.AppID != app {
			r.RoleName = rehearsalwork.PostgresRole(r.ID, app)
		}
	}
	return rehearsalpg.CloneResource(r), nil
}
func (m *MemStore) TransitionScenarioPostgres(ctx context.Context, r rehearsalpg.Resource, expected string) (rehearsalpg.Resource, error) {
	if e := ctx.Err(); e != nil {
		return r, e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := r.AccountID + ":" + r.RunID
	old, ok := m.scenarioPostgres[key]
	if !ok || old.ID != r.ID || old.OwnerID != r.OwnerID || old.Status != expected {
		return r, ErrConflict
	}
	allowed := map[string][]string{"provisioning": {"ready", "failed", "deleted"}, "ready": {"sealing", "failed", "deleted"}, "sealing": {"complete", "failed", "deleted"}, "failed": {"deleted"}}
	valid := false
	for _, s := range allowed[expected] {
		if s == r.Status {
			valid = true
		}
	}
	if !valid {
		return r, ErrConflict
	}
	old.Status = r.Status
	if r.Status == "ready" {
		old.Initial = r.Initial
	}
	if r.Status == "complete" {
		old.Evidence = r.Evidence
	}
	m.scenarioPostgres[key] = rehearsalpg.CloneResource(old)
	return rehearsalpg.CloneResource(old), nil
}
func (m *MemStore) ExpiredScenarioPostgres(ctx context.Context) ([]rehearsalpg.Resource, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []rehearsalpg.Resource
	for _, r := range m.scenarioPostgres {
		if r.Status != "complete" && r.Status != "deleted" && r.ExpiresAt.Before(time.Now()) {
			out = append(out, rehearsalpg.CloneResource(r))
		}
	}
	return out, nil
}
