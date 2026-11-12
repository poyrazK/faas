package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type storedConfigRollbackReceipt struct {
	Kind      string                    `json:"_kind"`
	ProofHash string                    `json:"_receipt_proof_hash"`
	Receipt   api.ConfigRollbackReceipt `json:"rollback"`
}

func writeReviewedConfigRollback(ctx context.Context, tx pgx.Tx, r ConfigRestore, revision int64, changed bool) error {
	if r.RequestID == "" {
		return nil
	}
	q := sqlc.New()
	hash := r.rollbackRequestHash()
	raw, err := json.Marshal(storedConfigRollbackReceipt{Kind: "config_rollback", ProofHash: envBatchProofHash(r.ReceiptProof), Receipt: r.rollbackReceipt(revision, changed)})
	if err != nil {
		return err
	}
	if err := q.WriteEnvBatchReceipt(ctx, tx, sqlc.WriteEnvBatchReceiptParams{AppID: mustPgUUID(r.Finish.AppID), AccountID: mustPgUUID(r.Finish.AccountID), Scope: r.Finish.Scope, RequestID: mustPgUUID(r.RequestID), RequestSha256: hash[:], Receipt: raw}); err != nil {
		return err
	}
	return q.PruneEnvBatchReceipts(ctx, tx, sqlc.PruneEnvBatchReceiptsParams{AppID: mustPgUUID(r.Finish.AppID), Before: pgTimestamptzUTC(time.Now().UTC().Add(-24 * time.Hour))})
}

func (s *PgStore) ConfigRollbackReceipt(ctx context.Context, accountID, appID, scope, requestID, proof string) (api.ConfigRollbackReceipt, error) {
	app, err := s.AppByID(ctx, appID)
	if err != nil {
		return api.ConfigRollbackReceipt{}, err
	}
	if app.AccountID != accountID || app.Status == AppDeleted {
		return api.ConfigRollbackReceipt{}, ErrNotFound
	}
	environmentID := ""
	if scope != DefaultEnvScope {
		env, err := s.ProjectEnvironmentBySlug(ctx, accountID, app.ProjectID, scope)
		if err != nil {
			return api.ConfigRollbackReceipt{}, err
		}
		environmentID = env.ID
	}
	row, err := sqlc.New().ReadEnvBatchReceipt(ctx, s.pool, sqlc.ReadEnvBatchReceiptParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), Scope: scope, RequestID: mustPgUUID(requestID)})
	if err != nil {
		return api.ConfigRollbackReceipt{}, mapErr(err)
	}
	var saved storedConfigRollbackReceipt
	if err := json.Unmarshal(row.Receipt, &saved); err != nil {
		return api.ConfigRollbackReceipt{}, err
	}
	out := saved.Receipt
	if saved.Kind != "config_rollback" || !envBatchProofMatches(saved.ProofHash, proof) || out.AppID != appID || out.Scope != scope || out.RequestID != requestID || !envBatchEnvironmentMatches(out.EnvironmentID, environmentID) {
		return api.ConfigRollbackReceipt{}, ErrNotFound
	}
	current, err := s.AppByID(ctx, appID)
	if err != nil {
		return api.ConfigRollbackReceipt{}, err
	}
	if current.AccountID != accountID || current.Status == AppDeleted || current.ProjectID != app.ProjectID {
		return api.ConfigRollbackReceipt{}, ErrNotFound
	}
	if scope != DefaultEnvScope {
		env, err := s.ProjectEnvironmentBySlug(ctx, accountID, current.ProjectID, scope)
		if err != nil {
			return api.ConfigRollbackReceipt{}, err
		}
		if env.ID != environmentID {
			return api.ConfigRollbackReceipt{}, ErrNotFound
		}
	}
	return out, nil
}

func (s *PgStore) ApplyConfigRestore(ctx context.Context, r ConfigRestore) (ConfigRevision, error) {
	if !r.valid() {
		return ConfigRevision{}, ErrInvalidArgument
	}
	return retryTrafficSerialization(ctx, func() (ConfigRevision, error) { return s.applyConfigRestoreOnce(ctx, r) })
}

func (s *PgStore) ApplyReviewedConfigRollback(ctx context.Context, r ConfigRestore) (ConfigRevision, error) {
	if r.ExpectedRevision == nil || r.Finish.Source != ConfigSourceRollback {
		return ConfigRevision{}, ErrInvalidArgument
	}
	return s.ApplyConfigRestore(ctx, r)
}

func (s *PgStore) applyConfigRestoreOnce(ctx context.Context, r ConfigRestore) (ConfigRevision, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ConfigRevision{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	f, q := r.Finish, sqlc.New()
	// Match customer secret writers: target locks precede the app row.
	keys := make([]string, 0, len(r.Secrets))
	for _, secret := range r.Secrets {
		keys = append(keys, secret.Key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := lockAppSecretTarget(ctx, tx, f.AppID, f.Scope, key); err != nil {
			return ConfigRevision{}, err
		}
	}
	if err := q.LockAppConfigScope(ctx, tx, sqlc.LockAppConfigScopeParams{AppID: mustPgUUID(f.AppID), Scope: f.Scope}); err != nil {
		return ConfigRevision{}, err
	}
	if _, err := q.LockRuntimeSecretAppExclusive(ctx, tx, sqlc.LockRuntimeSecretAppExclusiveParams{AccountID: mustPgUUID(f.AccountID), AppID: mustPgUUID(f.AppID)}); err != nil {
		return ConfigRevision{}, mapErr(err)
	}
	if r.ExpectedRevision != nil && r.ExpectedEnvironmentID != "" {
		env, err := q.LockAppScalingEnvironment(ctx, tx, sqlc.LockAppScalingEnvironmentParams{ID: mustPgUUID(f.AppID), AccountID: mustPgUUID(f.AccountID), Slug: f.Scope})
		if err != nil {
			return ConfigRevision{}, mapErr(err)
		}
		if !sameStandardUUID(uuid.UUID(env.ID.Bytes).String(), r.ExpectedEnvironmentID) || env.Protected {
			return ConfigRevision{}, ErrConfigRestoreChanged
		}
	}
	if r.RequestID != "" {
		row, err := q.ReadEnvBatchReceipt(ctx, tx, sqlc.ReadEnvBatchReceiptParams{AccountID: mustPgUUID(f.AccountID), AppID: mustPgUUID(f.AppID), Scope: f.Scope, RequestID: mustPgUUID(r.RequestID)})
		if err == nil {
			var saved storedConfigRollbackReceipt
			hash := r.rollbackRequestHash()
			if json.Unmarshal(row.Receipt, &saved) != nil || saved.Kind != "config_rollback" || !bytes.Equal(row.RequestSha256, hash[:]) {
				return ConfigRevision{}, ErrEnvBatchRequestConflict
			}
			return ConfigRevision{}, ErrConfigRollbackRecorded
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return ConfigRevision{}, err
		}
	}
	variables, secrets, err := s.configStateTx(ctx, tx, f.AccountID, f.AppID, f.Scope)
	if err != nil {
		return ConfigRevision{}, err
	}
	if r.ExpectedRevision != nil {
		latest, exists, err := s.latestConfigRevisionTx(ctx, tx, f.AppID, f.Scope)
		if err != nil {
			return ConfigRevision{}, err
		}
		if !envBatchRevisionMatches(latest, exists, variables, secrets, *r.ExpectedRevision) {
			return ConfigRevision{}, ErrConfigRestoreChanged
		}
	}
	var guard ConfigGuard
	if r.Guard != nil {
		row, err := q.LockAppConfigGuardForRestore(ctx, tx, mustPgUUID(r.Guard.ID))
		if err != nil {
			return ConfigRevision{}, mapErr(err)
		}
		guard = configGuardFromRow(row)
	}
	if !r.unchanged(variables, secrets, guard) {
		return ConfigRevision{}, ErrConfigRestoreChanged
	}
	if r.ExpectedRevision != nil && len(r.Variables) == 0 && len(r.Secrets) == 0 {
		latest, _, err := s.latestConfigRevisionTx(ctx, tx, f.AppID, f.Scope)
		if err != nil {
			return ConfigRevision{}, err
		}
		if err := writeReviewedConfigRollback(ctx, tx, r, latest.Revision, false); err != nil {
			return ConfigRevision{}, err
		}
		return latest, mapErr(tx.Commit(ctx))
	}
	var restoreGuard *api.ConfigGuardSummary
	if r.ExpectedRevision != nil {
		guard, err := openEnvBatchGuard(ctx, tx, EnvBatchChange{AccountID: f.AccountID, AppID: f.AppID, Scope: f.Scope, Guard: r.ReviewedGuard}, *r.ExpectedRevision, f.Now)
		if err != nil {
			return ConfigRevision{}, err
		}
		restoreGuard = guard
		if guard != nil {
			f.GuardID = guard.ID
		}
	}
	if err := applyConfigRestoreWrites(ctx, tx, r); err != nil {
		return ConfigRevision{}, err
	}
	revision, err := s.finishConfigChangeTx(ctx, tx, f)
	if err != nil {
		return ConfigRevision{}, err
	}
	if r.Guard != nil {
		closed, err := closeConfigGuardTx(ctx, tx, r.Guard.ID, ConfigGuardRestored, r.decision(revision.Revision), r.Guard.HeadRevision, f.Now)
		if err != nil {
			return ConfigRevision{}, err
		}
		if !closed {
			return ConfigRevision{}, ErrConfigRestoreChanged
		}
	}
	if err := invalidateConfigRestoreTx(ctx, tx, r); err != nil {
		return ConfigRevision{}, err
	}
	if err := queueConfigRestoreTx(ctx, tx, r); err != nil {
		return ConfigRevision{}, err
	}
	if err := writeReviewedConfigRollback(ctx, tx, r, revision.Revision, true); err != nil {
		return ConfigRevision{}, err
	}
	revision.RestoreGuard = restoreGuard
	return revision, mapErr(tx.Commit(ctx))
}

func applyConfigRestoreWrites(ctx context.Context, tx pgx.Tx, r ConfigRestore) error {
	f, q := r.Finish, sqlc.New()
	for _, v := range r.Variables {
		var err error
		if v.Delete {
			err = q.DeleteAppConfigVariable(ctx, tx, sqlc.DeleteAppConfigVariableParams{AccountID: mustPgUUID(f.AccountID), AppID: mustPgUUID(f.AppID), Scope: f.Scope, Key: v.Key})
		} else {
			err = q.RestoreAppConfigVariable(ctx, tx, sqlc.RestoreAppConfigVariableParams{AccountID: mustPgUUID(f.AccountID), AppID: mustPgUUID(f.AppID), Scope: f.Scope, Key: v.Key, Value: v.Value})
		}
		if err != nil {
			return mapErr(err)
		}
	}
	for _, secret := range r.Secrets {
		if err := rejectManagedSecretDelete(ctx, tx, q, f.AccountID, f.AppID, f.Scope, secret.Key); err != nil {
			return err
		}
		if secret.Existed {
			n, err := q.RestoreAppConfigSecret(ctx, tx, sqlc.RestoreAppConfigSecretParams{AccountID: mustPgUUID(f.AccountID), AppID: mustPgUUID(f.AppID), Scope: f.Scope, Key: secret.Key, Kid: secret.Kid, Ciphertext: secret.Ciphertext, ValueHash: secret.ValueHash, SecretClass: defaultSecretClass(secret.SecretClass)})
			if err != nil {
				return mapErr(err)
			}
			if n != 1 {
				return ErrConflict
			}
		} else if _, err := deleteAppSecretInScopeTx(ctx, tx, f.AccountID, f.AppID, f.Scope, secret.Key); err != nil {
			return err
		}
	}

	return nil
}

func invalidateConfigRestoreTx(ctx context.Context, tx pgx.Tx, r ConfigRestore) error {
	q, f := sqlc.New(), r.Finish
	if f.Scope != DefaultEnvScope && len(r.Secrets) == 0 {
		_, err := q.InvalidateEnvironmentGitOpsRuntimeConfig(ctx, tx, sqlc.InvalidateEnvironmentGitOpsRuntimeConfigParams{AppID: mustPgUUID(f.AppID), Scope: f.Scope})
		return err
	}
	if err := q.StampConfigRestoreRuntimeChange(ctx, tx, mustPgUUID(f.AppID)); err != nil {
		return err
	}
	return q.InvalidateApplicationStandardSnapshots(ctx, tx, mustPgUUID(f.AppID))
}

func queueConfigRestoreTx(ctx context.Context, tx pgx.Tx, r ConfigRestore) error {
	queue := func(channel, kind, key string) error {
		payload, err := json.Marshal(db.RuntimeConfigChangedPayload{Kind: kind, AppID: r.Finish.AppID, AccountID: r.Finish.AccountID, Scope: r.Finish.Scope, Key: key})
		if err != nil {
			return err
		}
		return sqlc.New().NotifyConfigRestoreChange(ctx, tx, sqlc.NotifyConfigRestoreChangeParams{Channel: channel, Payload: string(payload)})
	}
	for _, v := range r.Variables {
		kind := "set"
		if v.Delete {
			kind = "delete"
		}
		if err := queue(db.NotifyAppEnvChanged, kind, v.Key); err != nil {
			return err
		}
	}
	for _, secret := range r.Secrets {
		kind := "set"
		if !secret.Existed {
			kind = "delete"
		}
		if err := queue(db.NotifySecretRotated, kind, secret.Key); err != nil {
			return err
		}
	}
	if r.Guard != nil {
		claimed, err := sqlc.New().CompareAndSetTrafficAppStatus(ctx, tx, sqlc.CompareAndSetTrafficAppStatusParams{AppID: mustPgUUID(r.Finish.AppID), Prior: string(AppActive), Next: string(AppEvictedCold)})
		if err != nil {
			return err
		}
		if claimed > 0 {
			payload, err := json.Marshal(map[string]string{"app_id": r.Finish.AppID, "wake_id": uuid.NewString()})
			if err != nil {
				return err
			}
			// A process crash after commit must not leave a completed guard
			// with serving instances that still hold the rejected config.
			return db.EnqueueDurableNotificationTx(ctx, tx, db.NotifyRuntimeConfigRestart, string(payload))
		}
	}
	return nil
}
