package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func projectEnvironmentObjectStorageCloneName(target string, source state.ObjectBucket) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{source.AppID, source.ID, target}, "\x00")))
	return "env-" + target + "-" + hex.EncodeToString(sum[:6])
}

func (s *server) ensureProjectEnvironmentObjectStorageCloneBucket(
	ctx context.Context,
	acct state.Account,
	target string,
	plan projectEnvironmentBindingClone,
	cleanup *[]func(context.Context) error,
) (state.ObjectBucket, error) {
	store, ok := s.store.(state.ObjectBucketStore)
	if !ok {
		return state.ObjectBucket{}, errors.New("object storage bucket store is unavailable")
	}
	reservations, ok := s.store.(state.ObjectBucketReservationResultStore)
	if !ok {
		return state.ObjectBucket{}, errors.New("object storage reservation lifecycle is unavailable")
	}
	name := projectEnvironmentObjectStorageCloneName(target, plan.bucket)
	bucketID := uuid.NewString()
	reserved, created, err := reservations.ReserveObjectBucketWithResult(ctx, state.ObjectBucket{
		ID: bucketID, AccountID: acct.ID, AppID: plan.app.ID,
		Name: name, Scope: target, Region: plan.bucket.Region,
		BackendID: plan.bucket.BackendID, BackendFingerprint: plan.bucket.BackendFingerprint,
		PhysicalName:                   "gregale-" + strings.ReplaceAll(bucketID, "-", ""),
		EnvironmentCloneSourceBucketID: plan.bucket.ID,
	}, s.objectStorage.MaxBucketsPerApp)
	if err != nil {
		return state.ObjectBucket{}, err
	}
	if reserved.EnvironmentCloneSourceBucketID != plan.bucket.ID || reserved.BackendID != plan.bucket.BackendID ||
		reserved.BackendFingerprint != plan.bucket.BackendFingerprint || reserved.Region != plan.bucket.Region ||
		reserved.PublicRead || reserved.ServeAt != "" {
		return state.ObjectBucket{}, state.ErrConflict
	}
	if created {
		bucketID, sourceBucketID, appID, scope := reserved.ID, plan.bucket.ID, plan.app.ID, target
		*cleanup = append(*cleanup, func(cleanupCtx context.Context) error {
			return s.deleteProjectEnvironmentObjectStorageCloneBucket(cleanupCtx, acct.ID, appID, scope, bucketID, sourceBucketID)
		})
	}
	if reserved.State == "provisioning" {
		reserved, err = s.provisionBucket(ctx, store, reserved)
		if err != nil {
			return state.ObjectBucket{}, err
		}
	}
	if reserved.State != "ready" {
		return state.ObjectBucket{}, state.ErrConflict
	}
	return reserved, nil
}

func copyProjectEnvironmentObjectStorageObjects(ctx context.Context, provider objectstorage.Provider, source, destination string) error {
	copier, ok := provider.(objectstorage.CrossBucketObjectCopier)
	if !ok {
		return errIsolatedObjectStorageCloneUnsupported
	}
	copyCtx, cancel := context.WithTimeout(ctx, api.ObjectTransferTimeout)
	defer cancel()
	cursor := ""
	seen := map[string]bool{}
	for range api.ObjectStorageInventoryMaxPages {
		page, err := provider.ListObjects(copyCtx, source, "", cursor, 1000)
		if err != nil {
			return fmt.Errorf("list source bucket objects: %w", err)
		}
		if len(page.Items) > 1000 {
			return objectstorage.ErrInvalid
		}
		for _, item := range page.Items {
			if !objectstorage.ValidKey(item.Key) || item.Size < 0 {
				return objectstorage.ErrInvalid
			}
			if _, err := copier.CopyObjectBetweenBuckets(copyCtx, source, destination, objectstorage.CopyObjectRequest{
				SourceKey: item.Key, DestinationKey: item.Key,
			}); err != nil {
				return fmt.Errorf("copy source bucket object: %w", err)
			}
		}
		if page.NextCursor == "" {
			return nil
		}
		if seen[page.NextCursor] || len(page.NextCursor) > 8192 {
			return objectstorage.ErrInvalid
		}
		seen[page.NextCursor] = true
		cursor = page.NextCursor
	}
	return objectstorage.ErrUnavailable
}

func (s *server) prepareIsolatedProjectEnvironmentObjectStorageBinding(
	r *http.Request,
	acct state.Account,
	target string,
	plan projectEnvironmentBindingClone,
	cleanup *[]func(context.Context) error,
) (string, int, error) {
	if !s.objectStorageEnabled() {
		return "", 0, objectstorage.ErrUnavailable
	}
	if !objectStorageSecretSealReady() {
		return "", 0, errors.New("object storage secret sealing is unavailable")
	}
	backend, err := s.objectStorage.Resolve(plan.bucket.BackendID, plan.bucket.BackendFingerprint)
	if err != nil {
		return "", 0, err
	}
	if _, ok := backend.Provider.(objectstorage.CrossBucketObjectCopier); !ok {
		return "", 0, errIsolatedObjectStorageCloneUnsupported
	}
	bucket, err := s.ensureProjectEnvironmentObjectStorageCloneBucket(r.Context(), acct, target, plan, cleanup)
	if err != nil {
		return "", 0, err
	}
	if err := copyProjectEnvironmentObjectStorageObjects(r.Context(), backend.Provider, plan.bucket.PhysicalName, bucket.PhysicalName); err != nil {
		return "", 0, err
	}
	store, ok := s.store.(state.ObjectS3CredentialBindingStore)
	if !ok {
		return "", 0, errors.New("object storage credential store is unavailable")
	}
	accessKeyID, secretAccessKey, err := api.GenerateObjectS3Credential()
	if err != nil {
		return "", 0, fmt.Errorf("generate isolated object storage credential: %w", err)
	}
	recipient := setSecretRecipient()
	sealed, err := secretbox.SealBytes(recipient, s3gateway.CredentialSecretNamespace, []byte(secretAccessKey), 64)
	if err != nil {
		return "", 0, fmt.Errorf("seal isolated object storage credential: %w", err)
	}
	credential, err := store.CreateObjectS3Credential(r.Context(), state.ObjectS3Credential{
		ID: uuid.NewString(), AccountID: acct.ID, BucketID: bucket.ID, AccessKeyID: accessKeyID,
		SecretSealed: sealed, KID: recipient.String(), Label: plan.objectCred.Label,
		Permission: plan.objectCred.Permission, Status: state.ObjectS3CredentialStatusActive,
		ManagedAppID: plan.app.ID, ManagedScope: target, ManagedPrefix: plan.objectCred.ManagedPrefix,
	}, api.MaxObjectS3CredentialsPerBucket)
	if err != nil {
		return "", 0, fmt.Errorf("create isolated object storage credential: %w", err)
	}
	credentialID, bucketID := credential.ID, bucket.ID
	*cleanup = append(*cleanup, func(ctx context.Context) error {
		revokeErr := store.RevokeObjectS3Credential(ctx, acct.ID, bucketID, credentialID)
		if errors.Is(revokeErr, state.ErrNotFound) {
			revokeErr = nil
		}
		secretErr := s.store.DeleteManagedObjectStorageSecrets(ctx, credentialID)
		return errors.Join(revokeErr, secretErr)
	})
	values := objectStorageBindingSecretValues(
		objectStorageBindingSecretKeys(plan.objectCred.ManagedPrefix), s.objectStorage.PublicEndpoint,
		s.objectStorage.PublicRegion, bucket.Name, accessKeyID, secretAccessKey,
	)
	if problem := s.persistObjectStorageBindingSecrets(r, acct, plan.app, credential.ID, target, values, api.MustLimitsFor(acct.Plan)); problem != nil {
		return "", 0, errors.New(problem.Detail)
	}
	return credential.ID, len(values), nil
}

func (s *server) deleteProjectEnvironmentObjectStorageCloneBucket(ctx context.Context, accountID, appID, scope, bucketID, sourceBucketID string) error {
	buckets, ok := s.store.(state.ObjectBucketStore)
	if !ok {
		return errors.New("object storage bucket store is unavailable")
	}
	bucket, err := buckets.GetObjectBucket(ctx, accountID, appID, bucketID)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if bucket.Scope != scope || bucket.EnvironmentCloneSourceBucketID != sourceBucketID {
		return state.ErrConflict
	}
	credentials, ok := s.store.(state.ObjectS3CredentialBindingStore)
	if !ok {
		return errors.New("object storage credential store is unavailable")
	}
	active, err := credentials.ListObjectS3Credentials(ctx, accountID, bucketID)
	if err != nil {
		return err
	}
	if len(active) != 0 {
		return nil
	}
	token := uuid.NewString()
	claimed, err := buckets.ClaimObjectBucket(ctx, accountID, appID, bucketID, token, "deleting")
	if err != nil {
		return err
	}
	return s.executeBucketOperation(ctx, buckets, claimed)
}
