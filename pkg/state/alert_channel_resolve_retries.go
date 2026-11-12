package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// AlertChannelResolveRetry is a recovery notice a notification channel did
// not take (ADR-20261031144636). Observed and ResolvedAt are what the first
// attempt reported; the retries repeat them.
type AlertChannelResolveRetry struct {
	RuleID, ChannelID string
	Observed          float64
	ResolvedAt        time.Time
	Attempts          int
	NextAttemptAt     time.Time
	LastError         string
}

// AlertChannelResolveRetryStore keeps undelivered recovery notices. Optional
// on Store; callers type-assert.
type AlertChannelResolveRetryStore interface {
	// RecordAlertChannelResolveRetry stores a failed attempt: the first
	// keeps Observed and ResolvedAt, a later one counts an attempt and sets
	// NextAttemptAt and LastError.
	RecordAlertChannelResolveRetry(ctx context.Context, r AlertChannelResolveRetry) error
	// DeleteAlertChannelResolveRetry forgets one channel's retry.
	DeleteAlertChannelResolveRetry(ctx context.Context, ruleID, channelID string) error
	// ClearAlertChannelResolveRetries forgets a rule's retries; a new fire
	// supersedes the recovery it had not delivered.
	ClearAlertChannelResolveRetries(ctx context.Context, ruleID string) error
	// DueAlertChannelResolveRetries lists up to maxRows retries due at now
	// whose rule is still enabled and ok, the longest-waiting first.
	DueAlertChannelResolveRetries(ctx context.Context, now time.Time, maxRows int) ([]AlertChannelResolveRetry, error)
}

func (s *PgStore) RecordAlertChannelResolveRetry(ctx context.Context, r AlertChannelResolveRetry) error {
	rule, channel, err := alertChannelRetryKeys(r.RuleID, r.ChannelID)
	if err != nil {
		return err
	}
	err = sqlc.New().RecordAlertChannelResolveRetry(ctx, s.pool, sqlc.RecordAlertChannelResolveRetryParams{
		RuleID: rule, ChannelID: channel, Observed: r.Observed,
		ResolvedAt:    pgtype.Timestamptz{Time: r.ResolvedAt, Valid: true},
		NextAttemptAt: pgtype.Timestamptz{Time: r.NextAttemptAt, Valid: true},
		LastError:     truncateUTF8Bytes(r.LastError, alertChannelRetryErrorMax),
	})
	if err != nil {
		return fmt.Errorf("state: record channel resolve retry: %w", mapErr(err))
	}
	return nil
}

func (s *PgStore) DeleteAlertChannelResolveRetry(ctx context.Context, ruleID, channelID string) error {
	rule, channel, err := alertChannelRetryKeys(ruleID, channelID)
	if err != nil {
		return err
	}
	if err := sqlc.New().DeleteAlertChannelResolveRetry(ctx, s.pool, sqlc.DeleteAlertChannelResolveRetryParams{RuleID: rule, ChannelID: channel}); err != nil {
		return fmt.Errorf("state: delete channel resolve retry: %w", err)
	}
	return nil
}

func (s *PgStore) ClearAlertChannelResolveRetries(ctx context.Context, ruleID string) error {
	rule, err := alertPgUUID(ruleID)
	if err != nil || !rule.Valid {
		return ErrInvalidArgument
	}
	if err := sqlc.New().ClearAlertChannelResolveRetries(ctx, s.pool, rule); err != nil {
		return fmt.Errorf("state: clear channel resolve retries: %w", err)
	}
	return nil
}

func (s *PgStore) DueAlertChannelResolveRetries(ctx context.Context, now time.Time, maxRows int) ([]AlertChannelResolveRetry, error) {
	rows, err := sqlc.New().DueAlertChannelResolveRetries(ctx, s.pool, sqlc.DueAlertChannelResolveRetriesParams{
		Now: pgtype.Timestamptz{Time: now, Valid: true}, MaxRows: int32(maxRows),
	})
	if err != nil {
		return nil, fmt.Errorf("state: list due channel resolve retries: %w", err)
	}
	out := make([]AlertChannelResolveRetry, 0, len(rows))
	for _, row := range rows {
		out = append(out, AlertChannelResolveRetry{
			RuleID: row.RuleID, ChannelID: row.ChannelID, Observed: row.Observed,
			ResolvedAt: row.ResolvedAt.Time.UTC(), Attempts: int(row.Attempts),
		})
	}
	return out, nil
}

// alertChannelRetryErrorMax mirrors the last_error CHECK.
const alertChannelRetryErrorMax = 512

func alertChannelRetryKeys(ruleID, channelID string) (pgtype.UUID, pgtype.UUID, error) {
	rule, err := alertPgUUID(ruleID)
	if err != nil || !rule.Valid {
		return pgtype.UUID{}, pgtype.UUID{}, ErrInvalidArgument
	}
	channel, err := alertPgUUID(channelID)
	if err != nil || !channel.Valid {
		return pgtype.UUID{}, pgtype.UUID{}, ErrInvalidArgument
	}
	return rule, channel, nil
}

// truncateUTF8Bytes cuts s to at most n bytes on a rune boundary.
func truncateUTF8Bytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

type alertChannelRetryKey struct{ rule, channel string }

func (m *MemStore) RecordAlertChannelResolveRetry(_ context.Context, r AlertChannelResolveRetry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.alertRules[r.RuleID]; !ok {
		return ErrNotFound // the foreign key
	}
	if m.alertChannelResolveRetries == nil {
		m.alertChannelResolveRetries = map[alertChannelRetryKey]AlertChannelResolveRetry{}
	}
	key := alertChannelRetryKey{r.RuleID, r.ChannelID}
	r.LastError = truncateUTF8Bytes(r.LastError, alertChannelRetryErrorMax)
	if prev, ok := m.alertChannelResolveRetries[key]; ok {
		prev.Attempts++
		prev.NextAttemptAt, prev.LastError = r.NextAttemptAt, r.LastError
		m.alertChannelResolveRetries[key] = prev
		return nil
	}
	r.Attempts = 1
	m.alertChannelResolveRetries[key] = r
	return nil
}

func (m *MemStore) DeleteAlertChannelResolveRetry(_ context.Context, ruleID, channelID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.alertChannelResolveRetries, alertChannelRetryKey{ruleID, channelID})
	return nil
}

func (m *MemStore) ClearAlertChannelResolveRetries(_ context.Context, ruleID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearAlertChannelResolveRetriesLocked(func(k alertChannelRetryKey) bool { return k.rule == ruleID })
	return nil
}

// clearAlertChannelResolveRetriesLocked drops the matching retries, as the
// foreign keys' ON DELETE CASCADE does.
func (m *MemStore) clearAlertChannelResolveRetriesLocked(match func(alertChannelRetryKey) bool) {
	for k := range m.alertChannelResolveRetries {
		if match(k) {
			delete(m.alertChannelResolveRetries, k)
		}
	}
}

func (m *MemStore) DueAlertChannelResolveRetries(_ context.Context, now time.Time, maxRows int) ([]AlertChannelResolveRetry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AlertChannelResolveRetry
	for _, r := range m.alertChannelResolveRetries {
		rule, ok := m.alertRules[r.RuleID]
		if ok && rule.Enabled && rule.State == AlertStateOk && !r.NextAttemptAt.After(now) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].NextAttemptAt.Equal(out[j].NextAttemptAt) {
			return out[i].NextAttemptAt.Before(out[j].NextAttemptAt)
		}
		if out[i].RuleID != out[j].RuleID {
			return out[i].RuleID < out[j].RuleID
		}
		return out[i].ChannelID < out[j].ChannelID
	})
	return out[:min(len(out), maxRows)], nil
}

var (
	_ AlertChannelResolveRetryStore = (*PgStore)(nil)
	_ AlertChannelResolveRetryStore = (*MemStore)(nil)
)
