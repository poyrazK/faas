// adr: 590
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/objectstorageactivity"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestObjectMultipartInitiationMem(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "positive reply", true: "lost reply"}[lost], func(t *testing.T) {
			multipartInitiationContract(t, state.NewMemStore(), lost)
		})
	}
}

func TestObjectMultipartInitiationPG(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "positive reply", true: "lost reply"}[lost], func(t *testing.T) {
			st, _, _ := pgStoreWithPool(t)
			multipartInitiationContract(t, st, lost)
		})
	}
}

func multipartInitiationContract(t *testing.T, st accountingStore, lost bool) {
	t.Helper()
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	sessions := st.(state.ObjectMultipartUploadStore)
	initiation := st.(state.ObjectMultipartInitiationStore)
	fences := st.(state.ObjectBucketWriteFenceStore)
	u, err := sessions.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: "original", ExpiresAt: time.Now().Add(time.Hour)}, 100)
	if err != nil {
		t.Fatal(err)
	}
	u, err = sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "original-owner", state.ObjectMultipartInitiating, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := initiation.ObserveObjectMultipartInitiation(ctx, u, "native"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("observed reply without dispatch", err)
	}
	d, err := initiation.ReadObjectMultipartInitiation(ctx, u)
	if err != nil || d.Dispatched || d.Receipt.MultipartUploadID != u.ID {
		t.Fatal(d, err)
	}
	if err := fences.FinishObjectBucketMutation(ctx, d.Receipt); !errors.Is(err, state.ErrConflict) {
		t.Fatal("generic finish removed original receipt", err)
	}
	if _, err := fences.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationRequest); err != nil {
		t.Fatal(err)
	}
	f, err := fences.AcquireObjectBucketWriteFence(ctx, b, uuid.NewString())
	if err != nil || f.Requests != 2 || f.Multipart != 1 {
		t.Fatal(f, err)
	}
	for _, change := range []func(*state.ObjectMultipartUpload){
		func(v *state.ObjectMultipartUpload) { v.ID = uuid.NewString() },
		func(v *state.ObjectMultipartUpload) { v.AccountID = uuid.NewString() },
		func(v *state.ObjectMultipartUpload) { v.Key = "other" },
		func(v *state.ObjectMultipartUpload) { v.LeaseToken = "other-owner" },
		func(v *state.ObjectMultipartUpload) { v.SizeBytes++ },
	} {
		changed := u
		change(&changed)
		if err := initiation.DispatchObjectMultipartInitiation(ctx, changed); !errors.Is(err, state.ErrConflict) {
			t.Fatal("changed authority dispatched", err)
		}
	}
	writes := 0
	call := func(callCtx context.Context, dispatch func(context.Context) error) (string, error) {
		if dispatch == nil {
			t.Fatal("bound initiation entered legacy admission")
		}
		if err := dispatch(callCtx); err != nil {
			return "", err
		}
		writes++
		if lost {
			return "", objectstorage.ErrUnavailable
		}
		return "native-original", nil
	}
	id, err := objectstorageactivity.ExecuteMultipartInitiation(ctx, st, st, b, u, call)
	if writes != 1 || lost && !errors.Is(err, objectstorage.ErrUnavailable) || !lost && (err != nil || id != "native-original") {
		t.Fatal(id, err, writes)
	}
	if err := initiation.DispatchObjectMultipartInitiation(ctx, u); !errors.Is(err, state.ErrConflict) {
		t.Fatal("second dispatch accepted", err)
	}
	id, err = objectstorageactivity.ExecuteMultipartInitiation(ctx, st, st, b, u, call)
	if writes != 1 || lost && !errors.Is(err, objectstorage.ErrUnavailable) || !lost && (err != nil || id != "native-original") {
		t.Fatal("replay wrote to provider", id, err, writes)
	}
	if lost {
		f, err = fences.ReadObjectBucketWriteFence(ctx, b, f.Token)
		if err != nil || f.Requests != 2 || f.Multipart != 1 {
			t.Fatal("lost reply drained receipt", f, err)
		}
		return
	}
	if err := sessions.ActivateObjectMultipartUpload(ctx, u.ID, u.LeaseToken, "wrong-native"); err == nil {
		t.Fatal("activated an unobserved identity")
	}
	if err := sessions.ActivateObjectMultipartUpload(ctx, u.ID, u.LeaseToken, id); err != nil {
		t.Fatal(err)
	}
	u, err = sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "abort-owner", state.ObjectMultipartAborting, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.(state.ObjectMultipartTransferStore).FinishVerifiedObjectMultipartAbort(ctx, u.ID, u.LeaseToken); err != nil {
		t.Fatal(err)
	}
	f, err = fences.ReadObjectBucketWriteFence(ctx, b, f.Token)
	if err != nil || f.Requests != 1 || f.Multipart != 0 {
		t.Fatal("settlement erased unrelated receipt", f, err)
	}
}
