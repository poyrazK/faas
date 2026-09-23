package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

var errProjectEnvironmentCloneCleanup = errors.New("project environment clone cleanup incomplete")
var errIsolatedObjectStorageCloneUnsupported = errors.New("isolated object-storage environment cloning is not supported")

type projectEnvironmentBindingClone struct {
	kind       string
	app        state.App
	postgres   managedpostgres.Binding
	database   managedpostgres.Database
	bucket     state.ObjectBucket
	objectCred state.ObjectS3Credential
}

type projectEnvironmentBindingCloneError struct {
	cause   error
	cleanup error
}

func (e *projectEnvironmentBindingCloneError) Error() string {
	return fmt.Sprintf("%v; resource compensation failed: %v", e.cause, e.cleanup)
}

func (e *projectEnvironmentBindingCloneError) Unwrap() []error {
	return []error{errProjectEnvironmentCloneCleanup, e.cause, e.cleanup}
}

func cleanupProjectEnvironmentBindingClone(ctx context.Context, cleanup []func(context.Context) error) error {
	var cleanupErr error
	for i := len(cleanup) - 1; i >= 0; i-- {
		if err := cleanup[i](ctx); err != nil && !errors.Is(err, state.ErrNotFound) && !errors.Is(err, managedpostgres.ErrNotFound) {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	return cleanupErr
}

func (s *server) planProjectEnvironmentBindingClones(ctx context.Context, acct state.Account, project state.Project, source string, shareResources bool) ([]projectEnvironmentBindingClone, error) {
	apps, err := s.store.AppsForProject(ctx, acct.ID, project.ID)
	if err != nil {
		return nil, fmt.Errorf("list project workloads for resource clone: %w", err)
	}
	plans := make([]projectEnvironmentBindingClone, 0)
	for _, app := range apps {
		secrets, err := s.store.ListAppSecretsInScope(ctx, acct.ID, app.ID, source)
		if err != nil {
			return nil, fmt.Errorf("inspect source resource bindings for workload %q: %w", app.Slug, err)
		}
		postgresIDs := map[string]bool{}
		objectSecretKeys := map[string]map[string]bool{}
		for _, secret := range secrets {
			if secret.ManagedPostgresBindingID != "" {
				postgresIDs[secret.ManagedPostgresBindingID] = true
			}
			if secret.ManagedObjectStorageCredentialID != "" {
				id := secret.ManagedObjectStorageCredentialID
				if objectSecretKeys[id] == nil {
					objectSecretKeys[id] = map[string]bool{}
				}
				objectSecretKeys[id][secret.Key] = true
			}
		}
		for bindingID := range postgresIDs {
			if s.managedPostgresBindings == nil || s.managedPostgres == nil {
				return nil, managedpostgres.ErrUnavailable
			}
			binding, err := s.managedPostgresBindings.Get(ctx, acct.ID, bindingID)
			if err != nil {
				return nil, fmt.Errorf("inspect managed PostgreSQL binding %q: %w", bindingID, err)
			}
			if binding.AppID != app.ID || binding.Scope != source || binding.State != managedpostgres.BindingStateReady {
				return nil, fmt.Errorf("managed PostgreSQL binding %q is not ready in the source environment", bindingID)
			}
			database, err := s.managedPostgres.Get(ctx, acct.ID, binding.DatabaseID)
			if err != nil {
				return nil, fmt.Errorf("inspect managed PostgreSQL database for binding %q: %w", bindingID, err)
			}
			if database.State != managedpostgres.StateReady {
				return nil, fmt.Errorf("managed PostgreSQL database for binding %q is not ready", bindingID)
			}
			if !shareResources {
				if err := validateManagedPostgresEnvironmentClonePlan(acct, database); err != nil {
					return nil, err
				}
			}
			plans = append(plans, projectEnvironmentBindingClone{kind: "managed_postgres", app: app, postgres: binding, database: database})
		}
		if len(objectSecretKeys) == 0 {
			continue
		}
		if !s.objectStorageEnabled() {
			return nil, errors.New("object storage is unavailable for binding cloning")
		}
		if !objectStorageSecretSealReady() {
			return nil, errors.New("object storage secret sealing is unavailable for binding cloning")
		}
		bucketStore, bucketOK := s.store.(state.ObjectBucketStore)
		credentialStore, credentialOK := s.store.(state.ObjectS3CredentialBindingStore)
		if !bucketOK || !credentialOK {
			return nil, errors.New("object storage binding cloning is unavailable")
		}
		buckets, err := bucketStore.ListObjectBuckets(ctx, acct.ID, app.ID)
		if err != nil {
			return nil, fmt.Errorf("list object storage buckets for workload %q: %w", app.Slug, err)
		}
		for credentialID, keys := range objectSecretKeys {
			var foundBucket state.ObjectBucket
			var foundCredential state.ObjectS3Credential
			for _, bucket := range buckets {
				credential, getErr := credentialStore.GetObjectS3Credential(ctx, acct.ID, bucket.ID, credentialID)
				if errors.Is(getErr, state.ErrNotFound) {
					continue
				}
				if getErr != nil {
					return nil, fmt.Errorf("inspect object storage binding %q: %w", credentialID, getErr)
				}
				foundBucket, foundCredential = bucket, credential
				break
			}
			if foundCredential.ID == "" || foundBucket.State != "ready" ||
				foundCredential.Status != state.ObjectS3CredentialStatusActive ||
				foundCredential.ManagedAppID != app.ID || foundCredential.ManagedScope != source ||
				foundCredential.ManagedPrefix == "" || foundBucket.Scope != source {
				return nil, fmt.Errorf("object storage binding %q is not ready in the source environment", credentialID)
			}
			if !shareResources {
				backend, resolveErr := s.objectStorage.Resolve(foundBucket.BackendID, foundBucket.BackendFingerprint)
				if resolveErr != nil {
					return nil, fmt.Errorf("resolve object storage provider for isolated binding %q: %w", credentialID, resolveErr)
				}
				if _, ok := backend.Provider.(objectstorage.CrossBucketObjectCopier); !ok {
					return nil, errIsolatedObjectStorageCloneUnsupported
				}
			}
			expected := objectStorageBindingSecretKeys(foundCredential.ManagedPrefix)
			for _, key := range []string{expected.Endpoint, expected.Region, expected.Bucket, expected.AccessKeyID, expected.SecretAccessKey, expected.AddressingStyle} {
				if !keys[key] {
					return nil, fmt.Errorf("object storage binding %q is missing a managed secret", credentialID)
				}
			}
			plans = append(plans, projectEnvironmentBindingClone{
				kind: "object_storage", app: app, bucket: foundBucket, objectCred: foundCredential,
			})
		}
	}
	sort.Slice(plans, func(i, j int) bool {
		if plans[i].app.Slug != plans[j].app.Slug {
			return plans[i].app.Slug < plans[j].app.Slug
		}
		if plans[i].kind != plans[j].kind {
			return plans[i].kind < plans[j].kind
		}
		if plans[i].postgres.ID != plans[j].postgres.ID {
			return plans[i].postgres.ID < plans[j].postgres.ID
		}
		return plans[i].objectCred.ID < plans[j].objectCred.ID
	})
	return plans, nil
}

func (s *server) cloneProjectEnvironmentBindings(r *http.Request, acct state.Account, target string, plans []projectEnvironmentBindingClone) (int, []string, error) {
	cleanup := make([]func(context.Context) error, 0, len(plans))
	shared := map[string]bool{}
	rollback := func(ctx context.Context, cause error) (int, []string, error) {
		cleanupCtx := context.WithoutCancel(ctx)
		cleanupErr := cleanupProjectEnvironmentBindingClone(cleanupCtx, cleanup)
		if cleanupErr != nil {
			return 0, nil, &projectEnvironmentBindingCloneError{cause: cause, cleanup: cleanupErr}
		}
		return 0, nil, errors.Join(cause, cleanupErr)
	}
	for _, plan := range plans {
		switch plan.kind {
		case "managed_postgres":
			binding, created, err := s.managedPostgresBindings.CreateWithResult(r.Context(), managedpostgres.CreateBindingRequest{
				AccountID: acct.ID, DatabaseID: plan.postgres.DatabaseID, AppID: plan.app.ID,
				Scope: target, EnvironmentKey: plan.postgres.EnvironmentKey, Access: plan.postgres.Access,
			})
			if created && binding.ID != "" {
				bindingID := binding.ID
				cleanup = append(cleanup, func(ctx context.Context) error {
					_, deleteErr := s.managedPostgresBindings.Delete(ctx, acct.ID, bindingID)
					return deleteErr
				})
			}
			if err != nil {
				return rollback(r.Context(), fmt.Errorf("recreate managed PostgreSQL binding for workload %q: %w", plan.app.Slug, err))
			}
			if binding.State != managedpostgres.BindingStateReady {
				return rollback(r.Context(), fmt.Errorf("managed PostgreSQL binding for workload %q is still provisioning", plan.app.Slug))
			}
			shared["managed_postgres_data"] = true
		case "object_storage":
			if err := s.cloneSharedObjectStorageBinding(r, acct, target, plan, &cleanup); err != nil {
				return rollback(r.Context(), err)
			}
			shared["object_storage_bucket_data"] = true
		default:
			return rollback(r.Context(), fmt.Errorf("unsupported managed binding kind %q", plan.kind))
		}
	}

	sharedKinds := make([]string, 0, len(shared))
	for kind := range shared {
		sharedKinds = append(sharedKinds, kind)
	}
	sort.Strings(sharedKinds)
	return len(plans), sharedKinds, nil
}

func validateManagedPostgresEnvironmentClonePlan(acct state.Account, database managedpostgres.Database) error {
	limits, ok := api.ManagedPostgresLimitsFor(acct.Plan)
	if !ok || limits.DatabasesMax == 0 || !managedPostgresPlanAllows(limits, database.Spec) ||
		(!database.Spec.ScaleToZero && !limits.AlwaysOnAllowed) ||
		database.Spec.StorageLimitBytes > limits.StorageLimitBytes ||
		database.Spec.RestoreWindowSeconds <= 0 || database.Spec.RestoreWindowSeconds > limits.RestoreWindowSeconds {
		return managedpostgres.ErrQuotaExceeded
	}
	return nil
}

func projectEnvironmentDatabaseCloneName(project state.Project, target string, app state.App, sourceDatabaseID string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{project.ID, target, app.ID, sourceDatabaseID}, "\x00")))
	return "env-" + target + "-" + hex.EncodeToString(sum[:6])
}

func (s *server) ensureProjectEnvironmentDatabaseClone(ctx context.Context, acct state.Account, project state.Project, target string, plan projectEnvironmentBindingClone, cleanup *[]func(context.Context) error) (managedpostgres.Database, error) {
	if s.managedPostgres == nil {
		return managedpostgres.Database{}, managedpostgres.ErrUnavailable
	}
	name := projectEnvironmentDatabaseCloneName(project, target, plan.app, plan.database.ID)
	databases, err := s.managedPostgres.List(ctx, acct.ID)
	if err != nil {
		return managedpostgres.Database{}, err
	}
	var existing *managedpostgres.Database
	for i := range databases {
		if databases[i].Name == name {
			existing = &databases[i]
			break
		}
	}
	pointInTime := time.Now().UTC().Add(-time.Second)
	if existing != nil {
		if existing.RestoreSourceDatabaseID != plan.database.ID || existing.Spec != plan.database.Spec ||
			existing.State == managedpostgres.StateDeleting || existing.State == managedpostgres.StateDeleted {
			return managedpostgres.Database{}, managedpostgres.ErrConflict
		}
		pointInTime = existing.RestorePointInTime
	}
	cloned, err := s.managedPostgres.Restore(ctx, managedpostgres.RestoreDatabaseRequest{
		AccountID: acct.ID, SourceDatabaseID: plan.database.ID, Name: name, PointInTime: pointInTime,
	})
	if err != nil {
		// Restore persists its intent before provider I/O. Keep that durable row
		// so a retry can resume it; deleting a row discovered after an error
		// could race with another request that adopted the same deterministic
		// clone name.
		return managedpostgres.Database{}, err
	}
	if existing == nil {
		cloneID := cloned.ID
		*cleanup = append(*cleanup, func(cleanupCtx context.Context) error {
			_, deleteErr := s.managedPostgres.Delete(cleanupCtx, acct.ID, cloneID)
			return deleteErr
		})
	}
	if cloned.State != managedpostgres.StateReady || cloned.ID == plan.database.ID || cloned.RestoreSourceDatabaseID != plan.database.ID {
		return managedpostgres.Database{}, fmt.Errorf("isolated PostgreSQL clone for workload %q is not ready", plan.app.Slug)
	}
	return cloned, nil
}

func (s *server) prepareIsolatedProjectEnvironmentBindings(r *http.Request, acct state.Account, project state.Project, target string, plans []projectEnvironmentBindingClone) ([]string, int, []func(context.Context) error, error) {
	cleanup := make([]func(context.Context) error, 0, len(plans)*2)
	rollback := func(ctx context.Context, cause error) error {
		cleanupCtx := context.WithoutCancel(ctx)
		cleanupErr := cleanupProjectEnvironmentBindingClone(cleanupCtx, cleanup)
		if cleanupErr != nil {
			return &projectEnvironmentBindingCloneError{cause: cause, cleanup: cleanupErr}
		}
		return cause
	}
	preparedIDs := make([]string, 0, len(plans))
	preparedSecretCount := 0
	for _, plan := range plans {
		switch plan.kind {
		case "managed_postgres":
			database, err := s.ensureProjectEnvironmentDatabaseClone(r.Context(), acct, project, target, plan, &cleanup)
			if err != nil {
				return nil, 0, nil, rollback(r.Context(), fmt.Errorf("create isolated PostgreSQL database for workload %q: %w", plan.app.Slug, err))
			}
			binding, created, err := s.managedPostgresBindings.CreateWithResult(r.Context(), managedpostgres.CreateBindingRequest{
				AccountID: acct.ID, DatabaseID: database.ID, AppID: plan.app.ID,
				Scope: target, EnvironmentKey: plan.postgres.EnvironmentKey, Access: plan.postgres.Access,
			})
			if created && binding.ID != "" {
				bindingID := binding.ID
				cleanup = append(cleanup, func(ctx context.Context) error {
					_, deleteErr := s.managedPostgresBindings.Delete(ctx, acct.ID, bindingID)
					return deleteErr
				})
			}
			if err != nil {
				return nil, 0, nil, rollback(r.Context(), fmt.Errorf("create isolated PostgreSQL binding for workload %q: %w", plan.app.Slug, err))
			}
			if binding.State != managedpostgres.BindingStateReady || binding.DatabaseID != database.ID {
				return nil, 0, nil, rollback(r.Context(), fmt.Errorf("isolated PostgreSQL binding for workload %q is not ready", plan.app.Slug))
			}
			preparedIDs = append(preparedIDs, binding.ID)
			preparedSecretCount++
		case "object_storage":
			bindingID, secretCount, err := s.prepareIsolatedProjectEnvironmentObjectStorageBinding(r, acct, target, plan, &cleanup)
			if err != nil {
				return nil, 0, nil, rollback(r.Context(), fmt.Errorf("create isolated object-storage binding for workload %q: %w", plan.app.Slug, err))
			}
			preparedIDs = append(preparedIDs, bindingID)
			preparedSecretCount += secretCount
		default:
			return nil, 0, nil, rollback(r.Context(), fmt.Errorf("unsupported managed binding kind %q", plan.kind))
		}
	}
	return preparedIDs, preparedSecretCount, cleanup, nil
}

func (s *server) cloneSharedObjectStorageBinding(r *http.Request, acct state.Account, target string, plan projectEnvironmentBindingClone, cleanup *[]func(context.Context) error) error {
	store, ok := s.store.(state.ObjectS3CredentialBindingStore)
	if !ok {
		return errors.New("object storage binding cloning is unavailable")
	}
	if !objectStorageSecretSealReady() {
		return errors.New("object storage secret sealing is unavailable")
	}
	recipient := setSecretRecipient()
	accessKeyID, secretAccessKey, err := api.GenerateObjectS3Credential()
	if err != nil {
		return fmt.Errorf("generate isolated object storage credential: %w", err)
	}
	sealed, err := secretbox.SealBytes(recipient, s3gateway.CredentialSecretNamespace, []byte(secretAccessKey), 64)
	if err != nil {
		return fmt.Errorf("seal isolated object storage credential: %w", err)
	}
	credential, err := store.CreateObjectS3Credential(r.Context(), state.ObjectS3Credential{
		ID: uuid.NewString(), AccountID: acct.ID, BucketID: plan.bucket.ID, AccessKeyID: accessKeyID,
		SecretSealed: sealed, KID: recipient.String(), Label: plan.objectCred.Label,
		Permission: plan.objectCred.Permission, Status: state.ObjectS3CredentialStatusActive,
		ManagedAppID: plan.app.ID, ManagedScope: target, ManagedPrefix: plan.objectCred.ManagedPrefix,
	}, api.MaxObjectS3CredentialsPerBucket)
	if err != nil {
		return fmt.Errorf("create isolated object storage credential: %w", err)
	}
	credentialID, bucketID := credential.ID, plan.bucket.ID
	*cleanup = append(*cleanup, func(ctx context.Context) error {
		revokeErr := store.RevokeObjectS3Credential(ctx, acct.ID, bucketID, credentialID)
		if errors.Is(revokeErr, state.ErrNotFound) {
			revokeErr = nil
		}
		secretErr := s.store.DeleteManagedObjectStorageSecrets(ctx, credentialID)
		return errors.Join(revokeErr, secretErr)
	})
	values := objectStorageBindingSecretValues(
		objectStorageBindingSecretKeys(plan.objectCred.ManagedPrefix),
		s.objectStorage.PublicEndpoint, s.objectStorage.PublicRegion,
		plan.bucket.Name, accessKeyID, secretAccessKey,
	)
	if problem := s.persistObjectStorageBindingSecrets(r, acct, plan.app, credential.ID, target, values, api.MustLimitsFor(acct.Plan)); problem != nil {
		return errors.New(problem.Detail)
	}
	return nil
}
