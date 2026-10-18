package state

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) OperatorAuditTraceCoverage(ctx context.Context, since time.Time) (float64, error) {
	return sqlc.New().OperatorAuditTraceCoverage(ctx, s.pool, pgtype.Timestamptz{Time: since, Valid: true})
}

func (s *PgStore) OperatorOverviewActivity(ctx context.Context, auditSince, failureSince time.Time) (OperatorOverviewActivity, error) {
	q := sqlc.New()
	row, err := q.OperatorOverviewActivity(ctx, s.pool, pgtype.Timestamptz{Time: auditSince, Valid: true})
	if err != nil {
		return OperatorOverviewActivity{}, fmt.Errorf("operator overview counts: %w", err)
	}
	failures, err := q.OperatorRecentFailureCounts(ctx, s.pool, pgtype.Timestamptz{Time: failureSince, Valid: true})
	if err != nil {
		return OperatorOverviewActivity{}, fmt.Errorf("operator failure counts: %w", err)
	}
	out := OperatorOverviewActivity{OrgsTotal: int(row.OrgsTotal), AuditEvents24h: int(row.AuditEvents24h), Failures: []api.ObsOverviewFailureKind{}}
	for _, f := range failures {
		out.Failures = append(out.Failures, api.ObsOverviewFailureKind{Kind: f.Kind, Count: int(f.Hits)})
	}
	return out, nil
}

func (s *PgStore) OperatorTenantCounts(ctx context.Context, accountID string) (int, int, error) {
	id, err := uuid.Parse(accountID)
	if err != nil {
		return 0, 0, fmt.Errorf("operator tenant id: %w", err)
	}
	row, err := sqlc.New().OperatorTenantCounts(ctx, s.pool, pgtype.UUID{Bytes: id, Valid: true})
	if err != nil {
		return 0, 0, fmt.Errorf("operator tenant counts: %w", err)
	}
	return int(row.AppsCount), int(row.DeploymentsLiveCount), nil
}

func (s *PgStore) ListOperatorIncidents(ctx context.Context, f OperatorIncidentFilter) ([]api.ObsIncident, error) {
	rows, err := sqlc.New().OperatorIncidents(ctx, s.pool, sqlc.OperatorIncidentsParams{
		Since: pgtype.Timestamptz{Time: f.Since, Valid: true}, StuckBefore: pgtype.Timestamptz{Time: f.StuckBefore, Valid: true}, StaleBefore: pgtype.Timestamptz{Time: f.StaleBefore, Valid: true},
		Kind: f.Kind, Severity: f.Severity, BeforeID: f.BeforeID, BeforeAt: pgtype.Timestamptz{Time: f.BeforeAt, Valid: !f.BeforeAt.IsZero()}, PageLimit: int32(f.Limit),
	})
	if err != nil {
		return nil, fmt.Errorf("operator incidents: %w", err)
	}
	out := make([]api.ObsIncident, 0, len(rows))
	for _, r := range rows {
		out = append(out, api.ObsIncident{ID: r.ID, Type: r.Type, Severity: r.Severity, Status: r.Status, Summary: r.Summary, AccountID: r.AccountID, AppID: r.AppID, ResourceID: r.ResourceID, ResourceName: r.ResourceName, ObservedAt: r.ObservedAt.Time.UTC()})
	}
	return out, nil
}
