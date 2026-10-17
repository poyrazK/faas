package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func issueSavedViewRow(row sqlc.AppIssueSavedView) (api.IssueSavedView, error) {
	out := api.IssueSavedView{Key: row.ViewKey, Name: row.Name, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time, UpdatedByAccountID: issueID(row.UpdatedByAccountID)}
	if err := json.Unmarshal(row.Filters, &out.Filters); err != nil {
		return out, err
	}
	return out, nil
}

func (s *PgStore) ListIssueSavedViews(ctx context.Context, app, account string) (api.ListIssueSavedViewsResponse, error) {
	out := api.ListIssueSavedViewsResponse{Items: []api.IssueSavedView{}}
	q := sqlc.New()
	if _, err := q.IssueSavedViewOwner(ctx, s.pool, sqlc.IssueSavedViewOwnerParams{AppID: issueUUID(app), AccountID: issueUUID(account)}); err != nil {
		return out, mapErr(err)
	}
	rows, err := q.IssueListSavedViews(ctx, s.pool, sqlc.IssueListSavedViewsParams{AppID: issueUUID(app), AccountID: issueUUID(account)})
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		item, err := issueSavedViewRow(row)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func (s *PgStore) GetIssueSavedView(ctx context.Context, app, account, key string) (api.IssueSavedView, error) {
	if err := api.ValidateIssueSavedViewKey(key); err != nil {
		return api.IssueSavedView{}, ErrInvalidArgument
	}
	row, err := sqlc.New().IssueGetSavedView(ctx, s.pool, sqlc.IssueGetSavedViewParams{AppID: issueUUID(app), AccountID: issueUUID(account), ViewKey: key})
	if err != nil {
		return api.IssueSavedView{}, mapErr(err)
	}
	return issueSavedViewRow(row)
}

func (s *PgStore) PutIssueSavedView(ctx context.Context, app, account, actor, key string, in api.PutIssueSavedViewRequest, now time.Time) (api.IssueSavedView, error) {
	if err := api.ValidateIssueSavedViewKey(key); err != nil {
		return api.IssueSavedView{}, ErrInvalidArgument
	}
	in, err := api.NormalizeIssueSavedView(in)
	if err != nil {
		return api.IssueSavedView{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.IssueSavedView{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	owner, err := q.IssueLockApp(ctx, tx, issueUUID(app))
	if err != nil {
		return api.IssueSavedView{}, mapErr(err)
	}
	if issueID(owner.AccountID) != account {
		return api.IssueSavedView{}, ErrNotFound
	}
	if assignee := in.Filters.Assignee; assignee != "" && assignee != "me" && assignee != "unassigned" {
		allowed, err := q.IssueAssigneeAllowed(ctx, tx, sqlc.IssueAssigneeAllowedParams{AppID: owner.ID, Assignee: issueUUID(assignee)})
		if err != nil {
			return api.IssueSavedView{}, err
		}
		if !allowed {
			return api.IssueSavedView{}, ErrInvalidArgument
		}
	}
	if in.Filters.Team != "" {
		ok, e := q.IssueTeamExists(ctx, tx, sqlc.IssueTeamExistsParams{AppID: owner.ID, TeamID: issueUUID(in.Filters.Team)})
		if e != nil {
			return api.IssueSavedView{}, e
		}
		if !ok {
			return api.IssueSavedView{}, ErrInvalidArgument
		}
	}
	_, err = q.IssueGetSavedView(ctx, tx, sqlc.IssueGetSavedViewParams{AppID: owner.ID, AccountID: owner.AccountID, ViewKey: key})
	if errors.Is(err, pgx.ErrNoRows) {
		n, countErr := q.IssueCountSavedViews(ctx, tx, owner.ID)
		if countErr != nil {
			return api.IssueSavedView{}, countErr
		}
		if n >= api.IssueSavedViewsMax {
			return api.IssueSavedView{}, &IssueLimitError{Resource: "saved_views", Limit: api.IssueSavedViewsMax, Observed: n + 1}
		}
	} else if err != nil {
		return api.IssueSavedView{}, err
	}
	raw, err := json.Marshal(in.Filters)
	if err != nil {
		return api.IssueSavedView{}, err
	}
	if len(raw) > api.IssueSavedViewFiltersMaxBytes {
		return api.IssueSavedView{}, ErrInvalidArgument
	}
	row, err := q.IssuePutSavedView(ctx, tx, sqlc.IssuePutSavedViewParams{AppID: owner.ID, ViewKey: key, Name: in.Name, Filters: raw, ActorAccountID: issueUUID(actor), Now: issueTime(now)})
	if err != nil {
		return api.IssueSavedView{}, mapErr(err)
	}
	out, err := issueSavedViewRow(row)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func (s *PgStore) DeleteIssueSavedView(ctx context.Context, app, account, key string) error {
	if err := api.ValidateIssueSavedViewKey(key); err != nil {
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
	n, err := q.IssueDeleteSavedView(ctx, tx, sqlc.IssueDeleteSavedViewParams{AppID: owner.ID, ViewKey: key})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}
