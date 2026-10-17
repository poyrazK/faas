package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentQueueConsumerStore = (*PgStore)(nil)

func (s *PgStore) PrepareProjectEnvironmentQueueConsumers(ctx context.Context, accountID, projectID, deploymentID string) (ProjectEnvironmentQueueRuntimeSet, error) {
	return s.projectEnvironmentQueueConsumers(ctx, accountID, projectID, deploymentID, true)
}

func (s *PgStore) ProjectEnvironmentQueueConsumersForDeployment(ctx context.Context, accountID, projectID, deploymentID string) (ProjectEnvironmentQueueRuntimeSet, error) {
	return s.projectEnvironmentQueueConsumers(ctx, accountID, projectID, deploymentID, false)
}

func (s *PgStore) projectEnvironmentQueueConsumers(ctx context.Context, accountID, projectID, deploymentID string, prepare bool) (ProjectEnvironmentQueueRuntimeSet, error) {
	if err := validateQueuePreparationIDs(accountID, projectID, deploymentID); err != nil {
		return ProjectEnvironmentQueueRuntimeSet{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentQueueRuntimeSet{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	set, err := projectEnvironmentQueueConsumersDB(ctx, tx, accountID, projectID, deploymentID, prepare, true)
	if err != nil {
		return ProjectEnvironmentQueueRuntimeSet{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectEnvironmentQueueRuntimeSet{}, err
	}
	return set, nil
}

func projectEnvironmentQueueConsumersDB(ctx context.Context, db sqlc.DBTX, accountID, projectID, deploymentID string, prepare, requireLive bool) (ProjectEnvironmentQueueRuntimeSet, error) {
	queries := sqlc.New()
	// Environment ownership is locked first, matching admission and deletion.
	envID, err := queries.LockProjectEnvironmentQueuePreparationEnvironment(ctx, db, sqlc.LockProjectEnvironmentQueuePreparationEnvironmentParams{
		AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), DeploymentID: mustPgUUID(deploymentID), RequireLive: requireLive,
	})
	if err != nil {
		return ProjectEnvironmentQueueRuntimeSet{}, mapErr(err)
	}
	// Deployment creation and cutover lock the app before a deployment. Take
	// that ownership lock explicitly before serializing projection retries.
	appID, err := queries.LockProjectEnvironmentQueuePreparationApp(ctx, db, sqlc.LockProjectEnvironmentQueuePreparationAppParams{
		AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), EnvironmentID: envID, DeploymentID: mustPgUUID(deploymentID), RequireLive: requireLive,
	})
	if err != nil {
		return ProjectEnvironmentQueueRuntimeSet{}, mapErr(err)
	}
	row, err := queries.LockProjectEnvironmentQueuePreparationSpec(ctx, db, sqlc.LockProjectEnvironmentQueuePreparationSpecParams{
		AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), EnvironmentID: envID, DeploymentID: mustPgUUID(deploymentID), AppID: appID, RequireLive: requireLive,
	})
	if err != nil {
		if errors.Is(mapErr(err), ErrNotFound) {
			return ProjectEnvironmentQueueRuntimeSet{}, ErrProjectEnvironmentQueueCollectionUnavailable
		}
		return ProjectEnvironmentQueueRuntimeSet{}, mapErr(err)
	}
	spec := ProjectEnvironmentWorkloadSpec{
		ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), ProjectID: pgUUIDString(row.ProjectID),
		EnvironmentID: pgUUIDString(row.EnvironmentID), EnvironmentSlug: row.EnvironmentSlug, AppID: pgUUIDString(row.AppID),
		Revision: row.Revision, Hash: row.ConfigHash, CreatedAt: row.CreatedAt.Time,
	}
	if decodeQueueProjectionJSON(row.Settings, &spec.Settings) != nil {
		return ProjectEnvironmentQueueRuntimeSet{}, ErrConflict
	}
	book, err := queuePreparationBook(spec)
	if err != nil {
		return ProjectEnvironmentQueueRuntimeSet{}, err
	}
	set, err := readQueueRuntimeSetDB(ctx, db, spec.EnvironmentSlug, deploymentID)
	if errors.Is(err, ErrNotFound) {
		if !prepare {
			return ProjectEnvironmentQueueRuntimeSet{}, ErrProjectEnvironmentQueuePreparationUnavailable
		}
		set = newQueueRuntimeSet(spec, deploymentID, book)
		err = insertQueueRuntimeSetDB(ctx, db, set)
	}
	if err != nil {
		return ProjectEnvironmentQueueRuntimeSet{}, err
	}
	if err := validateQueueRuntimeSet(set, spec, deploymentID, book); err != nil {
		return ProjectEnvironmentQueueRuntimeSet{}, err
	}
	return set, nil
}

func readQueueRuntimeSetDB(ctx context.Context, db sqlc.DBTX, environment, deploymentID string) (ProjectEnvironmentQueueRuntimeSet, error) {
	return readQueueRuntimeSetModeDB(ctx, db, environment, deploymentID, false)
}

func readSnapshotQueueRuntimeSetDB(ctx context.Context, db sqlc.DBTX, environment, deploymentID string) (ProjectEnvironmentQueueRuntimeSet, error) {
	return readQueueRuntimeSetModeDB(ctx, db, environment, deploymentID, true)
}

// Snapshot queries retain the original JSON definition bytes and use the same
// strict decoder and ownership checks as locking transactional reads.
func readQueueRuntimeSetModeDB(ctx context.Context, db sqlc.DBTX, environment, deploymentID string, snapshot bool) (ProjectEnvironmentQueueRuntimeSet, error) {
	queries := sqlc.New()
	var row sqlc.ProjectEnvironmentQueueRuntimeSet
	var err error
	if snapshot {
		row, err = queries.ReadInvocationVersionQueueSet(ctx, db, mustPgUUID(deploymentID))
	} else {
		row, err = queries.ReadProjectEnvironmentQueueRuntimeSet(ctx, db, mustPgUUID(deploymentID))
	}
	if err != nil {
		return ProjectEnvironmentQueueRuntimeSet{}, mapErr(err)
	}
	set := ProjectEnvironmentQueueRuntimeSet{
		ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), ProjectID: pgUUIDString(row.ProjectID),
		EnvironmentID: pgUUIDString(row.EnvironmentID), EnvironmentSlug: environment, AppID: pgUUIDString(row.AppID),
		DeploymentID: pgUUIDString(row.DeploymentID), WorkloadSpecID: pgUUIDString(row.WorkloadSpecID), SettingsHash: row.SettingsHash,
		QueueRevision: row.QueueRevision, BindingCount: int(row.BindingCount), BookHash: row.BookHash, State: row.State,
		CreatedAt: row.CreatedAt.Time.UTC(), Consumers: []ProjectEnvironmentQueueConsumer{},
	}
	var rows []sqlc.ProjectEnvironmentQueueConsumer
	if snapshot {
		rows, err = queries.ReadInvocationVersionQueueConsumers(ctx, db, row.ID)
	} else {
		rows, err = queries.ReadProjectEnvironmentQueueConsumers(ctx, db, row.ID)
	}
	if err != nil {
		return ProjectEnvironmentQueueRuntimeSet{}, mapErr(err)
	}
	for _, row := range rows {
		definition, err := decodeQueueConsumerDefinition(row.Definition)
		if err != nil || definition.Name != row.Name || definition.QueueName != row.QueueName {
			return ProjectEnvironmentQueueRuntimeSet{}, ErrConflict
		}
		set.Consumers = append(set.Consumers, ProjectEnvironmentQueueConsumer{
			ID: pgUUIDString(row.ID), RuntimeSetID: pgUUIDString(row.RuntimeSetID), DefinitionHash: row.DefinitionHash,
			ProjectEnvironmentQueueDefinition: definition, CreatedAt: row.CreatedAt.Time.UTC(),
		})
	}
	return set, nil
}

func insertQueueRuntimeSetDB(ctx context.Context, db sqlc.DBTX, set ProjectEnvironmentQueueRuntimeSet) error {
	queries := sqlc.New()
	if err := queries.InsertProjectEnvironmentQueueRuntimeSet(ctx, db, sqlc.InsertProjectEnvironmentQueueRuntimeSetParams{
		ID: mustPgUUID(set.ID), AccountID: mustPgUUID(set.AccountID), ProjectID: mustPgUUID(set.ProjectID),
		EnvironmentID: mustPgUUID(set.EnvironmentID), AppID: mustPgUUID(set.AppID), DeploymentID: mustPgUUID(set.DeploymentID),
		WorkloadSpecID: mustPgUUID(set.WorkloadSpecID), SettingsHash: set.SettingsHash, QueueRevision: set.QueueRevision,
		BindingCount: int32(set.BindingCount), BookHash: set.BookHash, State: set.State,
		CreatedAt: pgtype.Timestamptz{Time: set.CreatedAt, Valid: true},
	}); err != nil {
		return mapErr(err)
	}
	for _, consumer := range set.Consumers {
		raw, err := json.Marshal(consumer.ProjectEnvironmentQueueDefinition)
		if err != nil {
			return ErrConflict
		}
		if err := queries.InsertProjectEnvironmentQueueConsumer(ctx, db, sqlc.InsertProjectEnvironmentQueueConsumerParams{
			ID: mustPgUUID(consumer.ID), RuntimeSetID: mustPgUUID(set.ID), Name: consumer.Name, QueueName: consumer.QueueName,
			Definition: raw, DefinitionHash: consumer.DefinitionHash, CreatedAt: pgtype.Timestamptz{Time: consumer.CreatedAt, Valid: true},
		}); err != nil {
			return mapErr(err)
		}
	}
	return nil
}
