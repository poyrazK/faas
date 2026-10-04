// adr:568
package objectstorage

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestUploadRouteCheckpointCoversActiveAndUnknownProviderWrites(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "observed", true: "unknown"}[unknown], func(t *testing.T) {
			f := newUploadFixture(t)
			b, err := f.store.GetObjectBucket(context.Background(), f.account.ID, f.app.ID, f.route.BucketID)
			if err != nil {
				t.Fatal(err)
			}
			token := uuid.NewString()
			f.provider.beforeWrite = func(ctx context.Context) {
				fence, err := f.store.AcquireObjectBucketWriteFence(ctx, b, token)
				if err != nil || fence.Requests != 1 || fence.NativeGrants != 0 {
					t.Fatalf("route writer absent from checkpoint: %+v %v", fence, err)
				}
			}
			if unknown {
				f.provider.err = errors.New("provider reply lost")
			}
			response := f.request("POST", "/uploads/avatar", "abc", "image/png")
			wantStatus, wantWriters := 201, int64(0)
			if unknown {
				wantStatus, wantWriters = 502, 1
			}
			if response.Code != wantStatus || f.provider.writes != 1 {
				t.Fatalf("route write = %d %s", response.Code, response.Body.String())
			}
			fence, err := f.store.ReadObjectBucketWriteFence(context.Background(), b, token)
			if err != nil || fence.Requests != wantWriters {
				t.Fatalf("route outcome falsely drained write: %+v %v", fence, err)
			}
			response = f.request("POST", "/uploads/avatar", "abc", "image/png")
			if response.Code != 503 || f.provider.writes != 1 {
				t.Fatalf("route bypassed active fence: %d %s", response.Code, response.Body.String())
			}
			if _, err := f.store.BeginObjectBucketMutation(context.Background(), b, state.ObjectBucketMutationRequest); !errors.Is(err, state.ErrObjectBucketWriteFenced) {
				t.Fatal("route fence disappeared", err)
			}
		})
	}
}

func TestUploadRouteCheckpointRejectionDoesNotConsumeIdempotencyKey(t *testing.T) {
	f := newUploadFixture(t)
	b, err := f.store.GetObjectBucket(context.Background(), f.account.ID, f.app.ID, f.route.BucketID)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	if _, err := f.store.AcquireObjectBucketWriteFence(context.Background(), b, token); err != nil {
		t.Fatal(err)
	}
	response := f.requestWithIdempotency("POST", "/uploads/avatar", "abc", "image/png", "paused-attempt")
	if response.Code != 503 || f.provider.writes != 0 {
		t.Fatalf("fenced route = %d %s", response.Code, response.Body.String())
	}
	if err := f.store.ReleaseObjectBucketWriteFence(context.Background(), b, token); err != nil {
		t.Fatal(err)
	}
	response = f.requestWithIdempotency("POST", "/uploads/avatar", "abc", "image/png", "paused-attempt")
	if response.Code != 201 || f.provider.writes != 1 {
		t.Fatalf("checkpoint rejection poisoned later retry: %d %s", response.Code, response.Body.String())
	}
}
