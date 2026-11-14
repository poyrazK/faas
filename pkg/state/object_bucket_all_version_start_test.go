package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261107214517
func TestObjectBucketAllVersionStartMem(t *testing.T) {
	objectBucketAllVersionStart(t, state.NewMemStore())
}
func TestObjectBucketAllVersionStartPG(t *testing.T) {
	st, _ := pgStore(t)
	objectBucketAllVersionStart(t, st)
}

// objectBucketAllVersionStart starts a provisioning bucket in all-version
// accounting and checks it is the state the first write and delete would
// otherwise reach: verified version inventory, a ready Suspended versioning
// row, and tracked writes admitted at once with nothing left to fence.
func objectBucketAllVersionStart(t *testing.T, st accountingStore) {
	ctx := t.Context()
	start := st.(state.ObjectBucketAllVersionStartStore)
	ready, _ := seedAccounting(t, st)
	if ok, err := start.StartObjectBucketAllVersionAccounting(ctx, ready.AccountID, ready.ID); err != nil || ok {
		t.Fatal("a ready bucket with accounting started", ok, err)
	}
	b := seedRecoveryBucket(t, st)
	if _, err := st.ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	other := seedRecoveryBucket(t, st)
	if ok, err := start.StartObjectBucketAllVersionAccounting(ctx, other.AccountID, b.ID); err != nil || ok {
		t.Fatal("another account started the bucket", ok, err)
	}
	if ok, err := start.StartObjectBucketAllVersionAccounting(ctx, b.AccountID, b.ID); err != nil || !ok {
		t.Fatal("provisioning bucket did not start", ok, err)
	}
	if ok, err := start.StartObjectBucketAllVersionAccounting(ctx, b.AccountID, b.ID); err != nil || ok {
		t.Fatal("a started bucket started twice", ok, err)
	}
	if err := st.FinishObjectBucket(ctx, b.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	status, err := st.(state.ObjectVersionInventoryStore).ObjectVersionAccountingStatus(ctx, b.AccountID, b.ID)
	if err != nil || status.Scope != state.ObjectInventoryAllVersions || status.NativeScanActive {
		t.Fatal("bucket did not start in all-version accounting", status, err)
	}
	versioning := st.(state.ObjectBucketVersioningStore)
	v, err := versioning.GetObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil || v.State != "ready" || v.ObservedStatus != "Suspended" || !v.VersionsRequired || v.CapacityJobID == "" {
		t.Fatal("versioning baseline was not recorded ready", v, err)
	}
	// The first delete observes Suspended again: nothing changes, nothing waits.
	if v, err = versioning.ObserveObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, "Suspended"); err != nil || v.State != "ready" || v.Revision != 1 {
		t.Fatal("observing the baseline restarted the versioning cutover", v, err)
	}
	report := api.ObjectStorageUsageReport{AccountID: b.AccountID, BackendID: b.BackendID, BackendFingerprint: b.BackendFingerprint, Source: "qualified-provider", PeriodStart: state.ObjectStoragePeriod(time.Now()), ObservedAt: time.Now().Add(-time.Minute).UTC().Truncate(time.Microsecond)}
	if err = st.RecordObjectUsageReport(ctx, report); err != nil {
		t.Fatal(err)
	}
	tracked := st.(state.ObjectTrackedGatewayUploadStore)
	if _, err = tracked.BeginTrackedGatewayUpload(ctx, versionCandidate(b, 1), accountingPolicy()); err != nil {
		t.Fatal("the first tracked write was fenced", err)
	}
}
