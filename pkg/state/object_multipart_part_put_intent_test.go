// adr: 590
package state_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func partBodySHA(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
func TestObjectMultipartPartPutIntentMem(t *testing.T) {
	multipartPartPutIntentSuite(t, state.NewMemStore())
}
func TestObjectMultipartPartPutIntentPG(t *testing.T) {
	st, _ := pgStore(t)
	multipartPartPutIntentSuite(t, st)
}
func multipartPartPutIntentSuite(t *testing.T, st accountingStore) {
	ctx := t.Context()
	b, u := activeTrackedUpload(t, st, "put-intent")
	put := st.(state.ObjectMultipartPartPutMutationStore)
	writers := st.(state.ObjectMultipartPartMutationStore)
	transfers := st.(state.ObjectMultipartTransferStore)
	fences := st.(state.ObjectBucketWriteFenceStore)
	if err := transfers.BeginObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, "original", 1, 3, 100, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	i := state.ObjectMultipartPartPutIntent{Schema: 1, DestinationKey: u.Key, ProviderUploadID: u.ProviderUploadID, ExpectedSize: 3, ExpectedSHA256: partBodySHA("abc")}
	for _, change := range []func(*state.ObjectMultipartPartPutIntent){
		func(i *state.ObjectMultipartPartPutIntent) { i.DestinationKey = "wrong" },
		func(i *state.ObjectMultipartPartPutIntent) { i.ProviderUploadID = "wrong" },
		func(i *state.ObjectMultipartPartPutIntent) { i.ExpectedSize = 4 },
		func(i *state.ObjectMultipartPartPutIntent) { i.ExpectedSHA256 = "invalid" },
		func(i *state.ObjectMultipartPartPutIntent) { i.BodySHA256 = i.ExpectedSHA256 },
	} {
		bad := i
		change(&bad)
		if _, err := put.DispatchObjectMultipartPartPutMutation(ctx, b, u.ID, 1, "original", bad); !errors.Is(err, state.ErrConflict) {
			t.Fatal("invalid PUT intent consumed authority", err)
		}
	}
	hold, err := fences.AcquireObjectBucketWriteFence(ctx, b, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	r, err := put.DispatchObjectMultipartPartPutMutation(ctx, b, u.ID, 1, "original", i)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := put.ReadObjectMultipartPartPutIntent(ctx, r); err != nil || got.BodySHA256 != "" || got.ExpectedSHA256 != i.ExpectedSHA256 || got.ExpectedSize != 3 {
		t.Fatal("invented body observation", got, err)
	}
	wrong := r
	wrong.Bucket.AppID = uuid.NewString()
	if err = put.ObserveObjectMultipartPartBody(ctx, wrong, i.ExpectedSHA256); !errors.Is(err, state.ErrConflict) {
		t.Fatal("cross-scope observation", err)
	}
	if err = put.ObserveObjectMultipartPartBody(ctx, r, partBodySHA("different")); !errors.Is(err, state.ErrConflict) {
		t.Fatal("signed digest mismatch", err)
	}
	for range 2 {
		if err = put.ObserveObjectMultipartPartBody(ctx, r, i.ExpectedSHA256); err != nil {
			t.Fatal("lost original observation", err)
		}
	}
	if err = transfers.SettleObjectMultipartPart(ctx, b.AccountID, u.ID, 1, "original"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("body treated as drain proof", err)
	}
	f, err := fences.ReadObjectBucketWriteFence(ctx, b, hold.Token)
	if err != nil || f.Requests != 2 {
		t.Fatal("observation lost custody", f, err)
	}
	if _, err = writers.DispatchObjectMultipartPartMutation(ctx, b, u.ID, 1, "original"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("intent replayed", err)
	}
	if err = writers.FinishObjectMultipartPartMutation(ctx, r); err != nil {
		t.Fatal(err)
	}
	if got, err := put.ReadObjectMultipartPartPutIntent(ctx, r); err != nil || got.BodySHA256 != i.ExpectedSHA256 {
		t.Fatal("settlement erased identity", got, err)
	}
	if err = put.ObserveObjectMultipartPartBody(ctx, r, i.ExpectedSHA256); !errors.Is(err, state.ErrConflict) {
		t.Fatal("observation reopened settlement", err)
	}
}

func TestObjectMultipartPartPutURLIntentMem(t *testing.T) {
	multipartPartPutURLIntentSuite(t, state.NewMemStore())
}
func TestObjectMultipartPartPutURLIntentPG(t *testing.T) {
	st, _ := pgStore(t)
	multipartPartPutURLIntentSuite(t, st)
}
func multipartPartPutURLIntentSuite(t *testing.T, st accountingStore) {
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	p := accountingPolicy()
	if err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "fixed-put", 10, true, p); err != nil {
		t.Fatal(err)
	}
	sessions := st.(state.ObjectMultipartUploadStore)
	u, err := sessions.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: "fixed-put", SizeBytes: 10, PartSizeBytes: 6, PartCount: 2, ExpiresAt: time.Now().Add(time.Hour)}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "init", state.ObjectMultipartInitiating, nil, false); err != nil {
		t.Fatal(err)
	}
	if err = sessions.ActivateObjectMultipartUpload(ctx, u.ID, "init", "provider"); err != nil {
		t.Fatal(err)
	}
	u, err = sessions.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, hash, _ := api.GenerateAPIKey()
	issuer, err := st.(state.Store).CreateAPIKey(ctx, b.AccountID, hash, "part issuer", []string{api.ScopeStorageWrite})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.(state.ObjectBucketAccessStore).SetObjectBucketAccessGrant(ctx, b.AccountID, b.ID, issuer.ID, state.ObjectBucketPermissionWrite); err != nil {
		t.Fatal(err)
	}
	access, _, err := api.GenerateObjectS3Credential()
	if err != nil {
		t.Fatal(err)
	}
	size := int64(4)
	c := state.ObjectS3Credential{ID: uuid.NewString(), AccountID: b.AccountID, BucketID: b.ID, AccessKeyID: access, SecretSealed: []byte("sealed"), KID: "recipient", Label: "part URL", Permission: state.ObjectBucketPermissionWrite, Status: state.ObjectS3CredentialStatusActive, URL: &state.ObjectURLCapability{Request: api.ObjectSignRequest{Method: "PUT", Key: u.Key, SizeBytes: &size, ExpiresIn: 300, ContentType: "application/octet-stream"}, APIKeyID: issuer.ID, ExpiresAt: time.Now().Add(4 * time.Minute), Multipart: &state.ObjectURLMultipartPart{UploadID: u.ID, PartNumber: 2}}}
	urls := st.(state.ObjectMultipartURLCapabilityStore)
	if _, err = urls.IssueObjectMultipartURLCredential(ctx, c, u, p); err != nil {
		t.Fatal(err)
	}
	if err = urls.BeginObjectURLMultipartPart(ctx, c.ID, "original", p); err != nil {
		t.Fatal(err)
	}
	put := st.(state.ObjectMultipartPartPutMutationStore)
	i := state.ObjectMultipartPartPutIntent{Schema: 1, DestinationKey: u.Key, ProviderUploadID: u.ProviderUploadID, ExpectedSize: 10}
	if _, err = put.DispatchObjectMultipartPartPutMutation(ctx, b, u.ID, 2, "original", i); !errors.Is(err, state.ErrConflict) {
		t.Fatal("URL size exceeded its layout", err)
	}
	fences := st.(state.ObjectBucketWriteFenceStore)
	hold, err := fences.AcquireObjectBucketWriteFence(ctx, b, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	i.ExpectedSize = 4
	r, err := put.DispatchObjectMultipartPartPutMutation(ctx, b, u.ID, 2, "original", i)
	if err != nil {
		t.Fatal("zero incremental grant hid fixed layout authority", err)
	}
	if err = put.ObserveObjectMultipartPartBody(ctx, r, partBodySHA("abcd")); err != nil {
		t.Fatal(err)
	}
	if got, err := fences.ReadObjectBucketWriteFence(ctx, b, hold.Token); err != nil || got.Requests != hold.Requests+1 {
		t.Fatal("URL body observation lost custody", got, err)
	}
	if err = st.(state.ObjectMultipartPartMutationStore).FinishObjectMultipartPartMutation(ctx, r); err != nil {
		t.Fatal(err)
	}
	if got, err := put.ReadObjectMultipartPartPutIntent(ctx, r); err != nil || got.ExpectedSize != 4 || got.BodySHA256 != partBodySHA("abcd") {
		t.Fatal("URL lost exact body identity", got, err)
	}
}
