package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ListIssueSlackDestinations(ctx context.Context, app, owner string) ([]IssueSlackDestination, error) {
	rows, err := sqlc.New().IssueSlackDestinationList(ctx, s.pool, sqlc.IssueSlackDestinationListParams{ID: issueUUID(app), AccountID: issueUUID(owner)})
	out := []IssueSlackDestination{}
	for _, r := range rows {
		out = append(out, IssueSlackDestination{ID: issueID(r.ID), TeamID: issueID(r.TeamID), TeamName: r.TeamName, ChannelID: r.ChannelID, Enabled: r.Enabled, Revision: r.Revision, OAuthRevision: r.OauthRevision.Int64})
	}
	return out, err
}
func (s *PgStore) SetIssueSlackDestination(ctx context.Context, app, owner string, in PutIssueSlackDestination) (IssueSlackDestination, error) {
	out := IssueSlackDestination{}
	id, err := uuid.Parse(in.TeamID)
	if err != nil || id == uuid.Nil || in.Revision < 0 || !ValidIssueSlackChannel(in.ChannelID) || len(in.TokenSealed) > api.IssueSlackTokenMaxBytes*2 {
		return out, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	a, err := q.IssueLockApp(ctx, tx, issueUUID(app))
	if err != nil {
		return out, mapErr(err)
	}
	if issueID(a.AccountID) != owner {
		return out, ErrNotFound
	}
	installed, err := readSlackInstallation(ctx, q, tx, app, owner)
	if err != nil {
		return out, err
	}
	if installed.Status == "connected" {
		return out, ErrInvalidArgument
	}
	out, err = putIssueSlackDestination(ctx, q, tx, a, in)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func putIssueSlackDestination(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, a sqlc.IssueLockAppRow, in PutIssueSlackDestination) (IssueSlackDestination, error) {
	out := IssueSlackDestination{}
	exists, err := q.IssueTeamExists(ctx, tx, sqlc.IssueTeamExistsParams{AppID: a.ID, TeamID: issueUUID(in.TeamID)})
	if err != nil {
		return out, err
	}
	if !exists {
		return out, ErrInvalidArgument
	}
	old, err := q.IssueSlackDestinationGet(ctx, tx, sqlc.IssueSlackDestinationGetParams{AppID: a.ID, AccountID: a.AccountID, TeamID: issueUUID(in.TeamID)})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if old.Revision != in.Revision {
		return out, api.NewProblem(409, "issue_slack_destination_changed", "Slack destination changed", "reload Slack settings before saving")
	}
	if len(in.TokenSealed) == 0 {
		in.TokenSealed = old.TokenSealed
	}
	if len(in.TokenSealed) == 0 {
		return out, ErrInvalidArgument
	}
	r, err := q.IssueSlackDestinationPut(ctx, tx, sqlc.IssueSlackDestinationPutParams{ID: issueUUID(uuid.NewString()), AppID: a.ID, AccountID: a.AccountID, TeamID: issueUUID(in.TeamID), ChannelID: in.ChannelID, TokenSealed: in.TokenSealed, Enabled: in.Enabled})
	if err != nil {
		return out, err
	}
	if err = q.IssueSlackCancelPrevious(ctx, tx, sqlc.IssueSlackCancelPreviousParams{DestinationID: r.ID, DestinationRevision: r.Revision}); err != nil {
		return out, err
	}
	return IssueSlackDestination{ID: issueID(r.ID), TeamID: issueID(r.TeamID), ChannelID: r.ChannelID, Enabled: r.Enabled, Revision: r.Revision}, nil
}

func (s *PgStore) ListIssueSlackDeliveries(ctx context.Context, app, owner string) ([]IssueSlackDelivery, error) {
	rows, err := sqlc.New().IssueSlackDeliveryList(ctx, s.pool, sqlc.IssueSlackDeliveryListParams{ID: issueUUID(app), AccountID: issueUUID(owner), Limit: api.IssueSlackDeliveryPageSize})
	out := []IssueSlackDelivery{}
	for _, r := range rows {
		out = append(out, IssueSlackDelivery{RoutingRuleKey: r.RoutingRuleKey, RoutingRevision: r.RoutingRevision, ID: issueID(r.ID), NotificationID: issueID(r.NotificationID), ActivityID: issueID(r.ActivityID), ReminderID: issueID(r.ReminderID), IssueID: issueID(r.IssueID), IncidentID: issueID(r.IncidentID), TeamName: r.TeamName, ChannelID: r.ChannelID, Kind: r.Kind, Status: r.Status, LastError: r.LastError, Attempts: r.TotalAttempts, DestinationRevision: r.DestinationRevision, CanRetry: r.CanRetry, ResponseCode: int(r.ResponseCode), NextAttemptAt: r.NextAttemptAt.Time, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time})
	}
	return out, err
}
func (s *PgStore) RetryIssueSlackDelivery(ctx context.Context, app, owner, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	a, err := q.IssueLockApp(ctx, tx, issueUUID(app))
	if err != nil {
		return mapErr(err)
	}
	if issueID(a.AccountID) != owner {
		return ErrNotFound
	}
	r, err := q.IssueSlackRetryGet(ctx, tx, sqlc.IssueSlackRetryGetParams{ID: issueUUID(id), ID_2: a.ID, AccountID: a.AccountID})
	if err != nil {
		return mapErr(err)
	}
	if !r.ApplicationRetryAllowed || !r.Enabled || r.CurrentRevision != r.DestinationRevision || (r.Status != "failed" && r.Status != "dead") {
		return api.NewProblem(409, "issue_slack_delivery_changed", "Delivery cannot be retried", "only retained failed deliveries for the current enabled destination may be retried")
	}
	if err = q.IssueSlackRetry(ctx, tx, sqlc.IssueSlackRetryParams{ID: r.ID, ExpiresAt: issueTime(time.Now().Add(api.IssueSlackDeliveryTTL))}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func issueSlackEntitledPlans() []string {
	out := []string{}
	for _, p := range api.Plans {
		if p.IssueLimits().Enabled {
			out = append(out, string(p))
		}
	}
	return out
}
func (s *PgStore) CleanupIssueSlackThreads(ctx context.Context) error {
	q := sqlc.New()
	if err := q.IssueSlackOAuthExpiredCleanup(ctx, s.pool, api.IssueSlackBatch); err != nil {
		return err
	}
	if err := q.IssueApplicationSlackHistoryExpire(ctx, s.pool, sqlc.IssueApplicationSlackHistoryExpireParams{Before: issueTime(time.Now().Add(-api.IssueApplicationSlackHistoryRetention)), BatchSize: api.IssueApplicationSlackCleanupBatch}); err != nil {
		return err
	}
	return q.IssueSlackThreadCleanup(ctx, s.pool)
}
