package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrEnvironmentCacheAccount = errors.New("account cannot edit environment cache policy")

type EnvironmentCachePolicyReader interface {
	GetEnvironmentCachePolicy(context.Context, string, string, string) (api.EnvironmentCachePolicy, error)
}

type EnvironmentCachePolicyStore interface {
	EnvironmentCachePolicyReader
	SetEnvironmentCachePolicy(context.Context, string, string, string, api.SetEnvironmentCachePolicyRequest) (api.EnvironmentCachePolicy, error)
}

func environmentCacheHash(scope string, req api.SetEnvironmentCachePolicyRequest) string {
	raw, _ := json.Marshal(struct {
		Scope   string
		Request api.SetEnvironmentCachePolicyRequest
	}{scope, req})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func pgEnvironmentCachePolicy(ctx context.Context, db sqlc.DBTX, account, app, scope string, lock bool) (api.EnvironmentCachePolicy, error) {
	out := api.EnvironmentCachePolicy{AppID: app, Scope: scope, Inherited: true, Rules: []api.EnvironmentCacheRule{}}
	if !api.ValidProjectEnvironmentSlug(scope) {
		return out, ErrInvalidArgument
	}
	if lock {
		row, err := sqlc.New().LockEnvironmentCacheIdentity(ctx, db, sqlc.LockEnvironmentCacheIdentityParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), Scope: scope})
		if err != nil {
			return out, routePolicyReadError(err)
		}
		out.EnvironmentID, out.ProjectID = row.EnvironmentID, row.ProjectID
	} else {
		row, err := sqlc.New().ReadEnvironmentCacheIdentity(ctx, db, sqlc.ReadEnvironmentCacheIdentityParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), Scope: scope})
		if err != nil {
			return out, routePolicyReadError(err)
		}
		out.EnvironmentID, out.ProjectID = row.EnvironmentID, row.ProjectID
	}
	raw, err := sqlc.New().ReadEnvironmentCachePolicy(ctx, db, sqlc.ReadEnvironmentCachePolicyParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), EnvironmentID: mustPgUUID(out.EnvironmentID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	// SQL row includes identity, revision, rules and updated_at. Scope is
	// resolved from the live registry, never taken from persisted slug data.
	if err = json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	return out, nil
}
func (s *PgStore) GetEnvironmentCachePolicy(ctx context.Context, account, app, scope string) (api.EnvironmentCachePolicy, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.EnvironmentCachePolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	out, err := pgEnvironmentCachePolicy(ctx, tx, account, app, scope, false)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) SetEnvironmentCachePolicy(ctx context.Context, account, app, scope string, req api.SetEnvironmentCachePolicyRequest) (api.EnvironmentCachePolicy, error) {
	if api.ValidateEnvironmentCachePolicyRequest(req) != nil || req.AppID != app {
		return api.EnvironmentCachePolicy{}, ErrInvalidArgument
	}
	req.Rules, _ = api.NormalizeEnvironmentCacheRules(req.Rules)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.EnvironmentCachePolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	owner, err := pgRouteMonitorOwner(ctx, tx, account, app, true)
	if err != nil {
		return api.EnvironmentCachePolicy{}, err
	}
	if !owner.Account.MayDeploy() {
		return api.EnvironmentCachePolicy{}, ErrEnvironmentCacheAccount
	}
	current, err := pgEnvironmentCachePolicy(ctx, tx, account, app, scope, true)
	if err != nil {
		return current, err
	}
	if current.ProjectID != req.ProjectID || current.EnvironmentID != req.EnvironmentID {
		return current, ErrRouteHealthRevision
	}
	var source api.EnvironmentCachePolicy
	if req.CopyFrom != nil {
		source, err = pgEnvironmentCachePolicy(ctx, tx, account, app, req.CopyFrom.Scope, true)
		if err != nil {
			return current, err
		}
		if !environmentCacheCopyIdentity(source, current, req.CopyFrom) {
			return current, ErrRouteHealthRevision
		}
	}
	hash := environmentCacheHash(scope, req)
	receipt, err := sqlc.New().ReadEnvironmentCacheReceipt(ctx, tx, sqlc.ReadEnvironmentCacheReceiptParams{AccountID: mustPgUUID(account), RequestID: mustPgUUID(req.RequestID)})
	if err == nil {
		if receipt.RequestHash != hash {
			return current, ErrConflict
		}
		var saved api.EnvironmentCachePolicy
		if err = json.Unmarshal(receipt.Response, &saved); err != nil {
			return current, err
		}
		return saved, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return current, err
	}
	if current.Revision != *req.ExpectedRevision || req.CopyFrom != nil && !environmentCacheCopyMatches(source, req) {
		return current, ErrRouteHealthRevision
	}
	if req.RestoreRevision != nil {
		historical, e := pgEnvironmentCacheRevision(ctx, tx, account, current, *req.RestoreRevision)
		if e != nil {
			return current, e
		}
		if !environmentCacheRestoreMatches(historical, req) {
			return current, ErrRouteHealthRevision
		}
	}
	rules, _ := json.Marshal(req.Rules)
	err = sqlc.New().WriteEnvironmentCachePolicy(ctx, tx, sqlc.WriteEnvironmentCachePolicyParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), ProjectID: mustPgUUID(req.ProjectID), EnvironmentID: mustPgUUID(req.EnvironmentID), Revision: current.Revision + 1, Rules: rules, Inherited: req.Inherited})
	if err != nil {
		return current, err
	}
	updated, err := pgEnvironmentCachePolicy(ctx, tx, account, app, scope, false)
	if err != nil {
		return current, err
	}
	raw, _ := json.Marshal(updated)
	if err = sqlc.New().WriteEnvironmentCacheHistory(ctx, tx, sqlc.WriteEnvironmentCacheHistoryParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), ProjectID: mustPgUUID(req.ProjectID), EnvironmentID: mustPgUUID(req.EnvironmentID), Revision: updated.Revision, Snapshot: raw}); err != nil {
		return current, err
	}
	err = sqlc.New().WriteEnvironmentCacheReceipt(ctx, tx, sqlc.WriteEnvironmentCacheReceiptParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), EnvironmentID: mustPgUUID(req.EnvironmentID), RequestID: mustPgUUID(req.RequestID), RequestHash: hash, Response: raw})
	if err != nil {
		return current, err
	}
	return updated, tx.Commit(ctx)
}

// Identity is checked before receipt recovery; revision and contents are checked
// only before a new write, so an original receipt survives subsequent edits.
func environmentCacheCopyIdentity(source, target api.EnvironmentCachePolicy, pin *api.EnvironmentCachePolicySource) bool {
	return source.AppID == target.AppID && source.ProjectID == target.ProjectID && source.EnvironmentID == pin.EnvironmentID && source.EnvironmentID != target.EnvironmentID && source.Scope == pin.Scope
}
func environmentCacheCopyMatches(source api.EnvironmentCachePolicy, req api.SetEnvironmentCachePolicyRequest) bool {
	return !source.Inherited && source.Revision == req.CopyFrom.Revision && reflect.DeepEqual(source.Rules, req.Rules)
}

func environmentCacheRestoreMatches(historical api.EnvironmentCachePolicy, req api.SetEnvironmentCachePolicyRequest) bool {
	return historical.Inherited == req.Inherited && reflect.DeepEqual(historical.Rules, req.Rules)
}
