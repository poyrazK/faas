package state

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

// MaxAPIConsumerPlanAlertThresholds bounds a plan's usage alert thresholds.
const MaxAPIConsumerPlanAlertThresholds = 5

// APIConsumerUsageAlert records one plan alert threshold a consumer crossed
// in one UTC month (ADR-849). It is the source of a consumer.usage_threshold
// webhook and is recorded at most once per consumer, month, and threshold.
type APIConsumerUsageAlert struct {
	ID               string
	AccountID        string
	AppID            string
	ConsumerID       string
	PlanID           string
	MonthStart       time.Time
	ThresholdPercent int32
	LimitUnits       int64
	UsedUnits        int64
	CrossedAt        time.Time
}

// normalizeAPIConsumerPlanAlerts sorts thresholds and returns a non-nil
// slice, so an empty list is stored as "no alerts".
func normalizeAPIConsumerPlanAlerts(thresholds []int32) []int32 {
	out := slices.Clone(thresholds)
	if out == nil {
		out = []int32{}
	}
	slices.Sort(out)
	return out
}

// validateAPIConsumerPlanAlerts checks thresholds are distinct percentages
// of a monthly unit limit the plan actually has.
func validateAPIConsumerPlanAlerts(thresholds []int32, maxUnitsPerMonth int64) error {
	if len(thresholds) == 0 {
		return nil
	}
	if len(thresholds) > MaxAPIConsumerPlanAlertThresholds {
		return fmt.Errorf("consumer plan: at most %d alert thresholds", MaxAPIConsumerPlanAlertThresholds)
	}
	if maxUnitsPerMonth <= 0 {
		return fmt.Errorf("consumer plan: alert thresholds need a monthly unit limit")
	}
	sorted := normalizeAPIConsumerPlanAlerts(thresholds)
	for i, t := range sorted {
		if t < 1 || t > 100 {
			return fmt.Errorf("consumer plan: alert thresholds must be percentages from 1 to 100")
		}
		if i > 0 && sorted[i-1] == t {
			return fmt.Errorf("consumer plan: alert thresholds must be distinct")
		}
	}
	return nil
}

// usageAlertThresholdUnits is the first used-unit count at or above percent
// of limit, computed without overflowing for large limits.
func usageAlertThresholdUnits(limit int64, percent int32) int64 {
	p := int64(percent)
	return limit/100*p + (limit%100*p+99)/100
}

// crossedUsageAlertThresholds returns the thresholds whose unit count lies
// in (before, after]: those this admission reached.
func crossedUsageAlertThresholds(thresholds []int32, limit, before, after int64) []int32 {
	if limit <= 0 || after <= before {
		return nil
	}
	var crossed []int32
	for _, t := range thresholds {
		if units := usageAlertThresholdUnits(limit, t); before < units && units <= after {
			crossed = append(crossed, t)
		}
	}
	return crossed
}

func consumerUsageThresholdWebhookPayload(alert APIConsumerUsageAlert, externalRef string) (json.RawMessage, error) {
	return json.Marshal(api.APIConsumerUsageThresholdWebhookPayload{
		AlertID: alert.ID, AppID: alert.AppID, ConsumerID: alert.ConsumerID, ExternalRef: externalRef,
		PlanID: alert.PlanID, ThresholdPercent: alert.ThresholdPercent, LimitUnits: alert.LimitUnits,
		UsedUnits: alert.UsedUnits, MonthStart: alert.MonthStart.UTC(), CrossedAt: alert.CrossedAt.UTC(),
	})
}

// --- MemStore ---

func (m *MemStore) recordAPIConsumerUsageAlertsLocked(accountID, consumerID string, policy APIConsumerPlanPolicy, counter planAdmissionCounter, crossed []int32, now time.Time) error {
	consumer, ok := m.apiConsumers[consumerID]
	if !ok || policy.PlanID == "" || consumer.AccountID != accountID || consumer.AppID != policy.AppID {
		return nil
	}
	for _, threshold := range crossed {
		if slices.ContainsFunc(m.apiConsumerUsageAlerts, func(a APIConsumerUsageAlert) bool {
			return a.ConsumerID == consumerID && a.MonthStart.Equal(counter.MonthStart) && a.ThresholdPercent == threshold
		}) {
			continue
		}
		alert := APIConsumerUsageAlert{ID: uuid.NewString(), AccountID: accountID, AppID: consumer.AppID, ConsumerID: consumerID,
			PlanID: policy.PlanID, MonthStart: counter.MonthStart, ThresholdPercent: threshold,
			LimitUnits: policy.MaxUnitsPerMonth, UsedUnits: counter.MonthUsed, CrossedAt: now}
		m.apiConsumerUsageAlerts = append(m.apiConsumerUsageAlerts, alert)
		if err := m.enqueueConsumerUsageThresholdLocked(alert, consumer.ExternalRef, now); err != nil {
			return err
		}
	}
	return nil
}

func (m *MemStore) enqueueConsumerUsageThresholdLocked(alert APIConsumerUsageAlert, externalRef string, now time.Time) error {
	var recipients []string
	for _, hook := range m.appWebhooks {
		if hook.Scope == AppWebhookScopeApp && hook.AppID == alert.AppID && hook.AccountID == alert.AccountID && hook.Enabled &&
			(len(hook.EventFilter) == 0 || slices.Contains(hook.EventFilter, string(AppWebhookEventConsumerUsageThreshold))) {
			recipients = append(recipients, hook.ID)
		}
	}
	if len(recipients) == 0 {
		return nil
	}
	payload, err := consumerUsageThresholdWebhookPayload(alert, externalRef)
	if err != nil {
		return err
	}
	sort.Strings(recipients)
	if m.appWebhookEventOutbox == nil {
		m.appWebhookEventOutbox = make(map[string]appWebhookOutboxEvent)
	}
	id := uuid.NewString()
	m.appWebhookEventOutbox[id] = appWebhookOutboxEvent{
		ID: id, AccountID: alert.AccountID, AppID: alert.AppID, Event: AppWebhookEventConsumerUsageThreshold,
		SourceID: alert.ID, Payload: payload, RecipientWebhookIDs: recipients, CreatedAt: now,
	}
	return nil
}

func (m *MemStore) ListAPIConsumerUsageAlerts(_ context.Context, accountID, appID, consumerID string, limit int) ([]APIConsumerUsageAlert, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []APIConsumerUsageAlert{}
	for _, alert := range m.apiConsumerUsageAlerts {
		if alert.AccountID == accountID && alert.AppID == appID && alert.ConsumerID == consumerID {
			out = append(out, alert)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CrossedAt.After(out[j].CrossedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// --- PgStore ---

const apiConsumerUsageAlertCols = `id, account_id, app_id, consumer_id, plan_id, month_start, threshold_percent, limit_units, used_units, crossed_at`

func scanAPIConsumerUsageAlert(row pgx.Row) (APIConsumerUsageAlert, error) {
	var a APIConsumerUsageAlert
	err := row.Scan(&a.ID, &a.AccountID, &a.AppID, &a.ConsumerID, &a.PlanID, &a.MonthStart,
		&a.ThresholdPercent, &a.LimitUnits, &a.UsedUnits, &a.CrossedAt)
	a.MonthStart, a.CrossedAt = a.MonthStart.UTC(), a.CrossedAt.UTC()
	return a, err
}

// recordAPIConsumerUsageAlertsTx records crossed thresholds inside the
// admission transaction and enqueues one webhook per newly recorded alert.
// A threshold already recorded this month is skipped by the unique key.
func recordAPIConsumerUsageAlertsTx(ctx context.Context, tx pgx.Tx, accountID, consumerID string, policy APIConsumerPlanPolicy, counter planAdmissionCounter, crossed []int32) error {
	if policy.PlanID == "" || policy.AppID == "" {
		return nil
	}
	rows, err := tx.Query(ctx, `insert into api_consumer_usage_alerts
			(account_id, app_id, consumer_id, plan_id, month_start, threshold_percent, limit_units, used_units)
		select c.account_id, c.app_id, c.id, $4::uuid, $5, t, $6, $7
		  from api_consumers c cross join unnest($8::int[]) as t
		 where c.id = $3::uuid and c.account_id = $1::uuid and c.app_id = $2::uuid
		on conflict (consumer_id, month_start, threshold_percent) do nothing
		returning `+apiConsumerUsageAlertCols,
		accountID, policy.AppID, consumerID, policy.PlanID, counter.MonthStart, policy.MaxUnitsPerMonth, counter.MonthUsed, crossed)
	if err != nil {
		return fmt.Errorf("record consumer usage alerts: %w", err)
	}
	var alerts []APIConsumerUsageAlert
	for rows.Next() {
		alert, err := scanAPIConsumerUsageAlert(rows)
		if err != nil {
			rows.Close()
			return fmt.Errorf("scan consumer usage alert: %w", err)
		}
		alerts = append(alerts, alert)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read consumer usage alerts: %w", err)
	}
	if len(alerts) == 0 {
		return nil
	}
	var externalRef string
	if err := tx.QueryRow(ctx, `select external_ref from api_consumers where id = $1::uuid`, consumerID).Scan(&externalRef); err != nil {
		return fmt.Errorf("read consumer for usage alert: %w", err)
	}
	for _, alert := range alerts {
		payload, err := consumerUsageThresholdWebhookPayload(alert, externalRef)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			insert into app_webhook_event_outbox
				(account_id, app_id, event, source_id, payload, recipient_webhook_ids)
			select $1::uuid, $2::uuid, $3, $4::uuid, $5::jsonb, array_agg(h.id order by h.id)
			  from app_webhooks h
			 where h.account_id = $1::uuid and h.app_id = $2::uuid
			   and h.scope = 'app' and h.enabled
			   and (cardinality(h.event_filter) = 0 or $3 = any(h.event_filter))
			having count(*) > 0
			on conflict (event, source_id) do nothing
		`, alert.AccountID, alert.AppID, string(AppWebhookEventConsumerUsageThreshold), alert.ID, string(payload)); err != nil {
			return fmt.Errorf("enqueue consumer usage threshold webhook: %w", err)
		}
	}
	return nil
}

func (s *PgStore) ListAPIConsumerUsageAlerts(ctx context.Context, accountID, appID, consumerID string, limit int) ([]APIConsumerUsageAlert, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `select `+apiConsumerUsageAlertCols+` from api_consumer_usage_alerts
		where account_id = $1::uuid and app_id = $2::uuid and consumer_id = $3::uuid
		order by crossed_at desc, threshold_percent desc limit $4`, accountID, appID, consumerID, limit)
	if err != nil {
		return nil, fmt.Errorf("list consumer usage alerts: %w", err)
	}
	defer rows.Close()
	out := []APIConsumerUsageAlert{}
	for rows.Next() {
		alert, err := scanAPIConsumerUsageAlert(rows)
		if err != nil {
			return nil, fmt.Errorf("scan consumer usage alert: %w", err)
		}
		out = append(out, alert)
	}
	return out, rows.Err()
}
