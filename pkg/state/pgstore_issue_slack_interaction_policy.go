package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) IssueSlackInteractionVerifier(ctx context.Context, app string) (IssueSlackInteractionPolicy, error) {
	return readSlackInteractionPolicy(ctx, sqlc.New(), s.pool, app)
}
func readSlackInteractionPolicy(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, app string) (IssueSlackInteractionPolicy, error) {
	r, err := q.IssueSlackInteractionPolicy(ctx, db, issueUUID(app))
	if errors.Is(err, pgx.ErrNoRows) {
		return IssueSlackInteractionPolicy{}, nil
	}
	return IssueSlackInteractionPolicy{Enabled: r.Enabled, Revision: r.Revision, WorkspaceID: r.WorkspaceID, SlackAppID: r.SlackAppID, SecretSealed: r.SecretSealed}, err
}
func (s *PgStore) GetIssueSlackInteractionPolicy(ctx context.Context, app, owner string) (IssueSlackInteractionPolicy, error) {
	if _, err := issueApplicationSlackPolicy(ctx, sqlc.New(), s.pool, app, owner); err != nil {
		return IssueSlackInteractionPolicy{}, err
	}
	out, err := readSlackInteractionPolicy(ctx, sqlc.New(), s.pool, app)
	out.SecretSealed = nil
	return out, err
}
func (s *PgStore) SetIssueSlackInteractionPolicy(ctx context.Context, app, owner string, in IssueSlackInteractionPolicy) (IssueSlackInteractionPolicy, error) {
	empty := IssueSlackInteractionPolicy{}
	if in.Revision < 0 || !slackWorkspace.MatchString(in.WorkspaceID) || !slackApp.MatchString(in.SlackAppID) || len(in.SecretSealed) > api.IssueSlackTokenMaxBytes*2 {
		return empty, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	a, err := q.IssueLockApp(ctx, tx, issueUUID(app))
	if err != nil {
		return empty, mapErr(err)
	}
	if issueID(a.AccountID) != owner {
		return empty, ErrNotFound
	}
	if _, err = issueApplicationSlackPolicy(ctx, q, tx, app, owner); err != nil {
		return empty, err
	}
	installed, err := readSlackInstallation(ctx, q, tx, app, owner)
	if err != nil {
		return empty, err
	}
	if installed.Status == "connected" {
		return empty, slackOAuthChanged()
	}
	old, err := readSlackInteractionPolicy(ctx, q, tx, app)
	if err != nil {
		return empty, err
	}
	if old.Revision != in.Revision {
		return empty, api.NewProblem(409, "issue_slack_interactions_changed", "Slack interaction settings changed", "reload settings before saving")
	}
	if len(in.SecretSealed) == 0 {
		if old.WorkspaceID != in.WorkspaceID || old.SlackAppID != in.SlackAppID {
			return empty, ErrInvalidArgument
		}
		if old.Enabled == in.Enabled {
			in.SecretSealed = nil
			return in, tx.Commit(ctx)
		}
		in.SecretSealed = old.SecretSealed
	}
	if len(in.SecretSealed) == 0 {
		return empty, ErrInvalidArgument
	}
	in.Revision++
	if err = q.IssueSlackInteractionPolicyPut(ctx, tx, sqlc.IssueSlackInteractionPolicyPutParams{AppID: a.ID, Enabled: in.Enabled, Revision: in.Revision, WorkspaceID: in.WorkspaceID, SlackAppID: in.SlackAppID, SecretSealed: in.SecretSealed}); err != nil {
		return empty, err
	}
	if err = q.IssueSlackIdentitiesRevoke(ctx, tx, a.ID); err != nil {
		return empty, err
	}
	in.SecretSealed = nil
	return in, tx.Commit(ctx)
}
func (s *PgStore) PrepareIssueSlackActions(ctx context.Context, c IssueSlackClaim) (bool, error) {
	if !c.Application {
		return false, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.IssueLockApp(ctx, tx, issueUUID(c.AppID)); err != nil {
		return false, mapErr(err)
	}
	args := sqlc.IssueSlackActionPrepareParams{ID: issueUUID(c.ID), ClaimID: issueUUID(c.ClaimID)}
	if err = q.IssueSlackActionPrepare(ctx, tx, args); err != nil {
		return false, err
	}
	ok, err := q.IssueSlackActionPrepared(ctx, tx, sqlc.IssueSlackActionPreparedParams(args))
	if err != nil {
		return false, err
	}
	return ok, tx.Commit(ctx)
}
