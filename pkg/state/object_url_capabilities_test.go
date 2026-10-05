package state_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type objectURLTestStore interface {
	accountingStore
	state.Store
	state.ObjectS3CredentialBindingStore
	state.ObjectURLCapabilityStore
	state.ObjectTrackedGatewayUploadStore
	state.ObjectBucketAccessStore
}

// adr: 557
func TestObjectURLCapabilitiesMem(t *testing.T) { objectURLCapabilitySuite(t, state.NewMemStore()) }
func TestObjectURLCapabilitiesPG(t *testing.T)  { st, _ := pgStore(t); objectURLCapabilitySuite(t, st) }

func objectURLCapabilitySuite(t *testing.T, st objectURLTestStore) {
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	p := accountingPolicy()
	_, hash, _ := api.GenerateAPIKey()
	k, err := st.CreateAPIKey(ctx, b.AccountID, hash, "issuer", []string{api.ScopeStorageWrite})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.SetObjectBucketAccessGrant(ctx, b.AccountID, b.ID, k.ID, state.ObjectBucketPermissionWrite); err != nil {
		t.Fatal(err)
	}
	access, _, err := api.GenerateObjectS3Credential()
	if err != nil {
		t.Fatal(err)
	}
	size := int64(10)
	c := state.ObjectS3Credential{ID: uuid.NewString(), AccountID: b.AccountID, BucketID: b.ID, AccessKeyID: access, SecretSealed: []byte("sealed"), KID: "recipient", Label: "url", Permission: state.ObjectBucketPermissionWrite, Status: state.ObjectS3CredentialStatusActive}
	c.URL = &state.ObjectURLCapability{Request: api.ObjectSignRequest{Method: "PUT", Key: "object", ExpiresIn: 300, SizeBytes: &size, ContentType: "application/octet-stream", Metadata: map[string]string{"color": "blue"}, Encryption: &api.ObjectEncryption{Algorithm: "AES256"}}, APIKeyID: k.ID, ReceiptID: uuid.NewString(), ExpiresAt: time.Now().UTC().Add(4 * time.Minute).Truncate(time.Microsecond)}
	intent := state.ObjectUploadCompletion{ID: c.URL.ReceiptID, AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: c.ID, Key: "object", Bytes: 10, ContentType: c.URL.Request.ContentType, Status: "pending", Encryption: state.ObjectEncryptionSnapshot{AccountID: uuid.MustParse(b.AccountID).String(), Selection: *c.URL.Request.Encryption}}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, e := st.IssueObjectURLCredential(ctx, c, intent, p)
			if e == nil {
				wins.Add(1)
			} else if !errors.Is(e, state.ErrConflict) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("duplicate URL admission", wins.Load())
	}
	saved, err := st.GetObjectUploadReceipt(ctx, b.AccountID, b.AppID, "", c.ID, intent.ID)
	if err != nil || !saved.RecoveryRetryAt.Equal(c.URL.ExpiresAt) || saved.WritePhase != state.ObjectUploadPrepared {
		t.Fatal("receipt deadline", saved, err)
	}
	u, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil || u.Authorizations != 1 || u.Buckets[0].GrantedBytes != 10 {
		t.Fatal("URL double admission", u, err)
	}
	items, err := st.ListObjectS3Credentials(ctx, b.AccountID, b.ID)
	if err != nil || len(items) != 0 {
		t.Fatal("URL leaked into inventory", items, err)
	}
	if _, err = st.GetObjectS3Credential(ctx, b.AccountID, b.ID, c.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("URL projected into binding", err)
	}
	resolved, _, err := st.ResolveObjectS3Credential(ctx, access)
	if err != nil || resolved.URL == nil || resolved.URL.Request.Metadata["color"] != "blue" {
		t.Fatal("URL descriptor lost", err)
	}
	resolved.URL.Request.Metadata["color"] = "red"
	*resolved.URL.Request.SizeBytes = 99
	resolved, _, err = st.ResolveObjectS3Credential(ctx, access)
	if err != nil || resolved.URL.Request.Metadata["color"] != "blue" || *resolved.URL.Request.SizeBytes != 10 {
		t.Fatal("URL aliases durable authority", err)
	}
	if _, err = st.CreateObjectS3Credential(ctx, c, 10); !errors.Is(err, state.ErrConflict) {
		t.Fatal("ordinary create bypass", err)
	}
	other := intent
	other.ID = uuid.NewString()
	if _, err = st.BeginTrackedGatewayUpload(ctx, other, p); !errors.Is(err, state.ErrConflict) {
		t.Fatal("old gateway admitted second write", err)
	}
	if err = st.DeleteObjectBucketAccessGrant(ctx, b.AccountID, b.ID, k.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = st.ResolveObjectS3Credential(ctx, access); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("revoked grant resolved URL", err)
	}
	if _, err = st.DispatchObjectURLUpload(ctx, b.AccountID, b.ID, intent.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("revoked grant dispatched URL", err)
	}
	if _, err = st.SetObjectBucketAccessGrant(ctx, b.AccountID, b.ID, k.ID, state.ObjectBucketPermissionWrite); err != nil {
		t.Fatal(err)
	}
	wins.Store(0)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := st.DispatchObjectURLUpload(ctx, b.AccountID, b.ID, intent.ID)
			if e == nil {
				wins.Add(1)
			} else if !errors.Is(e, state.ErrConflict) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("duplicate URL dispatch", wins.Load())
	}
	if err = st.DeleteAPIKey(ctx, b.AccountID, k.ID); err != nil {
		t.Fatal("issuer deletion blocked by URL", err)
	}
	if _, _, err = st.ResolveObjectS3Credential(ctx, access); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("deleted issuer resolved URL", err)
	}
	done := saved
	done.Status = "completed"
	done.ETag = `"object"`
	done.VerifiedEncryption = done.Encryption.Selection
	if _, err = st.FinishTrackedObjectUpload(ctx, done); err != nil {
		t.Fatal("inflight settlement blocked after revocation", err)
	}
	if _, err = st.DispatchObjectURLUpload(ctx, b.AccountID, b.ID, intent.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("settled URL replayed", err)
	}
	// A URL still being staged cannot dispatch once expiry makes its
	// prepared journal eligible for recovery.
	expired := c
	expired.ID = uuid.NewString()
	expired.AccessKeyID, _, err = api.GenerateObjectS3Credential()
	if err != nil {
		t.Fatal(err)
	}
	expired.URL = c.URL.Clone()
	expired.URL.APIKeyID = ""
	expired.URL.ReceiptID = uuid.NewString()
	expired.URL.Request.Key = "expired"
	expired.URL.Request.ExpiresIn = 1
	expired.URL.ExpiresAt = time.Now().Add(300 * time.Millisecond).UTC().Truncate(time.Microsecond)
	prepared := intent
	prepared.ID = expired.URL.ReceiptID
	prepared.SubjectID = expired.ID
	prepared.Key = "expired"
	if _, prepared, err = st.IssueObjectURLCredential(ctx, expired, prepared, p); err != nil {
		t.Fatal(err)
	}
	due, err := st.DueTrackedObjectUploads(ctx, api.ObjectUploadRecoveryBatch)
	if err != nil || len(due) != 0 {
		t.Fatal("prepared URL recovered before expiry", due, err)
	}
	time.Sleep(time.Until(expired.URL.ExpiresAt) + 10*time.Millisecond)
	if _, _, err = st.ResolveObjectS3Credential(ctx, expired.AccessKeyID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("expired URL resolved", err)
	}
	if _, err = st.DispatchObjectURLUpload(ctx, b.AccountID, b.ID, prepared.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("expired URL dispatched", err)
	}
	failed, err := st.ClaimTrackedObjectUploadRecovery(ctx, b.AccountID, b.ID, prepared.ID, "expiry")
	if err != nil || failed.WritePhase != state.ObjectUploadSettled || failed.Status != "failed" || failed.ErrorCode != "preparation_expired" {
		t.Fatal("prepared URL expiry recovery", failed, err)
	}
	metrics, err := st.(state.ObjectStorageProviderUsageStore).ListObjectStorageProviderRequestMetrics(ctx, b.BackendID, b.BackendFingerprint, state.ObjectStoragePeriod(time.Now()))
	if err != nil || len(metrics) != 1 || metrics[0].RequestCount != 1 {
		t.Fatal("losing or expired URL metered a native attempt", metrics, err)
	}
}
