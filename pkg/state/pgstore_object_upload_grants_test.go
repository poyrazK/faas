//go:build !no_pg

// adr: 569
package state_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestObjectUploadGrantPGPruningDoesNotDrainActiveWriter(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	b := mutationBucketFixture(t, s)
	spec := uploadGrantSpec(b)
	g, err := s.CreateObjectUploadGrant(ctx, spec, 60)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := s.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationRequest)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	if _, err := s.AcquireObjectBucketWriteFence(ctx, b, token); err != nil {
		t.Fatal(err)
	}
	// Expire only the request-admission grant. The provider request has its
	// own independent receipt and must survive expiry and pruning.
	if _, err := pool.Exec(ctx, "update object_storage_upload_grants set created_at=clock_timestamp()-interval '2 minutes',expires_at=clock_timestamp()-interval '1 minute' where id=$1", g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveObjectUploadGrant(ctx, g.TokenHash); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expired grant resolved: %v", err)
	}
	if count, err := s.PruneExpiredObjectUploadGrants(ctx, api.ObjectUploadGrantPruneBatch); err != nil || count != 1 {
		t.Fatalf("prune = %d %v", count, err)
	}
	fence, err := s.ReadObjectBucketWriteFence(ctx, b, token)
	if err != nil || fence.Requests != 1 {
		t.Fatalf("grant expiry/pruning falsely drained writer: %+v %v", fence, err)
	}
	if err := s.FinishObjectBucketMutation(ctx, writer); err != nil {
		t.Fatal(err)
	}
	fence, err = s.ReadObjectBucketWriteFence(ctx, b, token)
	if err != nil || fence.Requests != 0 {
		t.Fatalf("independent completion lost: %+v %v", fence, err)
	}
}
