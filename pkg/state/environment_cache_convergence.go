package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const EnvironmentCacheObservationFreshness = 90 * time.Second
const EnvironmentCacheObservationBatchSize = 100

type GatewayEnvironmentCachePolicy struct {
	AccountID     string
	AppID         string
	EnvironmentID string
	Scope         string
	Revision      int64
}

func (p GatewayEnvironmentCachePolicy) Key() string { return p.AppID + "/" + p.EnvironmentID }

type EnvironmentCacheConvergenceReader interface {
	GetEnvironmentCacheConvergence(context.Context, string, string, string) (api.EnvironmentCacheConvergence, error)
}
type EnvironmentCacheGatewayObserver interface {
	BeginEnvironmentCacheGatewaySession(context.Context, string, string, time.Time) error
	RefreshEnvironmentCacheGatewaySession(context.Context, string, string) error
	ListEnvironmentCacheGatewayPolicies(context.Context, string, string, string) ([]GatewayEnvironmentCachePolicy, error)
	RecordEnvironmentCacheGatewayObserved(context.Context, string, string, string, string, int64) error
}

func cacheConvergenceStatus(out *api.EnvironmentCacheConvergence) {
	out.Status = "pending"
	if out.Revision < 1 || out.ServingGateways == 0 || out.StaleGateways > 0 {
		out.Status = "degraded"
	} else if out.AcknowledgedGateways == out.ServingGateways {
		out.Status = "converged"
	}
}
func (s *PgStore) GetEnvironmentCacheConvergence(ctx context.Context, account, app, scope string) (api.EnvironmentCacheConvergence, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.EnvironmentCacheConvergence{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	policy, err := pgEnvironmentCachePolicy(ctx, tx, account, app, scope, false)
	if err != nil {
		return api.EnvironmentCacheConvergence{}, err
	}
	row, err := sqlc.New().ReadEnvironmentCacheConvergenceCounts(ctx, tx, sqlc.ReadEnvironmentCacheConvergenceCountsParams{AppID: mustPgUUID(app), EnvironmentID: mustPgUUID(policy.EnvironmentID), Revision: policy.Revision})
	if err != nil {
		return api.EnvironmentCacheConvergence{}, err
	}
	out := api.EnvironmentCacheConvergence{AppID: app, ProjectID: policy.ProjectID, EnvironmentID: policy.EnvironmentID, Scope: scope, Revision: policy.Revision, CheckedAt: row.CheckedAt.Time, ServingGateways: row.Serving, AcknowledgedGateways: row.Acknowledged, PendingGateways: row.Pending, StaleGateways: row.Stale}
	cacheConvergenceStatus(&out)
	return out, tx.Commit(ctx)
}
func (s *PgStore) BeginEnvironmentCacheGatewaySession(ctx context.Context, node, session string, started time.Time) error {
	if _, err := uuid.Parse(session); err != nil || node == "" || started.IsZero() {
		return ErrInvalidArgument
	}
	n, err := sqlc.New().BeginEnvironmentCacheGatewaySession(ctx, s.pool, sqlc.BeginEnvironmentCacheGatewaySessionParams{NodeName: node, SessionID: mustPgUUID(session), StartedAt: pgtype.Timestamptz{Time: started, Valid: true}})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func (s *PgStore) RefreshEnvironmentCacheGatewaySession(ctx context.Context, node, session string) error {
	n, err := sqlc.New().RefreshEnvironmentCacheGatewaySession(ctx, s.pool, sqlc.RefreshEnvironmentCacheGatewaySessionParams{NodeName: node, SessionID: mustPgUUID(session)})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func (s *PgStore) ListEnvironmentCacheGatewayPolicies(ctx context.Context, node, session, after string) ([]GatewayEnvironmentCachePolicy, error) {
	rows, err := sqlc.New().ListEnvironmentCacheGatewayPolicies(ctx, s.pool, sqlc.ListEnvironmentCacheGatewayPoliciesParams{NodeName: node, SessionID: mustPgUUID(session), AfterKey: after})
	if err != nil {
		return nil, err
	}
	out := make([]GatewayEnvironmentCachePolicy, len(rows))
	for i, r := range rows {
		out[i] = GatewayEnvironmentCachePolicy{AccountID: r.AccountID, AppID: r.AppID, EnvironmentID: r.EnvironmentID, Scope: r.Scope, Revision: r.Revision}
	}
	return out, nil
}
func (s *PgStore) RecordEnvironmentCacheGatewayObserved(ctx context.Context, node, session, app, environment string, revision int64) error {
	n, err := sqlc.New().RecordEnvironmentCacheGatewayObserved(ctx, s.pool, sqlc.RecordEnvironmentCacheGatewayObservedParams{NodeName: node, SessionID: mustPgUUID(session), AppID: mustPgUUID(app), EnvironmentID: mustPgUUID(environment), Revision: revision})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
