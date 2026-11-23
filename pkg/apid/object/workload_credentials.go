package object

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/capgrant"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// WorkloadCredentialTTL is how long a workload credential lives
// (ADR-20261122191423 slice 2). The guest's credential endpoint refreshes it
// before Expiration, as AWS SDKs do for container credentials.
const WorkloadCredentialTTL = api.WorkloadObjectCredentialTTL

// MaxLiveWorkloadCredentials caps one instance's unexpired credentials for one
// bucket (api.WorkloadObjectCredentialsPerInstanceBucket).
const MaxLiveWorkloadCredentials = api.WorkloadObjectCredentialsPerInstanceBucket

var (
	// ErrWorkloadBucketNotFound is a granted bucket that does not exist (or
	// is not ready) in its owner app's production scope.
	ErrWorkloadBucketNotFound = errors.New("object: granted bucket not found")
	// ErrWorkloadBucketNotGranted is a bucket the caller's uses: does not name.
	ErrWorkloadBucketNotGranted = errors.New("object: bucket not granted")
	// ErrWorkloadBucketAmbiguous is a request without a bucket from an app
	// holding several bucket grants.
	ErrWorkloadBucketAmbiguous = errors.New("object: several bucket grants; name the bucket")
	// ErrWorkloadCredentialLimit is an instance at MaxLiveWorkloadCredentials.
	ErrWorkloadCredentialLimit = errors.New("object: too many live workload credentials")
)

// ResolveWorkloadGrant picks caller's grant for bucket (or its only bucket
// grant when bucket is empty), resolves the grant's owner app (self, a
// service of the caller's project, or an app slug of acct) and returns the
// owner's production bucket.
func (s *Service) ResolveWorkloadGrant(ctx context.Context, acct state.Account, caller state.App, bucket string) (capgrant.BucketGrant, state.ObjectBucket, error) {
	var grants []capgrant.BucketGrant
	if caller.Manifest.Uses != nil {
		grants = caller.Manifest.Uses.Normalized().Buckets
	}
	var grant capgrant.BucketGrant
	switch {
	case bucket == "" && len(grants) == 1:
		grant = grants[0]
	case bucket == "" && len(grants) > 1:
		return capgrant.BucketGrant{}, state.ObjectBucket{}, ErrWorkloadBucketAmbiguous
	default:
		i := slices.IndexFunc(grants, func(g capgrant.BucketGrant) bool { return strings.EqualFold(g.Bucket, bucket) })
		if i < 0 {
			return capgrant.BucketGrant{}, state.ObjectBucket{}, ErrWorkloadBucketNotGranted
		}
		grant = grants[i]
	}
	owner, ok := s.workloadGrantOwner(ctx, acct, caller, grant.App)
	if !ok {
		return grant, state.ObjectBucket{}, ErrWorkloadBucketNotFound
	}
	found, err := s.workloadBucket(ctx, acct, owner, grant.Bucket)
	return grant, found, err
}

func (s *Service) workloadGrantOwner(ctx context.Context, acct state.Account, caller state.App, name string) (state.App, bool) {
	if name == capgrant.Self {
		return caller, true
	}
	store := s.Host.Store()
	if caller.ProjectID != "" {
		if apps, err := store.AppsForProject(ctx, caller.AccountID, caller.ProjectID); err == nil {
			for _, app := range apps {
				if strings.EqualFold(app.WorkloadName, name) && app.Status != state.AppDeleted {
					return app, true
				}
			}
		}
	}
	app, err := store.AppBySlug(ctx, name)
	if err != nil || app.Status == state.AppDeleted {
		return state.App{}, false
	}
	owner, err := state.AppResourceAccount(ctx, store, app)
	return app, err == nil && owner.ID == acct.ID
}

// workloadBucket finds the production bucket named name owned by owner.
func (s *Service) workloadBucket(ctx context.Context, acct state.Account, owner state.App, name string) (state.ObjectBucket, error) {
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
	live, err := store.CountLiveObjectWorkloadCredentials(ctx, instanceID, bucket.ID)
	if err != nil {
		return api.ObjectWorkloadCredentialResponse{}, objectstorage.ErrUnavailable
	}
	if live >= MaxLiveWorkloadCredentials {
		return api.ObjectWorkloadCredentialResponse{}, ErrWorkloadCredentialLimit
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

// RevokeUngrantedWorkloadCredentials deletes caller's live workload
// credentials that its current `uses:` no longer grants: a bucket it dropped,
// or a grant whose access or prefix changed. Credentials the grants still
// cover keep working, so a guest's cached credential survives an unrelated
// manifest change. Resolution stops honouring a deleted app or a stopped
// instance by itself; this covers the grant edits resolution cannot see.
func (s *Service) RevokeUngrantedWorkloadCredentials(ctx context.Context, acct state.Account, caller state.App) error {
	store, ok := s.Host.Store().(state.ObjectWorkloadCredentialStore)
	if !ok {
		return nil
	}
	live, err := store.ListLiveObjectWorkloadCredentials(ctx, caller.ID)
	if err != nil || len(live) == 0 {
		return err
	}
	type covered struct{ bucketID, access, prefix string }
	keep := map[covered]bool{}
	if caller.Status != state.AppDeleted && caller.Manifest.Uses != nil {
		for _, grant := range caller.Manifest.Uses.Normalized().Buckets {
			_, bucket, err := s.ResolveWorkloadGrant(ctx, acct, caller, grant.Bucket)
			if errors.Is(err, ErrWorkloadBucketNotFound) {
				continue
			}
			if err != nil {
				// Keep everything rather than revoke on a transient failure;
				// the next apply (or expiry) catches up.
				return err
			}
			keep[covered{bucket.ID, grant.Access, grant.Prefix}] = true
		}
	}
	var revoke []string
	for _, c := range live {
		if !keep[covered{c.BucketID, c.Permission, c.KeyPrefix}] {
			revoke = append(revoke, c.ID)
		}
	}
	_, err = store.DeleteObjectWorkloadCredentials(ctx, caller.ID, revoke)
	return err
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
