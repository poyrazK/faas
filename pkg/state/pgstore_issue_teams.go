package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func issueTeamRow(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, row sqlc.AppIssueTeam) (api.IssueTeam, error) {
	out := api.IssueTeam{ID: issueID(row.ID), Key: row.TeamKey, Name: row.Name, MemberAccountIDs: []string{}, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time, UpdatedByAccountID: issueID(row.UpdatedByAccountID)}
	members, err := q.IssueTeamMembers(ctx, db, row.ID)
	if err != nil {
		return out, err
	}
	for _, id := range members {
		out.MemberAccountIDs = append(out.MemberAccountIDs, issueID(id))
	}
	return out, nil
}
func (s *PgStore) ListIssueTeams(ctx context.Context, app, account string) (api.ListIssueTeamsResponse, error) {
	out := api.ListIssueTeamsResponse{Items: []api.IssueTeam{}}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.IssueSavedViewOwner(ctx, tx, sqlc.IssueSavedViewOwnerParams{AppID: issueUUID(app), AccountID: issueUUID(account)}); err != nil {
		return out, mapErr(err)
	}
	rows, err := q.IssueListTeams(ctx, tx, sqlc.IssueListTeamsParams{AppID: issueUUID(app), AccountID: issueUUID(account)})
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		t, err := issueTeamRow(ctx, q, tx, r)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, t)
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) GetIssueTeam(ctx context.Context, app, account, key string) (api.IssueTeam, error) {
	if api.ValidateIssueSavedViewKey(key) != nil {
		return api.IssueTeam{}, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.IssueTeam{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	r, err := q.IssueGetTeam(ctx, tx, sqlc.IssueGetTeamParams{AppID: issueUUID(app), AccountID: issueUUID(account), TeamKey: key})
	if err != nil {
		return api.IssueTeam{}, mapErr(err)
	}
	out, err := issueTeamRow(ctx, q, tx, r)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) PutIssueTeam(ctx context.Context, app, account, actor, key string, in api.PutIssueTeamRequest, now time.Time) (api.IssueTeam, error) {
	if api.ValidateIssueSavedViewKey(key) != nil {
		return api.IssueTeam{}, ErrInvalidArgument
	}
	in, err := api.NormalizeIssueTeam(in)
	if err != nil {
		return api.IssueTeam{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.IssueTeam{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	owner, err := q.IssueLockApp(ctx, tx, issueUUID(app))
	if err != nil {
		return api.IssueTeam{}, mapErr(err)
	}
	if issueID(owner.AccountID) != account {
		return api.IssueTeam{}, ErrNotFound
	}
	for _, member := range in.MemberAccountIDs {
		ok, err := q.IssueAssigneeAllowed(ctx, tx, sqlc.IssueAssigneeAllowedParams{AppID: owner.ID, Assignee: issueUUID(member)})
		if err != nil {
			return api.IssueTeam{}, err
		}
		if !ok {
			return api.IssueTeam{}, ErrInvalidArgument
		}
	}
	_, err = q.IssueGetTeam(ctx, tx, sqlc.IssueGetTeamParams{AppID: owner.ID, AccountID: owner.AccountID, TeamKey: key})
	if errors.Is(err, pgx.ErrNoRows) {
		n, e := q.IssueCountTeams(ctx, tx, owner.ID)
		if e != nil {
			return api.IssueTeam{}, e
		}
		if n >= api.IssueTeamsMax {
			return api.IssueTeam{}, &IssueLimitError{Resource: "teams", Limit: api.IssueTeamsMax, Observed: n + 1}
		}
	} else if err != nil {
		return api.IssueTeam{}, err
	}
	row, err := q.IssuePutTeam(ctx, tx, sqlc.IssuePutTeamParams{AppID: owner.ID, TeamKey: key, Name: in.Name, Now: issueTime(now), ActorAccountID: issueUUID(actor)})
	if err != nil {
		return api.IssueTeam{}, mapErr(err)
	}
	if err = q.IssueDeleteTeamMembers(ctx, tx, row.ID); err != nil {
		return api.IssueTeam{}, err
	}
	for _, member := range in.MemberAccountIDs {
		if err = q.IssueAddTeamMember(ctx, tx, sqlc.IssueAddTeamMemberParams{TeamID: row.ID, AccountID: issueUUID(member)}); err != nil {
			return api.IssueTeam{}, err
		}
	}
	out, err := issueTeamRow(ctx, q, tx, row)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) DeleteIssueTeam(ctx context.Context, app, account, key string) error {
	if api.ValidateIssueSavedViewKey(key) != nil {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	owner, err := q.IssueLockApp(ctx, tx, issueUUID(app))
	if err != nil {
		return mapErr(err)
	}
	if issueID(owner.AccountID) != account {
		return ErrNotFound
	}
	row, err := q.IssueGetTeam(ctx, tx, sqlc.IssueGetTeamParams{AppID: owner.ID, AccountID: owner.AccountID, TeamKey: key})
	if err != nil {
		return mapErr(err)
	}
	used, err := q.IssueTeamReferenced(ctx, tx, row.ID)
	if err != nil {
		return err
	}
	if used {
		return ErrIssueTeamInUse
	}
	if _, err = q.IssueDeleteTeam(ctx, tx, sqlc.IssueDeleteTeamParams{AppID: owner.ID, TeamKey: key}); err != nil {
		return mapErr(err)
	}
	return tx.Commit(ctx)
}
func issueAssignmentAllowed(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, app, account, team pgtype.UUID) (bool, error) {
	if team.Valid {
		return q.IssueTeamAllowed(ctx, db, sqlc.IssueTeamAllowedParams{AppID: app, TeamID: team})
	}
	return q.IssueAssigneeAllowed(ctx, db, sqlc.IssueAssigneeAllowedParams{AppID: app, Assignee: account})
}
