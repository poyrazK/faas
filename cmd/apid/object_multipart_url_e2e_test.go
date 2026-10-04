package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 416
func TestControlMultipartURLE2EMem(t *testing.T) {
	e := setup(t, api.PlanPro)
	controlMultipartURLE2E(t, e.s, e.store, e.acct, e.key, nil)
}
func TestControlMultipartURLE2EPG(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	controlMultipartURLE2E(t, e.s, e.store, e.acct, e.key, e.pool)
}

func controlMultipartURLE2E(t *testing.T, s *server, st state.Store, acct state.Account, bearer string, pool *pgxpool.Pool) {
	t.Helper()
	identity, teardown := withTestIdentities(t)
	defer teardown()
	native := &encryptionJournalHTTP{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { native.serve(t, w, r) }))
	defer upstream.Close()
	registry, b, backend, _ := seedEncryptionJournalStorage(t, s, st, acct, upstream.URL)
	if err := s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	var gateway http.Handler
	public := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gateway.ServeHTTP(w, r) }))
	defer public.Close()
	s.objectStorage.PublicEndpoint = public.URL
	h, err := s3gateway.New(s3gateway.Config{Registry: s.objectStorage, Store: st.(s3gateway.Store), RequestMetrics: st.(state.ObjectStorageProviderUsageStore), OpenSecret: func(blob []byte) (string, error) {
		ns, plain, e := secretbox.OpenBytes(identity, blob)
		if e != nil {
			return "", e
		}
		if ns != s3gateway.CredentialSecretNamespace {
			return "", errors.New("invalid URL secret namespace")
		}
		return string(plain), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	gateway = h
	management := httptest.NewServer(s.handler())
	defer management.Close()
	client := api.NewClient(management.URL, bearer)
	ctx := t.Context()
	selection := api.ObjectEncryption{Algorithm: "aws:kms", KeyID: backend.Encryption.Keys[0].Reference}
	u, err := client.CreateObjectMultipartUpload(ctx, "encrypted-journal", b.ID, api.CreateObjectMultipartUploadRequest{Key: "encrypted", SizeBytes: 3, ContentType: "application/octet-stream", Encryption: &selection})
	if err != nil || u.State != state.ObjectMultipartActive || u.Encryption == nil || u.Encryption.KeyID != selection.KeyID {
		t.Fatal("control encrypted initiation", u, err)
	}
	scoped, hash, _ := api.GenerateAPIKey()
	issuer, err := st.CreateAPIKey(ctx, acct.ID, hash, "part writer", []string{api.ScopeStorageWrite})
	if err != nil {
		t.Fatal(err)
	}
	access := st.(state.ObjectBucketAccessStore)
	if _, err = access.SetObjectBucketAccessGrant(ctx, acct.ID, b.ID, issuer.ID, state.ObjectBucketPermissionWrite); err != nil {
		t.Fatal(err)
	}
	writer := api.NewClient(management.URL, scoped)
	signed, err := writer.SignObjectMultipartPart(ctx, "encrypted-journal", b.ID, u.ID, 1, api.ObjectMultipartPartSignRequest{ExpiresIn: 60})
	if err != nil || !strings.HasPrefix(signed.URL, public.URL+"/assets/") || strings.Contains(signed.URL, "native-upload") || strings.Contains(signed.URL, journalNativeKMSKey) {
		t.Fatal("native part authority escaped", signed, err)
	}
	send := func(extra bool) signedURLResponse {
		t.Helper()
		r, e := http.NewRequestWithContext(ctx, signed.Method, signed.URL, strings.NewReader("abc"))
		if e != nil {
			t.Fatal(e)
		}
		for k, v := range signed.Headers {
			r.Header.Set(k, v)
		}
		if extra {
			r.Header.Set("X-Amz-Server-Side-Encryption", "AES256")
		}
		response, e := public.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = response.Body.Close() }()
		data, e := io.ReadAll(response.Body)
		if e != nil {
			t.Fatal(e)
		}
		return signedURLResponse{StatusCode: response.StatusCode, Header: response.Header.Clone(), Data: string(data)}
	}
	if response := send(true); response.StatusCode != http.StatusForbidden {
		t.Fatal("part URL allowed initiation cipher headers", response)
	}
	response := send(false)
	if response.StatusCode != http.StatusOK || response.Header.Get("ETag") != `"part"` || response.Header.Get("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id") != selection.KeyID || strings.Contains(response.Data, journalNativeKMSKey) {
		t.Fatal("owned part acknowledgment", response)
	}
	if err = access.DeleteObjectBucketAccessGrant(ctx, acct.ID, b.ID, issuer.ID); err != nil {
		t.Fatal(err)
	}
	if response = send(false); response.StatusCode != http.StatusForbidden {
		t.Fatal("revoked grant allowed a part retry", response)
	}
	if _, err = access.SetObjectBucketAccessGrant(ctx, acct.ID, b.ID, issuer.ID, state.ObjectBucketPermissionWrite); err != nil {
		t.Fatal(err)
	}
	if response = send(false); response.StatusCode != http.StatusOK {
		t.Fatal("sequential part replacement failed", response)
	}
	parts, err := client.ListObjectMultipartParts(ctx, "encrypted-journal", b.ID, u.ID, 0, 1)
	if err != nil || len(parts.Items) != 1 || parts.Items[0].ETag != `"part"` {
		t.Fatal("part discovery", parts, err)
	}
	complete := api.CompleteObjectMultipartUploadRequest{Parts: []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: `"part"`}}}
	if _, err = client.CompleteObjectMultipartUpload(ctx, "encrypted-journal", b.ID, u.ID, complete); err == nil {
		t.Fatal("lost completion ACK was accepted")
	}
	native.mu.Lock()
	native.disabled = true
	probes := native.keyChecks
	native.mu.Unlock()
	if pool != nil {
		if _, err = pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET retry_at=now()-interval '1 second' WHERE id=$1`, u.ID); err != nil {
			t.Fatal(err)
		}
		st = state.NewPgStore(pool)
	} else {
		st.(*state.MemStore).SetClockForTest(func() time.Time { return time.Now().Add(2 * time.Minute) })
	}
	fresh := newServer(st, s.log, "gregale.dev", noopNotifier{}).WithObjectStorage(registry())
	if err = fresh.reconcileObjectMultipartUploads(ctx, nil); err != nil {
		t.Fatal("restart recovery", err)
	}
	done, err := client.GetObjectMultipartUpload(ctx, "encrypted-journal", b.ID, u.ID)
	if err != nil || done.State != state.ObjectMultipartCompleted || done.ETag != `"completed"` || done.VersionID == "native-private-version" || done.Encryption == nil || done.Encryption.KeyID != selection.KeyID {
		t.Fatal("recovered owned completion", done, err)
	}
	if _, err = client.CompleteObjectMultipartUpload(ctx, "encrypted-journal", b.ID, u.ID, complete); err != nil {
		t.Fatal("terminal completion replay", err)
	}
	if response = send(false); response.StatusCode == http.StatusOK {
		t.Fatal("part URL wrote after completion")
	}
	native.mu.Lock()
	defer native.mu.Unlock()
	if native.partWrites != 2 || native.creates != 1 || native.keyChecks != probes {
		t.Fatal("replay/recovery dispatched another mutation or key probe", native.partWrites, native.creates, native.keyChecks, probes)
	}
}
