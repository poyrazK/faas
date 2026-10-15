package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) OperatorOverview(ctx context.Context, now time.Time) (api.ObsOverviewResponse, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.ObsOverviewResponse{}, fmt.Errorf("operator overview snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	out, err := pgOperatorOverview(ctx, tx, now.UTC())
	if err != nil {
		return api.ObsOverviewResponse{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.ObsOverviewResponse{}, fmt.Errorf("operator overview commit: %w", err)
	}
	return out, nil
}

func pgOperatorOverview(ctx context.Context, tx pgx.Tx, now time.Time) (api.ObsOverviewResponse, error) {
	out := api.ObsOverviewResponse{
		GeneratedAt: now, NodeHealth: []api.ObsOverviewNodeHealth{},
		RecentFailures1h: []api.ObsOverviewFailureKind{}, TopRateLimitedAccounts24h: []api.ObsOverviewRateLimited{},
	}
	q := sqlc.New()
	at := pgtype.Timestamptz{Time: now, Valid: true}
	auditSince := pgtype.Timestamptz{Time: now.Add(-api.ObsOverviewAuditWindow), Valid: true}
	cohortSince := pgtype.Timestamptz{Time: now.Add(-api.ObsOverviewCohortWindow), Valid: true}
	totals, err := q.OperatorOverviewTotals(ctx, tx, sqlc.OperatorOverviewTotalsParams{AuditSince: auditSince, ObservedAt: at})
	if err != nil {
		return out, fmt.Errorf("operator overview totals: %w", err)
	}
	out.Totals = api.ObsOverviewTotals{
		AccountsActive: int(totals.AccountsActive), AccountsPastDue: int(totals.AccountsPastDue), AccountsSuspended: int(totals.AccountsSuspended),
		AppsTotal: int(totals.AppsTotal), InstancesLive: int(totals.InstancesLive), InstancesWaking: int(totals.InstancesWaking),
		NodesActive: int(totals.NodesActive), NodesInactive: int(totals.NodesInactive), OrgsTotal: int(totals.OrgsTotal), AuditEvents24h: int(totals.AuditEvents24h),
	}
	funnel, err := q.OperatorOverviewFunnel(ctx, tx, sqlc.OperatorOverviewFunnelParams{ObservedAt: at, CohortSince: cohortSince})
	if err != nil {
		return out, fmt.Errorf("operator overview funnel: %w", err)
	}
	out.BetaFunnel14d = api.ObsBetaFunnel{
		WindowStartedAt: cohortSince.Time, AccountsCreated: int(funnel.AccountsCreated), EmailVerified: int(funnel.EmailVerified),
		WithApp: int(funnel.WithApp), WithLiveDeployment: int(funnel.WithLiveDeployment), WithSuccessfulRequest: int(funnel.WithSuccessfulRequest),
		SignupToFirstSuccessSamples: int(funnel.WithSuccessfulRequest), SignupToFirstSuccessP50Seconds: funnel.P50Seconds, SignupToFirstSuccessP95Seconds: funnel.P95Seconds,
	}
	nodes, err := q.OperatorOverviewNodes(ctx, tx)
	if err != nil {
		return out, fmt.Errorf("operator overview nodes: %w", err)
	}
	for _, node := range nodes {
		heartbeat := node.LastHeartbeatAt.Time.UTC()
		out.NodeHealth = append(out.NodeHealth, api.ObsOverviewNodeHealth{Name: node.Name, Active: node.Active.Bool, LastHeartbeatAt: heartbeat,
			Stale: !heartbeat.IsZero() && now.Sub(heartbeat) > api.ObsOverviewNodeStaleAfter})
	}
	failures, err := q.OperatorOverviewFailures(ctx, tx, sqlc.OperatorOverviewFailuresParams{
		Since: pgtype.Timestamptz{Time: now.Add(-api.ObsOverviewFailureWindow), Valid: true}, ObservedAt: at, TopN: api.ObsOverviewTopN,
	})
	if err != nil {
		return out, fmt.Errorf("operator overview failures: %w", err)
	}
	for _, failure := range failures {
		out.RecentFailures1h = append(out.RecentFailures1h, api.ObsOverviewFailureKind{Kind: failure.Kind, Count: int(failure.Hits)})
	}
	rateLimited, err := q.OperatorOverviewRateLimited(ctx, tx, sqlc.OperatorOverviewRateLimitedParams{Since: auditSince, ObservedAt: at, TopN: api.ObsOverviewTopN})
	if err != nil {
		return out, fmt.Errorf("operator overview rate limits: %w", err)
	}
	for _, account := range rateLimited {
		out.TopRateLimitedAccounts24h = append(out.TopRateLimitedAccounts24h, api.ObsOverviewRateLimited{AccountID: account.AccountID, Hits: int(account.Hits)})
	}
	return out, nil
}
