package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Saved insights (ADR-20261122190000): an environment's named analytics
// queries, run again on demand, and by meterd for digests and threshold
// alerts.

// ErrSavedInsightLimit is returned when an environment already holds the
// maximum number of saved insights.
var ErrSavedInsightLimit = errors.New("state: saved insight limit reached")

// Saved insight alert states.
const (
	SavedInsightOK     = "ok"
	SavedInsightFiring = "firing"
)

// SavedInsight is one saved analytics query, its schedule and its last run.
type SavedInsight struct {
	ID, AccountID, EnvironmentID, Key, Name, Kind string
	// Query is the kind's request without its range.
	Query           json.RawMessage
	RangeDays       int
	Digest          string
	AlertComparison string
	AlertThreshold  float64
	ChannelIDs      []string
	AlertState      string
	LastValue       *float64
	LastRunAt       *time.Time
	LastError       string
	NextDigestAt    *time.Time
	NextCheckAt     *time.Time
	CreatedBy       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	// Plan, ProjectID and EnvironmentSlug are set on claimed rows.
	Plan, ProjectID, EnvironmentSlug string
}

// SavedInsightSettle is the outcome of one scheduled run.
type SavedInsightSettle struct {
	ID               string
	ClaimedUpdatedAt time.Time
	Now              time.Time
	LastValue        *float64
	LastError        string
	AlertState       string
	NextDigestAt     *time.Time
	NextCheckAt      *time.Time
}

// SavedInsightStore keeps saved insights and leases the due ones.
type SavedInsightStore interface {
	CreateSavedInsight(ctx context.Context, in SavedInsight, maxInsights int) (SavedInsight, error)
	// UpdateSavedInsight replaces the definition and schedule of the saved
	// insight with in.EnvironmentID and in.Key.
	UpdateSavedInsight(ctx context.Context, in SavedInsight) (SavedInsight, error)
	GetSavedInsight(ctx context.Context, environmentID, key string) (SavedInsight, error)
	ListSavedInsights(ctx context.Context, environmentID string) ([]SavedInsight, error)
	DeleteSavedInsight(ctx context.Context, environmentID, key string) (bool, error)
	// ClaimDueSavedInsights leases up to limit saved insights whose next
	// check is due until leaseUntil.
	ClaimDueSavedInsights(ctx context.Context, now, leaseUntil time.Time, limit int) ([]SavedInsight, error)
	// SettleSavedInsight records a run and releases its lease. A saved
	// insight changed since its claim keeps its new schedule, and applied
	// is false.
	SettleSavedInsight(ctx context.Context, s SavedInsightSettle) (applied bool, err error)
	// ReleaseSavedInsight drops a claim's lease without recording a run.
	ReleaseSavedInsight(ctx context.Context, id string) error
	// SavedInsightChannels returns the account's channels among ids.
	SavedInsightChannels(ctx context.Context, accountID string, ids []string) ([]NotificationChannel, error)
}

func pgFloat8(v *float64) pgtype.Float8 {
	if v == nil {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: *v, Valid: true}
}

func pgOptionalTimestamptz(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgTimestamptzUTC(*t)
}

func savedInsightFromRow(r sqlc.SavedInsight) SavedInsight {
	s := SavedInsight{
		ID: pgUUIDString(r.ID), AccountID: pgUUIDString(r.AccountID), EnvironmentID: pgUUIDString(r.EnvironmentID),
		Key: r.Key, Name: r.Name, Kind: r.Kind, Query: json.RawMessage(r.Query), RangeDays: int(r.RangeDays),
		Digest: r.Digest, AlertComparison: r.AlertComparison, AlertThreshold: r.AlertThreshold,
		ChannelIDs: pgUUIDStrings(r.ChannelIds), AlertState: r.AlertState, LastError: r.LastError,
		LastRunAt: optionalTimestamptz(r.LastRunAt), NextDigestAt: optionalTimestamptz(r.NextDigestAt),
		NextCheckAt: optionalTimestamptz(r.NextCheckAt), CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.Time.UTC(),
		UpdatedAt: r.UpdatedAt.Time.UTC(),
	}
	if r.LastValue.Valid {
		v := r.LastValue.Float64
		s.LastValue = &v
	}
	return s
}

func savedInsightErr(op string, err error) error {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case isUniqueViolation(err):
		return ErrConflict
	default:
		return fmt.Errorf("saved insight %s: %w", op, err)
	}
}

// CreateSavedInsight implements SavedInsightStore.
func (s *PgStore) CreateSavedInsight(ctx context.Context, in SavedInsight, maxInsights int) (SavedInsight, error) {
	var row sqlc.SavedInsight
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := sqlc.New()
		// Concurrent creates would each see room under the cap; the
		// environment row lock makes them take turns.
		if err := q.LockSavedInsightEnvironment(ctx, tx, uuidToPgtype(in.EnvironmentID)); err != nil {
			return err
		}
		var err error
		row, err = q.CreateSavedInsight(ctx, tx, sqlc.CreateSavedInsightParams{
			AccountID: uuidToPgtype(in.AccountID), EnvironmentID: uuidToPgtype(in.EnvironmentID), Key: in.Key, Name: in.Name,
			Kind: in.Kind, Query: []byte(in.Query), RangeDays: int32(in.RangeDays), Digest: in.Digest,
			AlertComparison: in.AlertComparison, AlertThreshold: in.AlertThreshold, ChannelIds: pgUUIDList(in.ChannelIDs),
			NextDigestAt: pgOptionalTimestamptz(in.NextDigestAt), NextCheckAt: pgOptionalTimestamptz(in.NextCheckAt),
			CreatedBy: in.CreatedBy, MaxInsights: int64(maxInsights)})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return SavedInsight{}, ErrSavedInsightLimit
	}
	if err != nil {
		return SavedInsight{}, savedInsightErr("create", err)
	}
	return savedInsightFromRow(row), nil
}

// UpdateSavedInsight implements SavedInsightStore.
func (s *PgStore) UpdateSavedInsight(ctx context.Context, in SavedInsight) (SavedInsight, error) {
	row, err := sqlc.New().UpdateSavedInsight(ctx, s.pool, sqlc.UpdateSavedInsightParams{
		EnvironmentID: uuidToPgtype(in.EnvironmentID), Key: in.Key, Name: in.Name, Kind: in.Kind, Query: []byte(in.Query),
		RangeDays: int32(in.RangeDays), Digest: in.Digest, AlertComparison: in.AlertComparison,
		AlertThreshold: in.AlertThreshold, ChannelIds: pgUUIDList(in.ChannelIDs),
		NextDigestAt: pgOptionalTimestamptz(in.NextDigestAt), NextCheckAt: pgOptionalTimestamptz(in.NextCheckAt)})
	if err != nil {
		return SavedInsight{}, savedInsightErr("update", err)
	}
	return savedInsightFromRow(row), nil
}

// GetSavedInsight implements SavedInsightStore.
func (s *PgStore) GetSavedInsight(ctx context.Context, environmentID, key string) (SavedInsight, error) {
	row, err := sqlc.New().GetSavedInsight(ctx, s.pool, sqlc.GetSavedInsightParams{EnvironmentID: uuidToPgtype(environmentID), Key: key})
	if err != nil {
		return SavedInsight{}, savedInsightErr("get", err)
	}
	return savedInsightFromRow(row), nil
}

// ListSavedInsights implements SavedInsightStore.
func (s *PgStore) ListSavedInsights(ctx context.Context, environmentID string) ([]SavedInsight, error) {
	rows, err := sqlc.New().ListSavedInsights(ctx, s.pool, uuidToPgtype(environmentID))
	if err != nil {
		return nil, savedInsightErr("list", err)
	}
	out := make([]SavedInsight, 0, len(rows))
	for _, r := range rows {
		out = append(out, savedInsightFromRow(r))
	}
	return out, nil
}

// DeleteSavedInsight implements SavedInsightStore.
func (s *PgStore) DeleteSavedInsight(ctx context.Context, environmentID, key string) (bool, error) {
	n, err := sqlc.New().DeleteSavedInsight(ctx, s.pool, sqlc.DeleteSavedInsightParams{EnvironmentID: uuidToPgtype(environmentID), Key: key})
	if err != nil {
		return false, savedInsightErr("delete", err)
	}
	return n > 0, nil
}

// ClaimDueSavedInsights implements SavedInsightStore.
func (s *PgStore) ClaimDueSavedInsights(ctx context.Context, now, leaseUntil time.Time, limit int) ([]SavedInsight, error) {
	rows, err := sqlc.New().ClaimDueSavedInsights(ctx, s.pool, sqlc.ClaimDueSavedInsightsParams{
		Now: pgTimestamptzUTC(now), LeaseUntil: pgTimestamptzUTC(leaseUntil), MaxRows: int32(limit)})
	if err != nil {
		return nil, savedInsightErr("claim", err)
	}
	out := make([]SavedInsight, 0, len(rows))
	for _, r := range rows {
		si := savedInsightFromRow(sqlc.SavedInsight{
			ID: r.ID, AccountID: r.AccountID, EnvironmentID: r.EnvironmentID, Key: r.Key, Name: r.Name, Kind: r.Kind,
			Query: r.Query, RangeDays: r.RangeDays, Digest: r.Digest, AlertComparison: r.AlertComparison,
			AlertThreshold: r.AlertThreshold, ChannelIds: r.ChannelIds, AlertState: r.AlertState, LastValue: r.LastValue,
			LastRunAt: r.LastRunAt, LastError: r.LastError, NextDigestAt: r.NextDigestAt, NextCheckAt: r.NextCheckAt,
			LeaseUntil: r.LeaseUntil, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt})
		si.Plan, si.ProjectID, si.EnvironmentSlug = r.Plan, pgUUIDString(r.ProjectID), r.EnvironmentSlug
		out = append(out, si)
	}
	return out, nil
}

// SettleSavedInsight implements SavedInsightStore.
func (s *PgStore) SettleSavedInsight(ctx context.Context, st SavedInsightSettle) (bool, error) {
	q := sqlc.New()
	n, err := q.SettleSavedInsight(ctx, s.pool, sqlc.SettleSavedInsightParams{
		LastValue: pgFloat8(st.LastValue), Now: pgTimestamptzUTC(st.Now), LastError: truncateUTF8(st.LastError, 512),
		AlertState: st.AlertState, NextDigestAt: pgOptionalTimestamptz(st.NextDigestAt),
		NextCheckAt: pgOptionalTimestamptz(st.NextCheckAt), ID: uuidToPgtype(st.ID),
		ClaimedUpdatedAt: pgtype.Timestamptz{Time: st.ClaimedUpdatedAt, Valid: true}})
	if err != nil {
		return false, savedInsightErr("settle", err)
	}
	if n > 0 {
		return true, nil
	}
	// A definition changed since the claim was not settled; drop the lease.
	return false, s.ReleaseSavedInsight(ctx, st.ID)
}

// ReleaseSavedInsight implements SavedInsightStore.
func (s *PgStore) ReleaseSavedInsight(ctx context.Context, id string) error {
	if err := sqlc.New().ReleaseSavedInsightLease(ctx, s.pool, uuidToPgtype(id)); err != nil {
		return savedInsightErr("release", err)
	}
	return nil
}

// SavedInsightChannels implements SavedInsightStore.
func (s *PgStore) SavedInsightChannels(ctx context.Context, accountID string, ids []string) ([]NotificationChannel, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := sqlc.New().SavedInsightChannels(ctx, s.pool, sqlc.SavedInsightChannelsParams{
		AccountID: uuidToPgtype(accountID), ChannelIds: pgUUIDList(ids)})
	if err != nil {
		return nil, savedInsightErr("channels", err)
	}
	out := make([]NotificationChannel, 0, len(rows))
	for _, r := range rows {
		out = append(out, channelFromRow(r))
	}
	return out, nil
}

// MemStore.

func cloneSavedInsight(s SavedInsight) SavedInsight {
	s.Query = slices.Clone(s.Query)
	s.ChannelIDs = slices.Clone(s.ChannelIDs)
	for _, p := range []**time.Time{&s.LastRunAt, &s.NextDigestAt, &s.NextCheckAt} {
		if *p != nil {
			t := **p
			*p = &t
		}
	}
	if s.LastValue != nil {
		v := *s.LastValue
		s.LastValue = &v
	}
	return s
}

func (m *MemStore) memSavedInsight(environmentID, key string) *memSavedInsightRow {
	for _, s := range m.savedInsights {
		if s.EnvironmentID == environmentID && s.Key == key {
			return s
		}
	}
	return nil
}

type memSavedInsightRow struct {
	SavedInsight
	leaseUntil time.Time
}

// CreateSavedInsight implements SavedInsightStore.
func (m *MemStore) CreateSavedInsight(_ context.Context, in SavedInsight, maxInsights int) (SavedInsight, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.memSavedInsight(in.EnvironmentID, in.Key) != nil {
		return SavedInsight{}, ErrConflict
	}
	n := 0
	for _, s := range m.savedInsights {
		if s.EnvironmentID == in.EnvironmentID {
			n++
		}
	}
	if n >= maxInsights {
		return SavedInsight{}, ErrSavedInsightLimit
	}
	if m.savedInsights == nil {
		m.savedInsights = map[string]*memSavedInsightRow{}
	}
	now := time.Now().UTC()
	in.ID, in.AlertState, in.CreatedAt, in.UpdatedAt = uuid.NewString(), SavedInsightOK, now, now
	in.LastValue, in.LastRunAt, in.LastError = nil, nil, ""
	m.savedInsights[in.ID] = &memSavedInsightRow{SavedInsight: cloneSavedInsight(in)}
	return cloneSavedInsight(in), nil
}

// UpdateSavedInsight implements SavedInsightStore.
func (m *MemStore) UpdateSavedInsight(_ context.Context, in SavedInsight) (SavedInsight, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.memSavedInsight(in.EnvironmentID, in.Key)
	if s == nil {
		return SavedInsight{}, ErrNotFound
	}
	if s.AlertComparison != in.AlertComparison || s.AlertThreshold != in.AlertThreshold ||
		string(s.Query) != string(in.Query) || s.RangeDays != in.RangeDays {
		s.AlertState = SavedInsightOK
	}
	s.Name, s.Kind, s.Query, s.RangeDays, s.Digest = in.Name, in.Kind, slices.Clone(in.Query), in.RangeDays, in.Digest
	s.AlertComparison, s.AlertThreshold, s.ChannelIDs = in.AlertComparison, in.AlertThreshold, slices.Clone(in.ChannelIDs)
	s.NextDigestAt, s.NextCheckAt = in.NextDigestAt, in.NextCheckAt
	s.UpdatedAt = time.Now().UTC()
	return cloneSavedInsight(s.SavedInsight), nil
}

// GetSavedInsight implements SavedInsightStore.
func (m *MemStore) GetSavedInsight(_ context.Context, environmentID, key string) (SavedInsight, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.memSavedInsight(environmentID, key)
	if s == nil {
		return SavedInsight{}, ErrNotFound
	}
	return cloneSavedInsight(s.SavedInsight), nil
}

// ListSavedInsights implements SavedInsightStore.
func (m *MemStore) ListSavedInsights(_ context.Context, environmentID string) ([]SavedInsight, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []SavedInsight{}
	for _, s := range m.savedInsights {
		if s.EnvironmentID == environmentID {
			out = append(out, cloneSavedInsight(s.SavedInsight))
		}
	}
	slices.SortFunc(out, func(a, b SavedInsight) int { return compareStrings(a.Key, b.Key) })
	return out, nil
}

// DeleteSavedInsight implements SavedInsightStore.
func (m *MemStore) DeleteSavedInsight(_ context.Context, environmentID, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.memSavedInsight(environmentID, key)
	if s == nil {
		return false, nil
	}
	delete(m.savedInsights, s.ID)
	return true, nil
}

// ClaimDueSavedInsights implements SavedInsightStore.
func (m *MemStore) ClaimDueSavedInsights(_ context.Context, now, leaseUntil time.Time, limit int) ([]SavedInsight, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var due []*memSavedInsightRow
	for _, s := range m.savedInsights {
		if s.NextCheckAt != nil && !s.NextCheckAt.After(now) && s.leaseUntil.Before(now) {
			due = append(due, s)
		}
	}
	slices.SortFunc(due, func(a, b *memSavedInsightRow) int {
		if c := a.NextCheckAt.Compare(*b.NextCheckAt); c != 0 {
			return c
		}
		return compareStrings(a.ID, b.ID)
	})
	out := []SavedInsight{}
	for _, s := range due[:min(limit, len(due))] {
		s.leaseUntil = leaseUntil
		si := cloneSavedInsight(s.SavedInsight)
		if a, ok := m.accounts[s.AccountID]; ok {
			si.Plan = string(a.Plan)
		}
		for _, env := range m.projectEnvironments {
			if env.ID == s.EnvironmentID {
				si.ProjectID, si.EnvironmentSlug = env.ProjectID, env.Slug
			}
		}
		out = append(out, si)
	}
	return out, nil
}

// SettleSavedInsight implements SavedInsightStore.
func (m *MemStore) SettleSavedInsight(_ context.Context, st SavedInsightSettle) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.savedInsights[st.ID]
	if !ok {
		return false, nil
	}
	s.leaseUntil = time.Time{}
	if !s.UpdatedAt.Equal(st.ClaimedUpdatedAt) {
		return false, nil
	}
	now := st.Now.UTC()
	s.LastValue, s.LastRunAt, s.LastError, s.AlertState = st.LastValue, &now, st.LastError, st.AlertState
	s.NextDigestAt, s.NextCheckAt = st.NextDigestAt, st.NextCheckAt
	return true, nil
}

// ReleaseSavedInsight implements SavedInsightStore.
func (m *MemStore) ReleaseSavedInsight(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.savedInsights[id]; ok {
		s.leaseUntil = time.Time{}
	}
	return nil
}

// SavedInsightChannels implements SavedInsightStore.
func (m *MemStore) SavedInsightChannels(_ context.Context, accountID string, ids []string) ([]NotificationChannel, error) {
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
