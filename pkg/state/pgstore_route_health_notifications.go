package state

import (
	"context"
	"errors"
	"fmt"
	"time"

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
	if row.UpdatedAt.Valid {
		previous.Since = row.UpdatedAt.Time.UTC()
	}
	next, err := routeHealthTransition(previous, entry, snapshot.App.Slug)
	if err != nil || next.State == previous {
		return err
	}
	if err := q.WriteRouteHealthNotificationState(ctx, tx, sqlc.WriteRouteHealthNotificationStateParams{DeploymentID: entry.Report.DeploymentID, AppID: snapshot.App.ID, AccountID: snapshot.Account.ID,
		ContextKey: next.State.ContextKey, Status: next.State.Status, BlockedDecisionID: next.State.BlockedDecisionID, UpdatedAt: NewPgtypeTime(next.State.Since)}); err != nil {
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

// RouteHealthHoldAborter records that the hold deadline ended a held canary
// (ADR-20261028110044 amendment 2): the hold reads as aborted, so
// route_health_holds keeps counting it within its window, and app webhooks
// receive routes.health.aborted naming the hold's first blocking decision.
type RouteHealthHoldAborter interface {
	AbortRouteHealthHold(ctx context.Context, accountID, appID, deploymentID string, at time.Time) error
}

func (s *PgStore) AbortRouteHealthHold(ctx context.Context, accountID, appID, deploymentID string, at time.Time) error {
	app, err := s.AppByID(ctx, appID)
	if err != nil {
		return err
	}
	acct, err := s.AccountByID(ctx, accountID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin route health hold abort: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	blockedID, err := q.AbortRouteHealthHold(ctx, tx, sqlc.AbortRouteHealthHoldParams{DeploymentID: deploymentID, AppID: appID, AccountID: accountID, AbortedAt: NewPgtypeTime(at)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("record route health hold abort: %w", err)
	}
	if blockedID != "" && app.Status != AppDeleted && acct.MayDeploy() {
		body, err := q.ReadRouteHealthHistoryEntry(ctx, tx, sqlc.ReadRouteHealthHistoryEntryParams{ID: blockedID, AppID: appID, AccountID: accountID, DeploymentID: deploymentID})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// Pruned from bounded history: the abort is still recorded.
		case err != nil:
			return fmt.Errorf("read held route health decision: %w", err)
		default:
			entry, err := decodeRouteHealthHistory(body, appID, deploymentID)
			if err != nil {
				return err
			}
			payload, err := routeHealthHoldAbortedPayload(entry, app.Slug, at)
			if err != nil {
				return err
			}
			if err := q.EnqueueRouteHealthNotification(ctx, tx, sqlc.EnqueueRouteHealthNotificationParams{AppID: appID, AccountID: accountID,
				Event: string(AppWebhookEventRouteHealthAborted), DecisionID: blockedID, Payload: payload}); err != nil {
				return fmt.Errorf("enqueue route health hold abort notification: %w", err)
			}
		}
	}
	return tx.Commit(ctx)
}
