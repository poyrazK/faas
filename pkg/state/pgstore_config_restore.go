package state

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ApplyConfigRestore(ctx context.Context, r ConfigRestore) (ConfigRevision, error) {
	if !r.valid() {
		return ConfigRevision{}, ErrInvalidArgument
	}
	return retryTrafficSerialization(ctx, func() (ConfigRevision, error) { return s.applyConfigRestoreOnce(ctx, r) })
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
	variables, secrets, err := s.configStateTx(ctx, tx, f.AccountID, f.AppID, f.Scope)
	if err != nil {
		return ConfigRevision{}, err
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
