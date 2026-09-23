package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type projectEnvironmentManagedResourceCleanup struct {
	postgres      []projectEnvironmentPostgresCleanup
	objectStorage []projectEnvironmentObjectStorageCleanup
}

type projectEnvironmentPostgresCleanup struct {
	app            state.App
	binding        managedpostgres.Binding
	database       managedpostgres.Database
	deleteDatabase bool
}

type projectEnvironmentObjectStorageCleanup struct {
	app        state.App
	bucket     state.ObjectBucket
	credential state.ObjectS3Credential
}

// planProjectEnvironmentManagedResourceCleanup validates the target-scoped
// managed credentials before deleting the environment row. It performs no
// mutations, so a failed plan cannot leave a live environment without creds.
func (s *server) planProjectEnvironmentManagedResourceCleanup(
	ctx context.Context,
	acct state.Account,
	project state.Project,
	scope string,
) (projectEnvironmentManagedResourceCleanup, error) {
	plan := projectEnvironmentManagedResourceCleanup{}
	apps, err := s.store.AppsForProject(ctx, acct.ID, project.ID)
	if err != nil {
		return plan, fmt.Errorf("list environment workloads: %w", err)
	}
	var bucketStore state.ObjectBucketStore
	var credentialStore state.ObjectS3CredentialBindingStore
	for _, app := range apps {
		secrets, err := s.store.ListAppSecretsInScope(ctx, acct.ID, app.ID, scope)
		if err != nil {
			return plan, fmt.Errorf("list managed credentials for workload %q: %w", app.Slug, err)
		}
		postgresIDs := map[string]struct{}{}
		objectStorageIDs := map[string]struct{}{}
		for _, secret := range secrets {
			if secret.ManagedPostgresBindingID != "" {
				postgresIDs[secret.ManagedPostgresBindingID] = struct{}{}
			}
			if secret.ManagedObjectStorageCredentialID != "" {
				objectStorageIDs[secret.ManagedObjectStorageCredentialID] = struct{}{}
			}
		}
		for bindingID := range postgresIDs {
			if s.managedPostgresBindings == nil || s.managedPostgres == nil {
				return plan, managedpostgres.ErrUnavailable
			}
			binding, err := s.managedPostgresBindings.Get(ctx, acct.ID, bindingID)
			if err != nil {
				return plan, fmt.Errorf("load managed PostgreSQL binding for workload %q: %w", app.Slug, err)
			}
			if binding.AppID != app.ID || binding.Scope != scope {
				return plan, managedpostgres.ErrConflict
			}
			database, err := s.managedPostgres.Get(ctx, acct.ID, binding.DatabaseID)
			if err != nil {
				return plan, fmt.Errorf("load managed PostgreSQL database for workload %q: %w", app.Slug, err)
			}
			isEnvironmentClone := database.RestoreSourceDatabaseID != "" &&
				database.Name == projectEnvironmentDatabaseCloneName(project, scope, app, database.RestoreSourceDatabaseID)
			plan.postgres = append(plan.postgres, projectEnvironmentPostgresCleanup{
				app: app, binding: binding, database: database, deleteDatabase: isEnvironmentClone,
			})
		}
		if bucketStore == nil {
			var ok bool
			bucketStore, ok = s.store.(state.ObjectBucketStore)
			if !ok {
				if len(objectStorageIDs) == 0 {
					continue
				}
				return plan, errors.New("object storage bucket store is unavailable")
			}
		}
		if len(objectStorageIDs) > 0 && credentialStore == nil {
			var ok bool
			credentialStore, ok = s.store.(state.ObjectS3CredentialBindingStore)
			if !ok {
				return plan, errors.New("object storage credential store is unavailable")
			}
		}
		buckets, err := bucketStore.ListObjectBuckets(ctx, acct.ID, app.ID)
		if err != nil {
			return plan, fmt.Errorf("list object storage buckets for workload %q: %w", app.Slug, err)
		}
		if len(objectStorageIDs) == 0 {
			for _, bucket := range buckets {
				if bucket.Scope == scope && bucket.EnvironmentCloneSourceBucketID != "" {
					if credentialStore == nil {
						var ok bool
						credentialStore, ok = s.store.(state.ObjectS3CredentialBindingStore)
						if !ok {
							return plan, errors.New("object storage credential store is unavailable")
						}
					}
					plan.objectStorage = append(plan.objectStorage, projectEnvironmentObjectStorageCleanup{app: app, bucket: bucket})
				}
			}
			continue
		}
		seenCloneBuckets := map[string]bool{}
		for credentialID := range objectStorageIDs {
			found := false
			for _, bucket := range buckets {
				credential, getErr := credentialStore.GetObjectS3Credential(ctx, acct.ID, bucket.ID, credentialID)
				if errors.Is(getErr, state.ErrNotFound) {
					continue
				}
				if getErr != nil {
					return plan, fmt.Errorf("load object storage credential for workload %q: %w", app.Slug, getErr)
				}
				if credential.ManagedAppID != app.ID || credential.ManagedScope != scope {
					return plan, state.ErrConflict
				}
				plan.objectStorage = append(plan.objectStorage, projectEnvironmentObjectStorageCleanup{
					app: app, bucket: bucket, credential: credential,
				})
				seenCloneBuckets[bucket.ID] = true
				found = true
				break
			}
			if !found {
				return plan, fmt.Errorf("object storage credential %q is missing", credentialID)
			}
		}
		for _, bucket := range buckets {
			if bucket.Scope == scope && bucket.EnvironmentCloneSourceBucketID != "" && !seenCloneBuckets[bucket.ID] {
				plan.objectStorage = append(plan.objectStorage, projectEnvironmentObjectStorageCleanup{app: app, bucket: bucket})
			}
		}
	}
	return plan, nil
}

func projectEnvironmentCleanupResources(plan projectEnvironmentManagedResourceCleanup) state.ProjectEnvironmentCleanupResources {
	resources := state.ProjectEnvironmentCleanupResources{}
	for _, item := range plan.postgres {
		resources.Postgres = append(resources.Postgres, state.ProjectEnvironmentPostgresCleanupResource{
			AppID: item.app.ID, Scope: item.binding.Scope, BindingID: item.binding.ID, DatabaseID: item.database.ID,
			DatabaseName: item.database.Name, RestoreSourceDatabaseID: item.database.RestoreSourceDatabaseID,
			DeleteDatabase: item.deleteDatabase,
		})
	}
	for _, item := range plan.objectStorage {
		scope := item.bucket.Scope
		if item.credential.ManagedScope != "" {
			scope = item.credential.ManagedScope
		}
		deleteBucket := item.bucket.EnvironmentCloneSourceBucketID != "" && item.bucket.Scope == scope
		sourceBucketID := ""
		if deleteBucket {
			sourceBucketID = item.bucket.EnvironmentCloneSourceBucketID
		}
		resources.ObjectStorage = append(resources.ObjectStorage, state.ProjectEnvironmentObjectStorageCleanupResource{
			AppID: item.app.ID, Scope: scope, BucketID: item.bucket.ID,
			CredentialID:   item.credential.ID,
			DeleteBucket:   deleteBucket,
			SourceBucketID: sourceBucketID,
		})
	}
	return resources
}

func (s *server) cleanupProjectEnvironmentManagedResourcePayload(
	ctx context.Context,
	acct state.Account,
	resources state.ProjectEnvironmentCleanupResources,
) error {
	var cleanupErr error
	cloneDatabases := map[string]state.ProjectEnvironmentPostgresCleanupResource{}
	for _, item := range resources.Postgres {
		if s.managedPostgresBindings == nil {
			cleanupErr = errors.Join(cleanupErr, managedpostgres.ErrUnavailable)
			continue
		}
		binding, err := s.managedPostgresBindings.Get(ctx, acct.ID, item.BindingID)
		if errors.Is(err, managedpostgres.ErrNotFound) {
			err = nil
		} else if err == nil && (binding.AppID != item.AppID || binding.Scope != item.Scope || binding.DatabaseID != item.DatabaseID) {
			err = managedpostgres.ErrConflict
		}
		if err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("inspect PostgreSQL binding %q: %w", item.BindingID, err))
			continue
		}
		if binding.ID != "" {
			if _, err := s.managedPostgresBindings.Delete(ctx, acct.ID, item.BindingID); err != nil && !errors.Is(err, managedpostgres.ErrNotFound) {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("revoke PostgreSQL binding %q: %w", item.BindingID, err))
				continue
			}
		}
		if item.DeleteDatabase {
			cloneDatabases[item.DatabaseID] = item
		}
	}
	for databaseID, resource := range cloneDatabases {
		if s.managedPostgres == nil || s.managedPostgresBindings == nil {
			cleanupErr = errors.Join(cleanupErr, managedpostgres.ErrUnavailable)
			continue
		}
		database, err := s.managedPostgres.Get(ctx, acct.ID, databaseID)
		if errors.Is(err, managedpostgres.ErrNotFound) {
			continue
		}
		if err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("inspect cloned database %q: %w", resource.DatabaseName, err))
			continue
		}
		if database.Name != resource.DatabaseName || database.RestoreSourceDatabaseID != resource.RestoreSourceDatabaseID ||
			resource.RestoreSourceDatabaseID == "" {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("cloned database %q no longer matches its cleanup record: %w", resource.DatabaseName, managedpostgres.ErrConflict))
			continue
		}
		bindings, err := s.managedPostgresBindings.List(ctx, acct.ID, databaseID)
		if err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("check remaining bindings for database %q: %w", database.Name, err))
			continue
		}
		if len(bindings) != 0 {
			continue
		}
		if _, err := s.managedPostgres.Delete(ctx, acct.ID, databaseID); err != nil && !errors.Is(err, managedpostgres.ErrNotFound) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete cloned database %q: %w", database.Name, err))
		}
	}
	if len(resources.ObjectStorage) > 0 {
		credentialStore, ok := s.store.(state.ObjectS3CredentialBindingStore)
		if !ok {
			cleanupErr = errors.Join(cleanupErr, errors.New("object storage credential store is unavailable"))
		} else {
			for _, item := range resources.ObjectStorage {
				if item.DeleteBucket && item.CredentialID == "" {
					if err := s.deleteProjectEnvironmentObjectStorageCloneBucket(ctx, acct.ID, item.AppID, item.Scope, item.BucketID, item.SourceBucketID); err != nil {
						cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete isolated object storage bucket %q: %w", item.BucketID, err))
					}
					continue
				}
				credential, err := credentialStore.GetObjectS3Credential(ctx, acct.ID, item.BucketID, item.CredentialID)
				if errors.Is(err, state.ErrNotFound) {
					err = nil
				} else if err == nil && (credential.ManagedAppID != item.AppID || credential.ManagedScope != item.Scope) {
					err = state.ErrConflict
				}
				if err != nil {
					cleanupErr = errors.Join(cleanupErr, fmt.Errorf("inspect object storage credential %q: %w", item.CredentialID, err))
					continue
				}
				if credential.ID != "" && credential.Status == state.ObjectS3CredentialStatusActive {
					if err := credentialStore.RevokeObjectS3Credential(ctx, acct.ID, item.BucketID, item.CredentialID); err != nil && !errors.Is(err, state.ErrNotFound) {
						cleanupErr = errors.Join(cleanupErr, fmt.Errorf("revoke object storage credential %q: %w", item.CredentialID, err))
						continue
					}
				}
				if err := s.store.DeleteManagedObjectStorageSecrets(ctx, item.CredentialID); err != nil {
					cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove object storage secrets for credential %q: %w", item.CredentialID, err))
					continue
				}
				if item.DeleteBucket {
					if err := s.deleteProjectEnvironmentObjectStorageCloneBucket(ctx, acct.ID, item.AppID, item.Scope, item.BucketID, item.SourceBucketID); err != nil {
						cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete isolated object storage bucket %q: %w", item.BucketID, err))
					}
				}
			}
		}
	}
	return cleanupErr
}
