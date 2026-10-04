// adr: 532 — inventory retains missing-consumer evidence until the queue owner repairs it.
package state_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBindingInventoryStoreMem(t *testing.T) { bindingInventoryStoreSuite(t, state.NewMemStore()) }

func TestBindingInventoryStorePG(t *testing.T) {
	store, _ := pgStore(t)
	bindingInventoryStoreSuite(t, store)
}

func bindingInventoryStoreSuite(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@inventory.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "inventory-" + uuid.NewString()[:8], Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	otherApp, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "other-inventory-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	var productionBinding state.ObjectS3Credential
	for _, scope := range []string{"production", "staging"} {
		binding := seedInventoryStorageBinding(t, store, account.ID, app.ID, scope)
		if scope == "production" {
			productionBinding = binding
		}
	}
	seedInventoryStorageBinding(t, store, account.ID, otherApp.ID, "production")
	storage := store.(state.ObjectStorageBindingInventoryStore)
	for _, tc := range []struct {
		accountID, appID, scope string
		count                   int
	}{
		{account.ID, app.ID, "", 2}, {account.ID, app.ID, "production", 1},
		{account.ID, app.ID, "missing", 0}, {uuid.NewString(), app.ID, "", 0},
		{account.ID, otherApp.ID, "", 1},
	} {
		items, err := storage.ListObjectStorageBindingsForApp(ctx, tc.accountID, tc.appID, tc.scope)
		if err != nil || len(items) != tc.count {
			t.Fatalf("storage scope=%q count=%d items=%+v err=%v", tc.scope, tc.count, items, err)
		}
		for _, item := range items {
			if item.State != "active" || item.Prefix != "GREGALE_S3_ASSETS" || item.RotationPending || item.BindingID == "" {
				t.Fatalf("storage metadata=%+v", item)
			}
		}
	}
	rotation := state.ObjectS3CredentialRotationRequest{AccountID: account.ID, BucketID: productionBinding.BucketID, BindingID: productionBinding.ID,
		WakeID: uuid.NewString(), AccessKeyID: "GRGABBBBBBBBBBBBBBBB", KID: "age1new", SecretSealed: []byte("sealed-new")}
	for _, suffix := range []string{"_ACCESS_KEY_ID", "_SECRET_ACCESS_KEY"} {
		rotation.Secrets = append(rotation.Secrets, state.AppSecret{AccountID: account.ID, AppID: app.ID, Scope: "production", Key: "GREGALE_S3_ASSETS" + suffix,
			Ciphertext: []byte("sealed-new"), Kid: "age1new", ValueHash: "fedcba9876543210", ManagedObjectStorageCredentialID: productionBinding.ID})
	}
	rotations := store.(state.ObjectS3CredentialRotationStore)
	if _, err := rotations.StageObjectS3CredentialRotation(ctx, rotation); err != nil {
		t.Fatal(err)
	}
	rotated, err := storage.ListObjectStorageBindingsForApp(ctx, account.ID, app.ID, "production")
	if err != nil || len(rotated) != 1 || rotated[0].RotationRevisionID == "" || !rotated[0].RotationPending {
		t.Fatalf("rotation projection=%+v err=%v", rotated, err)
	}
	if err := rotations.StampObjectS3CredentialRotation(ctx, app.ID, rotation.WakeID); err != nil {
		t.Fatal(err)
	}
	if err := rotations.FinalizeObjectS3CredentialRotationsForApp(ctx, app.ID, rotation.WakeID); err != nil {
		t.Fatal(err)
	}
	finalized, err := storage.ListObjectStorageBindingsForApp(ctx, account.ID, app.ID, "production")
	if err != nil || len(finalized) != 1 || finalized[0].RotationRevisionID != rotated[0].RotationRevisionID || finalized[0].RotationPending {
		t.Fatalf("finalization changed revision=%+v err=%v", finalized, err)
	}
	queue, err := store.CreateQueueBinding(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "orders", QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	consumers := store.(state.QueueBindingConsumerInventoryStore)
	items, err := consumers.ListQueueBindingConsumersForApp(ctx, account.ID, app.ID)
	if err != nil || len(items) != 1 || items[0].BindingID != queue.ID || items[0].ConsumerEnabled != nil || items[0].LastPollAt != nil {
		t.Fatalf("missing consumer=%+v err=%v", items, err)
	}
	result, err := store.(state.QueueBindingConsumerStore).UpdateQueueBindingWithConsumer(ctx, account.ID, app.ID, queue.ID, state.UpdateQueueBindingParams{})
	if err != nil {
		t.Fatalf("repair missing queue consumer: %v", err)
	}
	if len(result.Changes) != 1 || result.Changes[0].Kind != "created" {
		t.Fatalf("missing consumer repair=%+v", result)
	}
	polled := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.(state.TriggerConsumerHealthStore).RecordTriggerConsumerHealth(ctx, result.Changes[0].TriggerID, state.TriggerConsumerHealthObservation{LastPollAt: polled, Error: "PRIVATE_ERROR"}); err != nil {
		t.Fatal(err)
	}
	items, err = consumers.ListQueueBindingConsumersForApp(ctx, account.ID, app.ID)
	if err != nil || len(items) != 1 || items[0].ConsumerEnabled == nil || !*items[0].ConsumerEnabled || items[0].LastPollAt == nil || !items[0].LastPollAt.Equal(polled) || items[0].LastErrorAt == nil {
		t.Fatalf("observed consumer=%+v err=%v", items, err)
	}
	foreign, err := consumers.ListQueueBindingConsumersForApp(ctx, uuid.NewString(), app.ID)
	if err != nil || len(foreign) != 0 {
		t.Fatalf("cross-account consumers=%+v err=%v", foreign, err)
	}
}

func seedInventoryStorageBinding(t *testing.T, store state.Store, accountID, appID, scope string) state.ObjectS3Credential {
	t.Helper()
	ctx := context.Background()
	buckets := store.(state.ObjectBucketStore)
	bucketID := uuid.NewString()
	bucket, err := buckets.ReserveObjectBucket(ctx, state.ObjectBucket{
		ID: bucketID, AccountID: accountID, AppID: appID, Name: "assets", Scope: scope, Region: "eu",
		BackendID: "test", BackendFingerprint: strings.Repeat("a", 64), PhysicalName: "inventory-" + strings.ReplaceAll(bucketID, "-", ""),
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buckets.ClaimObjectBucket(ctx, accountID, appID, bucket.ID, "provision", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := buckets.FinishObjectBucket(ctx, bucket.ID, "provision", "ready"); err != nil {
		t.Fatal(err)
	}
	access, secret, err := api.GenerateObjectS3Credential()
	if err != nil {
		t.Fatal(err)
	}
	credentialID := uuid.NewString()
	request := state.ObjectS3ComputeBindingCreateRequest{Credential: state.ObjectS3Credential{
		ID: credentialID, AccountID: accountID, BucketID: bucket.ID, AccessKeyID: access, SecretSealed: []byte(secret),
		KID: "age1test", Label: "compute", Permission: "read", Status: "active",
		ManagedAppID: appID, ManagedScope: scope, ManagedPrefix: "GREGALE_S3_ASSETS",
	}, MaxCredentialsPerBucket: 10, MaxSecretsPerApp: 30}
	for _, suffix := range []string{"_ENDPOINT", "_REGION", "_BUCKET", "_ACCESS_KEY_ID", "_SECRET_ACCESS_KEY", "_ADDRESSING_STYLE"} {
		request.Secrets = append(request.Secrets, state.AppSecret{AccountID: accountID, AppID: appID, Scope: scope,
			Key: "GREGALE_S3_ASSETS" + suffix, Ciphertext: []byte(fmt.Sprintf("sealed-%s", suffix)), Kid: "age1test",
			ValueHash: "0123456789abcdef", ManagedObjectStorageCredentialID: credentialID,
		})
	}
	created, err := store.(state.ObjectS3CredentialBindingStore).CreateObjectS3ComputeBinding(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	return created
}
