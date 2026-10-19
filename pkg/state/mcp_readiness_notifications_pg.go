package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func readinessNotificationEventFromSQLC(row sqlc.McpReadinessIncidentEvent) (readinessIncidentEventRecord, error) {
	e := readinessIncidentEventRecord{ID: pgUUIDString(row.ID), IncidentID: pgUUIDString(row.IncidentID), RuleID: pgUUIDString(row.RuleID), Kind: row.Kind, Sequence: row.Sequence, At: row.OccurredAt.Time, Payload: row.Payload, Observed: row.ObservedValue, Status: row.DeliveryStatus, Attempts: int(row.AttemptCount), AvailableAt: row.AvailableAt.Time, LeaseToken: pgUUIDString(row.LeaseToken), LeaseUntil: row.LeaseUntil.Time, DeliveryID: pgUUIDString(row.LastDeliveryID), LastStatusCode: int(row.LastStatusCode.Int32), SuppressionReason: row.SuppressionReason, NotificationReason: row.NotificationReason, Version: row.DeliveryVersion, ReminderNumber: int(row.ReminderNumber)}
	if err := json.Unmarshal(row.DestinationDeliveries, &e.Destinations); err != nil {
		return e, err
	}
	if err := json.Unmarshal(row.DeliveryRouting, &e.Routing); err != nil {
		return e, err
	}
	if row.LastAttemptAt.Valid {
		at := row.LastAttemptAt.Time
		e.LastAttemptAt = &at
	}
	if err := json.Unmarshal(row.MaintenanceWindowIds, &e.MaintenanceWindowIDs); err != nil {
		return e, fmt.Errorf("readiness maintenance IDs: %w", err)
	}
	if len(row.LastOperatorAction) > 0 {
		if err := json.Unmarshal(row.LastOperatorAction, &e.LastOperatorAction); err != nil {
			return e, fmt.Errorf("readiness operator receipt: %w", err)
		}
	}
	return e, nil
}
func (s *PgStore) ListMCPReadinessNotificationQueues(ctx context.Context, account, app, scope, after string, limit int, now time.Time) (api.MCPReadinessNotificationQueues, error) {
	if err := readinessNotificationListArgs(scope, after, limit); err != nil {
		return api.MCPReadinessNotificationQueues{}, err
	}
	cursor, err := alertPgUUID(after)
	if err != nil {
		return api.MCPReadinessNotificationQueues{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.MCPReadinessNotificationQueues{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	accountID, appID := mustPgUUID(account), mustPgUUID(app)
	if _, err = q.ReadMCPNotificationOwnedApp(ctx, tx, sqlc.ReadMCPNotificationOwnedAppParams{AppID: appID, AccountID: accountID}); err != nil {
		return api.MCPReadinessNotificationQueues{}, mapErr(err)
	}
	rules, err := q.ListMCPNotificationRules(ctx, tx, sqlc.ListMCPNotificationRulesParams{AccountID: accountID, AppID: appID, Scope: scope, AfterID: cursor, PageLimit: int32(limit + 1)})
	if err != nil {
		return api.MCPReadinessNotificationQueues{}, err
	}
	out := api.MCPReadinessNotificationQueues{ObservedAt: now, Queues: []api.MCPReadinessNotificationQueue{}}
	if len(rules) > limit {
		rules = rules[:limit]
		out.NextAfter = pgUUIDString(rules[limit-1].ID)
	}
	windows, err := readinessMaintenanceWindowsPG(ctx, tx, account, app)
	if err != nil {
		return out, err
	}
	for _, row := range rules {
		rule := customerAlertRule(row)
		events, incidents, err := readReadinessOutstandingPG(ctx, tx, rule)
		if err != nil {
			return out, err
		}
		policy, err := readReadinessFallbackPG(ctx, tx, rule.ID)
		if err != nil {
			return out, err
		}
		out.Queues = append(out.Queues, readinessNotificationQueues(rule, policy, events, incidents, now, windows))
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) ActMCPReadinessNotification(ctx context.Context, account, app string, r api.MCPReadinessNotificationAction, now time.Time) (api.MCPReadinessNotificationReceipt, error) {
	r, err := canonicalReadinessNotificationAction(r)
	if err != nil {
		return api.MCPReadinessNotificationReceipt{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.MCPReadinessNotificationReceipt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockReadinessApp(ctx, tx, account, app); err != nil {
		return api.MCPReadinessNotificationReceipt{}, err
	}
	q := sqlc.New()
	accountID, appID := mustPgUUID(account), mustPgUUID(app)
	old, err := q.ReadMCPNotificationAction(ctx, tx, sqlc.ReadMCPNotificationActionParams{AppID: appID, RequestID: mustPgUUID(r.RequestID)})
	if err == nil {
		if pgUUIDString(old.AccountID) != account {
			return api.MCPReadinessNotificationReceipt{}, ErrNotFound
		}
		prior := api.MCPReadinessNotificationAction{Destination: old.Destination, RequestID: r.RequestID, EventID: pgUUIDString(old.EventID), Action: old.Action, ExpectedVersion: &old.ExpectedVersion, Reason: old.Reason}
		if !sameReadinessNotificationAction(prior, r) {
			return api.MCPReadinessNotificationReceipt{}, ErrConflict
		}
		var receipt api.MCPReadinessNotificationReceipt
		if err = json.Unmarshal(old.Receipt, &receipt); err != nil {
			return receipt, err
		}
		return receipt, tx.Commit(ctx)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return api.MCPReadinessNotificationReceipt{}, err
	}
	ruleID, err := q.ReadMCPNotificationEventRule(ctx, tx, sqlc.ReadMCPNotificationEventRuleParams{EventID: mustPgUUID(r.EventID), AccountID: accountID, AppID: appID})
	if err != nil {
		return api.MCPReadinessNotificationReceipt{}, mapErr(err)
	}
	rule, err := lockReadinessRule(ctx, tx, pgUUIDString(ruleID))
	if err != nil {
		return api.MCPReadinessNotificationReceipt{}, err
	}
	if rule.AccountID != account || rule.AppID != app {
		return api.MCPReadinessNotificationReceipt{}, ErrNotFound
	}
	events, incidents, err := readReadinessOutstandingPG(ctx, tx, rule)
	if err != nil {
		return api.MCPReadinessNotificationReceipt{}, err
	}
	policy, err := readReadinessFallbackPG(ctx, tx, rule.ID)
	if err != nil {
		return api.MCPReadinessNotificationReceipt{}, err
	}
	e, err := readinessNotificationActionEvent(rule, policy, events, incidents, r)
	if err != nil {
		return api.MCPReadinessNotificationReceipt{}, err
	}
	windows, err := readinessMaintenanceWindowsPG(ctx, tx, account, app)
	if err != nil {
		return api.MCPReadinessNotificationReceipt{}, err
	}
	uncertain := readinessActionPrimaryUncertain(e, r, now)
	e, receipt, err := decideReadinessNotificationAction(rule, e, r, now, windows)
	if err != nil {
		return receipt, err
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return receipt, err
	}
	if uncertain {
		route := incidents[e.IncidentID].Routing
		route.PrimaryInformed = true
		if err = saveReadinessRoutingPG(ctx, tx, e.IncidentID, route); err != nil {
			return receipt, err
		}
	}
	if err = saveReadinessDestinationEventPG(ctx, tx, e); err != nil {
		return receipt, err
	}
	if err = q.InsertMCPNotificationAction(ctx, tx, sqlc.InsertMCPNotificationActionParams{Destination: r.Destination, AppID: appID, AccountID: accountID, RequestID: mustPgUUID(r.RequestID), EventID: mustPgUUID(e.ID), Action: r.Action, ExpectedVersion: *r.ExpectedVersion, Reason: r.Reason, Receipt: raw}); err != nil {
		return receipt, err
	}
	return receipt, tx.Commit(ctx)
}
