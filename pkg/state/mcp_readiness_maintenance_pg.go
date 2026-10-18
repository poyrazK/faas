package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

const readinessMaintenanceSelect = `id::text,scope,starts_at,ends_at,reason,created_by::text,created_at,cancelled_at,coalesce(cancelled_by::text,''),coalesce(cancel_reason,'')`

func scanReadinessMaintenance(row pgx.Row) (api.MCPReadinessMaintenanceWindow, error) {
	var w api.MCPReadinessMaintenanceWindow
	err := row.Scan(&w.ID, &w.Scope, &w.StartsAt, &w.EndsAt, &w.Reason, &w.CreatedBy, &w.CreatedAt, &w.CancelledAt, &w.CancelledBy, &w.CancelReason)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return w, err
}

// App locks serialize maintenance edits with observations and claims. Always
// acquire app before rule, then event; transport completion only locks event.
func lockReadinessApp(ctx context.Context, tx pgx.Tx, account, app string) error {
	var id string
	err := tx.QueryRow(ctx, `SELECT id::text FROM apps WHERE id=$1 AND account_id=$2 AND status<>'deleted' FOR UPDATE`, app, account).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
func lockReadinessRuleApp(ctx context.Context, tx pgx.Tx, id string) error {
	var app, account string
	err := tx.QueryRow(ctx, `SELECT app_id::text,account_id::text FROM alert_rules WHERE id=$1`, id).Scan(&app, &account)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return lockReadinessApp(ctx, tx, account, app)
}
func readinessMaintenanceWindowsPG(ctx context.Context, tx pgx.Tx, account, app string) ([]api.MCPReadinessMaintenanceWindow, error) {
	rows, err := tx.Query(ctx, `SELECT `+readinessMaintenanceSelect+` FROM mcp_readiness_maintenance_windows WHERE account_id=$1 AND app_id=$2`, account, app)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.MCPReadinessMaintenanceWindow{}
	for rows.Next() {
		w, err := scanReadinessMaintenance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
func (s *PgStore) CreateMCPReadinessMaintenance(ctx context.Context, account, app string, r api.CreateMCPReadinessMaintenanceRequest, now time.Time) (api.MCPReadinessMaintenanceWindow, error) {
	if api.ValidateMCPReadinessMaintenance(r, now) != nil {
		return api.MCPReadinessMaintenanceWindow{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.MCPReadinessMaintenanceWindow{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockReadinessApp(ctx, tx, account, app); err != nil {
		return api.MCPReadinessMaintenanceWindow{}, err
	}
	if r.StartsAt.IsZero() {
		r.StartsAt = now
	}
	w, err := scanReadinessMaintenance(tx.QueryRow(ctx, `INSERT INTO mcp_readiness_maintenance_windows(id,account_id,app_id,scope,starts_at,ends_at,reason,created_by,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$2,$8) RETURNING `+readinessMaintenanceSelect, uuid.NewString(), account, app, api.MCPConnectionScope(r.Scope), r.StartsAt, r.EndsAt, r.Reason, now))
	if err != nil {
		return w, err
	}
	return w, tx.Commit(ctx)
}
func (s *PgStore) CancelMCPReadinessMaintenance(ctx context.Context, account, app, id, reason string, now time.Time) (api.MCPReadinessMaintenanceWindow, error) {
	if uuid.Validate(id) != nil || !api.ValidMCPMaintenanceReason(reason) {
		return api.MCPReadinessMaintenanceWindow{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.MCPReadinessMaintenanceWindow{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockReadinessApp(ctx, tx, account, app); err != nil {
		return api.MCPReadinessMaintenanceWindow{}, err
	}
	w, err := scanReadinessMaintenance(tx.QueryRow(ctx, `SELECT `+readinessMaintenanceSelect+` FROM mcp_readiness_maintenance_windows WHERE id=$1 AND account_id=$2 AND app_id=$3 FOR UPDATE`, id, account, app))
	if err != nil {
		return w, err
	}
	if w.CancelledAt != nil {
		return w, nil
	}
	if !now.Before(w.EndsAt) {
		return w, ErrConflict
	}
	w, err = scanReadinessMaintenance(tx.QueryRow(ctx, `UPDATE mcp_readiness_maintenance_windows SET cancelled_at=$2,cancelled_by=$3,cancel_reason=$4 WHERE id=$1 RETURNING `+readinessMaintenanceSelect, id, now, account, reason))
	if err != nil {
		return w, err
	}
	return w, tx.Commit(ctx)
}
func (s *PgStore) ListMCPReadinessMaintenance(ctx context.Context, account, app, scope, before string, limit int) (api.MCPReadinessMaintenancePage, error) {
	at, id, err := maintenanceListArgs(scope, before, limit)
	if err != nil {
		return api.MCPReadinessMaintenancePage{}, err
	}
	owned, err := s.AppByID(ctx, app)
	if err != nil {
		return api.MCPReadinessMaintenancePage{}, err
	}
	if owned.AccountID != account || owned.Status == AppDeleted {
		return api.MCPReadinessMaintenancePage{}, ErrNotFound
	}
	var cursorAt, cursorID any
	if !at.IsZero() {
		cursorAt = at
		cursorID = id
	}
	rows, err := s.pool.Query(ctx, `SELECT `+readinessMaintenanceSelect+` FROM mcp_readiness_maintenance_windows WHERE account_id=$1 AND app_id=$2 AND scope=$3 AND ($4::timestamptz IS NULL OR (created_at,id)<($4::timestamptz,$5::uuid)) ORDER BY created_at DESC,id DESC LIMIT $6`, account, app, api.MCPConnectionScope(scope), cursorAt, cursorID, limit+1)
	if err != nil {
		return api.MCPReadinessMaintenancePage{}, err
	}
	defer rows.Close()
	out := api.MCPReadinessMaintenancePage{Windows: []api.MCPReadinessMaintenanceWindow{}}
	for rows.Next() {
		w, err := scanReadinessMaintenance(rows)
		if err != nil {
			return out, err
		}
		if len(out.Windows) == limit {
			last := out.Windows[limit-1]
			out.NextBefore = api.MCPReadinessIncidentCursor(last.CreatedAt, last.ID)
			break
		}
		out.Windows = append(out.Windows, w)
	}
	return out, rows.Err()
}
func suppressReadinessQueuePG(ctx context.Context, tx pgx.Tx, rule AlertRule, windows []api.MCPReadinessMaintenanceWindow, now time.Time) ([]readinessIncidentEventRecord, error) {
	if len(windows) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `SELECT `+readinessEventSelect+` FROM mcp_readiness_incident_events WHERE rule_id=$1 AND incident_id IN(SELECT id FROM mcp_readiness_incidents WHERE account_id=$2 AND app_id=$3) AND (delivery_status NOT IN ('delivered','suppressed','abandoned') OR incident_id IN(SELECT id FROM mcp_readiness_incidents WHERE resolved_at IS NULL)) ORDER BY sequence FOR UPDATE`, rule.ID, rule.AccountID, rule.AppID)
	if err != nil {
		return nil, err
	}
	events := []readinessIncidentEventRecord{}
	for rows.Next() {
		e, err := scanReadinessEvent(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		events = append(events, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range events {
		e := &events[i]
		if readinessNotificationTerminal(e.Status) || e.LeaseToken != "" && now.Before(e.LeaseUntil) {
			continue
		}
		ids := readinessQueuedMaintenanceIDs(rule, *e, windows, now)
		if len(ids) == 0 {
			continue
		}
		if err = retainReadinessUncertainPrimaryPG(ctx, tx, *e, now); err != nil {
			return nil, err
		}
		suppressReadinessEvent(e, ids, "maintenance")
		raw, _ := json.Marshal(ids)
		if _, err = tx.Exec(ctx, `UPDATE mcp_readiness_incident_events SET delivery_version=delivery_version+1,delivery_status='suppressed',maintenance_window_ids=$2,suppression_reason='maintenance',lease_token=NULL,lease_until=NULL WHERE id=$1`, e.ID, raw); err != nil {
			return nil, err
		}
		if readinessSuppressionNeedsReconciliation(rule, *e, events, windows, now) {
			if _, err = tx.Exec(ctx, `UPDATE mcp_readiness_incidents SET maintenance_pending=true WHERE id=$1 AND resolved_at IS NULL`, e.IncidentID); err != nil {
				return nil, err
			}
		}
	}
	return events, nil
}
