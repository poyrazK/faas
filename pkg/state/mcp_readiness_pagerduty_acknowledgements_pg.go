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

func readReadinessPagerDutyAcknowledgementsPG(ctx context.Context, db sqlc.DBTX, id string, destinations ...string) (MCPReadinessPagerDutyAcknowledgementConfig, error) {
	destination, err := readinessPagerDutyAcknowledgementSelector(destinations)
	if err != nil {
		return MCPReadinessPagerDutyAcknowledgementConfig{}, err
	}
	row, err := sqlc.New().ReadMCPPagerDutyAcknowledgements(ctx, db, sqlc.ReadMCPPagerDutyAcknowledgementsParams{RuleID: mustPgUUID(id), Destination: destination})
	if errors.Is(err, pgx.ErrNoRows) {
		legacy := true
		if destination != "" {
			c, readErr := readReadinessPagerDutyAcknowledgementsPG(ctx, db, id)
			if readErr != nil {
				return c, readErr
			}
			legacy = c.Version == 0
		}
		if destination == "" {
			destination = "primary"
		}
		return MCPReadinessPagerDutyAcknowledgementConfig{Destination: destination, LegacyReceiver: legacy}, nil
	}
	if err != nil {
		return MCPReadinessPagerDutyAcknowledgementConfig{}, err
	}
	return MCPReadinessPagerDutyAcknowledgementConfig{SyncUnacknowledgements: row.SyncUnacknowledgements, LegacyReceiver: row.LegacyReceiver, Destination: row.Destination, FallbackVersion: row.FallbackVersion, Version: row.Version, Enabled: row.Enabled, SigningSecretSealed: row.SigningSecretSealed, PagerDutyVersion: row.PagerdutyVersion, Region: row.Region, Fingerprint: row.Fingerprint, ServiceID: row.ServiceID}, nil
}
func ownedReadinessAcknowledgementPG(ctx context.Context, db sqlc.DBTX, account, app, id string) error {
	q := sqlc.New()
	if _, err := q.ReadMCPNotificationOwnedApp(ctx, db, sqlc.ReadMCPNotificationOwnedAppParams{AppID: mustPgUUID(app), AccountID: mustPgUUID(account)}); err != nil {
		return mapErr(err)
	}
	row, err := q.ReadCustomerAlertRule(ctx, db, mustPgUUID(id))
	if err != nil {
		return mapErr(err)
	}
	return readinessWebhookRule(customerAlertRule(row), account, app)
}
func (s *PgStore) GetMCPReadinessPagerDutyAcknowledgements(ctx context.Context, account, app, id string, destinations ...string) (api.MCPReadinessPagerDutyAcknowledgementPolicy, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementPolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = ownedReadinessAcknowledgementPG(ctx, tx, account, app, id); err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementPolicy{}, err
	}
	config, err := readReadinessPagerDutyAcknowledgementsPG(ctx, tx, id, destinations...)
	if err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementPolicy{}, err
	}
	if len(destinations) == 1 && destinations[0] != "" {
		config.LegacyReceiver = false
	}
	return readinessPagerDutyAcknowledgementView(id, config), tx.Commit(ctx)
}
func (s *PgStore) SetMCPReadinessPagerDutyAcknowledgements(ctx context.Context, account, app, id string, expected int64, config MCPReadinessPagerDutyAcknowledgementConfig) (api.MCPReadinessPagerDutyAcknowledgementPolicy, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementPolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockReadinessApp(ctx, tx, account, app); err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementPolicy{}, err
	}
	rule, err := lockReadinessRule(ctx, tx, id)
	if err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementPolicy{}, err
	}
	if err = readinessWebhookRule(rule, account, app); err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementPolicy{}, err
	}
	destination := config.Destination
	if config.Enabled && destination == "" {
		destination = "primary"
	}
	old, err := readReadinessPagerDutyAcknowledgementsPG(ctx, tx, id, destination)
	if err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementPolicy{}, err
	}
	config.Destination, config.LegacyReceiver = readinessPagerDutyAcknowledgementDestination(old), old.LegacyReceiver
	if old.Version != expected {
		return api.MCPReadinessPagerDutyAcknowledgementPolicy{}, ErrConflict
	}
	primary, err := readReadinessFallbackPG(ctx, tx, id)
	if err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementPolicy{}, err
	}
	config, err = bindReadinessPagerDutyAcknowledgements(expected, config, primary)
	if err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementPolicy{}, err
	}
	if err = sqlc.New().SaveMCPPagerDutyAcknowledgements(ctx, tx, sqlc.SaveMCPPagerDutyAcknowledgementsParams{SyncUnacknowledgements: config.SyncUnacknowledgements, LegacyReceiver: config.LegacyReceiver, RuleID: mustPgUUID(id), Destination: readinessPagerDutyAcknowledgementDestination(config), FallbackVersion: config.FallbackVersion, Version: config.Version, Enabled: config.Enabled, SigningSecretSealed: config.SigningSecretSealed, PagerdutyVersion: config.PagerDutyVersion, Region: config.Region, Fingerprint: config.Fingerprint, ServiceID: config.ServiceID}); err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementPolicy{}, err
	}
	return readinessPagerDutyAcknowledgementView(id, config), tx.Commit(ctx)
}
func (s *PgStore) ReadMCPReadinessPagerDutyAcknowledgementReceiver(ctx context.Context, id string, destinations ...string) (MCPReadinessPagerDutyAcknowledgementConfig, error) {
	destination, err := readinessPagerDutyAcknowledgementSelector(destinations)
	if err != nil {
		return MCPReadinessPagerDutyAcknowledgementConfig{}, err
	}
	row, err := sqlc.New().ReadMCPPagerDutyAcknowledgementReceiver(ctx, s.pool, sqlc.ReadMCPPagerDutyAcknowledgementReceiverParams{RuleID: mustPgUUID(id), Destination: destination})
	if err != nil {
		return MCPReadinessPagerDutyAcknowledgementConfig{}, mapErr(err)
	}
	p := row.McpReadinessPagerdutyAcknowledgement
	return MCPReadinessPagerDutyAcknowledgementConfig{SyncUnacknowledgements: p.SyncUnacknowledgements, LegacyReceiver: p.LegacyReceiver, Destination: p.Destination, FallbackVersion: p.FallbackVersion, Version: p.Version, Enabled: p.Enabled, SigningSecretSealed: p.SigningSecretSealed, PagerDutyVersion: p.PagerdutyVersion, Region: p.Region, Fingerprint: p.Fingerprint, ServiceID: p.ServiceID, AccountID: pgUUIDString(row.AccountID), AppID: pgUUIDString(row.AppID)}, nil
}
func (s *PgStore) ApplyMCPReadinessPagerDutyAcknowledgement(ctx context.Context, ruleID string, version int64, e MCPReadinessPagerDutyAcknowledgementInput, now time.Time) (api.MCPReadinessPagerDutyAcknowledgementEvent, error) {
	if err := validateReadinessPagerDutyAcknowledgement(e, now); err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementEvent{}, err
	}
	now = now.UTC().Truncate(time.Microsecond)
	e.IncidentID = uuid.MustParse(e.IncidentID).String()
	e.OccurredAt = e.OccurredAt.UTC()
	receiver, err := s.ReadMCPReadinessPagerDutyAcknowledgementReceiver(ctx, ruleID, e.BindingDestination)
	if err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementEvent{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementEvent{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockReadinessApp(ctx, tx, receiver.AccountID, receiver.AppID); err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementEvent{}, err
	}
	rule, err := lockReadinessRule(ctx, tx, ruleID)
	if err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementEvent{}, err
	}
	if err = readinessWebhookRule(rule, receiver.AccountID, receiver.AppID); err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementEvent{}, err
	}
	config, err := readReadinessPagerDutyAcknowledgementsPG(ctx, tx, ruleID, e.BindingDestination)
	if err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementEvent{}, err
	}
	if !config.Enabled || config.Version != version || e.RequireLegacyReceiver && !config.LegacyReceiver {
		return api.MCPReadinessPagerDutyAcknowledgementEvent{}, ErrConflict
	}
	config.AccountID, config.AppID = rule.AccountID, rule.AppID
	q := sqlc.New()
	row, err := q.ReadMCPAttentionIncident(ctx, tx, sqlc.ReadMCPAttentionIncidentParams{IncidentID: mustPgUUID(e.IncidentID), AccountID: mustPgUUID(rule.AccountID), AppID: mustPgUUID(rule.AppID)})
	if err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementEvent{}, mapErr(err)
	}
	incident := readinessAttentionIncidentFromSQLC(row)
	incident.Routing, err = readReadinessRoutingPG(ctx, tx, e.IncidentID)
	if err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementEvent{}, err
	}
	if incident.RuleID != uuid.MustParse(ruleID).String() || !readinessPagerDutyAcknowledgementMatches(config, incident, e) {
		return api.MCPReadinessPagerDutyAcknowledgementEvent{}, ErrNotFound
	}
	if e.Action == "release" && !config.SyncUnacknowledgements {
		return api.MCPReadinessPagerDutyAcknowledgementEvent{}, tx.Commit(ctx)
	}
	delivery := readinessPagerDutyAcknowledgementDelivery(e, config)
	old, err := q.ReadMCPPagerDutyAcknowledgementEvent(ctx, tx, sqlc.ReadMCPPagerDutyAcknowledgementEventParams{RuleID: mustPgUUID(ruleID), EventID: e.EventID})
	if err == nil {
		if old.Digest != readinessPagerDutyAcknowledgementDigest(e) {
			return api.MCPReadinessPagerDutyAcknowledgementEvent{}, ErrConflict
		}

		if old.DeliveryDestination != delivery {
			old, err = q.ReadMCPPagerDutyReleaseEvent(ctx, tx, sqlc.ReadMCPPagerDutyReleaseEventParams{RuleID: mustPgUUID(ruleID), EventID: e.EventID, DeliveryDestination: delivery})
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return api.MCPReadinessPagerDutyAcknowledgementEvent{}, err
			}
		}
		if err == nil {
			var receipt api.MCPReadinessPagerDutyAcknowledgementEvent
			if err = json.Unmarshal(old.Receipt, &receipt); err != nil {
				return receipt, err
			}
			return receipt, tx.Commit(ctx)
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return api.MCPReadinessPagerDutyAcknowledgementEvent{}, err
	}
	count, err := q.CountMCPPagerDutyAcknowledgementEvents(ctx, tx, mustPgUUID(e.IncidentID))
	if err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementEvent{}, err
	}
	if count >= api.MCPReadinessPagerDutyWebhookEventsPerIncident {
		return api.MCPReadinessPagerDutyAcknowledgementEvent{}, tx.Commit(ctx)
	}
	attention, err := readReadinessAttentionPG(ctx, tx, e.IncidentID)
	if err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementEvent{}, err
	}
	attention, receipt, action, err := decideReadinessPagerDutyAcknowledgement(incident, attention, config, e, now)
	if err != nil {
		return receipt, err
	}
	if err = saveReadinessAttentionPG(ctx, tx, incident.ID, attention); err != nil {
		return receipt, err
	}
	if action != nil {
		requestJSON, _ := json.Marshal(action.Request)
		receiptJSON, _ := json.Marshal(action.Receipt)
		if err = q.InsertMCPAttentionAction(ctx, tx, sqlc.InsertMCPAttentionActionParams{AppID: mustPgUUID(rule.AppID), AccountID: mustPgUUID(rule.AccountID), IncidentID: mustPgUUID(incident.ID), RequestID: mustPgUUID(action.Receipt.RequestID), Request: requestJSON, Receipt: receiptJSON, CreatedAt: pgtype.Timestamptz{Time: now, Valid: true}}); err != nil {
			return receipt, err
		}
		if err = reconcileReadinessRemindersPG(ctx, tx, rule, now); err != nil {
			return receipt, err
		}
	}
	raw, _ := json.Marshal(receipt)
	if err = q.InsertMCPPagerDutyAcknowledgementEvent(ctx, tx, sqlc.InsertMCPPagerDutyAcknowledgementEventParams{DeliveryDestination: delivery, RuleID: mustPgUUID(ruleID), EventID: e.EventID, ID: mustPgUUID(receipt.ID), IncidentID: mustPgUUID(incident.ID), Digest: readinessPagerDutyAcknowledgementDigest(e), Receipt: raw, ReceivedAt: pgtype.Timestamptz{Time: now, Valid: true}}); err != nil {
		return receipt, err
	}
	return receipt, tx.Commit(ctx)
}
func (s *PgStore) ListMCPReadinessPagerDutyAcknowledgementEvents(ctx context.Context, account, app, rule, before string, limit int) (api.MCPReadinessPagerDutyAcknowledgementEvents, error) {
	at, id, err := validateReadinessIncidentList("", before, limit)
	if err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementEvents{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementEvents{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = ownedReadinessAcknowledgementPG(ctx, tx, account, app, rule); err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementEvents{}, err
	}
	cursor, err := alertPgUUID(id)
	if err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementEvents{}, err
	}
	rows, err := sqlc.New().ListMCPPagerDutyAcknowledgementEvents(ctx, tx, sqlc.ListMCPPagerDutyAcknowledgementEventsParams{RuleID: mustPgUUID(rule), BeforeAt: pgtype.Timestamptz{Time: at, Valid: !at.IsZero()}, BeforeID: cursor, PageLimit: int32(limit + 1)})
	if err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementEvents{}, err
	}
	out := api.MCPReadinessPagerDutyAcknowledgementEvents{Events: []api.MCPReadinessPagerDutyAcknowledgementEvent{}}
	for n, raw := range rows {
		if n == limit {
			last := out.Events[n-1]
			out.NextBefore = api.MCPReadinessIncidentCursor(last.ReceivedAt, last.ID)
			break
		}
		var receipt api.MCPReadinessPagerDutyAcknowledgementEvent
		if err = json.Unmarshal(raw, &receipt); err != nil {
			return out, err
		}
		out.Events = append(out.Events, receipt)
	}
	return out, tx.Commit(ctx)
}
