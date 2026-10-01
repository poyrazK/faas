package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestObjectMultipartConditionalMem(t *testing.T) {
	multipartConditionalSuite(t, state.NewMemStore())
}

func TestObjectMultipartConditionalPG(t *testing.T) {
	s, _ := pgStore(t)
	multipartConditionalSuite(t, s)
}

func multipartConditionalSuite(t *testing.T, st accountingStore) {
	ctx := context.Background()
	b, u := activeTrackedUpload(t, st, "conditional")
	p := accountingPolicy()
	sessions := st.(state.ObjectMultipartUploadStore)
	transfers := st.(state.ObjectMultipartTransferStore)
	if err := transfers.BeginObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, "part", 1, 30, 100, p); err != nil {
		t.Fatal(err)
	}
	if err := transfers.SettleObjectMultipartPart(ctx, b.AccountID, u.ID, 1, "part"); err != nil {
		t.Fatal(err)
	}
	u, err := sessions.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	u.CompletionConditions = api.ObjectWriteConditions{IfNoneMatch: "*"}
	parts := []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: `"etag"`}}
	prepared, err := transfers.PrepareObjectMultipartCompletion(ctx, u, "complete", 30, parts, p)
	if err != nil || prepared.State != state.ObjectMultipartCompletingConditional || prepared.CompletionConditions != u.CompletionConditions || prepared.SizeBytes != 30 {
		t.Fatalf("lost conditional intent: %+v %v", prepared, err)
	}
	if err = sessions.RetryObjectMultipartUpload(ctx, u.ID, "complete", "temporary", time.Second); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	due, err := sessions.DueObjectMultipartUploads(ctx, 10)
	if err != nil || len(due) != 1 || due[0].CompletionConditions != u.CompletionConditions || due[0].State != state.ObjectMultipartCompletingConditional {
		t.Fatalf("conditional recovery not due: %+v %v", due, err)
	}
	prepared.LeaseToken = ""
	for _, changed := range []api.ObjectWriteConditions{{}, {IfMatch: `"other"`}} {
		copy := prepared
		copy.CompletionConditions = changed
		if _, err = transfers.PrepareObjectMultipartCompletion(ctx, copy, "changed", 30, parts, p); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("changed/removed conditions accepted: %v", err)
		}
	}
	if _, err = sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "old-worker", state.ObjectMultipartCompleting, parts, true); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unconditional worker claimed conditional completion: %v", err)
	}
	claimed, err := sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "recover", state.ObjectMultipartCompletingConditional, parts, true)
	if err != nil || claimed.CompletionConditions != u.CompletionConditions {
		t.Fatalf("recovery removed conditions: %+v %v", claimed, err)
	}
	for _, test := range []struct{ token, code string }{{"wrong", "precondition_failed"}, {"recover", "provider-secret"}} {
		if err = transfers.RejectObjectMultipartCompletion(ctx, u.ID, test.token, test.code); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("invalid rejection accepted: %v", err)
		}
	}
	if err = transfers.RejectObjectMultipartCompletion(ctx, u.ID, "recover", "precondition_failed"); err != nil {
		t.Fatal(err)
	}
	rejected, err := sessions.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
	if err != nil || rejected.State != state.ObjectMultipartAborting || rejected.CompletionErrorCode != "precondition_failed" || rejected.CompletionConditions != u.CompletionConditions || rejected.LeaseToken != "" {
		t.Fatalf("lost rejection: %+v %v", rejected, err)
	}
	if err = transfers.BeginObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, "late", 2, 10, 100, p); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("rejected upload still writable: %v", err)
	}
	usage, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil || usage.Buckets[0].MultipartBytes != 30 {
		t.Fatalf("released before verified cleanup: %+v %v", usage, err)
	}
	if _, err = sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "cleanup", state.ObjectMultipartAborting, nil, true); err != nil {
		t.Fatal(err)
	}
	if err = transfers.FinishVerifiedObjectMultipartAbort(ctx, u.ID, "cleanup"); err != nil {
		t.Fatal(err)
	}
	rejected, err = sessions.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
	if err != nil || rejected.State != state.ObjectMultipartAborted || rejected.CompletionErrorCode != "precondition_failed" {
		t.Fatalf("cleanup erased terminal outcome: %+v %v", rejected, err)
	}
	usage, err = st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil || usage.Buckets[0].MultipartBytes != 0 || state.SummarizeObjectUsage(usage, p, time.Now()).CapacityBytes != 30 {
		t.Fatalf("cleanup must release parts, preserve object grant: %+v %v", usage, err)
	}
}
