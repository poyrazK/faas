package globaldirectory

import (
	"context"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) OwnerByID(ctx context.Context, id string) (Owner, error) {
	row, err := (&sqlc.Queries{}).ReadGlobalDirectoryOwner(ctx, s.pool, idArg(id))
	return Owner{ID: row.OwnerID, AccountID: row.AccountID, OrgID: row.OrgID}, directoryError("read owner", err)
}
func (s *PgStore) ResolveOrgOwner(ctx context.Context, orgID string) (Owner, error) {
	var out Owner
	err := s.transaction(ctx, "resolve organization owner", func(tx pgx.Tx) error {
		id, err := (&sqlc.Queries{}).ResolveGlobalOrgOwner(ctx, tx, idArg(orgID))
		if err != nil {
			return err
		}
		out = Owner{ID: id, OrgID: orgID}
		return nil
	})
	return out, err
}
func (s *PgStore) CreateOrgApp(ctx context.Context, ownerID, actorID string, plan api.Plan, slug string, limit int) (App, error) {
	if !ValidID(ownerID) || !validOwnerID(actorID) || !ValidSlug(slug) {
		return App{}, ErrConflict
	}
	if limit < math.MinInt32 || limit > math.MaxInt32 {
		return App{}, ErrLimit
	}
	var out App
	err := s.transaction(ctx, "register organization app", func(tx pgx.Tx) error {
		q := &sqlc.Queries{}
		_, err := q.CreateGlobalOrgApp(ctx, tx, sqlc.CreateGlobalOrgAppParams{OwnerID: idArg(ownerID), ActorID: idArg(actorID), Slug: slug, ExpectedPlan: string(plan), AppLimit: int32(limit)})
		if err != nil {
			return err
		}
		row, err := q.GlobalAppIdentity(ctx, tx, sqlc.GlobalAppIdentityParams{OwnerID: idArg(ownerID), Slug: slug})
		out = App{ID: row.GlobalAppID, OwnerID: row.OwnerID, Slug: row.Slug, RoutingPolicy: row.RoutingPolicy, CreatedAt: row.CreatedAt.Time}
		return err
	})
	return out, err
}

var _ OwnerStore = (*PgStore)(nil)
