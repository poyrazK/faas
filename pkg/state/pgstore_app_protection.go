package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const appProtectionColumns = `app_id::text, account_id::text, state, created_at, updated_at, next_reconcile_at`

func scanAppProtection(row pgx.Row) (AppProtection, error) {
	var p AppProtection
	err := row.Scan(&p.AppID, &p.AccountID, &p.State, &p.CreatedAt, &p.UpdatedAt, &p.NextReconcileAt)
	return p, err
}

// GetAppProtection implements AppProtectionStore.
func (s *PgStore) GetAppProtection(ctx context.Context, appID string) (AppProtection, bool, error) {
	p, err := scanAppProtection(s.pool.QueryRow(ctx, `
		select `+appProtectionColumns+`
		  from app_protection
		 where app_id = $1::uuid`, appID))
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
	p, err := scanAppProtection(s.pool.QueryRow(ctx, `
		insert into app_protection (app_id, account_id, state)
		values ($1::uuid, $2::uuid, $3)
		on conflict (app_id) do update
		   set state = excluded.state, updated_at = now(), next_reconcile_at = now()
		returning `+appProtectionColumns,
		appID, accountID, state))
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
	obj, ok := normalizeAppProtectionObject(obj)
	if !ok {
		return ErrInvalidArgument
	}
	tag, err := s.pool.Exec(ctx, `
		insert into app_protection_objects (app_id, item, status, object_id, reason, written)
		values ($1::uuid, $2, $3, $4, $5, $6::jsonb)
		on conflict (app_id, item) do update
		   set status = excluded.status, object_id = excluded.object_id, reason = excluded.reason,
		       written = excluded.written, created_at = now()
		 where app_protection_objects.status <> 'created'`,
		obj.AppID, string(obj.Item), obj.Status, obj.ObjectID, obj.Reason, string(obj.Written))
	if isAppProtectionMissingParent(err) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("state: record app protection object: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

// ListAppProtectionObjects implements AppProtectionStore.
func (s *PgStore) ListAppProtectionObjects(ctx context.Context, appID string) ([]AppProtectionObject, error) {
	rows, err := s.pool.Query(ctx, `
		select app_id::text, item, status, object_id, reason, written, created_at
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
		if err := rows.Scan(&obj.AppID, &item, &obj.Status, &obj.ObjectID, &obj.Reason, &obj.Written, &obj.CreatedAt); err != nil {
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

// ClaimAppProtectionReconciles implements AppProtectionStore.
func (s *PgStore) ClaimAppProtectionReconciles(ctx context.Context, limit int, lease time.Duration) ([]AppProtection, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		update app_protection p
		   set lease_until = now() + $2::interval
		 where p.app_id in (select app_id from app_protection
		                     where state = 'on' and next_reconcile_at <= now()
		                       and (lease_until is null or lease_until <= now())
		                     order by next_reconcile_at, app_id
		                     for update skip locked
		                     limit $1)
		returning `+appProtectionColumns,
		limit, lease)
	if err != nil {
		return nil, fmt.Errorf("state: claim app protection reconciles: %w", err)
	}
	defer rows.Close()
	var out []AppProtection
	for rows.Next() {
		p, err := scanAppProtection(rows)
		if err != nil {
			return nil, fmt.Errorf("state: scan app protection: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: claim app protection reconciles: %w", err)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].NextReconcileAt.Equal(out[j].NextReconcileAt) {
			return out[i].NextReconcileAt.Before(out[j].NextReconcileAt)
		}
		return out[i].AppID < out[j].AppID
	})
	return out, nil
}

// FinishAppProtectionReconcile implements AppProtectionStore.
func (s *PgStore) FinishAppProtectionReconcile(ctx context.Context, appID string, next time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		update app_protection
		   set next_reconcile_at = $2, lease_until = null
		 where app_id = $1::uuid`, appID, next.UTC())
	if err != nil {
		return fmt.Errorf("state: finish app protection reconcile: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ClaimProtectionHealthProbes implements AppProtectionStore.
func (s *PgStore) ClaimProtectionHealthProbes(ctx context.Context, limit int) ([]ProtectionHealthProbe, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		with due as (
			select p.app_id, c.id as check_id
			  from app_protection p
			  join app_protection_objects o
			    on o.app_id = p.app_id and o.item = 'health_check' and o.status = 'created'
			  join synthetic_checks c on c.app_id = p.app_id and c.id::text = o.object_id
			  join apps a on a.id = p.app_id
			 where p.state = 'on' and c.enabled
			   and a.status <> 'deleted' and a.visibility <> 'internal'
			   and (p.health_probe_at is null
			        or p.health_probe_at <= now() - make_interval(secs => c.interval_seconds))
			 order by p.health_probe_at nulls first, p.app_id
			 for update of p skip locked
			 limit $1)
		update app_protection p
		   set health_probe_at = now()
		  from due
		 where p.app_id = due.app_id
		returning p.app_id::text, p.account_id::text, due.check_id::text`, limit)
	if err != nil {
		return nil, fmt.Errorf("state: claim protection health probes: %w", err)
	}
	defer rows.Close()
	var out []ProtectionHealthProbe
	for rows.Next() {
		var p ProtectionHealthProbe
		if err := rows.Scan(&p.AppID, &p.AccountID, &p.CheckID); err != nil {
			return nil, fmt.Errorf("state: scan protection health probe: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: claim protection health probes: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AppID < out[j].AppID })
	return out, nil
}

// isAppProtectionMissingParent reports a foreign-key violation: the app,
// account or enrollment row does not exist.
func isAppProtectionMissingParent(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
