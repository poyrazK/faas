package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func setS3Flag(t *testing.T, e testEnv, enabled bool) {
	t.Helper()
	_, err := e.store.UpsertRuntimeConfig(context.Background(), state.RuntimeConfigUpdate{Key: runtimeConfigS3, Scope: state.RuntimeConfigScopeGlobal, DesiredValue: boolJSON(enabled), ApplyMode: state.RuntimeConfigApplyHot, Reason: "storage test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.runtimeConfig.reconcile(context.Background(), e.store); err != nil {
		t.Fatal(err)
	}
}

func TestObjectStorageHotFlag(t *testing.T) {
	e := setup(t, api.PlanHobby)
	createApp(t, e, "hot-storage")
	a, b := &fakeObjectProvider{}, &fakeObjectProvider{}
	e.s.WithObjectStorage(objectRegistry(t, a, b, "external"))
	path := "/v1/apps/hot-storage/buckets"
	if r := e.do(t, "POST", path, map[string]any{"name": "assets"}, nil); r.Code != 503 {
		t.Fatal("default not off", r.Code)
	}
	setS3Flag(t, e, true)
	first := bucketResponse(t, e.do(t, "POST", path, map[string]any{"name": "assets"}, nil), 201)
	setS3Flag(t, e, false)
	var listing api.ObjectBucketList
	if err := json.Unmarshal(e.do(t, "GET", path, nil, nil).Body.Bytes(), &listing); err != nil || listing.Enabled || len(listing.Items) != 1 {
		t.Fatal(listing, err)
	}
	for _, method := range []string{"GET", "PUT"} {
		body := map[string]any{"method": method, "key": "file"}
		if method == "PUT" {
			body["size_bytes"] = 1
		}
		if r := e.do(t, "POST", path+"/"+first.ID+"/signed-url", body, nil); r.Code != 503 {
			t.Fatal("disabled signing", r.Code)
		}
	}
	if r := e.do(t, "GET", path+"/"+first.ID+"/objects", nil, nil); r.Code != 200 {
		t.Fatal("cleanup listing blocked", r.Code)
	}
	if r := e.do(t, "DELETE", path+"/"+first.ID+"/objects?key=file", nil, nil); r.Code != 204 {
		t.Fatal("object cleanup blocked", r.Code)
	}
	if r := e.do(t, "DELETE", path+"/"+first.ID, nil, nil); r.Code != 204 {
		t.Fatal("bucket cleanup blocked", r.Code)
	}
	setS3Flag(t, e, true)
	if r := e.do(t, "POST", path, map[string]any{"name": "again"}, nil); r.Code != 201 {
		t.Fatal("hot reenable failed", r.Code)
	}
	// A persisted true flag alone must never construct a provider or enable access.
	e.s.WithObjectStorage(nil)
	if e.s.objectStorageEnabled() {
		t.Fatal("enabled without registry")
	}
}

func reserveRecoveryBucket(t *testing.T, e testEnv) state.ObjectBucket {
	t.Helper()
	createApp(t, e, "recover-storage")
	app, err := e.store.AppBySlug(context.Background(), "recover-storage")
	if err != nil {
		t.Fatal(err)
	}
	backend, err := e.s.objectStorage.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.store.ReserveObjectBucket(context.Background(), state.ObjectBucket{ID: uuid.NewString(), AccountID: e.acct.ID, AppID: app.ID, Name: "assets", Scope: "default", Region: "us-east-1", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, PhysicalName: "gregale-" + uuid.NewString()}, 10)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestObjectStorageRecoveryAndDisabledCleanup(t *testing.T) {
	e := setup(t, api.PlanHobby)
	a, b := &fakeObjectProvider{}, &fakeObjectProvider{}
	e.s.WithObjectStorage(objectRegistry(t, a, b, "external"))
	row := reserveRecoveryBucket(t, e)
	ctx := context.Background()
	if err := e.s.reconcileObjectBuckets(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if len(a.created) != 0 {
		t.Fatal("provisioned while disabled")
	}
	setS3Flag(t, e, true)
	if err := e.s.reconcileObjectBuckets(ctx, nil); err != nil {
		t.Fatal(err)
	}
	got, err := e.store.GetObjectBucket(ctx, row.AccountID, row.AppID, row.ID)
	if err != nil || got.State != "ready" || len(a.created) != 1 || a.created[0] != row.PhysicalName {
		t.Fatal(got, err, a.created)
	}
	// Persist a deletion intent as though the process exited before upstream I/O.
	if _, err := e.store.ClaimObjectBucket(ctx, row.AccountID, row.AppID, row.ID, "delete", "deleting"); err != nil {
		t.Fatal(err)
	}
	if err := e.store.FinishObjectBucket(ctx, row.ID, "delete", "deleting"); err != nil {
		t.Fatal(err)
	}
	setS3Flag(t, e, false)
	if err := e.s.reconcileObjectBuckets(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.GetObjectBucket(ctx, row.AccountID, row.AppID, row.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("deletion not recovered", err)
	}
	if len(a.accessed) != 1 || a.accessed[0] != row.PhysicalName {
		t.Fatal("wrong cleanup target", a.accessed)
	}
}

func TestObjectStorageRecoveryPersistsCooldown(t *testing.T) {
	for _, cause := range []error{objectstorage.ErrUnavailable, objectstorage.ErrConfiguration, objectstorage.ErrInvalid} {
		t.Run(cause.Error(), func(t *testing.T) {
			e := setup(t, api.PlanHobby)
			a, b := &fakeObjectProvider{createErr: cause}, &fakeObjectProvider{}
			e.s.WithObjectStorage(objectRegistry(t, a, b, "external"))
			setS3Flag(t, e, true)
			row := reserveRecoveryBucket(t, e)
			ctx := context.Background()
			if err := e.s.reconcileObjectBuckets(ctx, nil); err != nil {
				t.Fatal(err)
			}
			got, err := e.store.GetObjectBucket(ctx, row.AccountID, row.AppID, row.ID)
			code, delay := objectRetryPolicy(cause, 1)
			if err != nil || got.AttemptCount != 1 || got.LastErrorCode != code || time.Until(got.RetryAt) < delay-time.Second || got.LeaseToken != "" {
				t.Fatal(got, err)
			}
			if err := e.s.reconcileObjectBuckets(ctx, nil); err != nil {
				t.Fatal(err)
			}
			if len(a.created) != 1 {
				t.Fatal("cooldown ignored")
			}
		})
	}
}

func TestObjectStorageMultipartRecoveryAbortsExpiredUploadWhileDisabled(t *testing.T) {
	e := setup(t, api.PlanHobby)
	provider := &fakeObjectProvider{}
	e.s.WithObjectStorage(objectRegistry(t, provider, &fakeObjectProvider{}, "external"))
	setS3Flag(t, e, true)
	bucket := reserveRecoveryBucket(t, e)
	ctx := context.Background()
	if err := e.s.reconcileObjectBuckets(ctx, nil); err != nil {
		t.Fatal(err)
	}

	uploads := e.s.store.(state.ObjectMultipartUploadStore)
	upload, err := uploads.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{
		ID: uuid.NewString(), AccountID: bucket.AccountID, AppID: bucket.AppID, BucketID: bucket.ID,
		Key: "expired.bin", SizeBytes: 10, PartSizeBytes: api.DefaultMultipartPartBytes, PartCount: 1,
		ExpiresAt: time.Now().Add(-time.Minute),
	}, api.MaxActiveMultipartUploadsPerBucket)
	if err != nil {
		t.Fatal(err)
	}
	upload, err = uploads.ClaimObjectMultipartUpload(ctx, bucket.AccountID, bucket.AppID, bucket.ID, upload.ID, "init", state.ObjectMultipartInitiating, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = uploads.ActivateObjectMultipartUpload(ctx, upload.ID, upload.LeaseToken, "provider-expired"); err != nil {
		t.Fatal(err)
	}

	setS3Flag(t, e, false)
	if err = e.s.reconcileObjectMultipartUploads(ctx, nil); err != nil {
		t.Fatal(err)
	}
	got, err := uploads.GetObjectMultipartUpload(ctx, bucket.AccountID, bucket.AppID, bucket.ID, upload.ID)
	if err != nil || got.State != state.ObjectMultipartAborted {
		t.Fatal(got, err)
	}
	if len(provider.multipartAborted) != 1 || provider.multipartAborted[0] != "provider-expired" {
		t.Fatal("expired provider upload was not aborted", provider.multipartAborted)
	}
}

func TestObjectStorageRetryPolicy(t *testing.T) {
	for _, tt := range []struct {
		err     error
		attempt int32
		code    string
		delay   time.Duration
	}{
		{objectstorage.ErrUnavailable, 1, "temporary", 30 * time.Second},
		{objectstorage.ErrUnavailable, 2, "temporary", time.Minute},
		{objectstorage.ErrConflict, 30, "conflict", 15 * time.Minute},
		{objectstorage.ErrConfiguration, 1, "configuration", time.Hour},
		{objectstorage.ErrInvalid, 1, "invalid", time.Hour},
	} {
		code, delay := objectRetryPolicy(tt.err, tt.attempt)
		if code != tt.code || delay != tt.delay {
			t.Fatal(code, delay, tt)
		}
	}
}

func TestObjectStorageRuntimeConfigAdminAndReplica(t *testing.T) {
	e := newObsEnv(t, []string{"admin"}, "ops@faas.dev", "ops@faas.dev")
	for _, enabled := range []bool{true, false} {
		r := e.doAdmin(t, "PATCH", "/v1/admin/config/s3_enabled", map[string]any{"value": enabled, "reason": "storage rollout"}, nil)
		if r.Code != 200 {
			t.Fatal(r.Code, r.Body.String())
		}
		// A different process snapshot repairs from durable state, not the PATCH request.
		replica := newRuntimeConfigManager(func(string) string { return "" })
		if err := replica.reconcile(context.Background(), e.store); err != nil {
			t.Fatal(err)
		}
		if replica.Bool(runtimeConfigS3, !enabled) != enabled {
			t.Fatal("replica did not converge")
		}
	}
	if r := e.doAdmin(t, "PATCH", "/v1/admin/config/s3_enabled", map[string]any{"value": "yes", "reason": "bad type"}, nil); r.Code != 400 {
		t.Fatal("accepted nonboolean", r.Code)
	}
}

func TestObjectStorageRecoveryStopsOnCancellation(t *testing.T) {
	e := setup(t, api.PlanHobby)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { e.s.runObjectStorageRecovery(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker ignored cancellation")
	}
}
