package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type ownedCleanupProvider struct {
	*fakeObjectProvider
	versioning string
	protected  bool
	readErr    error
	listErr    error
	onList     func()
}

func (p *ownedCleanupProvider) GetBucketVersioning(context.Context, string) (objectstorage.BucketVersioning, error) {
	return objectstorage.BucketVersioning{Status: p.versioning}, p.readErr
}
func (*ownedCleanupProvider) PutBucketVersioning(context.Context, string, string) error {
	return objectstorage.ErrUnsupported
}
func (p *ownedCleanupProvider) GetBucketObjectLock(context.Context, string) (api.ObjectBucketObjectLockConfiguration, error) {
	return api.ObjectBucketObjectLockConfiguration{Enabled: p.protected}, p.readErr
}
func (*ownedCleanupProvider) PutBucketObjectLock(context.Context, string, api.ObjectBucketObjectLockConfiguration) error {
	return objectstorage.ErrUnsupported
}
func (p *ownedCleanupProvider) ListObjects(ctx context.Context, bucket, prefix, cursor string, limit int32) (objectstorage.ObjectPage, error) {
	if p.onList != nil {
		p.onList()
	}
	if p.listErr != nil {
		return objectstorage.ObjectPage{}, p.listErr
	}
	return p.fakeObjectProvider.ListObjects(ctx, bucket, prefix, cursor, limit)
}

func TestOwnedBucketCleanupFenceMem(t *testing.T) {
	e := setup(t, api.PlanHobby)
	ownedBucketCleanupSuite(t, e.s, e.store, e.acct)
}

func ownedBucketCleanupSuite(t *testing.T, srv *server, st state.Store, account state.Account) {
	for _, tc := range []struct {
		name, versioning string
		pending, lock    bool
		readErr          error
		want             error
	}{
		{name: "accepted write", pending: true, want: state.ErrConflict},
		{name: "enabled versions", versioning: "Enabled", want: objectstorage.ErrUnsupported},
		{name: "suspended versions", versioning: "Suspended", want: objectstorage.ErrUnsupported},
		{name: "protected versions", lock: true, want: objectstorage.ErrUnsupported},
		{name: "unknown configuration", readErr: objectstorage.ErrUnavailable, want: objectstorage.ErrUnavailable},
		{name: "ordinary cleanup"},
		{name: "restart cleanup"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &ownedCleanupProvider{fakeObjectProvider: &fakeObjectProvider{objects: map[string][]byte{"proof": []byte("abc")}}, versioning: tc.versioning, protected: tc.lock, readErr: tc.readErr}
			srv.WithObjectStorage(objectRegistry(t, provider, &fakeObjectProvider{}, "external"))
			app, err := st.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "dev-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1, IdleTimeoutS: 30, PreviewOfSlug: "source"})
			if err != nil {
				t.Fatal(err)
			}
			backend, err := srv.objectStorage.Default("us-east-1")
			if err != nil {
				t.Fatal(err)
			}
			buckets := st.(state.ObjectBucketStore)
			bucket, err := buckets.ReserveObjectBucket(t.Context(), state.ObjectBucket{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, Name: "assets", Scope: "default", Region: "us-east-1", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, PhysicalName: "gregale-" + uuid.NewString()}, 10)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = buckets.ClaimObjectBucket(t.Context(), account.ID, app.ID, bucket.ID, "create", "provisioning"); err != nil {
				t.Fatal(err)
			}
			if err = buckets.FinishObjectBucket(t.Context(), bucket.ID, "create", "ready"); err != nil {
				t.Fatal(err)
			}
			bucket.State = "ready"
			writes := st.(state.ObjectCapacityStore)
			writeID := uuid.NewString()
			if tc.pending {
				accounting := st.(state.ObjectStorageAccountingStore)
				if err = accounting.ClaimObjectInventory(t.Context(), bucket.ID, "inventory"); err != nil {
					t.Fatal(err)
				}
				if err = accounting.FinishObjectInventory(t.Context(), bucket.ID, "inventory", 0, 0); err != nil {
					t.Fatal(err)
				}
				report := api.ObjectStorageUsageReport{AccountID: account.ID, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Source: "provider", PeriodStart: state.ObjectStoragePeriod(time.Now()), ObservedAt: time.Now().Add(-time.Minute)}
				if err = accounting.RecordObjectUsageReport(t.Context(), report); err != nil {
					t.Fatal(err)
				}
				if err = writes.BeginObjectWrite(t.Context(), account.ID, bucket.ID, writeID, "proof", 3, srv.objectStorage.Accounting); err != nil {
					t.Fatal(err)
				}
			}
			provider.onList = func() {
				claimed, err := buckets.GetObjectBucket(t.Context(), account.ID, app.ID, bucket.ID)
				if err != nil || claimed.State != "deleting" || claimed.LeaseToken == "" {
					t.Error("provider cleanup ran without a claimed fence", claimed, err)
				}
			}
			if tc.name == "restart cleanup" {
				if _, err = buckets.ClaimObjectBucket(t.Context(), account.ID, app.ID, bucket.ID, "crashed", "deleting"); err != nil {
					t.Fatal(err)
				}
				if err = buckets.FinishObjectBucket(t.Context(), bucket.ID, "crashed", "deleting"); err != nil {
					t.Fatal(err)
				}
				restarted := newServer(st, testLogger(), "gregale.dev", noopNotifier{}).WithObjectStorage(srv.objectStorage)
				err = restarted.reconcileObjectBuckets(t.Context(), nil)
			} else {
				err = srv.cleanupDevSessionBucket(t.Context(), buckets, bucket)
			}
			if !errors.Is(err, tc.want) {
				t.Fatal("cleanup result", err)
			}
			if tc.want == nil {
				if len(provider.objects) != 0 {
					t.Fatal("ordinary bucket not emptied")
				}
				if _, err = buckets.GetObjectBucket(t.Context(), account.ID, app.ID, bucket.ID); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("bucket not tombstoned", err)
				}
				return
			}
			if len(provider.accessed) != 0 || len(provider.objects) != 1 {
				t.Fatal("unqualified cleanup deleted recovery evidence", provider.accessed, provider.objects)
			}
			kept, err := buckets.GetObjectBucket(t.Context(), account.ID, app.ID, bucket.ID)
			if err != nil || kept.State == "deleted" {
				t.Fatal("cleanup forgot its durable owner", kept, err)
			}
			if tc.pending {
				if err = writes.SettleObjectWrite(t.Context(), account.ID, bucket.ID, writeID); err != nil {
					t.Fatal(err)
				}
				// Only verified settlement removes the write guard.
				if err = srv.cleanupDevSessionBucket(t.Context(), buckets, kept); err != nil || len(provider.objects) != 0 {
					t.Fatal("settled cleanup did not recover", err)
				}
			} else if kept.State != "deleting" || kept.LeaseToken != "" || kept.LastErrorCode == "" || !kept.RetryAt.After(time.Now()) {
				t.Fatal("unqualified cleanup did not persist its retry fence", kept)
			}
		})
	}
}
