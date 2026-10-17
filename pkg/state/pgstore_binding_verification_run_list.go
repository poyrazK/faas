package state

import (
	"context"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ListBindingVerificationRuns(ctx context.Context, account, app string, o api.BindingVerificationRunListOptions) (api.BindingVerificationRunList, error) {
	c, e := prepareVerificationRunList(account, app, "", &o)
	if e != nil {
		return api.BindingVerificationRunList{}, e
	}
	rows, e := sqlc.New().ListBindingVerificationRunSummaries(ctx, s.pool, sqlc.ListBindingVerificationRunSummariesParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), ObservedAt: pgtype.Timestamptz{Time: c.ObservedAt, Valid: true}, IncludeMembers: o.IncludeMembers, Deployment: o.DeploymentID, Scope: o.Scope, StatusFilter: o.Status, HasCursor: o.Cursor != "", BeforeAt: pgtype.Timestamptz{Time: c.BeforeAt, Valid: true}, BeforeID: mustPgUUID(c.BeforeID), PageLimit: int32(o.Limit + 1)})
	if e != nil {
		return api.BindingVerificationRunList{}, mapErr(e)
	}
	out := make([]api.BindingVerificationRunSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, api.BindingVerificationRunSummary{ID: pgUUIDString(r.ID), App: r.App, DeploymentID: pgUUIDString(r.DeploymentID), Scope: r.Scope, GraphRunID: pgUUIDString(r.GraphRunID), Status: r.Status, CreatedAt: r.CreatedAt.Time, ExpiresAt: r.ExpiresAt.Time})
	}
	return finishVerificationRunList(out, c, o.Limit), nil
}
func (s *PgStore) ListProjectBindingVerificationRuns(ctx context.Context, account, project, environment string, o api.BindingVerificationRunListOptions) (api.BindingVerificationRunList, error) {
	c, e := prepareVerificationRunList(account, project, environment, &o)
	if e != nil {
		return api.BindingVerificationRunList{}, e
	}
	rows, e := sqlc.New().ListProjectBindingVerificationRunSummaries(ctx, s.pool, sqlc.ListProjectBindingVerificationRunSummariesParams{AccountID: mustPgUUID(account), ProjectID: mustPgUUID(project), Environment: environment, ObservedAt: pgtype.Timestamptz{Time: c.ObservedAt, Valid: true}, Deployment: o.DeploymentID, StatusFilter: o.Status, HasCursor: o.Cursor != "", BeforeAt: pgtype.Timestamptz{Time: c.BeforeAt, Valid: true}, BeforeID: mustPgUUID(c.BeforeID), PageLimit: int32(o.Limit + 1)})
	if e != nil {
		return api.BindingVerificationRunList{}, mapErr(e)
	}
	out := make([]api.BindingVerificationRunSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, api.BindingVerificationRunSummary{ID: pgUUIDString(r.ID), Project: r.Project, Environment: r.Environment, Status: r.Status, CreatedAt: r.CreatedAt.Time, ExpiresAt: r.ExpiresAt.Time})
	}
	return finishVerificationRunList(out, c, o.Limit), nil
}
