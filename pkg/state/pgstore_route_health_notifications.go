package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// The caller holds the traffic transaction's app/deployment locks. A blocked
// check commits its history and event together; allowed checks commit only
// when all later traffic, audit and lease checks succeed.
func pgRouteHealthNotification(ctx context.Context, tx pgx.Tx, snapshot RoutePolicySnapshot, entry api.RouteHealthHistoryEntry) error {
	if entry.ID == "" || snapshot.App.Status == AppDeleted || !snapshot.Account.MayDeploy() {
		return nil
	}
	q := sqlc.New()
	row, err := q.ReadRouteHealthNotificationState(ctx, tx, sqlc.ReadRouteHealthNotificationStateParams{DeploymentID: entry.Report.DeploymentID, AppID: snapshot.App.ID, AccountID: snapshot.Account.ID})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("read route health notification state: %w", err)
	}
	previous := routeHealthNotificationState{ContextKey: row.ContextKey, Status: row.Status, BlockedDecisionID: row.BlockedDecisionID}
	next, err := routeHealthTransition(previous, entry, snapshot.App.Slug)
	if err != nil || next.State == previous {
		return err
	}
	if err := q.WriteRouteHealthNotificationState(ctx, tx, sqlc.WriteRouteHealthNotificationStateParams{DeploymentID: entry.Report.DeploymentID, AppID: snapshot.App.ID, AccountID: snapshot.Account.ID,
		ContextKey: next.State.ContextKey, Status: next.State.Status, BlockedDecisionID: next.State.BlockedDecisionID, UpdatedAt: NewPgtypeTime(entry.CheckedAt)}); err != nil {
		return fmt.Errorf("write route health notification state: %w", err)
	}
	if next.Event == "" {
		return nil
	}
	if err := q.EnqueueRouteHealthNotification(ctx, tx, sqlc.EnqueueRouteHealthNotificationParams{AppID: snapshot.App.ID, AccountID: snapshot.Account.ID,
		Event: string(next.Event), DecisionID: entry.ID, Payload: next.Payload}); err != nil {
		return fmt.Errorf("enqueue route health notification: %w", err)
	}
	return nil
}
