package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 417
func TestEncryptedUploadRouteE2EMem(t *testing.T) {
	for _, mode := range []string{"confirmed", "lost", "wrong-key"} {
		t.Run(mode, func(t *testing.T) {
			e := setup(t, api.PlanPro)
			encryptedUploadRouteE2E(t, e.s, e.store, e.acct, mode, nil)
		})
	}
}
func TestEncryptedUploadRouteE2EPG(t *testing.T) {
	for _, mode := range []string{"confirmed", "lost", "wrong-key"} {
		t.Run(mode, func(t *testing.T) {
			e := setupPGHandler(t, api.PlanPro)
			encryptedUploadRouteE2E(t, e.s, e.store, e.acct, mode, e.pool)
		})
	}
}

func encryptedUploadRouteE2E(t *testing.T, s *server, st state.Store, acct state.Account, mode string, pool *pgxpool.Pool) {
	t.Helper()
	_, teardown := withTestIdentities(t)
	defer teardown()
	ctx := t.Context()
	native := &signedURLNative{lost: mode == "lost"}
	var heads atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			heads.Add(1)
		}
		native.serve(w, r)
		if r.Method == http.MethodPut && mode == "wrong-key" {
			w.Header().Set("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id", "private-wrong-key")
		}
	}))
	defer upstream.Close()
	registry, b, backend, _ := seedEncryptionJournalStorage(t, s, st, acct, upstream.URL)
	if err := s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	management := httptest.NewServer(s.handler())
	defer management.Close()
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := st.CreateAPIKey(ctx, acct.ID, hash, "route writer", []string{api.ScopeStorageManage, api.ScopeStorageWrite})
	if err != nil {
		t.Fatal(err)
	}
	client := api.NewClient(management.URL, token)
	selection := api.ObjectEncryption{Algorithm: "aws:kms", KeyID: backend.Encryption.Keys[0].Reference}
	request := api.CreateObjectUploadRouteRequest{Name: "files", BucketID: b.ID, KeyPrefix: "uploads", MaxBytes: 3, Encryption: &selection}
	if _, err = client.CreateObjectUploadRoute(ctx, "encrypted-journal", request); err == nil {
		t.Fatal("route management ignored bucket grant")
	}
	if _, err = st.(state.ObjectBucketAccessStore).SetObjectBucketAccessGrant(ctx, acct.ID, b.ID, key.ID, state.ObjectBucketPermissionWrite); err != nil {
		t.Fatal(err)
	}
	route, err := client.CreateObjectUploadRoute(ctx, "encrypted-journal", request)
	if err != nil || route.Encryption == nil || route.Encryption.KeyID != selection.KeyID {
		t.Fatal("owned route creation", route, err)
	}
	list, err := client.ListObjectUploadRoutes(ctx, "encrypted-journal")
	if err != nil || len(list.Items) != 1 || list.Items[0].Encryption == nil || list.Items[0].Encryption.KeyID != selection.KeyID {
		t.Fatal("owned policy listing", list, err)
	}
	native.mu.Lock()
	probes := native.keys
	native.mu.Unlock()
	if probes != 0 {
		t.Fatal("management made native key probes", probes)
	}
	makeEdge := func(store state.Store, singlePutLimit int64) http.Handler {
		t.Helper()
		placement := registry()
		if singlePutLimit > 0 {
			placement.MaxSinglePutBytes = singlePutLimit
		}
		handler, e := objectstorage.NewUploadHandler(objectstorage.UploadConfig{Store: store.(objectstorage.PublicReadStore), Routes: store.(state.ObjectUploadRouteStore), Buckets: store.(state.ObjectBucketStore), Authenticator: store, Registry: placement, Accounting: store.(state.ObjectStorageAccountingStore), RequestMetrics: store.(state.ObjectStorageProviderUsageStore), AppsDomain: "apps.test", Next: http.NotFoundHandler()})
		if e != nil {
			t.Fatal(e)
		}
		return handler
	}
	edge := httptest.NewServer(makeEdge(st, 2))
	defer edge.Close()
	send := func(method, path, idem, bearer string, extra bool) signedURLResponse {
		t.Helper()
		var body io.Reader
		if method == http.MethodPost {
			body = strings.NewReader("abc")
		}
		r, e := http.NewRequestWithContext(ctx, method, edge.URL+path, body)
		if e != nil {
			t.Fatal(e)
		}
		r.Host = "encrypted-journal.apps.test"
		r.Header.Set("Authorization", "Bearer "+bearer)
		r.Header.Set("Content-Type", "application/octet-stream")
		if idem != "" {
			r.Header.Set("Idempotency-Key", idem)
		}
		if extra {
			r.Header.Set("X-Amz-Server-Side-Encryption", "AES256")
		}
		res, e := edge.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = res.Body.Close() }()
		data, e := io.ReadAll(res.Body)
		if e != nil {
			t.Fatal(e)
		}
		return signedURLResponse{StatusCode: res.StatusCode, Header: res.Header.Clone(), Data: string(data)}
	}
	if res := send(http.MethodPost, "/uploads/files", "current-limit", token, false); res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatal("encrypted route ignored the current single-PUT ceiling", res)
	}
	edge.Close()
	edge = httptest.NewServer(makeEdge(st, 0))
	defer edge.Close()
	if res := send(http.MethodPost, "/uploads/files", "override", token, true); res.StatusCode != http.StatusBadRequest {
		t.Fatal("request overrode route encryption", res)
	}
	response := send(http.MethodPost, "/uploads/files", "once", token, false)
	want := http.StatusCreated
	if mode != "confirmed" {
		want = http.StatusBadGateway
	}
	if response.StatusCode != want || strings.Contains(response.Data, journalNativeKMSKey) || strings.Contains(response.Data, "private-wrong") {
		t.Fatal("encrypted route result", response)
	}
	id := response.Header.Get("X-Gregale-Upload-ID")
	if id == "" {
		t.Fatal("route omitted durable receipt", response)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := send(http.MethodPost, "/uploads/files", "once", token, false)
			expected := http.StatusCreated
			if mode != "confirmed" {
				expected = http.StatusConflict
			}
			if res.StatusCode != expected {
				t.Error("concurrent route replay", res)
			}
		}()
	}
	wg.Wait()
	stored, err := st.(state.ObjectTrackedUploadStore).GetObjectUploadReceipt(ctx, acct.ID, b.AppID, route.ID, key.ID, id)
	if err != nil || stored.Encryption.Selection.KeyID != selection.KeyID {
		t.Fatal("receipt lost owned encryption", stored, err)
	}
	// Existing attempts keep their original selection when route policy changes.
	request.Encryption = nil
	if _, err = client.CreateObjectUploadRoute(ctx, "encrypted-journal", request); err != nil {
		t.Fatal(err)
	}
	native.mu.Lock()
	native.disabled = true
	probes = native.keys
	native.mu.Unlock()
	if pool != nil {
		if _, err = pool.Exec(ctx, `UPDATE object_upload_completions SET recovery_retry_at=now()-interval '1 second' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		st = state.NewPgStore(pool)
	} else {
		st.(*state.MemStore).SetClockForTest(func() time.Time { return time.Now().Add(2 * time.Minute) })
	}
	fresh := newServer(st, s.log, "gregale.dev", noopNotifier{}).WithObjectStorage(registry())
	if err = fresh.reconcileObjectUploads(ctx, nil); err != nil {
		t.Fatal("restart recovery", err)
	}
	edge.Close()
	edge = httptest.NewServer(makeEdge(st, 0))
	defer edge.Close()
	receipt := send(http.MethodGet, "/uploads/files/receipts/"+id, "", token, false)
	var out struct {
		Status     string                `json:"status"`
		ETag       string                `json:"etag"`
		Encryption *api.ObjectEncryption `json:"encryption"`
	}
	if receipt.StatusCode != http.StatusOK || json.Unmarshal([]byte(receipt.Data), &out) != nil || out.Status != "completed" || out.ETag != `"signed-url"` || out.Encryption == nil || out.Encryption.KeyID != selection.KeyID || strings.Contains(receipt.Data, journalNativeKMSKey) {
		t.Fatal("owned recovered receipt", receipt)
	}
	replay := send(http.MethodPost, "/uploads/files", "once", token, false)
	if replay.StatusCode != http.StatusCreated || !strings.Contains(replay.Data, selection.KeyID) {
		t.Fatal("replay used changed policy", replay)
	}
	otherToken, otherHash, _ := api.GenerateAPIKey()
	if _, err = st.CreateAPIKey(ctx, acct.ID, otherHash, "different subject", []string{api.ScopeStorageWrite}); err != nil {
		t.Fatal(err)
	}
	if res := send(http.MethodGet, "/uploads/files/receipts/"+id, "", otherToken, false); res.StatusCode != http.StatusNotFound {
		t.Fatal("foreign principal read receipt", res)
	}
	foreign, err := st.CreateAccount(ctx, "foreign-route-"+mode+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreignToken, foreignHash, _ := api.GenerateAPIKey()
	if _, err = st.CreateAPIKey(ctx, foreign.ID, foreignHash, "foreign", []string{api.ScopeStorageWrite}); err != nil {
		t.Fatal(err)
	}
	if res := send(http.MethodPost, "/uploads/files", "foreign", foreignToken, false); res.StatusCode != http.StatusUnauthorized {
		t.Fatal("foreign account uploaded", res)
	}
	native.mu.Lock()
	if native.writes != 1 || native.keys != probes {
		t.Error("retry or recovery repeated native mutation/key check", native.writes, native.keys, probes)
	}
	native.mu.Unlock()
	// A disabled key rejects before dispatch, but the key probe is still metered.
	request.Encryption = &selection
	if _, err = client.CreateObjectUploadRoute(ctx, "encrypted-journal", request); err != nil {
		t.Fatal(err)
	}
	rejected := send(http.MethodPost, "/uploads/files", "disabled", token, false)
	if rejected.StatusCode != http.StatusBadGateway {
		t.Fatal("disabled key was accepted", rejected)
	}
	failedID := rejected.Header.Get("X-Gregale-Upload-ID")
	failed, err := st.(state.ObjectTrackedUploadStore).GetObjectUploadReceipt(ctx, acct.ID, b.AppID, route.ID, key.ID, failedID)
	if err != nil || failed.Status != "failed" || failed.ErrorCode != "provider_write_rejected" {
		t.Fatal("pre-dispatch key failure lost outcome", failed, err)
	}
	metrics, err := st.(state.ObjectStorageProviderUsageStore).ListObjectStorageProviderRequestMetrics(ctx, backend.ID, backend.Fingerprint, state.ObjectStoragePeriod(time.Now()))
	native.mu.Lock()
	defer native.mu.Unlock()
	if err != nil || len(metrics) != 1 || metrics[0].RequestCount != int64(native.keys+native.writes)+int64(heads.Load()) || native.writes != 1 || native.keys != probes+1 {
		t.Fatal("native route request metering", metrics, err, native.keys, native.writes, heads.Load())
	}
}
