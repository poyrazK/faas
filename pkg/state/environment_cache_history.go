package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EnvironmentCacheHistoryReader interface {
	ListEnvironmentCachePolicyHistory(context.Context, string, string, string, int64, int) (api.EnvironmentCachePolicyHistory, error)
	GetEnvironmentCachePolicyRevision(context.Context, string, string, string, int64) (api.EnvironmentCachePolicy, error)
}

func initialEnvironmentCachePolicy(current api.EnvironmentCachePolicy) api.EnvironmentCachePolicy {
	current.Revision = 0
	current.Inherited = true
	current.Rules = []api.EnvironmentCacheRule{}
	current.UpdatedAt = nil
	return current
}
func decodeEnvironmentCacheHistory(raw []byte, current api.EnvironmentCachePolicy, revision int64) (api.EnvironmentCachePolicy, error) {
	var p api.EnvironmentCachePolicy
	if json.Unmarshal(raw, &p) != nil || p.AppID != current.AppID || p.ProjectID != current.ProjectID || p.EnvironmentID != current.EnvironmentID || p.Revision != revision || revision < 1 || revision > current.Revision || p.UpdatedAt == nil || p.UpdatedAt.IsZero() || p.Inherited && len(p.Rules) != 0 {
		return p, errors.New("invalid stored environment cache revision")
	}
	rules, err := api.NormalizeEnvironmentCacheRules(p.Rules)
	if err != nil {
		return p, err
	}
	p.Rules = rules
	p.Scope = current.Scope
	return p, nil
}
func pgEnvironmentCacheRevision(ctx context.Context, db sqlc.DBTX, account string, current api.EnvironmentCachePolicy, revision int64) (api.EnvironmentCachePolicy, error) {
	if revision < 0 || revision > api.RouteRequirementsMaxRevision {
		return api.EnvironmentCachePolicy{}, ErrInvalidArgument
	}
	if revision == 0 {
		return initialEnvironmentCachePolicy(current), nil
	}
	row, err := sqlc.New().ReadEnvironmentCacheHistory(ctx, db, sqlc.ReadEnvironmentCacheHistoryParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(current.AppID), EnvironmentID: mustPgUUID(current.EnvironmentID), Revision: revision})
	if err != nil {
		return api.EnvironmentCachePolicy{}, routePolicyReadError(err)
	}
	return decodeEnvironmentCacheHistory(row.Snapshot, current, row.Revision)
}
func cacheHistoryPage(current api.EnvironmentCachePolicy, entries []api.EnvironmentCachePolicy, limit int) api.EnvironmentCachePolicyHistory {
	// Revision zero is a synthetic initial inherited mode, never an old app-rule snapshot.
	if len(entries) <= limit {
		entries = append(entries, initialEnvironmentCachePolicy(current))
	}
	out := api.EnvironmentCachePolicyHistory{AppID: current.AppID, ProjectID: current.ProjectID, EnvironmentID: current.EnvironmentID, Scope: current.Scope, CurrentRevision: current.Revision, Entries: entries}
	if len(entries) > limit {
		out.Entries = entries[:limit]
		rev := out.Entries[limit-1].Revision
		out.NextBeforeRevision = &rev
	}
	return out
}
func validCacheHistoryPage(before int64, limit int) bool {
	return before >= 0 && before <= api.RouteRequirementsMaxRevision && limit >= 1 && limit <= api.EnvironmentCacheHistoryMaxLimit
}
func (s *PgStore) GetEnvironmentCachePolicyRevision(ctx context.Context, account, app, scope string, revision int64) (api.EnvironmentCachePolicy, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.EnvironmentCachePolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := pgEnvironmentCachePolicy(ctx, tx, account, app, scope, false)
	if err != nil {
		return current, err
	}
	out, err := pgEnvironmentCacheRevision(ctx, tx, account, current, revision)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) ListEnvironmentCachePolicyHistory(ctx context.Context, account, app, scope string, before int64, limit int) (api.EnvironmentCachePolicyHistory, error) {
	if !validCacheHistoryPage(before, limit) {
		return api.EnvironmentCachePolicyHistory{}, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.EnvironmentCachePolicyHistory{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := pgEnvironmentCachePolicy(ctx, tx, account, app, scope, false)
	if err != nil {
		return api.EnvironmentCachePolicyHistory{}, err
	}
	rows, err := sqlc.New().ListEnvironmentCacheHistory(ctx, tx, sqlc.ListEnvironmentCacheHistoryParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), EnvironmentID: mustPgUUID(current.EnvironmentID), BeforeRevision: before, PageLimit: int32(limit + 1)})
	if err != nil {
		return api.EnvironmentCachePolicyHistory{}, err
	}
	entries := make([]api.EnvironmentCachePolicy, 0, len(rows))
	for _, row := range rows {
		p, e := decodeEnvironmentCacheHistory(row.Snapshot, current, row.Revision)
		if e != nil {
			return api.EnvironmentCachePolicyHistory{}, e
		}
		entries = append(entries, p)
	}
	out := cacheHistoryPage(current, entries, limit)
	return out, tx.Commit(ctx)
}
func (m *MemStore) environmentCacheRevisionLocked(current api.EnvironmentCachePolicy, revision int64) (api.EnvironmentCachePolicy, error) {
	if revision < 0 || revision > api.RouteRequirementsMaxRevision {
		return api.EnvironmentCachePolicy{}, ErrInvalidArgument
	}
	if revision == 0 {
		return initialEnvironmentCachePolicy(current), nil
	}
	p, ok := m.environmentCacheHistory[memoryBudgetKey(current.AppID, current.Scope, current.EnvironmentID)][revision]
	if !ok {
		return api.EnvironmentCachePolicy{}, ErrNotFound
	}
	return cloneEnvironmentCachePolicy(p), nil
}
func (m *MemStore) GetEnvironmentCachePolicyRevision(_ context.Context, account, app, scope string, revision int64) (api.EnvironmentCachePolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.environmentCachePolicyLocked(account, app, scope)
	if err != nil {
		return current, err
	}
	return m.environmentCacheRevisionLocked(current, revision)
}
func (m *MemStore) ListEnvironmentCachePolicyHistory(_ context.Context, account, app, scope string, before int64, limit int) (api.EnvironmentCachePolicyHistory, error) {
	if !validCacheHistoryPage(before, limit) {
		return api.EnvironmentCachePolicyHistory{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.environmentCachePolicyLocked(account, app, scope)
	if err != nil {
		return api.EnvironmentCachePolicyHistory{}, err
	}
	entries := []api.EnvironmentCachePolicy{}
	for revision, p := range m.environmentCacheHistory[memoryBudgetKey(app, scope, current.EnvironmentID)] {
		if before == 0 || revision < before {
			entries = append(entries, cloneEnvironmentCachePolicy(p))
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Revision > entries[j].Revision })
	if len(entries) > limit+1 {
		entries = entries[:limit+1]
	}
	return cacheHistoryPage(current, entries, limit), nil
}
