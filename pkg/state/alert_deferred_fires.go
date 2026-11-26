package state

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// AlertFireRecipients is the binding snapshot and handoff decisions committed
// with a live fire claim.
type AlertFireRecipients struct {
	Channels []NotificationChannel
	Held     AlertDeferredRecipients
}

func claimAlertFireRecipients(ctx context.Context, tx sqlc.DBTX, ruleID, deliveryID string) (AlertFireRecipients, error) {
	var recipients AlertFireRecipients
	q := sqlc.New()
	rows, err := q.ListAlertRuleChannels(ctx, tx, mustPgUUID(ruleID))
	if err != nil {
		return recipients, fmt.Errorf("state: read claimed alert channels: %w", err)
	}
	ids := make([]pgtype.UUID, 0, len(rows))
	regions := make([]string, 0, len(rows))
	for _, row := range rows {
		recipients.Channels = append(recipients.Channels, channelFromRow(row))
		ids = append(ids, row.ID)
		regions = append(regions, row.PagerdutyRegion.String)
	}
	held, err := q.QueueDeferredAlertFires(ctx, tx, sqlc.QueueDeferredAlertFiresParams{
		DeliveryID: mustPgUUID(deliveryID), ChannelIds: ids, PagerdutyRegions: regions,
	})
	if err != nil {
		return AlertFireRecipients{}, fmt.Errorf("state: queue claimed deferred alert fires: %w", err)
	}
	for _, row := range held {
		recipients.Held.add(row.ChannelID, row.WebhookUrl)
	}
	return recipients, nil
}

type AlertDeferredRecipients struct {
	ChannelIDs map[string]bool
	WebhookURL string
}

func (r *AlertDeferredRecipients) add(channelID, webhookURL string) {
	if channelID == "" {
		r.WebhookURL = webhookURL
		return
	}
	if r.ChannelIDs == nil {
		r.ChannelIDs = make(map[string]bool)
	}
	r.ChannelIDs[channelID] = true
}

// AlertDeferredFire is one recipient of a live delivery held until its prior
// episode's terminal notification is settled.
type AlertDeferredFire struct {
	ID, DeliveryID, RuleID, ChannelID, WebhookURL string
	ChannelOnly, Blocked                          bool
	Status                                        AlertDeliveryStatus
	FiredAt                                       time.Time
	Observed                                      float64
	Payload                                       []byte
}

type AlertDeferredFireStore interface {
	AlertDeferredFireRecipients(ctx context.Context, ruleID string, firedAt time.Time) (AlertDeferredRecipients, error)
	DueDeferredAlertFires(ctx context.Context, now time.Time, maxRows int) ([]AlertDeferredFire, error)
	StartDeferredWebhookFire(ctx context.Context, id string) (bool, error)
	FinishDeferredAlertFire(ctx context.Context, id string) (lastRecipient bool, err error)
	RetryDeferredAlertFire(ctx context.Context, id string, next time.Time) error
}

func (s *PgStore) AlertDeferredFireRecipients(ctx context.Context, ruleID string, firedAt time.Time) (AlertDeferredRecipients, error) {
	var recipients AlertDeferredRecipients
	rule, err := alertPgUUID(ruleID)
	if err != nil {
		return recipients, err
	}
	rows, err := sqlc.New().AlertDeferredFireRecipients(ctx, s.pool, sqlc.AlertDeferredFireRecipientsParams{
		RuleID: rule, FiredAt: pgtype.Timestamptz{Time: firedAt.UTC().Truncate(time.Microsecond), Valid: true},
	})
	if err != nil {
		return recipients, err
	}
	for _, row := range rows {
		recipients.add(row.ChannelID, row.WebhookUrl)
	}
	return recipients, nil
}

func (s *PgStore) DueDeferredAlertFires(ctx context.Context, now time.Time, maxRows int) ([]AlertDeferredFire, error) {
	rows, err := sqlc.New().DueDeferredAlertFires(ctx, s.pool, sqlc.DueDeferredAlertFiresParams{
		Now: pgtype.Timestamptz{Time: now.UTC(), Valid: true}, MaxRows: int32(maxRows),
	})
	if err != nil {
		return nil, err
	}
	fires := make([]AlertDeferredFire, 0, len(rows))
	for _, row := range rows {
		fires = append(fires, AlertDeferredFire{
			ID: row.ID, DeliveryID: row.DeliveryID, RuleID: row.RuleID,
			ChannelID: row.ChannelID, WebhookURL: row.WebhookUrl, ChannelOnly: row.ChannelOnly,
			Blocked: row.Blocked, Status: AlertDeliveryStatus(row.Status), FiredAt: row.FiredAt.Time.UTC(),
			Observed: row.ObservedValue, Payload: row.Payload,
		})
	}
	return fires, nil
}

func (s *PgStore) StartDeferredWebhookFire(ctx context.Context, id string) (bool, error) {
	key, err := alertPgUUID(id)
	if err != nil {
		return false, err
	}
	count, err := sqlc.New().StartDeferredWebhookFire(ctx, s.pool, key)
	return count == 1, err
}

func (s *PgStore) FinishDeferredAlertFire(ctx context.Context, id string) (bool, error) {
	key, err := alertPgUUID(id)
	if err != nil {
		return false, err
	}
	last, err := sqlc.New().FinishDeferredAlertFire(ctx, s.pool, key)
	return last.Bool, err
}

func (s *PgStore) RetryDeferredAlertFire(ctx context.Context, id string, next time.Time) error {
	key, err := alertPgUUID(id)
	if err != nil {
		return err
	}
	return sqlc.New().RetryDeferredAlertFire(ctx, s.pool, sqlc.RetryDeferredAlertFireParams{
		ID: key, NextAttemptAt: pgtype.Timestamptz{Time: next.UTC(), Valid: true},
	})
}

type memDeferredAlertFire struct {
	AlertDeferredFire
	next        time.Time
	deliveryKey string
	started     bool
}

func (m *MemStore) queueDeferredAlertFiresLocked(rule AlertRule, delivery AlertDelivery) AlertFireRecipients {
	var recipients AlertFireRecipients
	if m.alertDeferredFires == nil {
		m.alertDeferredFires = make(map[string]memDeferredAlertFire)
	}
	add := func(channelID, webhookURL string) {
		recipients.Held.add(channelID, webhookURL)
		id := uuid.NewString()
		m.alertDeferredFires[id] = memDeferredAlertFire{
			AlertDeferredFire: AlertDeferredFire{
				ID: id, DeliveryID: delivery.ID, RuleID: rule.ID, FiredAt: delivery.FiredAt,
				ChannelID: channelID, WebhookURL: webhookURL, ChannelOnly: rule.WebhookURL == "",
			},
			next: delivery.FiredAt, deliveryKey: delivery.IdempotencyKey,
		}
	}
	standDowns := m.pagerDutyObservationStandDownChannelsLocked(rule)
	recipients.Channels = m.listAlertRuleChannelsLocked(rule)
	for _, ch := range recipients.Channels {
		alias := ch.Kind == "pagerduty" && slices.ContainsFunc(standDowns, func(older NotificationChannel) bool {
			return older.PagerDutyRegion == ch.PagerDutyRegion
		})
		if alias || m.alertChannelStandDowns[alertChannelRetryKey{rule.ID, ch.ID}].observationTransition {
			add(ch.ID, "")
		}
	}
	if rule.WebhookURL != "" && m.alertWebhookStandDownPendingLocked(rule.ID, rule.WebhookURL) {
		add("", rule.WebhookURL)
	}
	return recipients
}

func (m *MemStore) alertWebhookStandDownPendingLocked(ruleID, url string) bool {
	for _, notice := range m.alertWebhookStandDowns {
		if notice.Rule.ID == ruleID && notice.Rule.WebhookURL == url {
			return true
		}
	}
	return false
}

func (m *MemStore) hasUnsentDeferredWebhookLocked(rule AlertRule) bool {
	for _, fire := range m.alertDeferredFires {
		if fire.RuleID == rule.ID && fire.FiredAt.Equal(rule.LastFiredAt) && fire.WebhookURL == rule.WebhookURL && !fire.started &&
			m.alertDeliveries[fire.deliveryKey].Status == AlertDeliveryPending {
			return true
		}
	}
	return false
}

func (m *MemStore) AlertDeferredFireRecipients(_ context.Context, ruleID string, firedAt time.Time) (AlertDeferredRecipients, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var recipients AlertDeferredRecipients
	for _, fire := range m.alertDeferredFires {
		if fire.RuleID == ruleID && fire.FiredAt.Equal(firedAt) {
			if fire.ChannelID != "" || !fire.started && m.alertDeliveries[fire.deliveryKey].Status == AlertDeliveryPending {
				recipients.add(fire.ChannelID, fire.WebhookURL)
			}
		}
	}
	return recipients, nil
}

func (m *MemStore) DueDeferredAlertFires(_ context.Context, now time.Time, maxRows int) ([]AlertDeferredFire, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var due []memDeferredAlertFire
	for id, fire := range m.alertDeferredFires {
		delivery, ok := m.alertDeliveries[fire.deliveryKey]
		if !ok {
			delete(m.alertDeferredFires, id)
			continue
		}
		if fire.next.After(now) {
			continue
		}
		if fire.ChannelID == "" {
			fire.Blocked = m.alertWebhookStandDownPendingLocked(fire.RuleID, fire.WebhookURL)
		} else {
			fire.Blocked = m.alertChannelStandDowns[alertChannelRetryKey{fire.RuleID, fire.ChannelID}].observationTransition
		}
		fire.Status, fire.Observed, fire.Payload = delivery.Status, delivery.ObservedValue, bytes.Clone(delivery.Payload)
		due = append(due, fire)
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].next.Equal(due[j].next) {
			return due[i].ID < due[j].ID
		}
		return due[i].next.Before(due[j].next)
	})
	result := make([]AlertDeferredFire, 0, min(maxRows, len(due)))
	for _, fire := range due[:min(maxRows, len(due))] {
		result = append(result, fire.AlertDeferredFire)
	}
	return result, nil
}

func (m *MemStore) StartDeferredWebhookFire(_ context.Context, id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fire, ok := m.alertDeferredFires[id]
	if !ok || fire.WebhookURL == "" {
		return false, nil
	}
	rule := m.alertRules[fire.RuleID]
	if !rule.Enabled || rule.ObserveOnly || rule.State != AlertStateFiring ||
		!rule.LastFiredAt.Equal(fire.FiredAt) || rule.WebhookURL != fire.WebhookURL ||
		m.alertDeliveries[fire.deliveryKey].Status != AlertDeliveryPending ||
		m.alertWebhookStandDownPendingLocked(rule.ID, fire.WebhookURL) {
		return false, nil
	}
	fire.started = true
	m.alertDeferredFires[id] = fire
	return true, nil
}

func (m *MemStore) FinishDeferredAlertFire(_ context.Context, id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fire := m.alertDeferredFires[id]
	delete(m.alertDeferredFires, id)
	for _, other := range m.alertDeferredFires {
		if other.DeliveryID == fire.DeliveryID {
			return false, nil
		}
	}
	return true, nil
}

func (m *MemStore) RetryDeferredAlertFire(_ context.Context, id string, next time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if fire, ok := m.alertDeferredFires[id]; ok {
		fire.next = next
		m.alertDeferredFires[id] = fire
	}
	return nil
}

var (
	_ AlertDeferredFireStore = (*PgStore)(nil)
	_ AlertDeferredFireStore = (*MemStore)(nil)
)
