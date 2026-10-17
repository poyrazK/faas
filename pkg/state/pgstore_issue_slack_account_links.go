package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) GetIssueSlackAccountLink(ctx context.Context, app, owner, actor, issue, token string) (IssueSlackAccountLink, error) {
	out := IssueSlackAccountLink{}
	q := sqlc.New()
	if _, err := applicationTriageScope(ctx, q, s.pool, app, owner, actor, issue); err != nil {
		return out, mapErr(err)
	}
	row, err := q.IssueSlackIdentityMine(ctx, s.pool, sqlc.IssueSlackIdentityMineParams{AppID: issueUUID(app), AccountID: issueUUID(actor)})
	if err == nil {
		out.UserID = row.SlackUserID
		out.WorkspaceID = row.WorkspaceID
		out.ExpiresAt = trackerSnoozeTime(row.ExpiresAt)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if token != "" {
		if !slackLinkToken.MatchString(token) {
			return out, ErrInvalidArgument
		}
		ch, err := q.IssueSlackChallengeByToken(ctx, s.pool, sqlc.IssueSlackChallengeByTokenParams{AppID: issueUUID(app), Token: token})
		if err != nil {
			return out, mapErr(err)
		}
		if issueID(ch.IssueID) != issue {
			return out, ErrNotFound
		}
		out.ChallengeUserID = ch.SlackUserID
		out.ChallengeWorkspaceID = ch.WorkspaceID
		out.ChallengeToken = token
	}
	return out, nil
}
func (s *PgStore) ConfirmIssueSlackAccountLink(ctx context.Context, app, owner, actor, issue, token string) error {
	if !slackLinkToken.MatchString(token) {
		return ErrInvalidArgument
	}
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
	scope, err := applicationTriageScope(ctx, q, tx, app, owner, actor, issue)
	if err != nil {
		return mapErr(err)
	}
	if scope.State != "open" {
		return applicationTriageConflict()
	}
	ch, err := q.IssueSlackChallengeByToken(ctx, tx, sqlc.IssueSlackChallengeByTokenParams{AppID: a.ID, Token: token})
	if err != nil {
		return mapErr(err)
	}
	if issueID(ch.IssueID) != issue {
		return ErrNotFound
	}
	if err = q.IssueSlackIdentitiesExpire(ctx, tx, a.ID); err != nil {
		return err
	}
	n, err := q.IssueSlackIdentityCount(ctx, tx, a.ID)
	if err != nil {
		return err
	}
	if n >= api.IssueSlackIdentitiesMax {
		return api.ErrCapacity("Slack identity limit reached")
	}
	changed, err := q.IssueSlackIdentityPut(ctx, tx, sqlc.IssueSlackIdentityPutParams{AppID: a.ID, AccountID: issueUUID(actor), SlackUserID: ch.SlackUserID, PolicyRevision: ch.PolicyRevision, ExpiresAt: issueTime(scope.CheckedAt.Time.Add(api.IssueSlackIdentityTTL))})
	if err != nil {
		var constraint *pgconn.PgError
		if !errors.As(err, &constraint) || constraint.Code != "23505" {
			return err
		}
	}
	if err != nil || changed != 1 {
		return api.NewProblem(409, "issue_slack_link_changed", "Slack account link changed", "unlink an existing identity before linking a different account")
	}
	if err = q.IssueSlackChallengeConsume(ctx, tx, token); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *PgStore) DeleteIssueSlackAccountLink(ctx context.Context, app, actor string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.IssueLockApp(ctx, tx, issueUUID(app)); err != nil {
		return mapErr(err)
	}
	if err = q.IssueSlackIdentityDelete(ctx, tx, sqlc.IssueSlackIdentityDeleteParams{AppID: issueUUID(app), AccountID: issueUUID(actor)}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
