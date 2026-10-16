package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EnvironmentCachePurgeStore interface {
	CreateEnvironmentCachePurge(context.Context, string, string, string, api.CreateEnvironmentCachePurgeRequest) (api.EnvironmentCachePurgeReceipt, error)
	GetEnvironmentCachePurge(context.Context, string, string, string, string) (api.EnvironmentCachePurgeStatus, error)
}
type EnvironmentCachePurgeChange struct {
	ID                         int64
	AppID, EnvironmentID, Path string
}
type EnvironmentCachePurgeObserver interface {
	LatestEnvironmentCachePurgeID(context.Context) (int64, error)
	ListEnvironmentCachePurgesAfter(context.Context, int64) ([]EnvironmentCachePurgeChange, error)
	RecordEnvironmentCachePurgeWatermark(context.Context, string, string, int64) error
}
type memoryCachePurgeWatermark struct {
	Session string
	Last    int64
	At      time.Time
}

func decodeEnvironmentPurge(raw []byte, scope string) (api.EnvironmentCachePurgeReceipt, error) {
	var row struct {
		ID int64 `json:"id"`
		api.EnvironmentCachePurgeReceipt
	}
	if err := json.Unmarshal(raw, &row); err != nil {
		return row.EnvironmentCachePurgeReceipt, err
	}
	out := row.EnvironmentCachePurgeReceipt
	out.Scope = scope
	out.Generation = row.ID
	return out, nil
}
func purgeMatches(p api.EnvironmentCachePurgeReceipt, req api.CreateEnvironmentCachePurgeRequest) bool {
	return p.AppID == req.AppID && p.ProjectID == req.ProjectID && p.EnvironmentID == req.EnvironmentID && p.RequestID == req.RequestID && p.Path == req.Path
}
func (s *PgStore) CreateEnvironmentCachePurge(ctx context.Context, account, app, scope string, req api.CreateEnvironmentCachePurgeRequest) (api.EnvironmentCachePurgeReceipt, error) {
	var out api.EnvironmentCachePurgeReceipt
	if api.ValidateEnvironmentCachePurgeRequest(req) != nil || req.AppID != app {
		return out, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	owner, err := pgRouteMonitorOwner(ctx, tx, account, app, true)
	if err != nil {
		return out, err
	}
	if !owner.Account.MayDeploy() {
		return out, ErrEnvironmentCacheAccount
	}
	target, err := pgEnvironmentCachePolicy(ctx, tx, account, app, scope, true)
	if err != nil {
		return out, err
	}
	if target.ProjectID != req.ProjectID || target.EnvironmentID != req.EnvironmentID {
		return out, ErrRouteHealthRevision
	}
	raw, err := sqlc.New().ReadEnvironmentCachePurge(ctx, tx, sqlc.ReadEnvironmentCachePurgeParams{AccountID: mustPgUUID(account), RequestID: mustPgUUID(req.RequestID)})
	if err == nil {
		out, err = decodeEnvironmentPurge(raw, scope)
		if err != nil {
			return out, err
		}
		if !purgeMatches(out, req) {
			return out, ErrConflict
		}
		return out, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if err = sqlc.New().LockEnvironmentCachePurgeSequence(ctx, tx); err != nil {
		return out, err
	}
	raw, err = sqlc.New().InsertEnvironmentCachePurge(ctx, tx, sqlc.InsertEnvironmentCachePurgeParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), ProjectID: mustPgUUID(req.ProjectID), EnvironmentID: mustPgUUID(req.EnvironmentID), RequestID: mustPgUUID(req.RequestID), Path: req.Path})
	if err != nil {
		return out, err
	}
	out, err = decodeEnvironmentPurge(raw, scope)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) GetEnvironmentCachePurge(ctx context.Context, account, app, scope, request string) (api.EnvironmentCachePurgeStatus, error) {
	var out api.EnvironmentCachePurgeStatus
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	target, err := pgEnvironmentCachePolicy(ctx, tx, account, app, scope, false)
	if err != nil {
		return out, err
	}
	raw, err := sqlc.New().ReadEnvironmentCachePurge(ctx, tx, sqlc.ReadEnvironmentCachePurgeParams{AccountID: mustPgUUID(account), RequestID: mustPgUUID(request)})
	if err != nil {
		return out, routePolicyReadError(err)
	}
	out.EnvironmentCachePurgeReceipt, err = decodeEnvironmentPurge(raw, scope)
	if err != nil {
		return out, err
	}
	if out.AppID != app || out.ProjectID != target.ProjectID || out.EnvironmentID != target.EnvironmentID {
		return api.EnvironmentCachePurgeStatus{}, ErrNotFound
	}
	counts, err := sqlc.New().ReadEnvironmentCachePurgeCounts(ctx, tx, out.Generation)
	if err != nil {
		return out, err
	}
	out.CheckedAt = counts.CheckedAt.Time
	out.ServingGateways = counts.Serving
	out.AcknowledgedGateways = counts.Acknowledged
	out.PendingGateways = counts.Pending
	out.StaleGateways = counts.Stale
	purgeStatus(&out)
	return out, tx.Commit(ctx)
}
func purgeStatus(out *api.EnvironmentCachePurgeStatus) {
	out.Status = "pending"
	if out.ServingGateways == 0 || out.StaleGateways > 0 {
		out.Status = "degraded"
	} else if out.AcknowledgedGateways == out.ServingGateways {
		out.Status = "completed"
	}
}
func (s *PgStore) LatestEnvironmentCachePurgeID(ctx context.Context) (int64, error) {
	return sqlc.New().LatestEnvironmentCachePurge(ctx, s.pool)
}
func (s *PgStore) ListEnvironmentCachePurgesAfter(ctx context.Context, after int64) ([]EnvironmentCachePurgeChange, error) {
	rows, err := sqlc.New().ListEnvironmentCachePurgesAfter(ctx, s.pool, after)
	if err != nil {
		return nil, err
	}
	out := make([]EnvironmentCachePurgeChange, 0, len(rows))
	for _, p := range rows {
		out = append(out, EnvironmentCachePurgeChange{ID: p.ID, AppID: p.AppID, EnvironmentID: p.EnvironmentID, Path: p.Path})
	}
	return out, nil
}
func (s *PgStore) RecordEnvironmentCachePurgeWatermark(ctx context.Context, node, session string, last int64) error {
	if last < 0 || last > api.RouteRequirementsMaxRevision {
		return ErrInvalidArgument
	}
	n, err := sqlc.New().RecordEnvironmentCachePurgeWatermark(ctx, s.pool, sqlc.RecordEnvironmentCachePurgeWatermarkParams{NodeName: node, SessionID: mustPgUUID(session), LastID: last})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func (m *MemStore) CreateEnvironmentCachePurge(_ context.Context, account, app, scope string, req api.CreateEnvironmentCachePurgeRequest) (api.EnvironmentCachePurgeReceipt, error) {
	var out api.EnvironmentCachePurgeReceipt
	if api.ValidateEnvironmentCachePurgeRequest(req) != nil || req.AppID != app {
		return out, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	target, err := m.environmentCachePolicyLocked(account, app, scope)
	if err != nil {
		return out, err
	}
	if !m.accounts[account].MayDeploy() {
		return out, ErrEnvironmentCacheAccount
	}
	if target.ProjectID != req.ProjectID || target.EnvironmentID != req.EnvironmentID {
		return out, ErrRouteHealthRevision
	}
	key := account + "/" + req.RequestID
	if p, ok := m.environmentCachePurges[key]; ok {
		if !purgeMatches(p, req) {
			return p, ErrConflict
		}
		p.Scope = scope
		return p, nil
	}
	if m.environmentCachePurgeNext >= api.RouteRequirementsMaxRevision {
		return out, ErrConflict
	}
	m.environmentCachePurgeNext++
	out = api.EnvironmentCachePurgeReceipt{AppID: app, ProjectID: target.ProjectID, EnvironmentID: target.EnvironmentID, Scope: scope, RequestID: req.RequestID, Path: req.Path, Generation: m.environmentCachePurgeNext, RequestedAt: time.Now().UTC()}
	if m.environmentCachePurges == nil {
		m.environmentCachePurges = map[string]api.EnvironmentCachePurgeReceipt{}
	}
	m.environmentCachePurges[key] = out
	return out, nil
}
func (m *MemStore) GetEnvironmentCachePurge(_ context.Context, account, app, scope, request string) (api.EnvironmentCachePurgeStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out api.EnvironmentCachePurgeStatus
	target, err := m.environmentCachePolicyLocked(account, app, scope)
	if err != nil {
		return out, err
	}
	p, ok := m.environmentCachePurges[account+"/"+request]
	if !ok || p.AppID != app || p.ProjectID != target.ProjectID || p.EnvironmentID != target.EnvironmentID {
		return out, ErrNotFound
	}
	p.Scope = scope
	out.EnvironmentCachePurgeReceipt = p
	out.CheckedAt = time.Now().UTC()
	for _, n := range m.computeNodes {
		if !cacheGatewayEligible(n) {
			continue
		}
		out.ServingGateways++
		session, ok := m.environmentCacheGatewaySessions[n.ID]
		w := m.environmentCachePurgeWatermarks[n.ID]
		if !ok || session.Target != *n.GatewayTargetURL || !cacheObservationFresh(n.LastHeartbeatAt, out.CheckedAt) || !cacheObservationFresh(session.At, out.CheckedAt) || w.Session == session.ID && w.At.After(out.CheckedAt) {
			out.StaleGateways++
		} else if w.Session == session.ID && w.Last >= p.Generation {
			out.AcknowledgedGateways++
		} else {
			out.PendingGateways++
		}
	}
	purgeStatus(&out)
	return out, nil
}
func (m *MemStore) LatestEnvironmentCachePurgeID(_ context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.environmentCachePurgeNext, nil
}
func (m *MemStore) ListEnvironmentCachePurgesAfter(_ context.Context, after int64) ([]EnvironmentCachePurgeChange, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []EnvironmentCachePurgeChange{}
	for _, p := range m.environmentCachePurges {
		if p.Generation > after {
			out = append(out, EnvironmentCachePurgeChange{ID: p.Generation, AppID: p.AppID, EnvironmentID: p.EnvironmentID, Path: p.Path})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > 100 {
		out = out[:100]
	}
	return out, nil
}
func (m *MemStore) RecordEnvironmentCachePurgeWatermark(_ context.Context, node, session string, last int64) error {
	if last < 0 || last > api.RouteRequirementsMaxRevision {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, n := range m.computeNodes {
		current, ok := m.environmentCacheGatewaySessions[n.ID]
		if n.Name != node || !ok || current.ID != session || !cacheGatewayEligible(n) || current.Target != *n.GatewayTargetURL || !cacheObservationFresh(current.At, time.Now()) {
			continue
		}
		if m.environmentCachePurgeWatermarks == nil {
			m.environmentCachePurgeWatermarks = map[string]memoryCachePurgeWatermark{}
		}
		old := m.environmentCachePurgeWatermarks[n.ID]
		if old.Session == session && old.Last > last {
			return ErrConflict
		}
		m.environmentCachePurgeWatermarks[n.ID] = memoryCachePurgeWatermark{Session: session, Last: last, At: time.Now().UTC()}
		return nil
	}
	return ErrConflict
}
