// adr: 590
package state_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func partCopyIntent(b state.ObjectBucket, u state.ObjectMultipartUpload) state.ObjectMultipartPartCopyIntent {
	return state.ObjectMultipartPartCopyIntent{Schema: 1, SourceBucketID: b.ID, SourceBackendID: b.BackendID, SourceBackendFingerprint: b.BackendFingerprint, SourcePhysicalName: b.PhysicalName, SourceKey: "source", SourceVersionID: "private-version", SourceRequestedVersionID: "private-version", SourceETag: `"source-etag"`, SourceSize: 100, DestinationKey: u.Key, ProviderUploadID: u.ProviderUploadID, HasRange: true, RangeFirst: 10, RangeLast: 39, ExpectedSize: 30, IfNoneMatch: `"other"`, IfModifiedSince: "2026-01-01T00:00:00Z"}
}

func TestObjectMultipartPartCopyIntentMem(t *testing.T) {
	multipartPartCopyIntentSuite(t, state.NewMemStore())
}
func TestObjectMultipartPartCopyIntentPG(t *testing.T) {
	st, _ := pgStore(t)
	multipartPartCopyIntentSuite(t, st)
}

func multipartPartCopyIntentSuite(t *testing.T, st accountingStore) {
	ctx := context.Background()
	b, u := activeTrackedUpload(t, st, "copy-intent")
	transfers := st.(state.ObjectMultipartTransferStore)
	copies := st.(state.ObjectMultipartPartCopyMutationStore)
	writers := st.(state.ObjectMultipartPartMutationStore)
	if err := transfers.BeginObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, "original", 1, 30, 100, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	i := partCopyIntent(b, u)
	for _, change := range []func(*state.ObjectMultipartPartCopyIntent){
		func(i *state.ObjectMultipartPartCopyIntent) { i.SourcePhysicalName = "wrong" },
		func(i *state.ObjectMultipartPartCopyIntent) { i.DestinationKey = "wrong" },
		func(i *state.ObjectMultipartPartCopyIntent) { i.ProviderUploadID = "wrong" },
		func(i *state.ObjectMultipartPartCopyIntent) { i.SourceBucketID = uuid.NewString() },
		func(i *state.ObjectMultipartPartCopyIntent) { i.ExpectedSize = 29 },
		func(i *state.ObjectMultipartPartCopyIntent) { i.RangeLast = 100 },
	} {
		bad := i
		change(&bad)
		if _, err := copies.DispatchObjectMultipartPartCopyMutation(ctx, b, u.ID, 1, "original", bad); !errors.Is(err, state.ErrConflict) {
			t.Fatal("invalid intent claimed authority", bad, err)
		}
	}
	hold, err := st.(state.ObjectBucketWriteFenceStore).AcquireObjectBucketWriteFence(ctx, b, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	r, err := copies.DispatchObjectMultipartPartCopyMutation(ctx, b, u.ID, 1, "original", i)
	if err != nil {
		t.Fatal(err)
	}
	// Caller changes cannot mutate the journal. The original attempt may claim
	// under a hold, but neither copying nor ordinary dispatch may replay it.
	i.SourceVersionID = "replacement"
	got, err := copies.ReadObjectMultipartPartCopyIntent(ctx, r)
	if err != nil || !reflect.DeepEqual(got, partCopyIntent(b, u)) {
		t.Fatal("lost exact intent", got, err)
	}
	if _, err = copies.DispatchObjectMultipartPartCopyMutation(ctx, b, u.ID, 1, "original", i); !errors.Is(err, state.ErrConflict) {
		t.Fatal("copy replay", err)
	}
	if _, err = writers.DispatchObjectMultipartPartMutation(ctx, b, u.ID, 1, "original"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("copy downgraded into put", err)
	}
	wrong := r
	wrong.Bucket.AppID = uuid.NewString()
	if _, err = copies.ReadObjectMultipartPartCopyIntent(ctx, wrong); !errors.Is(err, state.ErrConflict) {
		t.Fatal("cross-scope intent read", err)
	}
	if err = transfers.SettleObjectMultipartPart(ctx, b.AccountID, u.ID, 1, "original"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("intent treated as proof", err)
	}
	f, err := st.(state.ObjectBucketWriteFenceStore).ReadObjectBucketWriteFence(ctx, b, hold.Token)
	if err != nil || f.Requests != 2 {
		t.Fatal("intent lost fence", f, err)
	}
	if err = writers.FinishObjectMultipartPartMutation(ctx, r); err != nil {
		t.Fatal(err)
	}
	if got, err = copies.ReadObjectMultipartPartCopyIntent(ctx, r); err != nil || got.SourceVersionID != "private-version" {
		t.Fatal("settlement erased history", got, err)
	}
}
