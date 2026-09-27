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

func TestObjectS3CredentialStoreMem(t *testing.T) {
	objectS3CredentialStoreSuite(t, state.NewMemStore())
}

func TestObjectS3CredentialStorePG(t *testing.T) {
	st, _ := pgStore(t)
	objectS3CredentialStoreSuite(t, st)
}

func TestObjectS3CredentialRotationMem(t *testing.T) {
	objectS3CredentialRotationSuite(t, state.NewMemStore())
}

func TestObjectS3CredentialRotationPG(t *testing.T) {
	st, _ := pgStore(t)
	objectS3CredentialRotationSuite(t, st)
}

func objectS3CredentialRotationSuite(t *testing.T, base state.Store) {
	t.Helper()
	ctx := context.Background()
	buckets := base.(state.ObjectBucketStore)
	bindings := base.(state.ObjectS3CredentialBindingStore)
	acct, err := base.CreateAccount(ctx, "s3-rotation-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := base.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "s3-rotation-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	bucketID := uuid.NewString()
	bucket, err := buckets.ReserveObjectBucket(ctx, state.ObjectBucket{ID: bucketID, AccountID: acct.ID, AppID: app.ID, Name: "assets", Scope: "default", Region: "us-east-1", BackendID: "provider", BackendFingerprint: strings.Repeat("a", 64), PhysicalName: "gregale-" + strings.ReplaceAll(bucketID, "-", "")}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = buckets.ClaimObjectBucket(ctx, acct.ID, app.ID, bucket.ID, "provision", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = buckets.FinishObjectBucket(ctx, bucket.ID, "provision", "ready"); err != nil {
		t.Fatal(err)
	}
	key := func(ch string) string { return "GRGA" + strings.Repeat(ch, 16) }
	parent, err := bindings.CreateObjectS3Credential(ctx, state.ObjectS3Credential{ID: uuid.NewString(), AccountID: acct.ID, BucketID: bucket.ID, AccessKeyID: key("A"), SecretSealed: []byte("old-seal"), KID: "age1old", Label: "compute", Permission: state.ObjectBucketPermissionReadWrite, Status: state.ObjectS3CredentialStatusActive, ManagedAppID: app.ID, ManagedScope: "default", ManagedPrefix: "GREGALE_S3_ASSETS"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{"GREGALE_S3_ASSETS_ACCESS_KEY_ID", "GREGALE_S3_ASSETS_SECRET_ACCESS_KEY"}
	for _, name := range keys {
		if err := base.PutManagedObjectStorageSecret(ctx, state.AppSecret{AccountID: acct.ID, AppID: app.ID, Scope: "default", Key: name, Ciphertext: []byte("old-" + name), Kid: "age1old", ValueHash: "oldhash", ManagedObjectStorageCredentialID: parent.ID}); err != nil {
			t.Fatal(err)
		}
	}
	makeRequest := func(ch string) state.ObjectS3CredentialRotationRequest {
		return state.ObjectS3CredentialRotationRequest{AccountID: acct.ID, BucketID: bucket.ID, BindingID: parent.ID, WakeID: uuid.NewString(), AccessKeyID: key(ch), SecretSealed: []byte("new-seal"), KID: "age1new", Secrets: []state.AppSecret{
			{AccountID: acct.ID, AppID: app.ID, Scope: "default", Key: keys[0], Ciphertext: []byte("new-access"), Kid: "age1new", ValueHash: "newhash", ManagedObjectStorageCredentialID: parent.ID},
			{AccountID: acct.ID, AppID: app.ID, Scope: "default", Key: keys[1], Ciphertext: []byte("new-secret"), Kid: "age1new", ValueHash: "newhash", ManagedObjectStorageCredentialID: parent.ID},
		}}
	}
	bad := makeRequest("B")
	bad.Secrets[1].Key = "MISSING_SECRET_ACCESS_KEY"
	if _, err := bindings.StageObjectS3CredentialRotation(ctx, bad); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid rotation = %v", err)
	}
	// A missing second row must roll back the first secret and credential update.
	if err := base.DeleteManagedObjectStorageSecrets(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	if err := base.PutManagedObjectStorageSecret(ctx, state.AppSecret{AccountID: acct.ID, AppID: app.ID, Scope: "default", Key: keys[0], Ciphertext: []byte("old-access"), Kid: "age1old", ValueHash: "oldhash", ManagedObjectStorageCredentialID: parent.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := bindings.StageObjectS3CredentialRotation(ctx, makeRequest("B")); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("missing secret rotation = %v", err)
	}
	if _, _, err := bindings.ResolveObjectS3Credential(ctx, parent.AccessKeyID); err != nil {
		t.Fatalf("old key after failed rotation: %v", err)
	}
	if _, _, err := bindings.ResolveObjectS3Credential(ctx, key("B")); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("new key after failed rotation: %v", err)
	}
	secret, err := base.GetAppSecretInScope(ctx, acct.ID, app.ID, "default", keys[0])
	if err != nil || string(secret.Ciphertext) != "old-access" {
		t.Fatalf("first secret after rollback = %+v, %v", secret, err)
	}
	if err := base.PutManagedObjectStorageSecret(ctx, state.AppSecret{AccountID: acct.ID, AppID: app.ID, Scope: "default", Key: keys[1], Ciphertext: []byte("old-secret"), Kid: "age1old", ValueHash: "oldhash", ManagedObjectStorageCredentialID: parent.ID}); err != nil {
		t.Fatal(err)
	}
	req := makeRequest("B")
	previousStamp, previouslyStamped, err := base.AppRuntimeConfigChangedAt(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := bindings.StageObjectS3CredentialRotation(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	beforeStamp, stampedBeforeExplicitStamp, err := base.AppRuntimeConfigChangedAt(ctx, app.ID)
	if err != nil || stampedBeforeExplicitStamp != previouslyStamped || (previouslyStamped && !beforeStamp.Equal(previousStamp)) {
		t.Fatalf("credential mutation moved the runtime stamp before explicit stamping: %v, %v, %v", beforeStamp, stampedBeforeExplicitStamp, err)
	}
	if err := bindings.StampObjectS3CredentialRotation(ctx, app.ID, req.WakeID); err != nil {
		t.Fatal(err)
	}
	stampedAt, stamped, err := base.AppRuntimeConfigChangedAt(ctx, app.ID)
	if err != nil || !stamped {
		t.Fatalf("rotation stamp = %v, %v, %v", stampedAt, stamped, err)
	}
	if err := bindings.StampObjectS3CredentialRotation(ctx, app.ID, req.WakeID); err != nil {
		t.Fatal(err)
	}
	retriedStamp, _, err := base.AppRuntimeConfigChangedAt(ctx, app.ID)
	if err != nil || !retriedStamp.Equal(stampedAt) {
		t.Fatalf("retry moved rotation stamp from %v to %v: %v", stampedAt, retriedStamp, err)
	}
	if rotated.ID != parent.ID || rotated.AccessKeyID != req.AccessKeyID {
		t.Fatalf("rotated binding = %+v", rotated)
	}
	for _, access := range []string{parent.AccessKeyID, req.AccessKeyID} {
		if _, _, err := bindings.ResolveObjectS3Credential(ctx, access); err != nil {
			t.Fatalf("key %s during overlap: %v", access, err)
		}
	}
	listed, err := bindings.ListObjectS3Credentials(ctx, acct.ID, bucket.ID)
	if err != nil || len(listed) != 1 {
		t.Fatalf("inventory during overlap = %d, %v", len(listed), err)
	}
	pending, err := bindings.PendingObjectS3CredentialRotation(ctx, acct.ID, bucket.ID, parent.ID)
	if err != nil || pending != req.WakeID {
		t.Fatalf("pending = %q, %v", pending, err)
	}
	if _, err := bindings.StageObjectS3CredentialRotation(ctx, makeRequest("C")); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("overlapping rotation = %v", err)
	}
	if err := bindings.FinalizeObjectS3CredentialRotationsForApp(ctx, app.ID, req.WakeID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := bindings.ResolveObjectS3Credential(ctx, parent.AccessKeyID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("old key after finalization: %v", err)
	}
	pending, err = bindings.PendingObjectS3CredentialRotation(ctx, acct.ID, bucket.ID, parent.ID)
	if err != nil || pending != "" {
		t.Fatalf("pending after finalization = %q, %v", pending, err)
	}
	second, err := bindings.StageObjectS3CredentialRotation(ctx, makeRequest("C"))
	if err != nil {
		t.Fatal(err)
	}
	if err := bindings.RevokeObjectS3Credential(ctx, acct.ID, bucket.ID, parent.ID); err != nil {
		t.Fatal(err)
	}
	for _, access := range []string{req.AccessKeyID, second.AccessKeyID} {
		if _, _, err := bindings.ResolveObjectS3Credential(ctx, access); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("key %s after revocation: %v", access, err)
		}
	}
}

func objectS3CredentialStoreSuite(t *testing.T, base state.Store) {
	t.Helper()
	ctx := context.Background()
	buckets := base.(state.ObjectBucketStore)
	credentials := base.(state.ObjectS3CredentialStore)
	rekeyStore := base.(state.ObjectS3CredentialRekeyStore)
	acct, err := base.CreateAccount(ctx, "s3-credentials-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := base.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "s3-credentials-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	bucketID := uuid.NewString()
	bucket, err := buckets.ReserveObjectBucket(ctx, state.ObjectBucket{
		ID: bucketID, AccountID: acct.ID, AppID: app.ID, Name: "assets", Scope: "default", Region: "us-east-1",
		BackendID: "provider", BackendFingerprint: strings.Repeat("a", 64), PhysicalName: "gregale-" + strings.ReplaceAll(bucketID, "-", ""),
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = buckets.ClaimObjectBucket(ctx, acct.ID, app.ID, bucket.ID, "provision", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = buckets.FinishObjectBucket(ctx, bucket.ID, "provision", "ready"); err != nil {
		t.Fatal(err)
	}

	newCredential := func(access string) state.ObjectS3Credential {
		return state.ObjectS3Credential{
			ID: uuid.NewString(), AccountID: acct.ID, BucketID: bucket.ID, AccessKeyID: access,
			SecretSealed: []byte("sealed"), KID: "age1test", Label: "deployment", Permission: state.ObjectBucketPermissionReadWrite,
			Status: state.ObjectS3CredentialStatusActive,
		}
	}
	first, err := credentials.CreateObjectS3Credential(ctx, newCredential("GRGAAAAAAAAAAAAAAAAA"), 1)
	if err != nil || first.CreatedAt.IsZero() {
		t.Fatal(first, err)
	}
	if _, err = credentials.CreateObjectS3Credential(ctx, newCredential("GRGABBBBBBBBBBBBBBBB"), 1); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("credential cap error = %v, want conflict", err)
	}
	resolved, resolvedBucket, err := credentials.ResolveObjectS3Credential(ctx, first.AccessKeyID)
	if err != nil || resolved.ID != first.ID || resolvedBucket.ID != bucket.ID {
		t.Fatal(resolved, resolvedBucket, err)
	}
	usedAt := time.Now().UTC().Add(-time.Second)
	if err = credentials.TouchObjectS3Credential(ctx, first.ID, usedAt); err != nil {
		t.Fatal(err)
	}
	rekeyRows, err := rekeyStore.ListObjectS3CredentialsForRekey(ctx, 10, "")
	if err != nil || len(rekeyRows) != 1 || rekeyRows[0].ID != first.ID {
		t.Fatal(rekeyRows, err)
	}
	if err = rekeyStore.ResealObjectS3Credential(ctx, first.ID, first.KID, "age1current", []byte("resealed")); err != nil {
		t.Fatal(err)
	}
	resolved, _, err = credentials.ResolveObjectS3Credential(ctx, first.AccessKeyID)
	if err != nil || resolved.KID != "age1current" || string(resolved.SecretSealed) != "resealed" {
		t.Fatal(resolved, err)
	}
	listed, err := credentials.ListObjectS3Credentials(ctx, acct.ID, bucket.ID)
	if err != nil || len(listed) != 1 || listed[0].LastUsedAt == nil {
		t.Fatal(listed, err)
	}
	if err = credentials.RevokeObjectS3Credential(ctx, acct.ID, bucket.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = credentials.ResolveObjectS3Credential(ctx, first.AccessKeyID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("revoked credential resolved: %v", err)
	}
	listed, err = credentials.ListObjectS3Credentials(ctx, acct.ID, bucket.ID)
	if err != nil || len(listed) != 0 {
		t.Fatal(listed, err)
	}
}
