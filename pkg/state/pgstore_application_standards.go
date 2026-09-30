package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ApplicationStandardStore = (*PgStore)(nil)

func applicationStandardRow(row sqlc.GetApplicationStandardVersionRow) (ApplicationStandardVersion, error) {
	definition, hash, err := appstandards.Parse(row.Definition, api.ApplicationStandardResolverLimits())
	if err != nil {
		return ApplicationStandardVersion{}, fmt.Errorf("decode application standard: %w", err)
	}
	if hash != row.DefinitionHash {
		return ApplicationStandardVersion{}, fmt.Errorf("application standard definition hash mismatch")
	}
	return ApplicationStandardVersion{StandardID: row.StandardID, OrgID: row.OrgID, Slug: row.Slug, Version: row.Version, Definition: definition, DefinitionHash: hash, Description: row.Description, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt.Time}, nil
}

func readApplicationStandard(ctx context.Context, db sqlc.DBTX, orgID pgtype.UUID, slug string, version int64) (ApplicationStandardVersion, error) {
	row, err := sqlc.New().GetApplicationStandardVersion(ctx, db, sqlc.GetApplicationStandardVersionParams{OrgID: orgID, Slug: slug, Version: version})
	if errors.Is(err, pgx.ErrNoRows) {
		return ApplicationStandardVersion{}, ErrNotFound
	}
	if err != nil {
		return ApplicationStandardVersion{}, fmt.Errorf("read application standard: %w", err)
	}
	return applicationStandardRow(row)
}

func (s *PgStore) GetApplicationStandardVersion(ctx context.Context, orgID, slug string, version int64) (ApplicationStandardVersion, error) {
	if !validApplicationStandardRead(orgID, slug, version) {
		return ApplicationStandardVersion{}, ErrInvalidArgument
	}
	return readApplicationStandard(ctx, s.pool, mustPgUUID(orgID), slug, version)
}

func (s *PgStore) ListApplicationStandards(ctx context.Context, orgID, after string, limit int) ([]ApplicationStandardVersion, error) {
	if !validApplicationStandardPage(orgID, after, limit) {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListApplicationStandards(ctx, s.pool, sqlc.ListApplicationStandardsParams{OrgID: mustPgUUID(orgID), AfterSlug: after, PageLimit: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("list application standards: %w", err)
	}
	result := []ApplicationStandardVersion{}
	for _, row := range rows {
		version, err := applicationStandardRow(sqlc.GetApplicationStandardVersionRow(row))
		if err != nil {
			return nil, err
		}
		result = append(result, version)
	}
	return result, nil
}

func (s *PgStore) PublishApplicationStandardVersion(ctx context.Context, p ApplicationStandardPublish) (ApplicationStandardVersion, error) {
	version, err := prepareApplicationStandard(p)
	if err != nil {
		return ApplicationStandardVersion{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ApplicationStandardVersion{}, fmt.Errorf("begin standard publication: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q, orgID, actorID := sqlc.New(), mustPgUUID(p.OrgID), mustPgUUID(p.ActorID)
	if _, err := q.LockApplicationStandardOrg(ctx, tx, sqlc.LockApplicationStandardOrgParams{OrgID: orgID, ActorID: actorID}); errors.Is(err, pgx.ErrNoRows) {
		return ApplicationStandardVersion{}, ErrNotFound
	} else if err != nil {
		return ApplicationStandardVersion{}, fmt.Errorf("lock standard organization: %w", err)
	}
	prior, err := readApplicationStandard(ctx, tx, orgID, p.Slug, 0)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return ApplicationStandardVersion{}, err
	}
	if prior.Version != p.ExpectedVersion {
		return ApplicationStandardVersion{}, ErrConflict
	}
	standardID := mustPgUUID(prior.StandardID)
	if prior.Version == 0 {
		standardID, err = q.CreateApplicationStandard(ctx, tx, sqlc.CreateApplicationStandardParams{OrgID: orgID, Slug: p.Slug, CreatedBy: actorID})
		if err != nil {
			return ApplicationStandardVersion{}, fmt.Errorf("create application standard: %w", err)
		}
	}
	definition, err := json.Marshal(version.Definition)
	if err != nil {
		return ApplicationStandardVersion{}, fmt.Errorf("encode application standard: %w", err)
	}
	err = q.InsertApplicationStandardVersion(ctx, tx, sqlc.InsertApplicationStandardVersionParams{OrgID: orgID, StandardID: standardID, Version: version.Version, Definition: definition, DefinitionHash: version.DefinitionHash, Description: p.Description, CreatedBy: actorID})
	if err != nil {
		return ApplicationStandardVersion{}, fmt.Errorf("insert application standard version: %w", err)
	}
	version, err = readApplicationStandard(ctx, tx, orgID, p.Slug, version.Version)
	if err != nil {
		return ApplicationStandardVersion{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ApplicationStandardVersion{}, fmt.Errorf("commit standard publication: %w", err)
	}
	return version, nil
}
