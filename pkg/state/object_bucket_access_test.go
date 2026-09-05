package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestObjectBucketAccessMem(t *testing.T) { objectBucketAccessSuite(t, state.NewMemStore()) }
func TestObjectBucketAccessPG(t *testing.T)  { st, _ := pgStore(t); objectBucketAccessSuite(t, st) }

func objectBucketAccessSuite(t *testing.T, base state.Store) {
	t.Helper()
	ctx := context.Background()
	buckets := base.(state.ObjectBucketStore)
	access := base.(state.ObjectBucketAccessStore)
	acct, err := base.CreateAccount(ctx, "bucket-access-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := base.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "bucket-access", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	bucket, err := buckets.ReserveObjectBucket(ctx, state.ObjectBucket{
		ID: id, AccountID: acct.ID, AppID: app.ID, Name: "assets", Scope: "default",
		Region: "us-east-1", BackendID: "external", BackendFingerprint: strings.Repeat("a", 64),
		PhysicalName: "gregale-" + strings.ReplaceAll(id, "-", ""),
	}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = buckets.ClaimObjectBucket(ctx, acct.ID, app.ID, bucket.ID, "ready", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = buckets.FinishObjectBucket(ctx, bucket.ID, "ready", "ready"); err != nil {
		t.Fatal(err)
	}
	createKey := func(label string, scopes []string) state.APIKey {
		t.Helper()
		_, hash, genErr := api.GenerateAPIKey()
		if genErr != nil {
			t.Fatal(genErr)
		}
		key, createErr := base.CreateAPIKey(ctx, acct.ID, hash, label, scopes)
		if createErr != nil {
			t.Fatal(createErr)
		}
		return key
	}
	readKey := createKey("reader", []string{api.ScopeStorageRead})
	writeKey := createKey("writer", []string{api.ScopeStorageWrite})
	adminKey := createKey("admin", []string{api.ScopeAdmin})

	if _, err = access.SetObjectBucketAccessGrant(ctx, acct.ID, bucket.ID, writeKey.ID, state.ObjectBucketPermissionRead); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("scope mismatch = %v, want conflict", err)
	}
	if _, err = access.SetObjectBucketAccessGrant(ctx, acct.ID, bucket.ID, adminKey.ID, state.ObjectBucketPermissionRead); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("admin grant = %v, want conflict", err)
	}
	grant, err := access.SetObjectBucketAccessGrant(ctx, acct.ID, bucket.ID, readKey.ID, state.ObjectBucketPermissionRead)
	if err != nil || grant.KeyLabel != "reader" || grant.Permission != state.ObjectBucketPermissionRead {
		t.Fatal(grant, err)
	}
	if ok, err := access.ObjectBucketKeyCan(ctx, acct.ID, bucket.ID, readKey.ID, state.ObjectBucketPermissionRead); err != nil || !ok {
		t.Fatal("read denied", ok, err)
	}
	if ok, err := access.ObjectBucketKeyCan(ctx, acct.ID, bucket.ID, readKey.ID, state.ObjectBucketPermissionWrite); err != nil || ok {
		t.Fatal("reader wrote", ok, err)
	}
	visible, err := access.ListObjectBucketsForKey(ctx, acct.ID, app.ID, readKey.ID)
	if err != nil || len(visible) != 1 || visible[0].ID != bucket.ID {
		t.Fatal("grant-filtered list", visible, err)
	}
	grants, err := access.ListObjectBucketAccessGrants(ctx, acct.ID, bucket.ID)
	if err != nil || len(grants) != 1 || grants[0].APIKeyID != readKey.ID {
		t.Fatal("grant list", grants, err)
	}

	_, successorHash, _ := api.GenerateAPIKey()
	successor, _, err := base.RotateAPIKey(ctx, acct.ID, readKey.ID, successorHash, "reader-next", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := access.ObjectBucketKeyCan(ctx, acct.ID, bucket.ID, successor.ID, state.ObjectBucketPermissionRead); err != nil || !ok {
		t.Fatal("rotation lost grant", ok, err)
	}
	if err := access.DeleteObjectBucketAccessGrant(ctx, acct.ID, bucket.ID, successor.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := access.ObjectBucketKeyCan(ctx, acct.ID, bucket.ID, successor.ID, state.ObjectBucketPermissionRead); err != nil || ok {
		t.Fatal("deleted grant remained", ok, err)
	}
}
