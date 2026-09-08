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
