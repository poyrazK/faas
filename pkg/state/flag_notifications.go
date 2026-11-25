package state

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Flag notifications (ADR-20261125085913): an environment's notification
// channels for its experiments and flag lifecycle, and the outbox apid
// queues notifications into and meterd delivers from.

// Flag notification kinds.
const (
	FlagNotifyExperimentDecision = "experiment_decision"
	FlagNotifyExperimentProblem  = "experiment_problem"
	FlagNotifyLifecycleDigest    = "lifecycle_digest"
	FlagNotifyFlagExpired        = "flag_expired"
)

// Flag notification statuses.
const (
	FlagNotificationPending   = "pending"
	FlagNotificationDelivered = "delivered"
	FlagNotificationAbandoned = "abandoned"
)

// FlagNotificationSettings are an environment's notification channels.
type FlagNotificationSettings struct {
	EnvironmentID, AccountID string
	ChannelIDs               []string
	UpdatedBy                string
	UpdatedAt                time.Time
}

// FlagNotification is one queued notification. Key is unique for good:
// queueing a key again does nothing, even after the notification was swept.
type FlagNotification struct {
	ID, AccountID, EnvironmentID string
	// OrgID is the organization of the notification's project, for the
	// release decision; empty outside an organization.
	OrgID                       string
	Kind, Key, Subject, Summary string
	ChannelIDs                  []string
	Status                      string
	Attempts                    int
	NextAttemptAt               time.Time
	LastError                   string
	CreatedAt                   time.Time
	SettledAt                   *time.Time
	// Plan is set on claimed rows.
	Plan string
}

// FlagNotificationSettle is the outcome of one delivery attempt: Status
// delivered or abandoned, or pending with the next attempt's time and the
// channels still owed the notification.
type FlagNotificationSettle struct {
	ID            string
	Status        string
	LastError     string
	NextAttemptAt time.Time
	Now           time.Time
	// ClaimedAttempts is the attempt count the claim returned; a row
	// reclaimed since (its lease ran out) is left alone.
	ClaimedAttempts int
	// Attempts is the count to record: ClaimedAttempts, or one less when
	// the attempt did not count.
	Attempts int
	// ChannelIDs are the channels still owed the notification.
	ChannelIDs []string
}

// FlagNotificationStore keeps flag notification settings and the outbox.
type FlagNotificationStore interface {
	// FlagNotificationSettings returns an environment's settings, or
	// ErrNotFound when it has none.
	FlagNotificationSettings(ctx context.Context, environmentID string) (FlagNotificationSettings, error)
	// SetFlagNotificationSettings replaces an environment's channels.
	SetFlagNotificationSettings(ctx context.Context, in FlagNotificationSettings) (FlagNotificationSettings, error)
	// EnqueueFlagNotification queues n at now unless a notification with
	// its key exists; queued reports which.
	EnqueueFlagNotification(ctx context.Context, n FlagNotification, now time.Time) (queued bool, err error)
	// ClaimDueFlagNotifications leases up to limit due pending
	// notifications until leaseUntil, counting an attempt on each.
	ClaimDueFlagNotifications(ctx context.Context, now, leaseUntil time.Time, limit int) ([]FlagNotification, error)
	// SettleFlagNotification records a delivery attempt.
	SettleFlagNotification(ctx context.Context, s FlagNotificationSettle) error
	// SweepFlagNotifications deletes up to limit notifications settled
	// before cutoff, and up to limit weekly digest keys queued before it.
	SweepFlagNotifications(ctx context.Context, cutoff time.Time, limit int) (int64, error)
	// FlagNotificationChannels returns the account's channels among ids.
	FlagNotificationChannels(ctx context.Context, accountID string, ids []string) ([]NotificationChannel, error)
}

func flagNotificationErr(op string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("flag notification %s: %w", op, err)
}

func flagNotificationFromRow(r sqlc.FlagNotification) FlagNotification {
	return FlagNotification{
		ID: pgUUIDString(r.ID), AccountID: pgUUIDString(r.AccountID), OrgID: pgUUIDString(r.OrgID), EnvironmentID: pgUUIDString(r.EnvironmentID),
		Kind: r.Kind, Key: r.Key, Subject: r.Subject, Summary: r.Summary, ChannelIDs: pgUUIDStrings(r.ChannelIds),
		Status: r.Status, Attempts: int(r.Attempts), NextAttemptAt: r.NextAttemptAt.Time.UTC(), LastError: r.LastError,
		CreatedAt: r.CreatedAt.Time.UTC(), SettledAt: optionalTimestamptz(r.SettledAt),
	}
}

// FlagNotificationSettings implements FlagNotificationStore.
func (s *PgStore) FlagNotificationSettings(ctx context.Context, environmentID string) (FlagNotificationSettings, error) {
	r, err := sqlc.New().GetFlagNotificationSettings(ctx, s.pool, uuidToPgtype(environmentID))
	if err != nil {
		return FlagNotificationSettings{}, flagNotificationErr("settings", err)
	}
	return FlagNotificationSettings{EnvironmentID: pgUUIDString(r.EnvironmentID), AccountID: pgUUIDString(r.AccountID),
		ChannelIDs: pgUUIDStrings(r.ChannelIds), UpdatedBy: r.UpdatedBy, UpdatedAt: r.UpdatedAt.Time.UTC()}, nil
}

// SetFlagNotificationSettings implements FlagNotificationStore.
func (s *PgStore) SetFlagNotificationSettings(ctx context.Context, in FlagNotificationSettings) (FlagNotificationSettings, error) {
	r, err := sqlc.New().UpsertFlagNotificationSettings(ctx, s.pool, sqlc.UpsertFlagNotificationSettingsParams{
		EnvironmentID: uuidToPgtype(in.EnvironmentID), AccountID: uuidToPgtype(in.AccountID),
		ChannelIds: pgUUIDList(in.ChannelIDs), UpdatedBy: in.UpdatedBy})
	if err != nil {
		return FlagNotificationSettings{}, flagNotificationErr("set settings", err)
	}
	return FlagNotificationSettings{EnvironmentID: pgUUIDString(r.EnvironmentID), AccountID: pgUUIDString(r.AccountID),
		ChannelIDs: pgUUIDStrings(r.ChannelIds), UpdatedBy: r.UpdatedBy, UpdatedAt: r.UpdatedAt.Time.UTC()}, nil
}

// EnqueueFlagNotification implements FlagNotificationStore.
func (s *PgStore) EnqueueFlagNotification(ctx context.Context, n FlagNotification, now time.Time) (bool, error) {
	rows, err := sqlc.New().EnqueueFlagNotification(ctx, s.pool, sqlc.EnqueueFlagNotificationParams{
		AccountID: uuidToPgtype(n.AccountID), OrgID: uuidToPgtype(n.OrgID), EnvironmentID: uuidToPgtype(n.EnvironmentID), Kind: n.Kind, Key: n.Key,
		Subject: n.Subject, Summary: n.Summary, ChannelIds: pgUUIDList(n.ChannelIDs), Now: pgTimestamptzUTC(now)})
	if err != nil {
		return false, flagNotificationErr("enqueue", err)
	}
	return rows > 0, nil
}

// ClaimDueFlagNotifications implements FlagNotificationStore.
func (s *PgStore) ClaimDueFlagNotifications(ctx context.Context, now, leaseUntil time.Time, limit int) ([]FlagNotification, error) {
	rows, err := sqlc.New().ClaimDueFlagNotifications(ctx, s.pool, sqlc.ClaimDueFlagNotificationsParams{
		Now: pgTimestamptzUTC(now), LeaseUntil: pgTimestamptzUTC(leaseUntil), MaxRows: int32(limit)})
	if err != nil {
		return nil, flagNotificationErr("claim", err)
	}
	out := make([]FlagNotification, 0, len(rows))
	for _, r := range rows {
		n := flagNotificationFromRow(sqlc.FlagNotification{
			ID: r.ID, AccountID: r.AccountID, OrgID: r.OrgID, EnvironmentID: r.EnvironmentID, Kind: r.Kind, Key: r.Key, Subject: r.Subject,
			Summary: r.Summary, ChannelIds: r.ChannelIds, Status: r.Status, Attempts: r.Attempts, NextAttemptAt: r.NextAttemptAt,
			LastError: r.LastError, CreatedAt: r.CreatedAt, SettledAt: r.SettledAt})
		n.Plan = r.Plan
		out = append(out, n)
	}
	return out, nil
}

// SettleFlagNotification implements FlagNotificationStore.
func (s *PgStore) SettleFlagNotification(ctx context.Context, st FlagNotificationSettle) error {
	next := st.NextAttemptAt
	if next.IsZero() {
		next = st.Now
	}
	_, err := sqlc.New().SettleFlagNotification(ctx, s.pool, sqlc.SettleFlagNotificationParams{
		Status: st.Status, LastError: truncateUTF8(st.LastError, 512), NextAttemptAt: pgTimestamptzUTC(next),
		Attempts: int32(st.Attempts), ChannelIds: pgUUIDList(st.ChannelIDs), Now: pgTimestamptzUTC(st.Now),
		ID: uuidToPgtype(st.ID), ClaimedAttempts: int32(st.ClaimedAttempts)})
	if err != nil {
		return flagNotificationErr("settle", err)
	}
	return nil
}

// SweepFlagNotifications implements FlagNotificationStore.
func (s *PgStore) SweepFlagNotifications(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	q := sqlc.New()
	n, err := q.SweepFlagNotifications(ctx, s.pool, sqlc.SweepFlagNotificationsParams{
		Cutoff: pgTimestamptzUTC(cutoff), MaxRows: int32(limit)})
	if err != nil {
		return 0, flagNotificationErr("sweep", err)
	}
	if _, err := q.SweepFlagNotificationDigestKeys(ctx, s.pool, sqlc.SweepFlagNotificationDigestKeysParams{
		Cutoff: pgTimestamptzUTC(cutoff), MaxRows: int32(limit)}); err != nil {
		return n, flagNotificationErr("sweep digest keys", err)
	}
	return n, nil
}

// FlagNotificationChannels implements FlagNotificationStore.
func (s *PgStore) FlagNotificationChannels(ctx context.Context, accountID string, ids []string) ([]NotificationChannel, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := sqlc.New().FlagNotificationChannels(ctx, s.pool, sqlc.FlagNotificationChannelsParams{
		AccountID: uuidToPgtype(accountID), ChannelIds: pgUUIDList(ids)})
	if err != nil {
		return nil, flagNotificationErr("channels", err)
	}
	out := make([]NotificationChannel, 0, len(rows))
	for _, r := range rows {
		out = append(out, channelFromRow(r))
	}
	return out, nil
}

// MemStore.

func cloneFlagNotification(n FlagNotification) FlagNotification {
	n.ChannelIDs = slices.Clone(n.ChannelIDs)
	if n.SettledAt != nil {
		t := *n.SettledAt
		n.SettledAt = &t
	}
	return n
}

// FlagNotificationSettings implements FlagNotificationStore.
func (m *MemStore) FlagNotificationSettings(_ context.Context, environmentID string) (FlagNotificationSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.flagNotificationSettings[environmentID]
	if !ok {
		return FlagNotificationSettings{}, ErrNotFound
	}
	st.ChannelIDs = slices.Clone(st.ChannelIDs)
	return st, nil
}

// SetFlagNotificationSettings implements FlagNotificationStore.
func (m *MemStore) SetFlagNotificationSettings(_ context.Context, in FlagNotificationSettings) (FlagNotificationSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.flagNotificationSettings == nil {
		m.flagNotificationSettings = map[string]FlagNotificationSettings{}
	}
	in.ChannelIDs = slices.Clone(in.ChannelIDs)
	if in.ChannelIDs == nil {
		in.ChannelIDs = []string{}
	}
	in.UpdatedAt = time.Now().UTC()
	m.flagNotificationSettings[in.EnvironmentID] = in
	in.ChannelIDs = slices.Clone(in.ChannelIDs)
	return in, nil
}

// EnqueueFlagNotification implements FlagNotificationStore.
func (m *MemStore) EnqueueFlagNotification(_ context.Context, n FlagNotification, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, seen := m.flagNotificationKeys[n.Key]; seen {
		return false, nil
	}
	if m.flagNotificationKeys == nil {
		m.flagNotificationKeys = map[string]flagNotificationKey{}
	}
	m.flagNotificationKeys[n.Key] = flagNotificationKey{kind: n.Kind, createdAt: now.UTC()}
	n.ID, n.Status, n.Attempts, n.LastError = uuid.NewString(), FlagNotificationPending, 0, ""
	n.NextAttemptAt, n.CreatedAt, n.SettledAt = now.UTC(), now.UTC(), nil
	m.flagNotifications = append(m.flagNotifications, cloneFlagNotification(n))
	return true, nil
}

// ClaimDueFlagNotifications implements FlagNotificationStore.
func (m *MemStore) ClaimDueFlagNotifications(_ context.Context, now, leaseUntil time.Time, limit int) ([]FlagNotification, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var due []int
	for i, n := range m.flagNotifications {
		if n.Status == FlagNotificationPending && !n.NextAttemptAt.After(now) {
			due = append(due, i)
		}
	}
	slices.SortFunc(due, func(a, b int) int {
		if c := m.flagNotifications[a].NextAttemptAt.Compare(m.flagNotifications[b].NextAttemptAt); c != 0 {
			return c
		}
		return compareStrings(m.flagNotifications[a].ID, m.flagNotifications[b].ID)
	})
	out := []FlagNotification{}
	for _, i := range due[:min(limit, len(due))] {
		n := &m.flagNotifications[i]
		n.NextAttemptAt, n.Attempts = leaseUntil.UTC(), n.Attempts+1
		claimed := cloneFlagNotification(*n)
		if acct, ok := m.accounts[n.AccountID]; ok {
			claimed.Plan = string(acct.Plan)
		}
		out = append(out, claimed)
	}
	return out, nil
}

// SettleFlagNotification implements FlagNotificationStore.
func (m *MemStore) SettleFlagNotification(_ context.Context, st FlagNotificationSettle) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.flagNotifications {
		n := &m.flagNotifications[i]
		if n.ID != st.ID || n.Status != FlagNotificationPending || n.Attempts != st.ClaimedAttempts {
			continue
		}
		n.Status, n.LastError = st.Status, truncateUTF8(st.LastError, 512)
		n.Attempts, n.ChannelIDs = st.Attempts, slices.Clone(st.ChannelIDs)
		n.NextAttemptAt = st.NextAttemptAt
		if n.NextAttemptAt.IsZero() {
			n.NextAttemptAt = st.Now
		}
		n.SettledAt = nil
		if st.Status != FlagNotificationPending {
			t := st.Now.UTC()
			n.SettledAt = &t
		}
	}
	return nil
}

// SweepFlagNotifications implements FlagNotificationStore.
func (m *MemStore) SweepFlagNotifications(_ context.Context, cutoff time.Time, limit int) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	m.flagNotifications = slices.DeleteFunc(m.flagNotifications, func(f FlagNotification) bool {
		if n < int64(limit) && f.SettledAt != nil && f.SettledAt.Before(cutoff) {
			n++
			return true
		}
		return false
	})
	swept := 0
	for key, k := range m.flagNotificationKeys {
		if swept < limit && k.kind == FlagNotifyLifecycleDigest && k.createdAt.Before(cutoff) {
			delete(m.flagNotificationKeys, key)
			swept++
		}
	}
	return n, nil
}

// flagNotificationKey is a MemStore record of a queued key.
type flagNotificationKey struct {
	kind      string
	createdAt time.Time
}

// FlagNotificationChannels implements FlagNotificationStore.
func (m *MemStore) FlagNotificationChannels(_ context.Context, accountID string, ids []string) ([]NotificationChannel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []NotificationChannel
	for _, c := range m.notificationChannels[accountID] {
		if slices.Contains(ids, c.ID) {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b NotificationChannel) int { return compareStrings(a.ID, b.ID) })
	return out, nil
}

// FlagNotificationsForTest returns every queued notification (tests).
func (m *MemStore) FlagNotificationsForTest() []FlagNotification {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]FlagNotification, 0, len(m.flagNotifications))
	for _, n := range m.flagNotifications {
		out = append(out, cloneFlagNotification(n))
	}
	return out
}
