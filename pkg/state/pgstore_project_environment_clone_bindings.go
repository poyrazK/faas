package state

import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func captureCloneBindingsDB(ctx context.Context, db sqlc.DBTX, accountID, appID, scope string, values projectCloneWorkloadValues) (ProjectEnvironmentCloneBindingDefinitions, error) {
	definitions := ProjectEnvironmentCloneBindingDefinitions{}
	q := new(sqlc.Queries)
	bindings, err := q.ReadProjectEnvironmentClonePostgresBindings(ctx, db, sqlc.ReadProjectEnvironmentClonePostgresBindingsParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), SourceScope: scope,
	})
	if err != nil {
		return definitions, mapErr(err)
	}
	for _, row := range bindings {
		var binding ProjectEnvironmentClonePostgresBinding
		if !row.Ready || json.Unmarshal(row.Definition, &binding) != nil {
			return definitions, ErrProjectEnvironmentCloneBindingCapture
		}
		definitions.Postgres = append(definitions.Postgres, binding)
	}
	buckets, err := q.ReadProjectEnvironmentCloneObjectBuckets(ctx, db, sqlc.ReadProjectEnvironmentCloneObjectBucketsParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), SourceScope: scope,
	})
	if err != nil {
		return definitions, mapErr(err)
	}
	for _, row := range buckets {
		var bucket ProjectEnvironmentCloneObjectBucket
		if !row.Ready || json.Unmarshal(row.Definition, &bucket) != nil {
			return definitions, ErrProjectEnvironmentCloneBindingCapture
		}
		definitions.Buckets = append(definitions.Buckets, bucket)
	}
	return normalizeCloneBindingDefinitions(appID, scope, values, definitions)
}

func (s *PgStore) ProjectEnvironmentCloneBindings(ctx context.Context, accountID, projectID, operationID string) ([]ProjectEnvironmentCloneBindings, error) {
	if _, err := s.ProjectEnvironmentCloneOperationByID(ctx, accountID, projectID, operationID); err != nil {
		return nil, err
	}
	records, err := cloneWorkloadRecordsDB(ctx, s.pool, accountID, projectID, operationID)
	if err != nil {
		return nil, err
	}
	return cloneBindingViews(records)
}
