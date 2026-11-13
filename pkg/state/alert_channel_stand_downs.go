package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Why an alert episode ended without its rule recovering
// (ADR-20261104142830); mirrors alert_channel_stand_downs_reason_check.
const (
	StandDownDisabled    = "disabled"
	StandDownNotReleased = "not_released"
	StandDownAppDeleted  = "app_deleted"
	StandDownDeleted     = "deleted"
)

// AlertChannelStandDown is a recovery notice a notification channel is owed
// for an episode that ended without its rule recovering. It carries the
// rule's name and metric so it can still be sent once the rule is gone.
type AlertChannelStandDown struct {
	RuleID, ChannelID, AccountID string
	AppID                        string // "" for an account-wide rule
	RuleName, RuleMetric, Reason string
	QueuedAt                     time.Time
	Attempts                     int
}

// AlertChannelStandDownStore queues and drains those notices. Optional on
// Store; callers type-assert. A deleted rule's notices are queued by the
// alert_rules_channel_stand_downs trigger (MemStore: deleteAlertRuleLocked).
type AlertChannelStandDownStore interface {
	// QueueAlertChannelStandDowns owes each channel bound to the rule a
	// notice when the rule has an open episode, and moves the rule's
	// undelivered resolve retries into the queue. It returns the rows
	// queued; a channel already owed one keeps it.
	QueueAlertChannelStandDowns(ctx context.Context, ruleID, reason string, at time.Time) (int64, error)
	// DueAlertChannelStandDowns lists up to maxRows notices due at now, the
	// longest-waiting first.
	DueAlertChannelStandDowns(ctx context.Context, now time.Time, maxRows int) ([]AlertChannelStandDown, error)
	// RecordAlertChannelStandDownAttempt counts a failed attempt.
	RecordAlertChannelStandDownAttempt(ctx context.Context, ruleID, channelID string, next time.Time, lastErr string) error
	DeleteAlertChannelStandDown(ctx context.Context, ruleID, channelID string) error
	// ClearAlertChannelStandDowns drops a rule's notices: a new fire
	// supersedes them.
	ClearAlertChannelStandDowns(ctx context.Context, ruleID string) error
	// ListStrandedAlertRules lists up to maxRows rules that are not ok but
	// are no longer evaluated: disabled, or their app deleted.
	ListStrandedAlertRules(ctx context.Context, maxRows int) ([]AlertRule, error)
	// AlertRuleEpisodeOpen reports whether the rule has an open episode,
	// that is, it turned firing and has not been ok since.
	AlertRuleEpisodeOpen(ctx context.Context, ruleID string) (bool, error)
}

func (s *PgStore) QueueAlertChannelStandDowns(ctx context.Context, ruleID, reason string, at time.Time) (int64, error) {
	rule, err := alertPgUUID(ruleID)
	if err != nil || !rule.Valid || !validStandDownReason(reason) {
		return 0, ErrInvalidArgument
	}
	n, err := sqlc.New().QueueAlertChannelStandDowns(ctx, s.pool, sqlc.QueueAlertChannelStandDownsParams{
		RuleID: rule, Reason: reason, At: pgtype.Timestamptz{Time: at.UTC(), Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("state: queue channel stand-downs: %w", mapErr(err))
	}
	return n, nil
}

func (s *PgStore) DueAlertChannelStandDowns(ctx context.Context, now time.Time, maxRows int) ([]AlertChannelStandDown, error) {
	rows, err := sqlc.New().DueAlertChannelStandDowns(ctx, s.pool, sqlc.DueAlertChannelStandDownsParams{
		Now: pgtype.Timestamptz{Time: now.UTC(), Valid: true}, MaxRows: int32(maxRows),
	})
	if err != nil {
		return nil, fmt.Errorf("state: list due channel stand-downs: %w", err)
	}
	out := make([]AlertChannelStandDown, 0, len(rows))
	for _, row := range rows {
		out = append(out, AlertChannelStandDown{
			RuleID: row.RuleID, ChannelID: row.ChannelID, AccountID: row.AccountID, AppID: row.AppID,
			RuleName: row.RuleName, RuleMetric: row.RuleMetric, Reason: row.Reason,
			QueuedAt: row.QueuedAt.Time.UTC(), Attempts: int(row.Attempts),
		})
	}
	return out, nil
}

func (s *PgStore) RecordAlertChannelStandDownAttempt(ctx context.Context, ruleID, channelID string, next time.Time, lastErr string) error {
	rule, channel, err := alertChannelRetryKeys(ruleID, channelID)
	if err != nil {
		return err
	}
	err = sqlc.New().RecordAlertChannelStandDownAttempt(ctx, s.pool, sqlc.RecordAlertChannelStandDownAttemptParams{
		NextAttemptAt: pgtype.Timestamptz{Time: next.UTC(), Valid: true},
		LastError:     truncateUTF8Bytes(lastErr, alertChannelRetryErrorMax), RuleID: rule, ChannelID: channel,
	})
	if err != nil {
		return fmt.Errorf("state: record channel stand-down attempt: %w", err)
	}
	return nil
}

func (s *PgStore) DeleteAlertChannelStandDown(ctx context.Context, ruleID, channelID string) error {
	rule, channel, err := alertChannelRetryKeys(ruleID, channelID)
	if err != nil {
		return err
	}
	if err := sqlc.New().DeleteAlertChannelStandDown(ctx, s.pool, sqlc.DeleteAlertChannelStandDownParams{RuleID: rule, ChannelID: channel}); err != nil {
		return fmt.Errorf("state: delete channel stand-down: %w", err)
	}
	return nil
}

func (s *PgStore) ClearAlertChannelStandDowns(ctx context.Context, ruleID string) error {
	rule, err := alertPgUUID(ruleID)
	if err != nil || !rule.Valid {
		return ErrInvalidArgument
	}
	if err := sqlc.New().ClearAlertChannelStandDowns(ctx, s.pool, rule); err != nil {
		return fmt.Errorf("state: clear channel stand-downs: %w", err)
	}
	return nil
}

func (s *PgStore) ListStrandedAlertRules(ctx context.Context, maxRows int) ([]AlertRule, error) {
	return customerAlertRules(sqlc.New().ListStrandedAlertRules(ctx, s.pool, int32(maxRows)))
}

func (s *PgStore) AlertRuleEpisodeOpen(ctx context.Context, ruleID string) (bool, error) {
	rule, err := alertPgUUID(ruleID)
	if err != nil || !rule.Valid {
		return false, ErrInvalidArgument
	}
	open, err := sqlc.New().AlertRuleEpisodeOpen(ctx, s.pool, rule)
	if err != nil {
		return false, fmt.Errorf("state: read open alert episode: %w", err)
	}
	return open, nil
}

func validStandDownReason(reason string) bool {
	switch reason {
	case StandDownDisabled, StandDownNotReleased, StandDownAppDeleted, StandDownDeleted:
		return true
	}
	return false
}

// memStandDown is one queued notice and its retry state.
type memStandDown struct {
	AlertChannelStandDown
	nextAttemptAt time.Time
	lastError     string
}

func (m *MemStore) QueueAlertChannelStandDowns(_ context.Context, ruleID, reason string, at time.Time) (int64, error) {
	if !validStandDownReason(reason) {
		return 0, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rule, ok := m.alertRules[ruleID]
	if !ok {
		return 0, nil
	}
	return m.queueAlertChannelStandDownsLocked(rule, reason, at.UTC()), nil
}

// queueAlertChannelStandDownsLocked mirrors QueueAlertChannelStandDowns and
// the alert_rules_channel_stand_downs trigger; m.mu is held.
func (m *MemStore) queueAlertChannelStandDownsLocked(rule AlertRule, reason string, at time.Time) int64 {
	owed := map[string]bool{}
	if m.alertRuleEpisodeOpenLocked(rule.ID) {
		for _, id := range m.alertRuleChannels[rule.ID] {
			owed[id] = true
		}
	}
	for k := range m.alertChannelResolveRetries {
		if k.rule == rule.ID {
			owed[k.channel] = true
		}
	}
	m.clearAlertChannelResolveRetriesLocked(func(k alertChannelRetryKey) bool { return k.rule == rule.ID })
	live := map[string]bool{}
	for _, c := range m.notificationChannels[rule.AccountID] {
		live[c.ID] = true
	}
	if m.alertChannelStandDowns == nil {
		m.alertChannelStandDowns = map[alertChannelRetryKey]memStandDown{}
	}
	var queued int64
	for channelID := range owed {
		key := alertChannelRetryKey{rule.ID, channelID}
		if _, dup := m.alertChannelStandDowns[key]; dup || !live[channelID] {
			continue
		}
		m.alertChannelStandDowns[key] = memStandDown{
			AlertChannelStandDown: AlertChannelStandDown{
				RuleID: rule.ID, ChannelID: channelID, AccountID: rule.AccountID, AppID: rule.AppID,
				RuleName: rule.Name, RuleMetric: string(rule.Metric), Reason: reason, QueuedAt: at,
			},
			nextAttemptAt: at,
		}
		queued++
	}
	return queued
}

func (m *MemStore) alertRuleEpisodeOpenLocked(ruleID string) bool {
	for _, ep := range m.alertEpisodes {
		if ep.RuleID == ruleID && ep.ResolvedAt == nil {
			return true
		}
	}
	return false
}

func (m *MemStore) DueAlertChannelStandDowns(_ context.Context, now time.Time, maxRows int) ([]AlertChannelStandDown, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	type due struct {
		AlertChannelStandDown
		next time.Time
	}
	var rows []due
	for _, s := range m.alertChannelStandDowns {
		if !s.nextAttemptAt.After(now) {
			rows = append(rows, due{s.AlertChannelStandDown, s.nextAttemptAt})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].next.Equal(rows[j].next) {
			return rows[i].next.Before(rows[j].next)
		}
		if rows[i].RuleID != rows[j].RuleID {
			return rows[i].RuleID < rows[j].RuleID
		}
		return rows[i].ChannelID < rows[j].ChannelID
	})
	out := make([]AlertChannelStandDown, 0, min(len(rows), maxRows))
	for _, r := range rows[:min(len(rows), maxRows)] {
		out = append(out, r.AlertChannelStandDown)
	}
	return out, nil
}

func (m *MemStore) RecordAlertChannelStandDownAttempt(_ context.Context, ruleID, channelID string, next time.Time, lastErr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := alertChannelRetryKey{ruleID, channelID}
	s, ok := m.alertChannelStandDowns[key]
	if !ok {
		return nil
	}
	s.Attempts++
	s.nextAttemptAt, s.lastError = next, truncateUTF8Bytes(lastErr, alertChannelRetryErrorMax)
	m.alertChannelStandDowns[key] = s
	return nil
}

func (m *MemStore) DeleteAlertChannelStandDown(_ context.Context, ruleID, channelID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.alertChannelStandDowns, alertChannelRetryKey{ruleID, channelID})
	return nil
}

func (m *MemStore) ClearAlertChannelStandDowns(_ context.Context, ruleID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearAlertChannelStandDownsLocked(func(k alertChannelRetryKey) bool { return k.rule == ruleID })
	return nil
}

// clearAlertChannelStandDownsLocked drops the matching notices, as the
// channel and account foreign keys' ON DELETE CASCADE does.
func (m *MemStore) clearAlertChannelStandDownsLocked(match func(alertChannelRetryKey) bool) {
	for k := range m.alertChannelStandDowns {
		if match(k) {
			delete(m.alertChannelStandDowns, k)
		}
	}
}

func (m *MemStore) ListStrandedAlertRules(_ context.Context, maxRows int) ([]AlertRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AlertRule
	for _, r := range m.alertRules {
		if r.State == AlertStateOk {
			continue
		}
		if app, ok := m.apps[r.AppID]; !r.Enabled || ok && app.Status == AppDeleted {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.Before(out[j].UpdatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out[:min(len(out), maxRows)], nil
}

func (m *MemStore) AlertRuleEpisodeOpen(_ context.Context, ruleID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.alertRuleEpisodeOpenLocked(ruleID), nil
}

var (
	_ AlertChannelStandDownStore = (*PgStore)(nil)
	_ AlertChannelStandDownStore = (*MemStore)(nil)
)
