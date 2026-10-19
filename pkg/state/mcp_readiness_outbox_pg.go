package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func lockReadinessRule(ctx context.Context, tx pgx.Tx, id string) (AlertRule, error) {
	var key string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM alert_rules WHERE id=$1 FOR UPDATE`, id).Scan(&key); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AlertRule{}, ErrNotFound
		}
		return AlertRule{}, err
	}
	row, err := sqlc.New().ReadCustomerAlertRule(ctx, tx, mustPgUUID(id))
	if err != nil {
		return AlertRule{}, err
	}
	return customerAlertRule(row), nil
}
func (s *PgStore) RecordMCPReadinessObservation(ctx context.Context, o MCPReadinessObservation) (MCPReadinessTransition, error) {
	if err := validateReadinessObservation(o); err != nil {
		return MCPReadinessTransition{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MCPReadinessTransition{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockReadinessRuleApp(ctx, tx, o.Rule.ID); err != nil {
		return MCPReadinessTransition{}, err
	}
	rule, err := lockReadinessRule(ctx, tx, o.Rule.ID)
	if err != nil {
		return MCPReadinessTransition{}, err
	}
	if !sameReadinessObservationRule(rule, o.Rule) {
		return MCPReadinessTransition{}, ErrConflict
	}
	var last time.Time
	err = tx.QueryRow(ctx, `SELECT last_observed_at FROM mcp_readiness_incident_cursors WHERE rule_id=$1`, rule.ID).Scan(&last)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return MCPReadinessTransition{}, err
	}
	if !o.At.After(last) {
		return MCPReadinessTransition{}, nil
	}
	windows, err := readinessMaintenanceWindowsPG(ctx, tx, rule.AccountID, rule.AppID)
	if err != nil {
		return MCPReadinessTransition{}, err
	}
	events, err := suppressReadinessQueuePG(ctx, tx, rule, windows, o.At)
	if err != nil {
		return MCPReadinessTransition{}, err
	}
	var active *readinessIncident
	var v readinessIncident
	err = tx.QueryRow(ctx, `SELECT id::text,rule_id::text,account_id::text,app_id::text,scope,coverage,opened_at,resolved_at,maintenance_pending FROM mcp_readiness_incidents WHERE rule_id=$1 AND resolved_at IS NULL`, rule.ID).Scan(&v.ID, &v.RuleID, &v.AccountID, &v.AppID, &v.Scope, &v.Coverage, &v.OpenedAt, &v.ResolvedAt, &v.MaintenancePending)
	if err == nil {
		active = &v
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return MCPReadinessTransition{}, err
	}
	if active != nil && (active.AccountID != rule.AccountID || active.AppID != rule.AppID) {
		return MCPReadinessTransition{}, ErrConflict
	}
	if active != nil && readinessMaintenanceSince(rule, o.Payload, windows, last, o.At) {
		active.MaintenancePending = true
	}
	event, incident, to, err := readinessMaintenanceTransition(rule, o, active, events, windows)
	if err != nil {
		return MCPReadinessTransition{}, err
	}
	out := MCPReadinessTransition{}
	if event != nil {
		if o.Breached && active == nil {
			_, err = tx.Exec(ctx, `INSERT INTO mcp_readiness_incidents(id,rule_id,account_id,app_id,scope,coverage,opened_at,maintenance_pending) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, incident.ID, rule.ID, rule.AccountID, rule.AppID, rule.MCPReadinessScope, incident.Coverage, incident.OpenedAt, incident.MaintenancePending)
		} else {
			_, err = tx.Exec(ctx, `UPDATE mcp_readiness_incidents SET resolved_at=$2 WHERE id=$1`, incident.ID, incident.ResolvedAt)
		}
		if err != nil {
			return out, err
		}
		if active == nil && o.Breached {
			if err = initializeReadinessReminderDefaultsPG(ctx, tx, rule.ID, incident.ID); err != nil {
				return out, err
			}
		}
		ids, _ := json.Marshal(append([]string{}, event.MaintenanceWindowIDs...))
		_, err = tx.Exec(ctx, `INSERT INTO mcp_readiness_incident_events(id,incident_id,rule_id,kind,occurred_at,payload,observed_value,available_at,delivery_status,maintenance_window_ids,suppression_reason,notification_reason,delivery_version) VALUES($1,$2,$3,$4,$5,$6,$7,$5,$8,$9,$10,$11,$12)`, event.ID, incident.ID, rule.ID, event.Kind, event.At, []byte(event.Payload), event.Observed, event.Status, ids, event.SuppressionReason, event.NotificationReason, event.Version)
		if err != nil {
			return out, err
		}
		out = MCPReadinessTransition{EventID: event.ID, IncidentID: incident.ID, Event: event.Kind}
	}
	if incident != nil {
		if _, err = tx.Exec(ctx, `UPDATE mcp_readiness_incidents SET maintenance_pending=$2 WHERE id=$1`, incident.ID, incident.MaintenancePending); err != nil {
			return out, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO mcp_readiness_incident_cursors(rule_id,last_observed_at) VALUES($1,$2) ON CONFLICT(rule_id) DO UPDATE SET last_observed_at=excluded.last_observed_at`, rule.ID, o.At)
	if err != nil {
		return out, err
	}
	_, err = tx.Exec(ctx, `UPDATE alert_rules SET state=$2,last_evaluated_at=$3,updated_at=$3,last_fired_at=CASE WHEN $4 THEN $3 ELSE last_fired_at END WHERE id=$1`, rule.ID, string(to), o.At, event != nil && o.Breached)
	if err != nil {
		return out, err
	}
	reminder, err := recordReadinessAttentionObservationPG(ctx, tx, rule, incident, o, event, windows)
	if err != nil {
		return MCPReadinessTransition{}, err
	}
	if reminder != nil {
		out = MCPReadinessTransition{EventID: reminder.ID, IncidentID: reminder.IncidentID, Event: reminder.Kind}
	}
	if err = tx.Commit(ctx); err != nil {
		return MCPReadinessTransition{}, err
	}
	return out, nil
}

const readinessEventSelect = `id::text,incident_id::text,rule_id::text,kind,sequence,occurred_at,payload,observed_value,delivery_status,attempt_count,available_at,coalesce(lease_token::text,''),lease_until,coalesce(last_delivery_id::text,''),last_attempt_at,coalesce(last_status_code,0),maintenance_window_ids,suppression_reason,notification_reason,delivery_version,last_operator_action,reminder_number,delivery_routing,destination_deliveries`

func scanReadinessEvent(row pgx.Row, extra ...any) (readinessIncidentEventRecord, error) {
	var e readinessIncidentEventRecord
	var until *time.Time
	args := []any{&e.ID, &e.IncidentID, &e.RuleID, &e.Kind, &e.Sequence, &e.At, &e.Payload, &e.Observed, &e.Status, &e.Attempts, &e.AvailableAt, &e.LeaseToken, &until, &e.DeliveryID, &e.LastAttemptAt, &e.LastStatusCode, &e.MaintenanceWindowIDs, &e.SuppressionReason, &e.NotificationReason, &e.Version, &e.LastOperatorAction, &e.ReminderNumber, &e.Routing, &e.Destinations}
	err := row.Scan(append(args, extra...)...)
	if until != nil {
		e.LeaseUntil = *until
	}
	return e, err
}
func (s *PgStore) ClaimMCPReadinessDelivery(ctx context.Context, ruleID string, now time.Time) (MCPReadinessDeliveryClaim, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MCPReadinessDeliveryClaim{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockReadinessRuleApp(ctx, tx, ruleID); err != nil {
		return MCPReadinessDeliveryClaim{}, false, err
	}
	rule, err := lockReadinessRule(ctx, tx, ruleID)
	if err != nil {
		return MCPReadinessDeliveryClaim{}, false, err
	}
	if !sameReadinessObservationRule(rule, rule) {
		return MCPReadinessDeliveryClaim{}, false, nil
	}
	windows, err := readinessMaintenanceWindowsPG(ctx, tx, rule.AccountID, rule.AppID)
	if err != nil {
		return MCPReadinessDeliveryClaim{}, false, err
	}
	if _, err = suppressReadinessQueuePG(ctx, tx, rule, windows, now); err != nil {
		return MCPReadinessDeliveryClaim{}, false, err
	}
	if err = reconcileReadinessRemindersPG(ctx, tx, rule, now); err != nil {
		return MCPReadinessDeliveryClaim{}, false, err
	}
	events, incidents, err := readReadinessOutstandingPG(ctx, tx, rule)
	if err != nil {
		return MCPReadinessDeliveryClaim{}, false, err
	}
	policy, err := readReadinessFallbackPG(ctx, tx, rule.ID)
	if err != nil {
		return MCPReadinessDeliveryClaim{}, false, err
	}
	for _, c := range readinessDestinationPlan(rule, policy, events, incidents) {
		if !readyReadinessDestination(c, now) {
			continue
		}
		claim := claimReadinessDestination(rule, policy, &c, now)
		delivery := claim.Delivery
		_, err = sqlc.New().InsertMCPRouteDelivery(ctx, tx, sqlc.InsertMCPRouteDeliveryParams{ID: mustPgUUID(delivery.ID), RuleID: mustPgUUID(rule.ID), AccountID: mustPgUUID(rule.AccountID), AppID: mustPgUUID(rule.AppID), IdempotencyKey: delivery.IdempotencyKey, Payload: delivery.Payload, ObservedValue: delivery.ObservedValue, FiredAt: pgtype.Timestamptz{Time: now, Valid: true}, ReadinessEventID: c.Event.ID, ReadinessDestination: delivery.ReadinessDestination, ReadinessFailoverReason: delivery.ReadinessFailoverReason})
		if err != nil {
			return MCPReadinessDeliveryClaim{}, false, err
		}
		if err = saveReadinessDestinationEventPG(ctx, tx, c.Event); err != nil {
			return MCPReadinessDeliveryClaim{}, false, err
		}
		if err = saveReadinessRoutingPG(ctx, tx, c.Event.IncidentID, c.Route); err != nil {
			return MCPReadinessDeliveryClaim{}, false, err
		}
		return claim, true, tx.Commit(ctx)
	}
	return MCPReadinessDeliveryClaim{}, false, tx.Commit(ctx)
}
func readReadinessOutstandingPG(ctx context.Context, tx pgx.Tx, rule AlertRule) ([]readinessIncidentEventRecord, map[string]readinessIncident, error) {
	rows, err := sqlc.New().ListMCPDestinationEvents(ctx, tx, sqlc.ListMCPDestinationEventsParams{RuleID: mustPgUUID(rule.ID), AccountID: mustPgUUID(rule.AccountID), AppID: mustPgUUID(rule.AppID)})
	if err != nil {
		return nil, nil, err
	}
	events := []readinessIncidentEventRecord{}
	incidents := map[string]readinessIncident{}
	for _, row := range rows {
		e, err := readinessNotificationEventFromSQLC(row.McpReadinessIncidentEvent)
		if err != nil {
			return nil, nil, err
		}
		i := readinessAttentionIncidentFromSQLC(row.McpReadinessIncident)
		if err = json.Unmarshal(row.McpReadinessIncident.DeliveryRouting, &i.Routing); err != nil {
			return nil, nil, err
		}
		events = append(events, e)
		incidents[i.ID] = i
	}
	return events, incidents, nil
}
func saveReadinessDestinationEventPG(ctx context.Context, tx pgx.Tx, e readinessIncidentEventRecord) error {
	routing, err := json.Marshal(e.Routing)
	if err != nil {
		return err
	}
	destinations, err := json.Marshal(e.Destinations)
	if err != nil {
		return err
	}
	if e.Destinations == nil {
		destinations = []byte("{}")
	}
	receipt, err := json.Marshal(e.LastOperatorAction)
	if err != nil {
		return err
	}
	if e.LastOperatorAction == nil {
		receipt = nil
	}
	token, err := alertPgUUID(e.LeaseToken)
	if err != nil {
		return err
	}
	delivery, err := alertPgUUID(e.DeliveryID)
	if err != nil {
		return err
	}
	at := pgtype.Timestamptz{}
	if e.LastAttemptAt != nil {
		at = pgtype.Timestamptz{Time: *e.LastAttemptAt, Valid: true}
	}
	return sqlc.New().SaveMCPDestinationEvent(ctx, tx, sqlc.SaveMCPDestinationEventParams{ID: mustPgUUID(e.ID), DeliveryVersion: e.Version, AttemptCount: int32(e.Attempts), DeliveryStatus: e.Status, AvailableAt: pgtype.Timestamptz{Time: e.AvailableAt, Valid: true}, LeaseToken: token, LeaseUntil: pgtype.Timestamptz{Time: e.LeaseUntil, Valid: e.LeaseToken != ""}, LastDeliveryID: delivery, LastAttemptAt: at, LastStatusCode: pgtype.Int4{Int32: int32(e.LastStatusCode), Valid: e.LastStatusCode != 0}, DeliveryRouting: routing, DestinationDeliveries: destinations, LastOperatorAction: receipt})
}

func (s *PgStore) CompleteMCPReadinessDelivery(ctx context.Context, eventID, token string, now time.Time) error {
	return s.completeReadinessDelivery(ctx, eventID, token, now, nil)
}
func (s *PgStore) completeReadinessDelivery(ctx context.Context, eventID, token string, now time.Time, result *MCPReadinessProviderResult) error {
	if token == "" {
		return ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	ruleID, err := q.ReadMCPDeliveryRuleID(ctx, tx, mustPgUUID(eventID))
	if err != nil {
		return mapErr(err)
	}
	if err = lockReadinessRuleApp(ctx, tx, pgUUIDString(ruleID)); err != nil {
		return err
	}
	rule, err := lockReadinessRule(ctx, tx, pgUUIDString(ruleID))
	if err != nil {
		return err
	}
	row, err := q.ReadMCPDeliveryEvent(ctx, tx, mustPgUUID(eventID))
	if err != nil {
		return mapErr(err)
	}
	event, err := readinessNotificationEventFromSQLC(row)
	if err != nil {
		return err
	}
	route, err := readReadinessRoutingPG(ctx, tx, event.IncidentID)
	if err != nil {
		return err
	}
	prepareReadinessDestinations(&event, route)
	destination, target, err := readinessDestinationForToken(event, token, now)
	if err != nil {
		return err
	}
	if result != nil {
		if target.Provider != "pagerduty" {
			return ErrConflict
		}
		delivery := applyReadinessProviderResult(AlertDelivery{}, *result, now)
		done := pgtype.Timestamptz{Time: now, Valid: result.Accepted}
		if err = q.CompleteMCPProviderDelivery(ctx, tx, sqlc.CompleteMCPProviderDeliveryParams{ID: mustPgUUID(target.DeliveryID), Status: string(delivery.Status), StatusCode: pgtype.Int4{Int32: int32(result.StatusCode), Valid: true}, LastError: pgtype.Text{String: result.Failure, Valid: true}, DeliveredAt: done}); err != nil {
			return err
		}
	}
	stored, err := q.ReadMCPRouteDeliveryResult(ctx, tx, mustPgUUID(target.DeliveryID))
	if err != nil {
		return err
	}
	if stored.Status == "pending" {
		stored.Status = "failed"
		if err = q.FailIncompleteMCPRouteDelivery(ctx, tx, mustPgUUID(target.DeliveryID)); err != nil {
			return err
		}
	}
	readinessProviderUncertain(&route, event, destination, result)
	finishReadinessDestination(&route, &event, destination, AlertDelivery{Status: AlertDeliveryStatus(stored.Status), LastStatusCode: int(stored.LastStatusCode.Int32)}, now, rule.CooldownMinutes)
	readinessProviderRetry(&event, destination, result)
	if err = saveReadinessDestinationEventPG(ctx, tx, event); err != nil {
		return err
	}
	if err = saveReadinessRoutingPG(ctx, tx, event.IncidentID, route); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
