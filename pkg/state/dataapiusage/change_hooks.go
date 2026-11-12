package dataapiusage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/dataapiusage/sqlc"
)

// RecordChangeHooks stores the change hook delivery an app's runtime reports
// (ADR-20261022121642 amendment 1), received at now. Callers validate the
// report. ErrNotFound means the app left the account or was deleted.
func (s *Store) RecordChangeHooks(ctx context.Context, accountID, appID string, report api.DataAPIChangeHookReport, now time.Time) error {
	n, err := s.q.UpsertChangeHookReport(ctx, s.pool, sqlc.UpsertChangeHookReportParams{
		AppID: appID, AccountID: accountID, ReportedAt: ts(now), Pending: report.Pending, Failed: report.Failed,
		OldestPendingAt: optionalTS(report.OldestPendingAt), LastPublishedAt: optionalTS(report.LastPublishedAt),
		LastFailureCode: pgtype.Text{String: report.LastFailureCode, Valid: report.LastFailureCode != ""}, Publishing: report.Publishing,
		LastDroppedAt: optionalTS(report.LastDroppedAt),
	})
	if err != nil {
		return fmt.Errorf("dataapiusage: store change hook report: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ChangeHooks returns the change hook delivery an app's runtime last
// reported, or nil when it never reported.
func (s *Store) ChangeHooks(ctx context.Context, appID string) (*api.DataAPIChangeHookStatus, error) {
	row, err := s.q.GetChangeHookReport(ctx, s.pool, appID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dataapiusage: read change hook report: %w", err)
	}
	return &api.DataAPIChangeHookStatus{
		ReportedAt: row.ReportedAt.Time.UTC(), Pending: row.Pending, Failed: row.Failed,
		OldestPendingAt: timePointer(row.OldestPendingAt), LastPublishedAt: timePointer(row.LastPublishedAt),
		LastFailureCode: row.LastFailureCode.String, Publishing: row.Publishing, LastDroppedAt: timePointer(row.LastDroppedAt),
	}, nil
}

func optionalTS(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return ts(*t)
}

func timePointer(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	utc := t.Time.UTC()
	return &utc
}
