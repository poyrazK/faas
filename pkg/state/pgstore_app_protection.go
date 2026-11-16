package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// GetAppProtection implements AppProtectionStore.
func (s *PgStore) GetAppProtection(ctx context.Context, appID string) (AppProtection, bool, error) {
	var p AppProtection
	err := s.pool.QueryRow(ctx, `
		select app_id::text, account_id::text, state, created_at, updated_at
		  from app_protection
		 where app_id = $1::uuid`, appID).
		Scan(&p.AppID, &p.AccountID, &p.State, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppProtection{}, false, nil
	}
	if err != nil {
		return AppProtection{}, false, fmt.Errorf("state: get app protection: %w", err)
	}
	return p, true, nil
}

// SetAppProtection implements AppProtectionStore.
func (s *PgStore) SetAppProtection(ctx context.Context, appID, accountID, state string) (AppProtection, error) {
	if state != AppProtectionOn && state != AppProtectionOff {
		return AppProtection{}, ErrInvalidArgument
	}
	var p AppProtection
	err := s.pool.QueryRow(ctx, `
		insert into app_protection (app_id, account_id, state)
		values ($1::uuid, $2::uuid, $3)
		on conflict (app_id) do update
		   set state = excluded.state, updated_at = now()
		returning app_id::text, account_id::text, state, created_at, updated_at`,
		appID, accountID, state).
		Scan(&p.AppID, &p.AccountID, &p.State, &p.CreatedAt, &p.UpdatedAt)
	if isAppProtectionMissingParent(err) {
		return AppProtection{}, ErrNotFound
	}
	if err != nil {
		return AppProtection{}, fmt.Errorf("state: set app protection: %w", err)
	}
	return p, nil
}

// RecordAppProtectionObject implements AppProtectionStore.
func (s *PgStore) RecordAppProtectionObject(ctx context.Context, obj AppProtectionObject) error {
	if !validAppProtectionObject(obj) || !json.Valid(appProtectionWritten(obj.Written)) {
		return ErrInvalidArgument
	}
	_, err := s.pool.Exec(ctx, `
		insert into app_protection_objects (app_id, item, object_id, written)
		values ($1::uuid, $2, $3, $4::jsonb)
		on conflict (app_id, item) do update
		   set object_id = excluded.object_id, written = excluded.written, created_at = now()`,
		obj.AppID, string(obj.Item), obj.ObjectID, string(appProtectionWritten(obj.Written)))
	if isAppProtectionMissingParent(err) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("state: record app protection object: %w", err)
	}
	return nil
}

// ListAppProtectionObjects implements AppProtectionStore.
func (s *PgStore) ListAppProtectionObjects(ctx context.Context, appID string) ([]AppProtectionObject, error) {
	rows, err := s.pool.Query(ctx, `
		select app_id::text, item, object_id, written, created_at
		  from app_protection_objects
		 where app_id = $1::uuid`, appID)
	if err != nil {
		return nil, fmt.Errorf("state: list app protection objects: %w", err)
	}
	defer rows.Close()
	var out []AppProtectionObject
	for rows.Next() {
		var obj AppProtectionObject
		var item string
		if err := rows.Scan(&obj.AppID, &item, &obj.ObjectID, &obj.Written, &obj.CreatedAt); err != nil {
			return nil, fmt.Errorf("state: scan app protection object: %w", err)
		}
		obj.Item = AppProtectionItem(item)
		out = append(out, obj)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list app protection objects: %w", err)
	}
	sort.Slice(out, func(i, j int) bool {
		return appProtectionItemRank(out[i].Item) < appProtectionItemRank(out[j].Item)
	})
	return out, nil
}

// isAppProtectionMissingParent reports a foreign-key violation: the app,
// account or enrollment row does not exist.
func isAppProtectionMissingParent(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
