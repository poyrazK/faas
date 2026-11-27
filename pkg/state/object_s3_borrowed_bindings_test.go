package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestObjectS3BorrowedComputeBindingMem(t *testing.T) {
	objectS3BorrowedComputeBindingSuite(t, state.NewMemStore())
}

func TestObjectS3BorrowedComputeBindingPG(t *testing.T) {
	st, _ := pgStore(t)
	objectS3BorrowedComputeBindingSuite(t, st)
}

// A route split's app holds its own key on the source app's bucket
// (ADR-20261121170000 amendment 4). Borrowed binds only an app other than
// the bucket's owner, in the bucket's account; without it the holder must
// own the bucket.
func objectS3BorrowedComputeBindingSuite(t *testing.T, base state.Store) {
	t.Helper()
	ctx := context.Background()
	buckets := base.(state.ObjectBucketStore)
	bindings := base.(state.ObjectS3CredentialBindingStore)
	held := base.(state.ObjectS3HeldBindingStore)
	acct, err := base.CreateAccount(ctx, "s3-borrow-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	newApp := func(accountID, name string) state.App {
		app, err := base.CreateApp(ctx, state.App{AccountID: accountID, Slug: name + "-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	owner, holder := newApp(acct.ID, "owner"), newApp(acct.ID, "holder")
	bucketID := uuid.NewString()
	bucket, err := buckets.ReserveObjectBucket(ctx, state.ObjectBucket{ID: bucketID, AccountID: acct.ID, AppID: owner.ID, Name: "assets", Scope: "default", Region: "us-east-1", BackendID: "provider", BackendFingerprint: strings.Repeat("a", 64), PhysicalName: "gregale-" + strings.ReplaceAll(bucketID, "-", "")}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buckets.ClaimObjectBucket(ctx, acct.ID, owner.ID, bucket.ID, "provision", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := buckets.FinishObjectBucket(ctx, bucket.ID, "provision", "ready"); err != nil {
		t.Fatal(err)
	}
	request := func(app state.App, prefix string, borrowed bool) state.ObjectS3ComputeBindingCreateRequest {
		id := uuid.NewString()
		accessKeyID, _, err := api.GenerateObjectS3Credential()
		if err != nil {
			t.Fatal(err)
		}
		req := state.ObjectS3ComputeBindingCreateRequest{Credential: state.ObjectS3Credential{
			ID: id, AccountID: acct.ID, BucketID: bucket.ID, AccessKeyID: accessKeyID,
			SecretSealed: []byte("sealed"), KID: "age1test", Label: "compute", Permission: state.ObjectBucketPermissionRead,
			Status: state.ObjectS3CredentialStatusActive, ManagedAppID: app.ID, ManagedScope: "default", ManagedPrefix: prefix,
		}, MaxCredentialsPerBucket: 10, MaxSecretsPerApp: 12, Borrowed: borrowed}
		for _, suffix := range []string{"_ENDPOINT", "_REGION", "_BUCKET", "_ACCESS_KEY_ID", "_SECRET_ACCESS_KEY", "_ADDRESSING_STYLE"} {
			req.Secrets = append(req.Secrets, state.AppSecret{AccountID: acct.ID, AppID: app.ID, Scope: "default", Key: prefix + suffix, Ciphertext: []byte("sealed-" + suffix), Kid: "age1test", ValueHash: "0123456789abcdef", ManagedObjectStorageCredentialID: id})
		}
		return req
	}

	if _, err := bindings.CreateObjectS3ComputeBinding(ctx, request(holder, "GREGALE_S3_ASSETS", false)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("non-owner without Borrowed = %v, want not found", err)
	}
	if _, err := bindings.CreateObjectS3ComputeBinding(ctx, request(owner, "GREGALE_S3_ASSETS", true)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("owner with Borrowed = %v, want not found", err)
	}
	stranger, err := base.CreateAccount(ctx, "s3-borrow-other-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreign := request(newApp(stranger.ID, "foreign"), "GREGALE_S3_ASSETS", true)
	if _, err := bindings.CreateObjectS3ComputeBinding(ctx, foreign); err == nil {
		t.Fatal("an app of another account borrowed the bucket")
	}

	own := request(owner, "GREGALE_S3_ASSETS", false)
	if _, err := bindings.CreateObjectS3ComputeBinding(ctx, own); err != nil {
		t.Fatalf("owner binding: %v", err)
	}
	borrowed := request(holder, "GREGALE_S3_ASSETS", true)
	created, err := bindings.CreateObjectS3ComputeBinding(ctx, borrowed)
	if err != nil || created.ManagedAppID != holder.ID {
		t.Fatalf("borrowed binding = %+v, %v", created, err)
	}
	if _, resolved, err := bindings.ResolveObjectS3Credential(ctx, borrowed.Credential.AccessKeyID); err != nil || resolved.ID != bucket.ID || resolved.AppID != owner.ID {
		t.Fatalf("resolve borrowed key = %+v, %v", resolved, err)
	}
	for _, secret := range borrowed.Secrets {
		if _, err := base.GetAppSecretInScope(ctx, acct.ID, holder.ID, "default", secret.Key); err != nil {
			t.Fatalf("holder secret %s: %v", secret.Key, err)
		}
	}

	holding, err := held.ObjectS3ComputeBindingsHeldBy(ctx, acct.ID, holder.ID)
	if err != nil || len(holding) != 1 || holding[0].Credential.ID != created.ID || holding[0].BucketAppID != owner.ID {
		t.Fatalf("held by holder = %+v, %v", holding, err)
	}
	owned, err := held.ObjectS3ComputeBindingsHeldBy(ctx, acct.ID, owner.ID)
	if err != nil || len(owned) != 1 || owned[0].BucketAppID != owner.ID {
		t.Fatalf("held by owner = %+v, %v", owned, err)
	}

	changed, err := bindings.RevokeObjectS3ComputeBinding(ctx, acct.ID, bucket.ID, created.ID)
	if err != nil || !changed {
		t.Fatalf("revoke borrowed = %v, %v", changed, err)
	}
	if holding, err := held.ObjectS3ComputeBindingsHeldBy(ctx, acct.ID, holder.ID); err != nil || len(holding) != 0 {
		t.Fatalf("held after revoke = %+v, %v", holding, err)
	}
	if _, err := base.GetAppSecretInScope(ctx, acct.ID, holder.ID, "default", "GREGALE_S3_ASSETS_ACCESS_KEY_ID"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("holder secret survived revoke: %v", err)
	}
	if owned, err := held.ObjectS3ComputeBindingsHeldBy(ctx, acct.ID, owner.ID); err != nil || len(owned) != 1 {
		t.Fatalf("owner binding after the borrowed one's revoke = %+v, %v", owned, err)
	}
}
