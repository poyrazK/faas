package state_test

// adr: 202610171907

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type companyObjectStore interface {
	state.Store
	state.ObjectBucketStore
	state.ObjectS3CredentialBindingStore
	state.ObjectStorageAccountingStore
	state.ObjectStorageBillingStore
	state.ObjectStorageProviderUsageStore
	state.ObjectAccountBucketCleanupStore
}

func TestMemStoreOrgObjectOwnership(t *testing.T) {
	orgObjectOwnership(t, state.NewMemStore())
}

func TestMemStoreOrgObjectQuota(t *testing.T) {
	orgObjectQuota(t, state.NewMemStore())
}

func TestMemStoreOrgObjectAdoption(t *testing.T) {
	orgObjectAdoption(t, state.NewMemStore())
}

func TestMemStoreOrgObjectAdoptionBillingConflict(t *testing.T) {
	orgObjectAdoptionBillingConflict(t, state.NewMemStore())
}

func orgObjectAdoptionBillingConflict(t *testing.T, base state.Store) {
	t.Helper()
	f := setupOffboarding(t, base)
	store := base.(companyObjectStore)
	bucket := companyObjectBucket(t, store, f.app, "already-billed")
	period := state.ObjectStoragePeriod(time.Now())
	if _, err := store.RecordObjectStorageBillingPeriod(t.Context(), state.ObjectStorageBillingRecord{AccountID: bucket.AccountID,
		PeriodStart: period, PeriodEnd: period.AddDate(0, 1, 0), Currency: "EUR", FinalizedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := base.UpdateAccountProviderCustomerID(t.Context(), f.org.ID, "cus_adoption_conflict"); err != nil {
		t.Fatal(err)
	}
	if err := base.UpdateAccountStripeSubscriptionItem(t.Context(), f.org.ID, "si_adoption_conflict"); err == nil {
		t.Fatal("adoption allowed a second financial allocation for a finalized month")
	}
	org, err := base.OrgByID(t.Context(), f.org.ID)
	if err != nil || org.ResourceOwnershipEnabled {
		t.Fatalf("failed adoption changed financial ownership: %+v %v", org, err)
	}
}

func adoptObjectCompany(t *testing.T, f offboardingFixture) {
	t.Helper()
	if err := f.store.UpdateAccountProviderCustomerID(t.Context(), f.org.ID, "cus_object_company"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateAccountStripeSubscriptionItem(t.Context(), f.org.ID, "si_object_company"); err != nil {
		t.Fatal(err)
	}
}

func companyObjectBucket(t *testing.T, store companyObjectStore, app state.App, name string) state.ObjectBucket {
	t.Helper()
	ctx := t.Context()
	id := uuid.NewString()
	b, err := store.ReserveObjectBucket(ctx, state.ObjectBucket{ID: id, AccountID: app.AccountID, AppID: app.ID,
		Name: name, Scope: "default", BackendID: "company-provider", BackendFingerprint: strings.Repeat("a", 64),
		PhysicalName: "company-" + strings.ReplaceAll(id, "-", ""), Region: "us-east-1"}, 10)
	if err != nil {
		t.Fatal("reserve "+name+":", err)
	}
	if _, err := store.ClaimObjectBucket(ctx, app.AccountID, app.ID, b.ID, "provision", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishObjectBucket(ctx, b.ID, "provision", "ready"); err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	if err := store.ClaimObjectInventory(ctx, b.ID, token); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishObjectInventory(ctx, b.ID, token, 0, 0); err != nil {
		t.Fatal(err)
	}
	b, err = store.GetObjectBucket(ctx, app.AccountID, app.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func companyObjectReport(t *testing.T, store companyObjectStore, bucket state.ObjectBucket, subject string, period time.Time) api.ObjectStorageUsageReport {
	t.Helper()
	report := api.ObjectStorageUsageReport{AccountID: subject, BackendID: bucket.BackendID, BackendFingerprint: bucket.BackendFingerprint,
		Source: "company-provider", PeriodStart: state.ObjectStoragePeriod(period), ObservedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if period.Before(state.ObjectStoragePeriod(time.Now())) {
		report.ObservedAt = state.ObjectStoragePeriod(period).AddDate(0, 1, 0).Add(-time.Second)
	}
	if err := store.RecordObjectUsageReport(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	return report
}

func objectIssuerContext(t *testing.T, store state.Store, org, actor string) context.Context {
	t.Helper()
	snapshot, err := store.OrgAccessSnapshot(t.Context(), org, actor)
	if err != nil {
		t.Fatal(err)
	}
	ctx, release := state.WithOrgAccessFence(t.Context(), state.OrgAccessFence{OrgID: org, AccountID: actor, Revision: snapshot.Revision})
	t.Cleanup(func() { release(nil) })
	return ctx
}

func companyObjectCredential(t *testing.T, ctx context.Context, store companyObjectStore, bucket state.ObjectBucket, app string) state.ObjectS3Credential {
	t.Helper()
	access, _, err := api.GenerateObjectS3Credential()
	if err != nil {
		t.Fatal(err)
	}
	c := state.ObjectS3Credential{ID: uuid.NewString(), AccountID: bucket.AccountID, BucketID: bucket.ID,
		AccessKeyID: access, SecretSealed: []byte("sealed-test-secret"), KID: "age1test",
		Label: "company key", Permission: state.ObjectBucketPermissionReadWrite, Status: state.ObjectS3CredentialStatusActive}
	if app == "" {
		saved, err := store.CreateObjectS3Credential(ctx, c, 10)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	c.ManagedAppID, c.ManagedScope, c.ManagedPrefix = app, "default", "GREGALE_S3_ASSETS"
	req := state.ObjectS3ComputeBindingCreateRequest{Credential: c, MaxCredentialsPerBucket: 10, MaxSecretsPerApp: 12}
	for _, suffix := range []string{"_ENDPOINT", "_REGION", "_BUCKET", "_ACCESS_KEY_ID", "_SECRET_ACCESS_KEY", "_ADDRESSING_STYLE"} {
		req.Secrets = append(req.Secrets, state.AppSecret{AccountID: c.AccountID, AppID: app, Scope: "default", Key: c.ManagedPrefix + suffix,
			Ciphertext: []byte("sealed-test-" + suffix), Kid: c.KID, ValueHash: "0123456789abcdef", ManagedObjectStorageCredentialID: c.ID})
	}
	saved, err := store.CreateObjectS3ComputeBinding(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func orgObjectOwnership(t *testing.T, base state.Store) {
	t.Helper()
	f := setupOffboarding(t, base)
	adoptObjectCompany(t, f)
	store := base.(companyObjectStore)
	bucket := companyObjectBucket(t, store, f.app, "assets")
	if bucket.OrgID != f.org.ID {
		t.Fatalf("bucket ownership: %+v", bucket)
	}
	ctx := t.Context()
	actorCtx := objectIssuerContext(t, base, f.org.ID, f.actor.ID)
	ownerCtx := objectIssuerContext(t, base, f.org.ID, f.owner.ID)
	actorKey := companyObjectCredential(t, actorCtx, store, bucket, "")
	ownerKey := companyObjectCredential(t, ownerCtx, store, bucket, "")
	binding := companyObjectCredential(t, actorCtx, store, bucket, f.app.ID)
	objectEvent, err := json.Marshal(map[string]any{"specversion": "1.0", "id": "retained-storage-event", "source": api.ObjectEventSource,
		"type": api.ObjectEventCreated, "accountid": f.actor.ID,
		"data": api.ObjectStorageEvent{BucketID: bucket.ID, AppID: f.app.ID, Key: "retained-object-event"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(t.Context(), "objectstorage", "event.published", &f.actor.ID, objectEvent); err != nil {
		t.Fatal(err)
	}
	if actorKey.IssuerAccountID != f.actor.ID || ownerKey.IssuerAccountID != f.owner.ID || binding.IssuerAccountID != "" {
		t.Fatalf("credential authority: actor=%+v owner=%+v binding=%+v", actorKey, ownerKey, binding)
	}
	secret, err := store.GetAppSecretInScope(ctx, bucket.AccountID, f.app.ID, "default", binding.ManagedPrefix+"_SECRET_ACCESS_KEY")
	if err != nil {
		t.Fatal(err)
	}
	companyObjectReport(t, store, bucket, f.org.ID, time.Now())
	previousMonth := state.ObjectStoragePeriod(time.Now()).AddDate(0, -1, 0)
	companyObjectReport(t, store, bucket, bucket.AccountID, previousMonth)
	for _, tc := range []struct {
		period time.Time
		want   string
	}{{time.Now(), f.org.ID}, {previousMonth, bucket.AccountID}} {
		rows, err := store.ListObjectStorageProviderBuckets(state.WithObjectUsagePeriod(ctx, tc.period), bucket.BackendID, bucket.BackendFingerprint)
		if err != nil || len(rows) != 1 || rows[0].AccountID != bucket.AccountID || rows[0].UsageSubjectID != tc.want {
			t.Fatalf("provider allocation for %s: %+v %v", tc.period, rows, err)
		}
	}
	if rows, err := store.ListAccountObjectBuckets(ctx, f.actor.ID, 10); err != nil || len(rows) != 0 {
		t.Fatalf("personal cleanup owns company bucket: %+v %v", rows, err)
	}
	if err := store.SoftDeleteOrg(ctx, f.org.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("organization deletion orphaned bucket: %v", err)
	}
	if err := store.UpdateOrgMemberRole(ctx, f.org.ID, f.actor.ID, state.OrgRoleViewer); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ResolveObjectS3Credential(ctx, actorKey.AccessKeyID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("demotion retained user key: %v", err)
	}
	if _, err := store.ListObjectBuckets(actorCtx, bucket.AccountID, bucket.AppID); !errors.Is(err, state.ErrOrgAccessRevoked) {
		t.Fatalf("stale inventory escaped membership fence: %v", err)
	}
	if err := store.UpdateOrgMemberRole(ctx, f.org.ID, f.actor.ID, state.OrgRoleDeveloper); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ResolveObjectS3Credential(ctx, actorKey.AccessKeyID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("promotion revived a revoked key")
	}
	if err := store.RemoveOrgMember(ctx, f.org.ID, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAccountDeletionPending(ctx, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAccount(ctx, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	retainedEvents, err := store.ListEvents(ctx, f.actor.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var retained bool
	for _, event := range retainedEvents {
		retained = retained || bytes.Contains(event.Data, []byte("retained-object-event"))
	}
	if !retained {
		t.Fatal("creator erasure discarded company storage event history")
	}
	for _, key := range []state.ObjectS3Credential{ownerKey, binding} {
		saved, b, err := store.ResolveObjectS3Credential(ctx, key.AccessKeyID)
		if err != nil || saved.ID != key.ID || !bytes.Equal(saved.SecretSealed, key.SecretSealed) || b.ID != bucket.ID || b.PhysicalName != bucket.PhysicalName || b.BackendFingerprint != bucket.BackendFingerprint {
			t.Fatalf("creator erasure changed company storage: %+v %+v %v", saved, b, err)
		}
	}
	// Serving during dunning and abuse holds follows company policy after
	// the human account has gone.
	if err := store.UpdateAccountStatus(ctx, f.org.ID, state.AccountPastDue); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ResolveObjectS3Credential(ctx, binding.AccessKeyID); err != nil {
		t.Fatal("dunning grace disabled application storage:", err)
	}
	if err := store.UpdateAccountStatus(ctx, f.org.ID, state.AccountActive); err != nil {
		t.Fatal(err)
	}
	holds := base.(state.AccountAbuseHoldStore)
	if _, err := holds.SetAccountAbuseHold(ctx, f.org.ID, state.AccountAbuseHoldOperator, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ResolveObjectS3Credential(ctx, binding.AccessKeyID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("company abuse hold missed application storage: %v", err)
	}
	if _, err := holds.ReleaseAccountAbuseHold(ctx, f.org.ID); err != nil {
		t.Fatal(err)
	}
	afterSecret, err := store.GetAppSecretInScope(ctx, bucket.AccountID, f.app.ID, "default", binding.ManagedPrefix+"_SECRET_ACCESS_KEY")
	if err != nil || !bytes.Equal(secret.Ciphertext, afterSecret.Ciphertext) || afterSecret.ManagedObjectStorageCredentialID != binding.ID {
		t.Fatalf("sealed app binding changed: %+v %v", afterSecret, err)
	}
	if err := store.AdmitObjectURL(ownerCtx, bucket.AccountID, bucket.ID, "retained", 1, true, accountingPolicy()); err != nil {
		t.Fatalf("company accounting depends on erased creator: %v", err)
	}
	for _, subject := range []string{f.org.ID, bucket.AccountID} {
		period := time.Now()
		if subject == bucket.AccountID {
			period = previousMonth
		}
		snapshot, err := store.ObjectUsageForPeriod(ctx, subject, period)
		if err != nil || len(snapshot.Reports) != 1 {
			t.Fatalf("erasure lost accounting evidence for %s: %+v %v", subject, snapshot, err)
		}
	}
}

func orgObjectQuota(t *testing.T, base state.Store) {
	t.Helper()
	f := setupOffboarding(t, base)
	adoptObjectCompany(t, f)
	store := base.(companyObjectStore)
	app, err := base.CreateApp(t.Context(), state.App{AccountID: f.org.ID, OrgID: f.org.ID, Slug: "new-company-app"})
	if err != nil {
		t.Fatal("create company-namespace app:", err)
	}
	buckets := []state.ObjectBucket{companyObjectBucket(t, store, f.app, "legacy"), companyObjectBucket(t, store, app, "new")}
	companyObjectReport(t, store, buckets[0], f.org.ID, time.Now())
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			b := buckets[i%2]
			err := store.AdmitObjectURL(t.Context(), b.AccountID, b.ID, uuid.NewString(), 10, true, accountingPolicy())
			if err == nil {
				winners.Add(1)
			} else if !errors.Is(err, state.ErrObjectCapacity) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	snapshot, err := store.ObjectUsage(t.Context(), f.org.ID, time.Now())
	if err != nil || winners.Load() != 10 || snapshot.Authorizations != 10 || len(snapshot.Buckets) != 2 {
		t.Fatalf("shared quota admission: winners=%d snapshot=%+v %v", winners.Load(), snapshot, err)
	}
	personal, err := store.ObjectUsage(t.Context(), f.actor.ID, time.Now())
	if err != nil || len(personal.Buckets) != 0 || personal.Authorizations != 0 {
		t.Fatalf("company admission charged creator: %+v %v", personal, err)
	}
	policy := gatewaySafetyPolicy()
	policy.MaxMonthlyRequests = 1
	if err := store.(state.ObjectStorageGatewayRequestStore).ReserveObjectStorageGatewayRequest(t.Context(), buckets[0].ID, time.Now(), policy); err != nil {
		t.Fatal(err)
	}
	if err := store.(state.ObjectStorageGatewayRequestStore).ReserveObjectStorageGatewayRequest(t.Context(), buckets[1].ID, time.Now(), policy); !errors.Is(err, state.ErrObjectBudget) {
		t.Fatalf("cross-namespace request budget bypassed: %v", err)
	}
}

func orgObjectAdoption(t *testing.T, base state.Store) {
	t.Helper()
	f := setupOffboarding(t, base)
	store := base.(companyObjectStore)
	bucket := companyObjectBucket(t, store, f.app, "legacy")
	oldReport := companyObjectReport(t, store, bucket, bucket.AccountID, time.Now())
	previousMonth := state.ObjectStoragePeriod(time.Now()).AddDate(0, -1, 0)
	companyObjectReport(t, store, bucket, bucket.AccountID, previousMonth)
	adoptObjectCompany(t, f)
	for _, subject := range []string{bucket.AccountID, f.org.ID} {
		snapshot, err := store.ObjectUsage(t.Context(), subject, time.Now())
		if err != nil || len(snapshot.Reports) != 0 {
			t.Fatalf("mixed current-period report remained billable: %+v %v", snapshot, err)
		}
	}
	// A new historical aggregate cannot restore the invalidated mutable head.
	oldReport.ObservedAt = oldReport.ObservedAt.Add(-time.Second)
	if err := store.RecordObjectUsageReport(t.Context(), oldReport); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("old mixed report replayed after cutover: %v", err)
	}
	if err := store.AdmitObjectURL(t.Context(), bucket.AccountID, bucket.ID, "needs-allocation", 1, true, accountingPolicy()); !errors.Is(err, state.ErrObjectUsageStale) {
		t.Fatalf("adoption admitted stale allocation: %v", err)
	}
	history, err := store.ObjectUsageForPeriod(t.Context(), bucket.AccountID, previousMonth)
	if err != nil || len(history.Reports) != 1 || len(history.Buckets) != 1 {
		t.Fatalf("adoption moved historical allocation: %+v %v", history, err)
	}
	companyObjectReport(t, store, bucket, f.org.ID, time.Now())
	if err := store.AdmitObjectURL(t.Context(), bucket.AccountID, bucket.ID, "qualified", 1, true, accountingPolicy()); err != nil {
		t.Fatalf("fresh company allocation refused: %v", err)
	}
}
