// adr: 583
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

type cloneBucketReservationTestStore interface {
	cloneBindingTestStore
	state.ProjectEnvironmentCloneWorkerLeaseStore
	state.ProjectEnvironmentCloneObjectBucketStore
	state.ObjectBucketReservationResultStore
	state.ObjectMultipartUploadStore
	state.ObjectUploadRouteStore
}

func TestMemCloneBucketReservationUsesFrozenCatalogueAndWorker(t *testing.T) {
	cloneBucketReservationContract(t, state.NewMemStore())
}

func cloneBucketReservationContract(t *testing.T, s cloneBucketReservationTestStore) {
	t.Helper()
	ctx := context.Background()
	account, project, app, op := cloneBindingFixture(t, s)
	source := cloneBindingBucket(t, s, account, app, "assets", "production", true)
	foreign := cloneBindingBucket(t, s, account, app, "unrelated", "other", true)
	lease, err := s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	op = lease.Operation
	if _, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, account.ID, project.ID, op.ID, op.Revision); err != nil {
		t.Fatal(err)
	}
	// Deleting source metadata cannot replace the captured placement or policy.
	if _, err := s.ClaimObjectBucket(ctx, account.ID, app.ID, source.ID, "delete-source", "deleting"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishObjectBucket(ctx, source.ID, "delete-source", "deleted"); err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"token", "revision", "status", "account", "project", "app", "source", "quota"} {
		bad, appID, sourceID, limit := lease, app.ID, source.ID, 10
		switch fault {
		case "token":
			bad.Token = uuid.NewString()
		case "revision":
			bad.Operation.Revision--
		case "status":
			bad.Operation.Status = state.CloneOperationPending
		case "account":
			bad.Operation.AccountID = uuid.NewString()
		case "project":
			bad.Operation.ProjectID = uuid.NewString()
		case "app":
			appID = uuid.NewString()
		case "source":
			sourceID = foreign.ID
		case "quota":
			limit = 1 // The unrelated bucket already consumes the quota.
		}
		if _, _, err := s.ReserveProjectEnvironmentCloneObjectBucket(ctx, bad, appID, sourceID, limit); !errors.Is(err, state.ErrConflict) && !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("accepted invalid %s reservation: %v", fault, err)
		}
		rows, err := s.ListObjectBuckets(ctx, account.ID, app.ID)
		if err != nil || len(rows) != 1 {
			t.Fatalf("failed %s reservation wrote targets: %+v, %v", fault, rows, err)
		}
	}
	target, created, err := s.ReserveProjectEnvironmentCloneObjectBucket(ctx, lease, app.ID, source.ID, 10)
	if err != nil || !created {
		t.Fatalf("reserve frozen bucket = %+v, %v, %v", target, created, err)
	}
	if target.ID == source.ID || target.PhysicalName == source.PhysicalName || target.Scope != op.TargetEnvironment ||
		target.EnvironmentCloneOperationID != op.ID || target.EnvironmentCloneSourceBucketID != source.ID ||
		target.BackendID != source.BackendID || target.BackendFingerprint != source.BackendFingerprint || target.Region != source.Region ||
		target.Name != state.ProjectEnvironmentCloneObjectBucketName(op, app.ID, source.ID) || target.PublicRead || target.ServeAt != "" {
		t.Fatalf("target does not match private captured placement: %+v", target)
	}
	// The desired public configuration remains captured while the copy is private.
	views, err := s.ProjectEnvironmentCloneBindings(ctx, account.ID, project.ID, op.ID)
	if err != nil || len(views) != 1 || len(views[0].Buckets) != 1 || !views[0].Buckets[0].PublicRead || views[0].Buckets[0].ServeAt != source.ServeAt {
		t.Fatalf("lost captured public configuration: %+v, %v", views, err)
	}
	if _, err := s.ClaimObjectBucket(ctx, account.ID, app.ID, target.ID, "create-target", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishObjectBucket(ctx, target.ID, "create-target", "ready"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.GetObjectBucket(ctx, account.ID, app.ID, target.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("customer accessed unfinished copy: %v", err)
	}
	if bucket, err := s.ProjectEnvironmentCloneObjectBucketForLease(ctx, lease, app.ID, source.ID, target.ID); err != nil || bucket.ID != target.ID || bucket.State != "ready" {
		t.Fatalf("worker lost private target: %+v, %v", bucket, err)
	}
	rows, err := s.ListObjectBuckets(ctx, account.ID, app.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("customer listed unfinished copy: %+v, %v", rows, err)
	}
	credential := state.ObjectS3Credential{ID: uuid.NewString(), AccountID: account.ID, BucketID: target.ID,
		AccessKeyID: "GRGAAAAAAAAAAAAAAAAA", SecretSealed: []byte("target-signing-material"), KID: "target-kid", Label: "premature", Permission: "read", Status: "active"}
	if _, err := s.CreateObjectS3Credential(ctx, credential, 10); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("issued credential before clone publication: %v", err)
	}
	_, keyHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.CreateAPIKey(ctx, account.ID, keyHash, "premature", []string{"storage:read"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetObjectBucketAccessGrant(ctx, account.ID, target.ID, key.ID, "read"); !errors.Is(err, state.ErrNotFound) && !errors.Is(err, state.ErrConflict) {
		t.Fatalf("granted access before clone publication: %v", err)
	}
	if err := s.ReleaseProjectEnvironmentCloneLease(ctx, lease, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReserveProjectEnvironmentCloneObjectBucket(ctx, lease, app.ID, source.ID, 10); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("released worker reserved bucket: %v", err)
	}
	replacement, err := s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	adopted, created, err := s.ReserveProjectEnvironmentCloneObjectBucket(ctx, replacement, app.ID, source.ID, 1)
	if err != nil || created || adopted.ID != target.ID || adopted.PhysicalName != target.PhysicalName || adopted.State != "ready" {
		t.Fatalf("takeover replaced completed reservation: %+v, %v, %v", adopted, created, err)
	}

	if _, err := s.UpsertObjectUploadRoute(ctx, state.ObjectUploadRoute{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID,
		Name: "premature", BucketID: target.ID, MaxBytes: 100, Enabled: true}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("created upload route before publication: %v", err)
	}
	if _, err := s.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID,
		BucketID: target.ID, Key: "premature", ExpiresAt: time.Now().UTC().Add(time.Hour)}, 10); !errors.Is(err, state.ErrNotFound) && !errors.Is(err, state.ErrConflict) {
		t.Fatalf("created multipart upload before publication: %v", err)
	}
	forged := target
	forged.ID, forged.Name = uuid.NewString(), "forged"
	if _, _, err := s.ReserveObjectBucketWithResult(ctx, forged, 10); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("generic writer forged operation ownership: %v", err)
	}
	// A new operation gets a distinct name even when its source and scope match.
	other := op
	other.ID = uuid.NewString()
	if state.ProjectEnvironmentCloneObjectBucketName(other, app.ID, source.ID) == target.Name {
		t.Fatal("reservation name omits operation identity")
	}
}

func TestMemCloneBucketReservationRejectsForeignTarget(t *testing.T) {
	cloneBucketForeignTargetContract(t, state.NewMemStore())
}

func cloneBucketForeignTargetContract(t *testing.T, s cloneBucketReservationTestStore) {
	t.Helper()
	ctx := context.Background()
	account, project, app, op := cloneBindingFixture(t, s)
	source := cloneBindingBucket(t, s, account, app, "assets", "production", true)
	lease, err := s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	op = lease.Operation
	if _, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, account.ID, project.ID, op.ID, op.Revision); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	foreign, err := s.ReserveObjectBucket(ctx, state.ObjectBucket{ID: id, AccountID: account.ID, AppID: app.ID,
		Name: state.ProjectEnvironmentCloneObjectBucketName(op, app.ID, source.ID), Scope: op.TargetEnvironment,
		Region: source.Region, BackendID: source.BackendID, BackendFingerprint: source.BackendFingerprint,
		PhysicalName: "gregale-" + strings.ReplaceAll(id, "-", ""), EnvironmentCloneSourceBucketID: source.ID}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReserveProjectEnvironmentCloneObjectBucket(ctx, lease, app.ID, source.ID, 10); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("adopted unowned target: %v", err)
	}
	rows, err := s.ListObjectBuckets(ctx, account.ID, app.ID)
	if err != nil || len(rows) != 2 || foreign.EnvironmentCloneOperationID != "" {
		t.Fatalf("foreign collision changed catalogue: %+v, %v", rows, err)
	}
}
