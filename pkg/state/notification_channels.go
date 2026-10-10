package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ErrNotificationChannelLimit is returned when a create would exceed the
// account's channel cap.
var ErrNotificationChannelLimit = errors.New("state: notification channel limit reached")

// NotificationChannel is an alert destination (ADR-749). TargetSealed holds
// the sealed Slack URL or PagerDuty routing key; TargetHint is a short,
// non-secret label (the Slack workspace path prefix, the key's last four
// characters) so a customer can tell channels apart.
type NotificationChannel struct {
	ID              string
	AccountID       string
	Name            string
	Kind            string
	TargetSealed    []byte
	TargetHint      string
	PagerDutyRegion string
	Email           string
	LastDeliveredAt time.Time
	LastError       string
	LastErrorAt     time.Time
	CreatedAt       time.Time
}

// NotificationChannelStore is implemented by PgStore and MemStore; optional
// on Store so older fakes keep compiling.
type NotificationChannelStore interface {
	CreateNotificationChannel(ctx context.Context, in NotificationChannel, maxPerAccount int) (NotificationChannel, error)
	ListNotificationChannels(ctx context.Context, accountID string) ([]NotificationChannel, error)
	GetNotificationChannel(ctx context.Context, accountID, id string) (NotificationChannel, error)
	DeleteNotificationChannel(ctx context.Context, accountID, id string) error
	// RecordNotificationChannelDelivery stores the latest outcome; an empty
	// errMsg is a success.
	RecordNotificationChannelDelivery(ctx context.Context, id string, at time.Time, errMsg string) error
}

func channelFromRow(row sqlc.NotificationChannel) NotificationChannel {
	return NotificationChannel{
		ID: uuid.UUID(row.ID.Bytes).String(), AccountID: uuid.UUID(row.AccountID.Bytes).String(),
		Name: row.Name, Kind: row.Kind, TargetSealed: row.TargetSealed, TargetHint: row.TargetHint,
		PagerDutyRegion: row.PagerdutyRegion.String, Email: row.Email.String,
		LastDeliveredAt: row.LastDeliveredAt.Time, LastError: row.LastError.String, LastErrorAt: row.LastErrorAt.Time,
		CreatedAt: row.CreatedAt.Time,
	}
}

func optText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

func channelKeys(accountID, id string) (pgtype.UUID, pgtype.UUID, error) {
	acct, err := alertPgUUID(accountID)
	if err != nil || !acct.Valid {
		return pgtype.UUID{}, pgtype.UUID{}, ErrInvalidArgument
	}
	key, err := alertPgUUID(id)
	if err != nil || !key.Valid {
		return pgtype.UUID{}, pgtype.UUID{}, ErrNotFound
	}
	return acct, key, nil
}

// CreateNotificationChannel inserts a channel; the cap is part of the insert.
func (s *PgStore) CreateNotificationChannel(ctx context.Context, in NotificationChannel, maxPerAccount int) (NotificationChannel, error) {
	acct, err := alertPgUUID(in.AccountID)
	if err != nil || !acct.Valid {
		return NotificationChannel{}, ErrInvalidArgument
	}
	row, err := sqlc.New().InsertNotificationChannel(ctx, s.pool, sqlc.InsertNotificationChannelParams{
		AccountID: acct, Name: in.Name, Kind: in.Kind, TargetSealed: in.TargetSealed, TargetHint: in.TargetHint,
		PagerdutyRegion: optText(in.PagerDutyRegion), Email: optText(in.Email), MaxPerAccount: int32(maxPerAccount), //nolint:gosec // limits.go bound
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return NotificationChannel{}, ErrNotificationChannelLimit
	}
	if err != nil {
		return NotificationChannel{}, fmt.Errorf("state: create notification channel %q: %w", in.Name, mapErr(err))
	}
	return channelFromRow(row), nil
}

// ListNotificationChannels returns an account's channels in name order.
func (s *PgStore) ListNotificationChannels(ctx context.Context, accountID string) ([]NotificationChannel, error) {
	acct, err := alertPgUUID(accountID)
	if err != nil || !acct.Valid {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListNotificationChannels(ctx, s.pool, acct)
	if err != nil {
		return nil, fmt.Errorf("state: list notification channels: %w", err)
	}
	out := make([]NotificationChannel, 0, len(rows))
	for _, row := range rows {
		out = append(out, channelFromRow(row))
	}
	return out, nil
}

// GetNotificationChannel returns one channel scoped to its account.
func (s *PgStore) GetNotificationChannel(ctx context.Context, accountID, id string) (NotificationChannel, error) {
	acct, key, err := channelKeys(accountID, id)
	if err != nil {
		return NotificationChannel{}, err
	}
	row, err := sqlc.New().GetNotificationChannel(ctx, s.pool, sqlc.GetNotificationChannelParams{AccountID: acct, ID: key})
	if err != nil {
		return NotificationChannel{}, mapErr(err)
	}
	return channelFromRow(row), nil
}

// DeleteNotificationChannel removes one channel; ErrNotFound when absent.
func (s *PgStore) DeleteNotificationChannel(ctx context.Context, accountID, id string) error {
	acct, key, err := channelKeys(accountID, id)
	if err != nil {
		return err
	}
	n, err := sqlc.New().DeleteNotificationChannel(ctx, s.pool, sqlc.DeleteNotificationChannelParams{AccountID: acct, ID: key})
	if err != nil {
		return fmt.Errorf("state: delete notification channel %s: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordNotificationChannelDelivery stores the latest delivery outcome.
func (s *PgStore) RecordNotificationChannelDelivery(ctx context.Context, id string, at time.Time, errMsg string) error {
	key, err := alertPgUUID(id)
	if err != nil || !key.Valid {
		return ErrInvalidArgument
	}
	if err := sqlc.New().RecordNotificationChannelDelivery(ctx, s.pool, sqlc.RecordNotificationChannelDeliveryParams{
		Err: optText(errMsg), At: pgtype.Timestamptz{Time: at.UTC(), Valid: true}, ID: key,
	}); err != nil {
		return fmt.Errorf("state: record notification channel delivery %s: %w", id, err)
	}
	return nil
}

// CreateNotificationChannel mirrors PgStore.
func (m *MemStore) CreateNotificationChannel(_ context.Context, in NotificationChannel, maxPerAccount int) (NotificationChannel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.notificationChannels == nil {
		m.notificationChannels = map[string][]NotificationChannel{}
	}
	existing := m.notificationChannels[in.AccountID]
	if len(existing) >= maxPerAccount {
		return NotificationChannel{}, ErrNotificationChannelLimit
	}
	for _, c := range existing {
		if c.Name == in.Name {
			return NotificationChannel{}, ErrConflict
		}
	}
	in.ID, in.CreatedAt = uuid.NewString(), time.Now().UTC()
	m.notificationChannels[in.AccountID] = append(existing, in)
	return in, nil
}

// ListNotificationChannels mirrors PgStore.
func (m *MemStore) ListNotificationChannels(_ context.Context, accountID string) ([]NotificationChannel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]NotificationChannel(nil), m.notificationChannels[accountID]...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// GetNotificationChannel mirrors PgStore.
func (m *MemStore) GetNotificationChannel(_ context.Context, accountID, id string) (NotificationChannel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.notificationChannels[accountID] {
		if c.ID == id {
			return c, nil
		}
	}
	return NotificationChannel{}, ErrNotFound
}

// DeleteNotificationChannel mirrors PgStore.
func (m *MemStore) DeleteNotificationChannel(_ context.Context, accountID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	chans := m.notificationChannels[accountID]
	for i, c := range chans {
		if c.ID == id {
			m.notificationChannels[accountID] = append(chans[:i:i], chans[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

// RecordNotificationChannelDelivery mirrors PgStore.
func (m *MemStore) RecordNotificationChannelDelivery(_ context.Context, id string, at time.Time, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for acct, chans := range m.notificationChannels {
		for i, c := range chans {
			if c.ID != id {
				continue
			}
			if errMsg == "" {
				c.LastDeliveredAt = at.UTC()
			} else {
				c.LastError, c.LastErrorAt = truncateRunes(errMsg, 256), at.UTC()
			}
			m.notificationChannels[acct][i] = c
			return nil
		}
	}
	return nil
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

var (
	_ NotificationChannelStore = (*PgStore)(nil)
	_ NotificationChannelStore = (*MemStore)(nil)
)
