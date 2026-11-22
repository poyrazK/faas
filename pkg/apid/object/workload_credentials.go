package object

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// WorkloadCredentialTTL is how long a workload credential lives
// (ADR-20261122191423 slice 2). The guest's credential endpoint refreshes it
// before Expiration, as AWS SDKs do for container credentials.
const WorkloadCredentialTTL = time.Hour

// ErrWorkloadBucketNotFound is a granted bucket that does not exist (or is
// not ready) in its owner app's production scope.
var ErrWorkloadBucketNotFound = errors.New("object: granted bucket not found")

// WorkloadBucket finds the production bucket named name owned by owner.
func (s *Service) WorkloadBucket(ctx context.Context, acct state.Account, owner state.App, name string) (state.ObjectBucket, error) {
	store, ok := s.Host.Store().(state.ObjectBucketStore)
	if !ok {
		return state.ObjectBucket{}, objectstorage.ErrUnavailable
	}
	buckets, err := store.ListObjectBuckets(ctx, acct.ID, owner.ID)
	if err != nil {
		return state.ObjectBucket{}, err
	}
	for _, bucket := range buckets {
		if strings.EqualFold(bucket.Name, name) && state.ProductionDeploymentScope(bucket.Scope) && bucket.State == "ready" {
			return bucket, nil
		}
	}
	return state.ObjectBucket{}, ErrWorkloadBucketNotFound
}

// IssueWorkloadCredential mints a one-hour S3 credential for one instance of
// caller on bucket, limited to permission and keyPrefix. The secret is sealed
// to the gateway; the caller receives it once, in the AWS container
// credential shape.
func (s *Service) IssueWorkloadCredential(ctx context.Context, acct state.Account, caller state.App, instanceID string, bucket state.ObjectBucket, permission, keyPrefix string, now time.Time) (api.ObjectWorkloadCredentialResponse, error) {
	store, ok := s.Host.Store().(state.ObjectWorkloadCredentialStore)
	if !ok || !s.ObjectStorageEnabled() || !s.ObjectStorageSecretSealReady() {
		return api.ObjectWorkloadCredentialResponse{}, objectstorage.ErrUnavailable
	}
	recipients, kid, err := s.Host.StorageCredentialRecipients()
	if err != nil {
		return api.ObjectWorkloadCredentialResponse{}, objectstorage.ErrUnavailable
	}
	accessKeyID, secretAccessKey, err := api.GenerateObjectS3Credential()
	if err != nil {
		return api.ObjectWorkloadCredentialResponse{}, objectstorage.ErrUnavailable
	}
	accessKeyID = state.ObjectWorkloadCredentialAccessKeyPrefix + strings.TrimPrefix(accessKeyID, api.ObjectS3AccessKeyPrefix)
	sealed, err := secretbox.SealBytesTo(recipients, s3gateway.CredentialSecretNamespace, []byte(secretAccessKey), 64)
	if err != nil {
		return api.ObjectWorkloadCredentialResponse{}, objectstorage.ErrUnavailable
	}
	now = now.UTC().Truncate(time.Second)
	expires := now.Add(WorkloadCredentialTTL)
	credential := state.ObjectWorkloadCredential{
		ID: uuid.NewString(), AccountID: acct.ID, BucketID: bucket.ID, CallerAppID: caller.ID, InstanceID: instanceID,
		AccessKeyID: accessKeyID, SecretSealed: sealed, KID: kid, Permission: permission, KeyPrefix: keyPrefix,
		CreatedAt: now, ExpiresAt: expires,
	}
	if err := store.CreateObjectWorkloadCredential(ctx, credential); err != nil {
		return api.ObjectWorkloadCredentialResponse{}, err
	}
	return api.ObjectWorkloadCredentialResponse{
		AccessKeyID: accessKeyID, SecretAccessKey: secretAccessKey, Token: credential.ID, Expiration: expires,
		Bucket: bucket.Name, KeyPrefix: keyPrefix, Endpoint: s.Host.ObjectStorage().PublicEndpoint, Region: s.Host.ObjectStorage().PublicRegion,
	}, nil
}

// WorkloadCredentialSweepInterval is how often apid deletes expired workload
// credentials. They stop resolving at expiry; the sweep only frees rows.
const WorkloadCredentialSweepInterval = 5 * time.Minute

// RunWorkloadCredentialSweep deletes expired workload credentials until ctx
// ends.
func (s *Service) RunWorkloadCredentialSweep(ctx context.Context) {
	store, ok := s.Host.Store().(state.ObjectWorkloadCredentialStore)
	if !ok {
		return
	}
	ticker := time.NewTicker(WorkloadCredentialSweepInterval)
	defer ticker.Stop()
	for {
		s.SweepWorkloadCredentials(ctx, store, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// SweepWorkloadCredentials deletes credentials that expired before now, in
// batches.
func (s *Service) SweepWorkloadCredentials(ctx context.Context, store state.ObjectWorkloadCredentialStore, now time.Time) {
	const batch = 1000
	for range 20 {
		n, err := store.DeleteExpiredObjectWorkloadCredentials(ctx, now, batch)
		if err != nil {
			if ctx.Err() == nil {
				s.Host.Logger().Warn("workload credential sweep failed", "error", err)
			}
			return
		}
		if n < batch {
			return
		}
	}
}
