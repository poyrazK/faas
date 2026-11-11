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

// AppendGatewayEdgeUsageEvent durably deduplicates one replayable edge frame,
// bytes a gateway served for an app without an instance, and applies them as
// the app's egress in the same transaction (ADR-20261104132753). A frame for
// an app that no longer exists is recorded and dropped, so the caller can ACK
// it instead of replaying it forever.
func (s *PgStore) AppendGatewayEdgeUsageEvent(ctx context.Context, nodeID, eventID, appID string, minute time.Time, txBytes int64) error {
	if txBytes < 0 {
		return fmt.Errorf("state: invalid gateway edge usage bytes")
	}
	node, err := parsePgUUID(nodeID)
	if err != nil {
		return err
	}
	event, err := parsePgUUID(eventID)
	if err != nil {
		return err
	}
	app, err := parsePgUUID(appID)
	if err != nil {
		return err
	}
	usageID, err := parsePgUUID(api.EdgeUsageInstanceID(appID))
	if err != nil {
		return err
	}
	at := pgtype.Timestamptz{Time: minute.UTC().Truncate(time.Minute), Valid: true}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	inserted, err := q.RegisterGatewayUsageEvent(ctx, tx, sqlc.RegisterGatewayUsageEventParams{
		NodeID: node, EventID: event, InstanceID: usageID, Minute: at,
	})
	if err != nil {
		return err
	}
	if inserted && txBytes > 0 {
		if _, err := q.ApplyGatewayEdgeUsageEvent(ctx, tx, sqlc.ApplyGatewayEdgeUsageEventParams{
			Minute: at, TxBytes: txBytes, AppID: app,
		}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// SyncRealtimeEgressUsage appends the realtime bytes delivered this UTC month
// and not yet metered as each app's egress (ADR-20261104132753), at now's
// minute. It returns the number of apps it appended bytes for.
func (s *PgStore) SyncRealtimeEgressUsage(ctx context.Context, now time.Time) (int64, error) {
	now = now.UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.LockRealtimeEgressSync(ctx, tx); err != nil {
		return 0, err
	}
	rows, err := q.SyncRealtimeEgressUsage(ctx, tx, sqlc.SyncRealtimeEgressUsageParams{
		Minute:     pgtype.Timestamptz{Time: now.Truncate(time.Minute), Valid: true},
		MonthStart: pgtype.Timestamptz{Time: start, Valid: true},
		MonthEnd:   pgtype.Timestamptz{Time: end, Valid: true},
	})
	if err != nil {
		return 0, err
	}
	return rows, tx.Commit(ctx)
}

// BuildSecondsThisMonth returns the plan of the account that pays for appID
// and the builder seconds its apps used this UTC month (ADR-20261104132753).
func (s *PgStore) BuildSecondsThisMonth(ctx context.Context, appID string, now time.Time) (api.Plan, int64, error) {
	app, err := parsePgUUID(appID)
	if err != nil {
		return "", 0, err
	}
	now = now.UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	row, err := sqlc.New().BuildSecondsThisMonthForApp(ctx, s.pool, sqlc.BuildSecondsThisMonthForAppParams{
		AppID: app, MonthStart: pgtype.Timestamptz{Time: start, Valid: true},
		MaxBuildSeconds: float64(api.BuildTimeoutSeconds),
	})
	if err != nil {
		return "", 0, err
	}
	return api.Plan(row.Plan), row.UsedSeconds, nil
}
