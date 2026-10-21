// Package dataapiusage stores hourly Data API usage per app (ADR-20261019102108).
// The Data API runtime reports deltas under workload identity; apid adds each
// report once and answers with month-to-date totals for owner caps. The counts
// are owner telemetry from the customer's own VM, never billing records.
package dataapiusage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/dataapiusage/sqlc"
)

// ErrNotFound means the app does not exist in the account, or was deleted.
var ErrNotFound = errors.New("dataapiusage: app not found")

// Counts are one hour's usage, or a delta to add to it.
type Counts struct {
	UserRequests      int64
	AnonymousRequests int64
	ServerRequests    int64
	RejectedRequests  int64
	ErrorRequests     int64
	CappedRequests    int64
	RowsReturned      int64
	ResponseBytes     int64
}

// Add returns the sum of two counts.
func (c Counts) Add(o Counts) Counts {
	return Counts{
		UserRequests: c.UserRequests + o.UserRequests, AnonymousRequests: c.AnonymousRequests + o.AnonymousRequests,
		ServerRequests: c.ServerRequests + o.ServerRequests, RejectedRequests: c.RejectedRequests + o.RejectedRequests,
		ErrorRequests: c.ErrorRequests + o.ErrorRequests, CappedRequests: c.CappedRequests + o.CappedRequests,
		RowsReturned: c.RowsReturned + o.RowsReturned, ResponseBytes: c.ResponseBytes + o.ResponseBytes,
	}
}

// Hour is one UTC hour's counts.
type Hour struct {
	Hour time.Time
	Counts
}

// MonthToDate is what owner caps compare against: admitted requests in the
// current UTC calendar month, all and anonymous.
type MonthToDate struct {
	Month             time.Time
	Requests          int64
	AnonymousRequests int64
}

// MonthStart is the first instant of t's UTC calendar month.
func MonthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// Store reads and writes data_api_usage_hourly and data_api_usage_reports.
type Store struct {
	pool *pgxpool.Pool
	q    *sqlc.Queries
}

// New returns a store over pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool, q: sqlc.New()} }

// Record adds a report's hourly deltas once per report id and returns the
// app's month-to-date totals. A repeated report id adds nothing. access, when
// set, replaces the app's reported access settings (validated JSON). Callers
// validate hours and deltas; Record also prunes this app's expired rows.
func (s *Store) Record(ctx context.Context, accountID, appID, reportID string, hours []Hour, access []byte, now time.Time) (MonthToDate, error) {
	var mtd MonthToDate
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := s.q.LockUsageApp(ctx, tx, sqlc.LockUsageAppParams{AppID: appID, AccountID: accountID}); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return fmt.Errorf("dataapiusage: lock app: %w", err)
		}
		claimed, err := s.q.ClaimUsageReport(ctx, tx, sqlc.ClaimUsageReportParams{AppID: appID, ReportID: reportID})
		if err != nil {
			return fmt.Errorf("dataapiusage: claim report: %w", err)
		}
		if claimed == 1 {
			for _, h := range hours {
				if err := s.q.AddUsageHour(ctx, tx, addParams(appID, h)); err != nil {
					return fmt.Errorf("dataapiusage: add hour: %w", err)
				}
			}
		}
		if access != nil {
			if err := s.q.UpsertAccessConfig(ctx, tx, sqlc.UpsertAccessConfigParams{AppID: appID, Config: access}); err != nil {
				return fmt.Errorf("dataapiusage: store access settings: %w", err)
			}
		}
		if err := s.prune(ctx, tx, appID, now); err != nil {
			return err
		}
		mtd, err = s.totals(ctx, tx, appID, now)
		return err
	})
	return mtd, err
}

// Access returns the access settings an app's runtime last reported, and
// when; ok is false when it never reported any.
func (s *Store) Access(ctx context.Context, appID string) (config []byte, reportedAt time.Time, ok bool, err error) {
	row, err := s.q.GetAccessConfig(ctx, s.pool, appID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, time.Time{}, false, nil
	}
	if err != nil {
		return nil, time.Time{}, false, fmt.Errorf("dataapiusage: read access settings: %w", err)
	}
	return row.Config, row.ReportedAt.Time, true, nil
}

// DeleteAccess forgets an app's reported access settings, so live reads
// without a user token stop until its runtime reports again.
func (s *Store) DeleteAccess(ctx context.Context, accountID, appID string) error {
	if _, err := s.q.DeleteAccessConfig(ctx, s.pool, sqlc.DeleteAccessConfigParams{AppID: appID, AccountID: accountID}); err != nil {
		return fmt.Errorf("dataapiusage: delete access settings: %w", err)
	}
	return nil
}

// MonthToDate returns an app's totals for now's UTC month.
func (s *Store) MonthToDate(ctx context.Context, appID string, now time.Time) (MonthToDate, error) {
	return s.totals(ctx, s.pool, appID, now)
}

// Hours lists an app's hours in [from, to), oldest first. Hours without
// usage are absent.
func (s *Store) Hours(ctx context.Context, appID string, from, to time.Time) ([]Hour, error) {
	rows, err := s.q.ListUsageHours(ctx, s.pool, sqlc.ListUsageHoursParams{AppID: appID, FromHour: ts(from), ToHour: ts(to)})
	if err != nil {
		return nil, fmt.Errorf("dataapiusage: list hours: %w", err)
	}
	hours := make([]Hour, 0, len(rows))
	for _, r := range rows {
		hours = append(hours, Hour{Hour: r.Hour.Time.UTC(), Counts: Counts{
			UserRequests: r.UserRequests, AnonymousRequests: r.AnonymousRequests, ServerRequests: r.ServerRequests,
			RejectedRequests: r.RejectedRequests, ErrorRequests: r.ErrorRequests, CappedRequests: r.CappedRequests,
			RowsReturned: r.RowsReturned, ResponseBytes: r.ResponseBytes,
		}})
	}
	return hours, nil
}

func (s *Store) totals(ctx context.Context, db sqlc.DBTX, appID string, now time.Time) (MonthToDate, error) {
	month := MonthStart(now)
	row, err := s.q.UsageTotals(ctx, db, sqlc.UsageTotalsParams{AppID: appID, FromHour: ts(month), ToHour: ts(month.AddDate(0, 1, 0))})
	if err != nil {
		return MonthToDate{}, fmt.Errorf("dataapiusage: month to date: %w", err)
	}
	return MonthToDate{Month: month, Requests: row.Requests, AnonymousRequests: row.AnonymousRequests}, nil
}

// prune removes this app's report ids past the dedup window and hours past
// retention, a bounded batch per report, so no sweeper is needed.
func (s *Store) prune(ctx context.Context, tx pgx.Tx, appID string, now time.Time) error {
	if _, err := s.q.PruneUsageReports(ctx, tx, sqlc.PruneUsageReportsParams{AppID: appID, Before: ts(now.Add(-api.DataAPIUsageDedupWindow)), RowLimit: api.DataAPIUsagePruneBatchLimit}); err != nil {
		return fmt.Errorf("dataapiusage: prune reports: %w", err)
	}
	if _, err := s.q.PruneUsageHours(ctx, tx, sqlc.PruneUsageHoursParams{AppID: appID, Before: ts(now.AddDate(0, 0, -api.DataAPIUsageRetentionDays)), RowLimit: api.DataAPIUsagePruneBatchLimit}); err != nil {
		return fmt.Errorf("dataapiusage: prune hours: %w", err)
	}
	return nil
}

func addParams(appID string, h Hour) sqlc.AddUsageHourParams {
	return sqlc.AddUsageHourParams{AppID: appID, Hour: ts(h.Hour),
		UserRequests: h.UserRequests, AnonymousRequests: h.AnonymousRequests, ServerRequests: h.ServerRequests,
		RejectedRequests: h.RejectedRequests, ErrorRequests: h.ErrorRequests, CappedRequests: h.CappedRequests,
		RowsReturned: h.RowsReturned, ResponseBytes: h.ResponseBytes}
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t.UTC(), Valid: true} }
