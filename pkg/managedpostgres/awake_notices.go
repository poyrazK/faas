package managedpostgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Awake notices (ADR-20261125231222): once a week, the account is told about
// each scale-to-zero database that was awake at least
// api.ManagedPostgresAwakeNoticeHoursPerDay a day over the last complete UTC
// week while one cause held at least api.ManagedPostgresAwakeNoticeCauseSharePercent
// of its starts. Notices are recorded per database and week, so a database is
// noticed at most once a week, and mailed once.

// AwakeNotice is one recorded notice.
type AwakeNotice struct {
	DatabaseID   string
	AccountID    string
	DatabaseName string
	WeekStart    time.Time
	AwakeSeconds int64
	Starts       int
	Cause        string
	AppSlug      string
	Detail       string
	CauseStarts  int
	RecordedAt   time.Time
}

// AwakeHoursPerDay is the week's average, rounded down.
func (n AwakeNotice) AwakeHoursPerDay() int {
	return int(n.AwakeSeconds / (7 * 3600))
}

// AwakeNoticeStore is an optional store capability.
type AwakeNoticeStore interface {
	// RecordAwakeNotices records the week's notices; a database already
	// noticed that week is left alone. It reports how many it recorded.
	RecordAwakeNotices(ctx context.Context, weekStart time.Time, minAwakeSeconds int64, minCausePercent int) (int64, error)
	// UnmailedAwakeNotices lists notices recorded at or after since that no
	// email has claimed, oldest first.
	UnmailedAwakeNotices(ctx context.Context, since time.Time, limit int) ([]AwakeNotice, error)
}

var _ AwakeNoticeStore = (*PostgresStore)(nil)

// RecordAwakeNotices implements AwakeNoticeStore.
func (s *PostgresStore) RecordAwakeNotices(ctx context.Context, weekStart time.Time, minAwakeSeconds int64, minCausePercent int) (int64, error) {
	n, err := sqlc.New().RecordManagedPostgresAwakeNotices(ctx, s.catalog, sqlc.RecordManagedPostgresAwakeNoticesParams{
		WeekStart: pgtype.Date{Time: weekStart.UTC(), Valid: true}, MinAwakeSeconds: minAwakeSeconds, MinCausePercent: int32(minCausePercent)})
	return n, mapPostgresError(err)
}

// UnmailedAwakeNotices implements AwakeNoticeStore.
func (s *PostgresStore) UnmailedAwakeNotices(ctx context.Context, since time.Time, limit int) ([]AwakeNotice, error) {
	if limit < 1 || limit > 500 {
		return nil, ErrInvalid
	}
	rows, err := sqlc.New().ListUnmailedManagedPostgresAwakeNotices(ctx, s.catalog, sqlc.ListUnmailedManagedPostgresAwakeNoticesParams{
		Since: databaseNullableTime(since), RowLimit: int32(limit)})
	if err != nil {
		return nil, mapPostgresError(err)
	}
	out := make([]AwakeNotice, 0, len(rows))
	for _, row := range rows {
		out = append(out, AwakeNotice{DatabaseID: postgresUUIDString(row.DatabaseID), AccountID: postgresUUIDString(row.AccountID),
			DatabaseName: row.Name, WeekStart: row.WeekStart.Time.UTC(), AwakeSeconds: row.AwakeSeconds, Starts: int(row.Starts),
			Cause: row.Cause, AppSlug: row.CauseAppSlug, Detail: row.CauseDetail, CauseStarts: int(row.CauseStarts),
			RecordedAt: row.RecordedAt.Time.UTC()})
	}
	return out, nil
}

// lastCompleteWeek is the Monday 00:00 UTC that starts the last complete
// week before now.
func lastCompleteWeek(now time.Time) time.Time {
	day := now.UTC().Truncate(24 * time.Hour)
	sinceMonday := (int(day.Weekday()) + 6) % 7
	return day.AddDate(0, 0, -sinceMonday-7)
}

// awakeNoticeBatch bounds the notices one pass mails.
const awakeNoticeBatch = 100

// noticeAwake records last week's notices and mails the unmailed ones, at
// most once an hour. A failure is logged; the next pass retries.
func (c *UsageCollector) noticeAwake(ctx context.Context) {
	store, ok := c.store.(AwakeNoticeStore)
	now := c.now()
	if !ok || c.notifyAwake == nil || now.Sub(c.awakeNoticedAt) < time.Hour {
		return
	}
	c.awakeNoticedAt = now
	minAwake := int64(api.ManagedPostgresAwakeNoticeHoursPerDay) * 7 * 3600
	if _, err := store.RecordAwakeNotices(ctx, lastCompleteWeek(now), minAwake, api.ManagedPostgresAwakeNoticeCauseSharePercent); err != nil {
		c.logger.Warn("managed postgres awake notices not recorded", "error", err)
		return
	}
	notices, err := store.UnmailedAwakeNotices(ctx, now.Add(-api.InternalMailManagedPostgresAwakeTTL), awakeNoticeBatch)
	if err != nil {
		c.logger.Warn("managed postgres awake notices not listed", "error", err)
		return
	}
	for _, notice := range notices {
		if err := c.notifyAwake(ctx, notice); err != nil {
			c.logger.Warn("managed postgres awake email not queued", "database_id", notice.DatabaseID, "error", err)
		}
	}
}
