// adr: 583
package state_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func uploadGrantSpec(b state.ObjectBucket) state.ObjectUploadGrant {
	token := sha256.Sum256([]byte(uuid.NewString()))
	return state.ObjectUploadGrant{ID: uuid.NewString(), Bucket: b, Kind: state.ObjectUploadGrantPut, Key: "path/file", SizeBytes: 3,
		TokenHash: hex.EncodeToString(token[:]), Headers: map[string]string{"Content-Length": "3", "Content-Type": "text/plain", "X-Amz-Meta-Test": "frozen"}}
}

func TestObjectUploadGrantMem(t *testing.T) { objectUploadGrantContract(t, state.NewMemStore()) }
func TestObjectUploadGrantPG(t *testing.T)  { s, _ := pgStore(t); objectUploadGrantContract(t, s) }

func objectUploadGrantContract(t *testing.T, base state.Store) {
	t.Helper()
	ctx := context.Background()
	b := mutationBucketFixture(t, base)
	st := base.(state.ObjectUploadGrantStore)
	spec := uploadGrantSpec(b)
	g, err := st.CreateObjectUploadGrant(ctx, spec, api.MaxObjectSignedURLExpiresSeconds)
	if err != nil || g.ID != spec.ID || g.ExpiresAt.Sub(g.CreatedAt) != 900*time.Second {
		t.Fatalf("create = %+v %v", g, err)
	}
	g.Headers["X-Amz-Meta-Test"] = "changed"
	resolved, err := st.ResolveObjectUploadGrant(ctx, spec.TokenHash)
	if err != nil || resolved.Headers["X-Amz-Meta-Test"] != "frozen" || resolved.Bucket.ID != b.ID {
		t.Fatalf("grant was mutable: %+v %v", resolved, err)
	}
	if _, err := st.ResolveObjectUploadGrant(ctx, uploadGrantSpec(b).TokenHash); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unknown capability = %v", err)
	}
	fences := base.(state.ObjectBucketWriteFenceStore)
	token := uuid.NewString()
	fence, err := fences.AcquireObjectBucketWriteFence(ctx, b, token)
	if err != nil || fence.Requests != 0 || fence.NativeGrants != 0 {
		t.Fatalf("broker grants were confused with native writers: %+v %v", fence, err)
	}
	if _, err := st.CreateObjectUploadGrant(ctx, uploadGrantSpec(b), 60); !errors.Is(err, state.ErrObjectBucketWriteFenced) {
		t.Fatalf("fenced grant issued: %v", err)
	}
	// Resolving an earlier URL is harmless. Its actual write must pass the
	// separate source guard, which prevents provider IO while fenced.
	if _, err := st.ResolveObjectUploadGrant(ctx, spec.TokenHash); err != nil {
		t.Fatal(err)
	}
	if _, err := fences.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationRequest); !errors.Is(err, state.ErrObjectBucketWriteFenced) {
		t.Fatalf("earlier URL bypassed write fence: %v", err)
	}
	if err := fences.ReleaseObjectBucketWriteFence(ctx, b, token); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*state.ObjectUploadGrant){
		func(g *state.ObjectUploadGrant) { g.Bucket.PhysicalName += "-other" },
		func(g *state.ObjectUploadGrant) { g.Bucket.AccountID = uuid.NewString() },
		func(g *state.ObjectUploadGrant) { g.Headers["Host"] = "other.test" },
		func(g *state.ObjectUploadGrant) { g.Headers["Content-Length"] = "4" },
		func(g *state.ObjectUploadGrant) { g.Headers["X-Amz-Meta-Test"] = "bad\r\nheader" },
		func(g *state.ObjectUploadGrant) { g.UploadID = uuid.NewString() },
	} {
		bad := uploadGrantSpec(b)
		change(&bad)
		if _, err := st.CreateObjectUploadGrant(ctx, bad, 60); err == nil {
			t.Fatalf("invalid grant accepted: %+v", bad)
		}
	}
	for _, ttl := range []int{0, -1, api.MaxObjectSignedURLExpiresSeconds + 1} {
		if _, err := st.CreateObjectUploadGrant(ctx, uploadGrantSpec(b), ttl); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("TTL %d accepted: %v", ttl, err)
		}
	}
	if count, err := st.PruneExpiredObjectUploadGrants(ctx, api.ObjectUploadGrantPruneBatch); err != nil || count != 0 {
		t.Fatalf("live grant pruned: %d %v", count, err)
	}
	if _, err := base.(state.ObjectBucketStore).ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, uuid.NewString(), "deleting"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ResolveObjectUploadGrant(ctx, spec.TokenHash); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted source grant resolved: %v", err)
	}
}

func TestObjectUploadGrantMultipartMem(t *testing.T) {
	objectUploadGrantMultipartContract(t, state.NewMemStore())
}
func TestObjectUploadGrantMultipartPG(t *testing.T) {
	s, _ := pgStore(t)
	objectUploadGrantMultipartContract(t, s)
}

func objectUploadGrantMultipartContract(t *testing.T, base state.Store) {
	t.Helper()
	ctx := context.Background()
	b := mutationBucketFixture(t, base)
	uploads := base.(state.ObjectMultipartUploadStore)
	u, err := uploads.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: "part/file", SizeBytes: 8, PartSizeBytes: 5, PartCount: 2, ExpiresAt: time.Now().UTC().Add(time.Minute)}, api.MaxActiveMultipartUploadsPerBucket)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	if _, err := uploads.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, token, state.ObjectMultipartInitiating, nil, false); err != nil {
		t.Fatal(err)
	}
	if err := uploads.ActivateObjectMultipartUpload(ctx, u.ID, token, "provider-upload"); err != nil {
		t.Fatal(err)
	}
	st := base.(state.ObjectUploadGrantStore)
	spec := uploadGrantSpec(b)
	spec.Kind = state.ObjectUploadGrantMultipartPart
	spec.Key = u.Key
	spec.UploadID = u.ID
	spec.ProviderUploadID = "provider-upload"
	spec.PartNumber = 2
	g, err := st.CreateObjectUploadGrant(ctx, spec, 900)
	if err != nil || g.ExpiresAt.After(u.ExpiresAt) {
		t.Fatalf("part expiry ignored session: %+v %v", g, err)
	}
	for _, change := range []func(*state.ObjectUploadGrant){
		func(g *state.ObjectUploadGrant) { g.ProviderUploadID = "another-upload" },
		func(g *state.ObjectUploadGrant) { g.Key = "another-key" },
		func(g *state.ObjectUploadGrant) { g.PartNumber = 1 },
		func(g *state.ObjectUploadGrant) { g.PartNumber = 3 },
	} {
		bad := spec
		bad.ID = uuid.NewString()
		hash := sha256.Sum256([]byte(bad.ID))
		bad.TokenHash = hex.EncodeToString(hash[:])
		change(&bad)
		if _, err := st.CreateObjectUploadGrant(ctx, bad, 60); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("unbound part grant accepted: %+v %v", bad, err)
		}
	}
	if _, err := uploads.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, uuid.NewString(), state.ObjectMultipartAborting, nil, false); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ResolveObjectUploadGrant(ctx, g.TokenHash); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("grant survived session abort: %v", err)
	}
}
