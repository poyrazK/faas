package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func readinessAttentionIncidentFromSQLC(i sqlc.McpReadinessIncident) readinessIncident {
	v := readinessIncident{ID: pgUUIDString(i.ID), RuleID: pgUUIDString(i.RuleID), AccountID: pgUUIDString(i.AccountID), AppID: pgUUIDString(i.AppID), Scope: i.Scope, Coverage: i.Coverage, OpenedAt: i.OpenedAt.Time, MaintenancePending: i.MaintenancePending}
	if i.ResolvedAt.Valid {
		v.ResolvedAt = &i.ResolvedAt.Time
	}
	return v
}
func readReadinessAttentionPG(ctx context.Context, tx pgx.Tx, id string) (readinessAttentionRecord, error) {
	raw, err := sqlc.New().ReadMCPAttentionSnapshot(ctx, tx, mustPgUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return readinessAttentionRecord{}, nil
	}
	var r readinessAttentionRecord
	if err == nil {
		err = json.Unmarshal(raw, &r)
	}
	return r, err
}
func saveReadinessAttentionPG(ctx context.Context, tx pgx.Tx, id string, r readinessAttentionRecord) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return sqlc.New().SaveMCPAttentionSnapshot(ctx, tx, sqlc.SaveMCPAttentionSnapshotParams{IncidentID: mustPgUUID(id), Snapshot: raw})
}
func reconcileReadinessRemindersPG(ctx context.Context, tx pgx.Tx, rule AlertRule, now time.Time) error {
	q := sqlc.New()
	rows, err := q.ListMCPQueuedReminders(ctx, tx, sqlc.ListMCPQueuedRemindersParams{RuleID: mustPgUUID(rule.ID), AccountID: mustPgUUID(rule.AccountID), AppID: mustPgUUID(rule.AppID)})
	if err != nil {
		return err
	}
	for _, row := range rows {
		e, err := readinessNotificationEventFromSQLC(row.McpReadinessIncidentEvent)
		if err != nil {
			return err
		}
		var r readinessAttentionRecord
		if err = json.Unmarshal(row.Snapshot, &r); err != nil {
			return err
		}
		if reason := readinessReminderSuppression(readinessAttentionIncidentFromSQLC(row.McpReadinessIncident), r, e, now); reason != "" {
			if err = retainReadinessUncertainPrimaryPG(ctx, tx, e, now); err != nil {
				return err
			}
			if err = q.SuppressMCPReminder(ctx, tx, sqlc.SuppressMCPReminderParams{Reason: reason, EventID: row.McpReadinessIncidentEvent.ID}); err != nil {
				return err
			}
		}
	}
	return nil
}
func recordReadinessAttentionObservationPG(ctx context.Context, tx pgx.Tx, rule AlertRule, i *readinessIncident, o MCPReadinessObservation, normal *readinessIncidentEventRecord, windows []api.MCPReadinessMaintenanceWindow) (*readinessIncidentEventRecord, error) {
	var r readinessAttentionRecord
	var err error
	if i != nil {
		r, err = readReadinessAttentionPG(ctx, tx, i.ID)
		if err != nil {
			return nil, err
		}
		r = recordReadinessAttentionObservation(r, *i, o, normal)
		if err = saveReadinessAttentionPG(ctx, tx, i.ID, r); err != nil {
			return nil, err
		}
	}
	if err = reconcileReadinessRemindersPG(ctx, tx, rule, o.At); err != nil {
		return nil, err
	}
	if i == nil {
		return nil, nil
	}
	q := sqlc.New()
	pending, err := q.HasPendingMCPIncidentNotification(ctx, tx, sqlc.HasPendingMCPIncidentNotificationParams{IncidentID: mustPgUUID(i.ID), RuleID: mustPgUUID(rule.ID), AccountID: mustPgUUID(rule.AccountID), AppID: mustPgUUID(rule.AppID)})
	if err != nil {
		return nil, err
	}
	r, event, err := decideReadinessReminder(rule, *i, r, o, normal, pending, windows)
	if err != nil || event == nil {
		return event, err
	}
	if err = q.InsertMCPReadinessReminder(ctx, tx, sqlc.InsertMCPReadinessReminderParams{ID: mustPgUUID(event.ID), IncidentID: mustPgUUID(i.ID), RuleID: mustPgUUID(rule.ID), OccurredAt: pgtype.Timestamptz{Time: o.At, Valid: true}, Payload: event.Payload, ObservedValue: event.Observed, ReminderNumber: int32(event.ReminderNumber)}); err != nil {
		return nil, err
	}
	if err = saveReadinessAttentionPG(ctx, tx, i.ID, r); err != nil {
		return nil, err
	}
	if err = q.MarkMCPReadinessReminderFired(ctx, tx, sqlc.MarkMCPReadinessReminderFiredParams{FiredAt: pgtype.Timestamptz{Time: o.At, Valid: true}, RuleID: mustPgUUID(rule.ID)}); err != nil {
		return nil, err
	}
	return event, nil
}
func (s *PgStore) ListMCPReadinessAttention(ctx context.Context, account, app, scope, after string, limit int, includeResolved bool, now time.Time) (api.MCPReadinessAttentionPage, error) {
	if err := readinessNotificationListArgs(scope, after, limit); err != nil {
		return api.MCPReadinessAttentionPage{}, err
	}
	cursor, err := alertPgUUID(after)
	if err != nil {
		return api.MCPReadinessAttentionPage{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.MCPReadinessAttentionPage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.ReadMCPNotificationOwnedApp(ctx, tx, sqlc.ReadMCPNotificationOwnedAppParams{AppID: mustPgUUID(app), AccountID: mustPgUUID(account)}); err != nil {
		return api.MCPReadinessAttentionPage{}, mapErr(err)
	}
	rows, err := q.ListMCPAttentionIncidents(ctx, tx, sqlc.ListMCPAttentionIncidentsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), Scope: scope, AfterID: cursor, IncludeResolved: includeResolved, PageLimit: int32(limit + 1)})
	if err != nil {
		return api.MCPReadinessAttentionPage{}, err
	}
	out := api.MCPReadinessAttentionPage{Incidents: []api.MCPReadinessIncidentAttention{}}
	if len(rows) > limit {
		rows = rows[:limit]
		out.NextAfter = pgUUIDString(rows[limit-1].McpReadinessIncident.ID)
	}
	for _, row := range rows {
		var r readinessAttentionRecord
		if err = json.Unmarshal(row.Snapshot, &r); err != nil {
			return out, err
		}
		i := readinessAttentionIncidentFromSQLC(row.McpReadinessIncident)
		out.Incidents = append(out.Incidents, readinessAttentionView(i, AlertRule{Name: row.Name, Enabled: row.Enabled}, r, now))
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) ActMCPReadinessAttention(ctx context.Context, account, app, id string, request api.MCPReadinessAttentionAction, now time.Time) (api.MCPReadinessAttentionReceipt, error) {
	request, err := canonicalReadinessAttentionAction(request)
	if err != nil || uuid.Validate(id) != nil {
		return api.MCPReadinessAttentionReceipt{}, ErrInvalidArgument
	}
	id = uuid.MustParse(id).String()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.MCPReadinessAttentionReceipt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockReadinessApp(ctx, tx, account, app); err != nil {
		return api.MCPReadinessAttentionReceipt{}, err
	}
	q := sqlc.New()
	old, err := q.ReadMCPAttentionAction(ctx, tx, sqlc.ReadMCPAttentionActionParams{AppID: mustPgUUID(app), RequestID: mustPgUUID(request.RequestID)})
	if err == nil {
		if pgUUIDString(old.AccountID) != account {
			return api.MCPReadinessAttentionReceipt{}, ErrNotFound
		}
		var prior api.MCPReadinessAttentionAction
		if err = json.Unmarshal(old.Request, &prior); err != nil {
			return api.MCPReadinessAttentionReceipt{}, err
		}
		if pgUUIDString(old.IncidentID) != id || !sameReadinessAttentionAction(prior, request) {
			return api.MCPReadinessAttentionReceipt{}, ErrConflict
		}
		var receipt api.MCPReadinessAttentionReceipt
		if err = json.Unmarshal(old.Receipt, &receipt); err != nil {
			return receipt, err
		}
		return receipt, tx.Commit(ctx)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return api.MCPReadinessAttentionReceipt{}, err
	}
	row, err := q.ReadMCPAttentionIncident(ctx, tx, sqlc.ReadMCPAttentionIncidentParams{IncidentID: mustPgUUID(id), AccountID: mustPgUUID(account), AppID: mustPgUUID(app)})
	if err != nil {
		return api.MCPReadinessAttentionReceipt{}, mapErr(err)
	}
	rule, err := lockReadinessRule(ctx, tx, pgUUIDString(row.RuleID))
	if err != nil {
		return api.MCPReadinessAttentionReceipt{}, err
	}
	if rule.AccountID != account || rule.AppID != app {
		return api.MCPReadinessAttentionReceipt{}, ErrNotFound
	}
	if string(rule.Metric) != api.MetricMCPReadinessUnhealthy || rule.Action != AlertActionWebhook {
		return api.MCPReadinessAttentionReceipt{}, ErrConflict
	}
	r, err := readReadinessAttentionPG(ctx, tx, id)
	if err != nil {
		return api.MCPReadinessAttentionReceipt{}, err
	}
	r, receipt, err := decideReadinessAttentionAction(readinessAttentionIncidentFromSQLC(row), r, request, now)
	if err != nil {
		return receipt, err
	}
	if err = saveReadinessAttentionPG(ctx, tx, id, r); err != nil {
		return receipt, err
	}
	rawRequest, err := json.Marshal(request)
	if err != nil {
		return receipt, err
	}
	rawReceipt, err := json.Marshal(receipt)
	if err != nil {
		return receipt, err
	}
	if err = q.InsertMCPAttentionAction(ctx, tx, sqlc.InsertMCPAttentionActionParams{AppID: mustPgUUID(app), AccountID: mustPgUUID(account), RequestID: mustPgUUID(request.RequestID), IncidentID: mustPgUUID(id), Request: rawRequest, Receipt: rawReceipt, CreatedAt: pgtype.Timestamptz{Time: now, Valid: true}}); err != nil {
		return receipt, err
	}
	if err = reconcileReadinessRemindersPG(ctx, tx, rule, now); err != nil {
		return receipt, err
	}
	return receipt, tx.Commit(ctx)
}
func (s *PgStore) ListMCPReadinessAttentionHistory(ctx context.Context, account, app, id, before string, limit int) (api.MCPReadinessAttentionHistory, error) {
	at, key, err := validateReadinessIncidentList("", before, limit)
	if err != nil || uuid.Validate(id) != nil {
		return api.MCPReadinessAttentionHistory{}, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.MCPReadinessAttentionHistory{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.ReadMCPNotificationOwnedApp(ctx, tx, sqlc.ReadMCPNotificationOwnedAppParams{AppID: mustPgUUID(app), AccountID: mustPgUUID(account)}); err != nil {
		return api.MCPReadinessAttentionHistory{}, mapErr(err)
	}
	if _, err = q.ReadMCPAttentionIncident(ctx, tx, sqlc.ReadMCPAttentionIncidentParams{IncidentID: mustPgUUID(id), AccountID: mustPgUUID(account), AppID: mustPgUUID(app)}); err != nil {
		return api.MCPReadinessAttentionHistory{}, mapErr(err)
	}
	cursorAt := pgtype.Timestamptz{Time: at, Valid: !at.IsZero()}
	cursorID, err := alertPgUUID(key)
	if err != nil {
		return api.MCPReadinessAttentionHistory{}, err
	}
	rows, err := q.ListMCPAttentionActions(ctx, tx, sqlc.ListMCPAttentionActionsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), IncidentID: mustPgUUID(id), BeforeAt: cursorAt, BeforeID: cursorID, PageLimit: int32(limit + 1)})
	if err != nil {
		return api.MCPReadinessAttentionHistory{}, err
	}
	out := api.MCPReadinessAttentionHistory{Actions: []api.MCPReadinessAttentionReceipt{}}
	for n, raw := range rows {
		if n == limit {
			v := out.Actions[n-1]
			out.NextBefore = api.MCPReadinessIncidentCursor(v.CreatedAt, v.RequestID)
			break
		}
		var receipt api.MCPReadinessAttentionReceipt
		if err = json.Unmarshal(raw, &receipt); err != nil {
			return out, err
		}
		out.Actions = append(out.Actions, receipt)
	}
	return out, tx.Commit(ctx)
}
