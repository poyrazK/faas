package state

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ApplicationStandardResourceStore = (*PgStore)(nil)

func standardLogDestinationRow(row sqlc.ApplicationStandardLogDestination) (ApplicationStandardLogDestination, error) {
	value, err := prepareStandardLogDestination(ApplicationStandardLogDestinationCreate{OrgID: pgUUIDString(row.OrgID), ActorID: pgUUIDString(row.CreatedBy), Name: row.Name, Kind: row.Kind, TargetURL: row.TargetUrl, AuthHeaderSealed: row.AuthHeaderSealed})
	if err != nil {
		return ApplicationStandardLogDestination{}, err
	}
	if value.ConfigHash != row.ConfigHash {
		return ApplicationStandardLogDestination{}, fmt.Errorf("destination configuration hash mismatch")
	}
	value.ID, value.CreatedAt = pgUUIDString(row.ID), row.CreatedAt.Time
	return value, nil
}

func standardPublisherRow(row sqlc.ApplicationStandardPublisher) (api.ApplicationStandardPublisher, error) {
	value, err := prepareStandardPublisher(ApplicationStandardPublisherCreate{OrgID: pgUUIDString(row.OrgID), ActorID: pgUUIDString(row.CreatedBy), Name: row.Name, PublicKeyDER: row.PublicKeyDer})
	if err != nil {
		return api.ApplicationStandardPublisher{}, err
	}
	if value.Fingerprint != row.Fingerprint {
		return api.ApplicationStandardPublisher{}, fmt.Errorf("publisher key fingerprint mismatch")
	}
	value.ID, value.CreatedAt = pgUUIDString(row.ID), row.CreatedAt.Time
	return value, nil
}

// Every resource publication and standard publication locks the same org row.
// This serializes ownership erasure and immutable reference validation.
func (s *PgStore) standardResourceTx(ctx context.Context, orgID, actorID string) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	_, err = sqlc.New().LockApplicationStandardOrg(ctx, tx, sqlc.LockApplicationStandardOrgParams{OrgID: mustPgUUID(orgID), ActorID: mustPgUUID(actorID)})
	if err != nil {
		_ = tx.Rollback(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrNotFound
		}
		return nil, err
	}
	return tx, nil
}

func (s *PgStore) CreateApplicationStandardLogDestination(ctx context.Context, in ApplicationStandardLogDestinationCreate) (ApplicationStandardLogDestination, error) {
	value, err := prepareStandardLogDestination(in)
	if err != nil {
		return ApplicationStandardLogDestination{}, err
	}
	tx, err := s.standardResourceTx(ctx, in.OrgID, in.ActorID)
	if err != nil {
		return ApplicationStandardLogDestination{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := sqlc.New().CreateApplicationStandardLogDestination(ctx, tx, sqlc.CreateApplicationStandardLogDestinationParams{OrgID: mustPgUUID(in.OrgID), Name: in.Name, Kind: in.Kind, TargetUrl: in.TargetURL, AuthHeaderSealed: value.AuthHeaderSealed, ConfigHash: value.ConfigHash, CreatedBy: mustPgUUID(in.ActorID)})
	if err != nil {
		return ApplicationStandardLogDestination{}, err
	}
	value, err = standardLogDestinationRow(row)
	if err != nil {
		return ApplicationStandardLogDestination{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ApplicationStandardLogDestination{}, err
	}
	return value, nil
}

func (s *PgStore) GetApplicationStandardLogDestination(ctx context.Context, orgID, id string) (ApplicationStandardLogDestination, error) {
	if !validStandardResourceRead(orgID, id) {
		return ApplicationStandardLogDestination{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetApplicationStandardLogDestination(ctx, s.pool, sqlc.GetApplicationStandardLogDestinationParams{OrgID: mustPgUUID(orgID), ResourceID: mustPgUUID(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ApplicationStandardLogDestination{}, ErrNotFound
	}
	if err != nil {
		return ApplicationStandardLogDestination{}, err
	}
	return standardLogDestinationRow(row)
}

func (s *PgStore) ListApplicationStandardLogDestinations(ctx context.Context, orgID, after string, limit int) ([]ApplicationStandardLogDestination, error) {
	if !validStandardResourcePage(orgID, after, limit) {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListApplicationStandardLogDestinations(ctx, s.pool, sqlc.ListApplicationStandardLogDestinationsParams{OrgID: mustPgUUID(orgID), AfterID: after, PageLimit: int32(limit)})
	if err != nil {
		return nil, err
	}
	values := []ApplicationStandardLogDestination{}
	for _, row := range rows {
		value, err := standardLogDestinationRow(row)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func (s *PgStore) CreateApplicationStandardPublisher(ctx context.Context, in ApplicationStandardPublisherCreate) (api.ApplicationStandardPublisher, error) {
	value, err := prepareStandardPublisher(in)
	if err != nil {
		return api.ApplicationStandardPublisher{}, err
	}
	tx, err := s.standardResourceTx(ctx, in.OrgID, in.ActorID)
	if err != nil {
		return api.ApplicationStandardPublisher{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	der, _ := base64.StdEncoding.DecodeString(value.PublicKeyDER)
	row, err := sqlc.New().CreateApplicationStandardPublisher(ctx, tx, sqlc.CreateApplicationStandardPublisherParams{OrgID: mustPgUUID(in.OrgID), Name: in.Name, PublicKeyDer: der, Fingerprint: value.Fingerprint, CreatedBy: mustPgUUID(in.ActorID)})
	if err != nil {
		return api.ApplicationStandardPublisher{}, err
	}
	value, err = standardPublisherRow(row)
	if err != nil {
		return api.ApplicationStandardPublisher{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return api.ApplicationStandardPublisher{}, err
	}
	return value, nil
}

func (s *PgStore) GetApplicationStandardPublisher(ctx context.Context, orgID, id string) (api.ApplicationStandardPublisher, error) {
	if !validStandardResourceRead(orgID, id) {
		return api.ApplicationStandardPublisher{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetApplicationStandardPublisher(ctx, s.pool, sqlc.GetApplicationStandardPublisherParams{OrgID: mustPgUUID(orgID), ResourceID: mustPgUUID(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ApplicationStandardPublisher{}, ErrNotFound
	}
	if err != nil {
		return api.ApplicationStandardPublisher{}, err
	}
	return standardPublisherRow(row)
}

func (s *PgStore) ListApplicationStandardPublishers(ctx context.Context, orgID, after string, limit int) ([]api.ApplicationStandardPublisher, error) {
	if !validStandardResourcePage(orgID, after, limit) {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListApplicationStandardPublishers(ctx, s.pool, sqlc.ListApplicationStandardPublishersParams{OrgID: mustPgUUID(orgID), AfterID: after, PageLimit: int32(limit)})
	if err != nil {
		return nil, err
	}
	values := []api.ApplicationStandardPublisher{}
	for _, row := range rows {
		value, err := standardPublisherRow(row)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func validateApplicationStandardRefs(ctx context.Context, db sqlc.DBTX, orgID string, definition appstandards.Definition) error {
	params := sqlc.ValidateApplicationStandardResourceRefsParams{OrgID: mustPgUUID(orgID)}
	for _, id := range standardResourceRefs(definition, appstandards.LogDestinations) {
		params.DestinationIds = append(params.DestinationIds, mustPgUUID(id))
	}
	for _, id := range standardResourceRefs(definition, appstandards.TrustedPublishers) {
		params.PublisherIds = append(params.PublisherIds, mustPgUUID(id))
	}
	valid, err := sqlc.New().ValidateApplicationStandardResourceRefs(ctx, db, params)
	if err != nil {
		return err
	}
	if !valid.Valid || !valid.Bool {
		return fmt.Errorf("standard references a resource outside its organization: %w", ErrInvalidArgument)
	}
	return nil
}
