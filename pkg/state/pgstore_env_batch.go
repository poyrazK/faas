package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/retry"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ApplyEnvBatch(ctx context.Context, in EnvBatchChange) (api.EnvBatchResponse, error) {
	hash, err := validateEnvBatch(&in)
	if err != nil {
		return api.EnvBatchResponse{}, err
	}
	// Legacy multi-row writers can encounter a lock-order deadlock with the
	// new transaction. PostgreSQL rolls that transaction back in full; retry
	// with the same receipt identity and revision instead of weakening guards.
	for attempt := 0; ; attempt++ {
		if err := retry.Wait(ctx, 25*time.Millisecond, 500*time.Millisecond, attempt); err != nil {
			return api.EnvBatchResponse{}, err
		}
		out, err := s.applyEnvBatchTx(ctx, in, hash)
		var pgErr *pgconn.PgError
		if attempt >= 2 || !errors.As(err, &pgErr) || pgErr.Code != "40P01" && pgErr.Code != "40001" {
			return out, err
		}
		if err := ctx.Err(); err != nil {
			return api.EnvBatchResponse{}, err
		}
	}
}

func (s *PgStore) EnvBatchReceipt(ctx context.Context, accountID, appID, scope, requestID, proof string) (api.EnvBatchResponse, error) {
	app, err := s.AppByID(ctx, appID)
	if err != nil {
		return api.EnvBatchResponse{}, err
	}
	if app.AccountID != accountID || app.Status == AppDeleted {
		return api.EnvBatchResponse{}, ErrNotFound
	}
	environmentID := ""
	if scope != DefaultEnvScope {
		env, err := s.ProjectEnvironmentBySlug(ctx, accountID, app.ProjectID, scope)
		if err != nil {
			return api.EnvBatchResponse{}, err
		}
		environmentID = env.ID
	}
	row, err := sqlc.New().ReadEnvBatchReceipt(ctx, s.pool, sqlc.ReadEnvBatchReceiptParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), Scope: scope, RequestID: mustPgUUID(requestID)})
	if err != nil {
		return api.EnvBatchResponse{}, mapErr(err)
	}
	var saved storedEnvBatchReceipt
	var tag struct {
		Kind string `json:"_kind"`
	}
	if err := json.Unmarshal(row.Receipt, &tag); err != nil || tag.Kind != "" {
		return api.EnvBatchResponse{}, ErrNotFound
	}
	if err := json.Unmarshal(row.Receipt, &saved); err != nil {
		return api.EnvBatchResponse{}, err
	}
	out := saved.EnvBatchResponse
	if !envBatchProofMatches(saved.ProofHash, proof) || out.AppID != appID || out.Scope != scope || out.RequestID != requestID || !envBatchEnvironmentMatches(out.EnvironmentID, environmentID) {
		return api.EnvBatchResponse{}, ErrNotFound
	}
	// A target can be deleted, moved or recreated while the receipt is read.
	// Recheck its immutable identities before releasing the historical data.
	current, err := s.AppByID(ctx, appID)
	if err != nil {
		return api.EnvBatchResponse{}, err
	}
	if current.AccountID != accountID || current.Status == AppDeleted || current.ProjectID != app.ProjectID {
		return api.EnvBatchResponse{}, ErrNotFound
	}
	if scope != DefaultEnvScope {
		env, err := s.ProjectEnvironmentBySlug(ctx, accountID, current.ProjectID, scope)
		if err != nil {
			return api.EnvBatchResponse{}, err
		}
		if env.ID != environmentID {
			return api.EnvBatchResponse{}, ErrNotFound
		}
	}
	return out, nil
}

func (s *PgStore) applyEnvBatchTx(ctx context.Context, in EnvBatchChange, hash [32]byte) (api.EnvBatchResponse, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.EnvBatchResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err = q.LockAppConfigScope(ctx, tx, sqlc.LockAppConfigScopeParams{AppID: mustPgUUID(in.AppID), Scope: in.Scope}); err != nil {
		return api.EnvBatchResponse{}, err
	}
	if err = q.LockAppConfigScope(ctx, tx, sqlc.LockAppConfigScopeParams{AppID: mustPgUUID(in.AppID), Scope: "__env_quota__"}); err != nil {
		return api.EnvBatchResponse{}, err
	}
	if _, err = q.LockEnvBatchOwner(ctx, tx, sqlc.LockEnvBatchOwnerParams{AppID: mustPgUUID(in.AppID), AccountID: mustPgUUID(in.AccountID)}); err != nil {
		return api.EnvBatchResponse{}, mapErr(err)
	}
	if in.Request.ExpectedEnvironmentID != "" {
		env, err := q.LockAppScalingEnvironment(ctx, tx, sqlc.LockAppScalingEnvironmentParams{ID: mustPgUUID(in.AppID), AccountID: mustPgUUID(in.AccountID), Slug: in.Scope})
		if err != nil {
			return api.EnvBatchResponse{}, mapErr(err)
		}
		if !sameStandardUUID(uuid.UUID(env.ID.Bytes).String(), in.Request.ExpectedEnvironmentID) || env.Protected {
			return api.EnvBatchResponse{}, ErrConflict
		}
	}
	saved, err := q.ReadEnvBatchReceipt(ctx, tx, sqlc.ReadEnvBatchReceiptParams{AppID: mustPgUUID(in.AppID), AccountID: mustPgUUID(in.AccountID), Scope: in.Scope, RequestID: mustPgUUID(in.Request.RequestID)})
	if err == nil {
		if !bytes.Equal(saved.RequestSha256, hash[:]) {
			return api.EnvBatchResponse{}, ErrEnvBatchRequestConflict
		}
		var out api.EnvBatchResponse
		if err := json.Unmarshal(saved.Receipt, &out); err != nil {
			return out, err
		}
		return out, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return api.EnvBatchResponse{}, err
	}
	variables, secrets, err := s.configStateTx(ctx, tx, in.AccountID, in.AppID, in.Scope)
	if err != nil {
		return api.EnvBatchResponse{}, err
	}
	latest, exists, err := s.latestConfigRevisionTx(ctx, tx, in.AppID, in.Scope)
	if err != nil {
		return api.EnvBatchResponse{}, err
	}
	if !exists && *in.Request.ExpectedRevision == 0 && (len(variables) != 0 || len(secrets) != 0) {
		// Bootstrap metadata for older unversioned scopes, then refuse this
		// batch so the agent reviews the newly visible baseline first.
		if _, err = s.insertConfigRevisionTx(ctx, tx, ConfigRevision{AccountID: in.AccountID, AppID: in.AppID, Scope: in.Scope, Revision: 1,
			Source: ConfigSourceBaseline, Actor: in.Actor, Variables: variables, Secrets: secrets, CreatedAt: time.Now().UTC()}); err != nil {
			return api.EnvBatchResponse{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return api.EnvBatchResponse{}, err
		}
		return api.EnvBatchResponse{}, ErrEnvBatchRevisionConflict
	}
	if !envBatchRevisionMatches(latest, exists, variables, secrets, *in.Request.ExpectedRevision) {
		return api.EnvBatchResponse{}, ErrEnvBatchRevisionConflict
	}
	next := envBatchValues(variables, in.Request)
	count, err := q.CountAppEnvironmentIntent(ctx, tx, sqlc.CountAppEnvironmentIntentParams{AccountID: mustPgUUID(in.AccountID), AppID: mustPgUUID(in.AppID)})
	if err != nil {
		return api.EnvBatchResponse{}, err
	}
	projected := int(count) - len(variables) + len(next)
	if projected > in.Limits.EnvVarsMax {
		return api.EnvBatchResponse{}, &EnvBatchQuotaError{projected}
	}
	now := time.Now().UTC()
	if !exists {
		latest, err = s.insertConfigRevisionTx(ctx, tx, ConfigRevision{AccountID: in.AccountID, AppID: in.AppID, Scope: in.Scope, Revision: 1, Source: ConfigSourceBaseline, Actor: in.Actor, Variables: variables, Secrets: secrets, CreatedAt: now})
		if err != nil {
			return api.EnvBatchResponse{}, err
		}
	}
	guard, err := openEnvBatchGuard(ctx, tx, in, latest.Revision, now)
	if err != nil {
		return api.EnvBatchResponse{}, err
	}
	// Sorting reduces row-lock inversions with other batch writers.
	for _, key := range sortedEnvBatchKeys(in.Request.Values) {
		if err = q.WriteEnvBatchVariable(ctx, tx, sqlc.WriteEnvBatchVariableParams{AccountID: mustPgUUID(in.AccountID), AppID: mustPgUUID(in.AppID), Scope: in.Scope, Key: key, Value: in.Request.Values[key]}); err != nil {
			return api.EnvBatchResponse{}, fmt.Errorf("write variable batch: %w", mapErr(err))
		}
	}
	for _, key := range in.Request.Delete {
		if err = q.DeleteEnvBatchVariable(ctx, tx, sqlc.DeleteEnvBatchVariableParams{AccountID: mustPgUUID(in.AccountID), AppID: mustPgUUID(in.AppID), Scope: in.Scope, Key: key}); err != nil {
			return api.EnvBatchResponse{}, fmt.Errorf("delete variable batch: %w", mapErr(err))
		}
	}
	revision := ConfigRevision{AccountID: in.AccountID, AppID: in.AppID, Scope: in.Scope, Revision: latest.Revision + 1, Source: ConfigSourceEnvBatch, Actor: in.Actor, Variables: next, Secrets: secrets, CreatedAt: now}
	if guard != nil {
		revision.GuardID = guard.ID
	}
	if _, err = s.insertConfigRevisionTx(ctx, tx, revision); err != nil {
		return api.EnvBatchResponse{}, err
	}
	if guard != nil {
		if _, err = q.RecordAppConfigGuardChange(ctx, tx, sqlc.RecordAppConfigGuardChangeParams{ID: mustPgUUID(guard.ID), HeadRevision: revision.Revision, ChangedAt: pgTimestamptzUTC(now)}); err != nil {
			return api.EnvBatchResponse{}, err
		}
	}
	if err = q.InvalidateEnvBatchSnapshots(ctx, tx, sqlc.InvalidateEnvBatchSnapshotsParams{AppID: mustPgUUID(in.AppID), Scope: in.Scope}); err != nil {
		return api.EnvBatchResponse{}, err
	}
	out := envBatchReceipt(in, revision.Revision, guard)
	raw, err := json.Marshal(storedEnvBatchReceipt{EnvBatchResponse: out, ProofHash: envBatchProofHash(in.Request.ReceiptProof)})
	if err != nil {
		return api.EnvBatchResponse{}, err
	}
	if err = q.WriteEnvBatchReceipt(ctx, tx, sqlc.WriteEnvBatchReceiptParams{AppID: mustPgUUID(in.AppID), AccountID: mustPgUUID(in.AccountID), Scope: in.Scope, RequestID: mustPgUUID(in.Request.RequestID), RequestSha256: hash[:], Receipt: raw}); err != nil {
		return api.EnvBatchResponse{}, err
	}
	if err = q.PruneAppConfigRevisions(ctx, tx, sqlc.PruneAppConfigRevisionsParams{AppID: mustPgUUID(in.AppID), Scope: in.Scope, KeepAfter: revision.Revision - api.ConfigRevisionsRetained}); err != nil {
		return api.EnvBatchResponse{}, err
	}
	// One day matches the ordinary native idempotency replay window. After
	// pruning, the old expected revision still prevents repeating a write.
	if err = q.PruneEnvBatchReceipts(ctx, tx, sqlc.PruneEnvBatchReceiptsParams{AppID: mustPgUUID(in.AppID), Before: pgTimestamptzUTC(now.Add(-24 * time.Hour))}); err != nil {
		return api.EnvBatchResponse{}, err
	}
	return out, tx.Commit(ctx)
}

func openEnvBatchGuard(ctx context.Context, tx pgx.Tx, in EnvBatchChange, baseline int64, now time.Time) (*api.ConfigGuardSummary, error) {
	q := sqlc.New()
	row, err := q.LockWatchingAppConfigGuard(ctx, tx, sqlc.LockWatchingAppConfigGuardParams{AppID: mustPgUUID(in.AppID), Scope: in.Scope})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	watching := err == nil
	err = nil
	guard := configGuardFromRow(row)
	if !in.Guard {
		if watching {
			_, err = closeConfigGuardTx(ctx, tx, guard.ID, ConfigGuardSuperseded, supersededConfigGuardDecision(configGuardSupersededByUnguarded), 0, now)
		}
		return nil, err
	}
	if !watching {
		row, err = q.InsertAppConfigGuard(ctx, tx, sqlc.InsertAppConfigGuardParams{ID: mustPgUUID(uuid.NewString()), AccountID: mustPgUUID(in.AccountID), AppID: mustPgUUID(in.AppID), Scope: in.Scope, BaseRevision: baseline, OpenedAt: pgTimestamptzUTC(now)})
		if err != nil {
			return nil, err
		}
		guard = configGuardFromRow(row)
	}
	return &api.ConfigGuardSummary{ID: guard.ID, Status: guard.Status, BaseRevision: guard.BaseRevision, WindowMinutes: api.ConfigGuardWindowMinutes}, nil
}

func sortedEnvBatchKeys(values map[string]string) []string { return slices.Sorted(maps.Keys(values)) }
