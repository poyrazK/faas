package state_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestObjectS3CredentialCreateLocksAccountBeforeBucket: inventory
// publication (FinishObjectInventory) locks the account's resource
// namespace FOR UPDATE and then inserts a sample whose bucket foreign key
// needs the bucket. Credential creation locked the bucket FOR UPDATE first
// and then waited on the account through its own foreign key, so the two
// deadlocked and apid answered 503 object_storage_unavailable (full CI run
// 39455888148). Credential creation must take the account first.
func TestObjectS3CredentialCreateLocksAccountBeforeBucket(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	acct, err := st.CreateAccount(ctx, "s3-lock-order-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "s3-lock-order-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	bucketID := uuid.NewString()
	bucket, err := st.ReserveObjectBucket(ctx, state.ObjectBucket{
		ID: bucketID, AccountID: acct.ID, AppID: app.ID, Name: "assets", Scope: "default", Region: "us-east-1",
		BackendID: "provider", BackendFingerprint: strings.Repeat("a", 64), PhysicalName: "gregale-" + strings.ReplaceAll(bucketID, "-", ""),
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimObjectBucket(ctx, acct.ID, app.ID, bucket.ID, "provision", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectBucket(ctx, bucket.ID, "provision", "ready"); err != nil {
		t.Fatal(err)
	}

	// Hold the account the way inventory publication does.
	sampler, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sampler.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = sampler.Exec(ctx, `SELECT id FROM resource_namespaces WHERE id=$1 FOR UPDATE`, acct.ID); err != nil {
		t.Fatal(err)
	}
	created := make(chan error, 1)
	go func() {
		_, createErr := st.CreateObjectS3Credential(ctx, state.ObjectS3Credential{
			ID: uuid.NewString(), AccountID: acct.ID, BucketID: bucket.ID, AccessKeyID: "GRGALOCKORDERTEST234",
			SecretSealed: []byte("sealed"), KID: "age1test", Label: "deployment", Permission: state.ObjectBucketPermissionReadWrite,
			Status: state.ObjectS3CredentialStatusActive,
		}, 5)
		created <- createErr
	}()
	// Wait until credential creation is blocked behind the held account.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err = pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		select {
		case createErr := <-created:
			t.Fatalf("credential creation did not wait for the held account: %v", createErr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("credential creation never waited on a lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The inventory sample's bucket foreign key takes this lock. It must not
	// be held by the waiting credential transaction.
	lockCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err = sampler.Exec(lockCtx, `SELECT id FROM object_buckets WHERE id=$1 FOR KEY SHARE`, bucket.ID); err != nil {
		t.Fatalf("inventory publication could not reach the bucket while credential creation waited: %v", err)
	}
	if err = sampler.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-created:
		if err != nil {
			t.Fatalf("credential creation after the sampler committed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("credential creation did not finish after the sampler committed")
	}
}
