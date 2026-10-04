package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type recoveryUploadProvider struct {
	*fakeObjectProvider
	receipts         map[string]string
	confirms, writes int
}

func (p *recoveryUploadProvider) WriteTrackedObject(_ context.Context, _, key, id string, body io.Reader, _ int64, _ objectstorage.ObjectMetadata) (objectstorage.UploadResult, error) {
	p.writes++
	data, err := io.ReadAll(body)
	if err != nil {
		return objectstorage.UploadResult{}, err
	}
	p.objects[key] = data
	p.receipts[key] = id
	// Accepted write with a lost acknowledgment.
	return objectstorage.UploadResult{}, objectstorage.ErrUnavailable
}
func (p *recoveryUploadProvider) ConfirmTrackedObject(_ context.Context, _, key, id string, size int64) (objectstorage.UploadResult, error) {
	p.confirms++
	data, ok := p.objects[key]
	if !ok {
		return objectstorage.UploadResult{}, objectstorage.ErrNotFound
	}
	if p.receipts[key] != id || int64(len(data)) != size {
		return objectstorage.UploadResult{}, objectstorage.ErrConflict
	}
	return objectstorage.UploadResult{ETag: "recovered"}, nil
}
func TestObjectUploadRecoveryAfterLostAcknowledgmentPG(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	st := state.NewPgStore(pool)
	acct, err := st.CreateAccount(ctx, "recover-upload@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "recover-upload", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	provider := &recoveryUploadProvider{fakeObjectProvider: &fakeObjectProvider{objects: map[string][]byte{}}, receipts: map[string]string{}}
	policy := api.ObjectStoragePolicy{MaxAccountBytes: 100, MaxBucketBytes: 100, MaxAccountKeys: 100, MaxMonthlyCostMillicents: 1000, MaxMonthlyRequests: 1000, MaxMonthlyEgressBytes: 1000, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600}
	registry, err := objectstorage.NewRegistry(objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "test"}, Backends: []objectstorage.BackendConfig{{ID: "test", Driver: "s3", Region: "us-east-1", Namespace: "test", Endpoint: "https://storage.test", S3Region: "us-east-1"}}}, func(string) string { return "" }, map[string]objectstorage.Factory{"s3": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) {
		return provider, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.ReserveObjectBucket(ctx, state.ObjectBucket{ID: uuid.NewString(), AccountID: acct.ID, AppID: app.ID, Name: "files", Scope: "default", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, PhysicalName: "upload-recovery", Region: "us-east-1"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimObjectBucket(ctx, acct.ID, app.ID, b.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectBucket(ctx, b.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	if err = st.ClaimObjectInventory(ctx, b.ID, "initial-inventory"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectInventory(ctx, b.ID, "initial-inventory", 0, 0); err != nil {
		t.Fatal(err)
	}
	report := api.ObjectStorageUsageReport{AccountID: acct.ID, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Source: "provider", PeriodStart: state.ObjectStoragePeriod(time.Now()), ObservedAt: time.Now()}
	if err = st.RecordObjectUsageReport(ctx, report); err != nil {
		t.Fatal(err)
	}
	route, err := st.UpsertObjectUploadRoute(ctx, state.ObjectUploadRoute{ID: uuid.NewString(), AccountID: acct.ID, AppID: app.ID, BucketID: b.ID, Name: "files", MaxBytes: 100, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	seed := func(ctx context.Context, key string) state.ObjectUploadCompletion {
		c, _, e := st.BeginTrackedObjectUpload(ctx, state.ObjectUploadCompletion{ID: uuid.NewString(), RouteID: route.ID, AccountID: acct.ID, AppID: app.ID, BucketID: b.ID, SubjectID: "owner", Key: key, Bytes: 5, Status: "pending", IdempotencyKey: key, RequestFingerprint: "fingerprint"}, registry.Accounting)
		if e != nil {
			t.Fatal(e)
		}
		c, e = st.DispatchTrackedObjectUpload(ctx, acct.ID, b.ID, c.ID)
		if e != nil {
			t.Fatal(e)
		}
		return c
	}
	due := func(ctx context.Context, id string) {
		if _, e := pool.Exec(ctx, `UPDATE object_upload_completions SET recovery_retry_at=now()-interval '1 second' WHERE id=$1`, id); e != nil {
			t.Fatal(e)
		}
	}
	c := seed(ctx, "committed")
	if _, err = provider.WriteTrackedObject(ctx, b.PhysicalName, c.Key, c.ID, strings.NewReader("hello"), c.Bytes, objectstorage.ObjectMetadata{}); !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatal(err)
	}
	due(ctx, c.ID)
	e := setup(t, api.PlanHobby)
	e.s.store = st
	e.s.WithObjectStorage(registry)
	setS3Flag(t, e, false)
	// Cleanup/recovery runs with storage disabled and an exhausted monetary budget.
	report.ObservedAt = time.Now().UTC()
	report.CostMillicents = policy.MaxMonthlyCostMillicents
	if err = st.RecordObjectUsageReport(ctx, report); err != nil {
		t.Fatal(err)
	}
	if err = e.s.reconcileObjectUploads(ctx, nil); err != nil {
		t.Fatal(err)
	}
	c, err = st.GetObjectUploadReceipt(ctx, acct.ID, app.ID, route.ID, "owner", c.ID)
	if err != nil || c.Status != "completed" || c.ETag != "recovered" || provider.writes != 1 || provider.confirms != 1 {
		t.Fatal(c, provider.writes, provider.confirms, err)
	}
	delete(provider.objects, c.Key)
	j, err := st.RequestObjectCapacityReconciliation(ctx, acct.ID, app.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.s.reconcileObjectCapacity(ctx, nil); err != nil {
		t.Fatal(err)
	}
	j, err = st.GetObjectCapacityReconciliation(ctx, acct.ID, b.ID, j.ID)
	if err != nil || j.State != "completed" || j.ReclaimedBytes != 5 {
		t.Fatal(j, err)
	}
	registry.Accounting.MaxMonthlyCostMillicents *= 2
	unknown := seed(ctx, "unknown")
	due(ctx, unknown.ID)
	if err = e.s.reconcileObjectUploads(ctx, nil); err != nil {
		t.Fatal(err)
	}
	pending, err := st.GetObjectUploadReceipt(ctx, acct.ID, app.ID, route.ID, "owner", unknown.ID)
	if err != nil || pending.Status != "pending" || pending.ErrorCode != "provider_write_uncertain" {
		t.Fatal("absence refunded", pending, err)
	}
	// Losing configuration only defers probing; it never closes a dispatched write.
	e.s.WithObjectStorage(nil)
	due(ctx, unknown.ID)
	if err = e.s.reconcileObjectUploads(ctx, nil); err != nil {
		t.Fatal(err)
	}
	pending, err = st.GetObjectUploadReceipt(ctx, acct.ID, app.ID, route.ID, "owner", unknown.ID)
	if err != nil || pending.Status != "pending" || pending.ErrorCode != "configuration" {
		t.Fatal(pending, err)
	}
}
