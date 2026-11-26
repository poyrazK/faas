package state

import (
	"bytes"
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// AlertWebhookStandDown holds the destination and payload fields of the live
// rule when its episode ended on entering Observe only.
type AlertWebhookStandDown struct {
	ID       string
	Rule     AlertRule
	QueuedAt time.Time
	Attempts int
}

type AlertWebhookStandDownStore interface {
	DueAlertWebhookStandDowns(ctx context.Context, now time.Time, maxRows int) ([]AlertWebhookStandDown, error)
	DeleteAlertWebhookStandDown(ctx context.Context, id string) error
	RetryAlertWebhookStandDown(ctx context.Context, id string, next time.Time) error
}

func (s *PgStore) DueAlertWebhookStandDowns(ctx context.Context, now time.Time, maxRows int) ([]AlertWebhookStandDown, error) {
	rows, err := sqlc.New().DueAlertWebhookStandDowns(ctx, s.pool, sqlc.DueAlertWebhookStandDownsParams{
		Now: pgtype.Timestamptz{Time: now.UTC(), Valid: true}, MaxRows: int32(maxRows),
	})
	if err != nil {
		return nil, err
	}
	notices := make([]AlertWebhookStandDown, 0, len(rows))
	for _, row := range rows {
		rule := AlertRule{
			ID: uuid.UUID(row.RuleID.Bytes).String(), Name: row.RuleName,
			Metric: AlertMetric(row.Metric), Comparison: AlertComparison(row.Comparison), Threshold: row.Threshold,
			WindowSpec: AlertWindowSpec(row.WindowSpec), EnvironmentScope: row.EnvironmentScope,
			WebhookURL: row.WebhookUrl, WebhookSecretSealed: row.WebhookSecretSealed,
		}
		if row.AppID.Valid {
			rule.AppID = uuid.UUID(row.AppID.Bytes).String()
		}
		notices = append(notices, AlertWebhookStandDown{
			ID: uuid.UUID(row.ID.Bytes).String(), Rule: rule, QueuedAt: row.QueuedAt.Time.UTC(), Attempts: int(row.Attempts),
		})
	}
	return notices, nil
}

func (s *PgStore) DeleteAlertWebhookStandDown(ctx context.Context, id string) error {
	key, err := alertPgUUID(id)
	if err != nil {
		return err
	}
	return sqlc.New().DeleteAlertWebhookStandDown(ctx, s.pool, key)
}

func (s *PgStore) RetryAlertWebhookStandDown(ctx context.Context, id string, next time.Time) error {
	key, err := alertPgUUID(id)
	if err != nil {
		return err
	}
	return sqlc.New().RetryAlertWebhookStandDown(ctx, s.pool, sqlc.RetryAlertWebhookStandDownParams{
		ID: key, NextAttemptAt: pgtype.Timestamptz{Time: next.UTC(), Valid: true},
	})
}

type memWebhookStandDown struct {
	AlertWebhookStandDown
	next time.Time
}

func (m *MemStore) queueAlertWebhookStandDownLocked(rule AlertRule, at time.Time) {
	if !rule.NotifyResolved || rule.WebhookURL == "" || !m.alertRuleEpisodeOpenLocked(rule.ID) || m.hasUnsentDeferredWebhookLocked(rule) {
		return
	}
	if m.alertWebhookStandDowns == nil {
		m.alertWebhookStandDowns = make(map[string]memWebhookStandDown)
	}
	id := uuid.NewString()
	snapshot := AlertRule{
		ID: rule.ID, AppID: rule.AppID, Name: rule.Name,
		Metric: rule.Metric, Comparison: rule.Comparison, Threshold: rule.Threshold,
		WindowSpec: rule.WindowSpec, EnvironmentScope: rule.EnvironmentScope,
		WebhookURL: rule.WebhookURL, WebhookSecretSealed: bytes.Clone(rule.WebhookSecretSealed),
	}
	m.alertWebhookStandDowns[id] = memWebhookStandDown{
		AlertWebhookStandDown: AlertWebhookStandDown{ID: id, Rule: snapshot, QueuedAt: at},
		next:                  at,
	}
}

func (m *MemStore) DueAlertWebhookStandDowns(_ context.Context, now time.Time, maxRows int) ([]AlertWebhookStandDown, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var due []memWebhookStandDown
	for _, notice := range m.alertWebhookStandDowns {
		if !notice.next.After(now) {
			due = append(due, notice)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].next.Equal(due[j].next) {
			return due[i].ID < due[j].ID
		}
		return due[i].next.Before(due[j].next)
	})
	out := make([]AlertWebhookStandDown, 0, min(len(due), maxRows))
	for _, notice := range due[:min(len(due), maxRows)] {
		notice.Rule.WebhookSecretSealed = bytes.Clone(notice.Rule.WebhookSecretSealed)
		out = append(out, notice.AlertWebhookStandDown)
	}
	return out, nil
}

func (m *MemStore) DeleteAlertWebhookStandDown(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.alertWebhookStandDowns, id)
	return nil
}

func (m *MemStore) RetryAlertWebhookStandDown(_ context.Context, id string, next time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	notice, ok := m.alertWebhookStandDowns[id]
	if ok {
		notice.Attempts++
		notice.next = next
		m.alertWebhookStandDowns[id] = notice
	}
	return nil
}

var (
	_ AlertWebhookStandDownStore = (*PgStore)(nil)
	_ AlertWebhookStandDownStore = (*MemStore)(nil)
)
