package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 546
func TestObjectVersionDeleteControlEndToEndPG(t *testing.T) {
	var calls atomic.Int32
	var deleted atomic.Bool
	const native = "private-version/+%?"
	const key = "目录 /+%.txt"
	f := newGatewayRecoveryFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "DELETE" || r.URL.Query().Get("versionId") != native || r.Header.Get("Authorization") == "" {
			t.Error("unowned upstream deletion", r.URL)
			w.WriteHeader(500)
			return
		}
		if !deleted.Swap(true) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		w.Header().Set("X-Amz-Version-Id", native)
		w.WriteHeader(204)
	}), 0)
	ctx := t.Context()
	refs, err := f.st.RecordObjectVersions(ctx, f.account.ID, f.bucket.ID, []state.ObjectVersionIdentity{{Key: key, ProviderVersionID: native}})
	if err != nil {
		t.Fatal(err)
	}
	version := refs[0].ID
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.st.CreateAPIKey(ctx, f.account.ID, hash, "delete-version", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	management := func() *httptest.Server {
		s := newServer(state.NewPgStore(f.pool), slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithObjectStorage(f.registry)
		srv := httptest.NewServer(s.handler())
		t.Cleanup(srv.Close)
		return srv
	}
	srv := management()
	client := api.NewClient(srv.URL, token)
	if _, err = client.DeleteObjectBucketVersion(ctx, f.app.Slug, f.bucket.ID, key, version); err == nil || calls.Load() != 1 {
		t.Fatal("uncertain deletion retried", err, calls.Load())
	}
	srv.Close()
	srv = management()
	client = api.NewClient(srv.URL, token)
	var deletionID string
	if e := f.pool.QueryRow(ctx, `SELECT id::text FROM object_deletions WHERE bucket_id=$1 AND state='dispatched'`, f.bucket.ID).Scan(&deletionID); e != nil {
		t.Fatal(e)
	}
	receipt, e := client.CreateObjectDeletion(ctx, f.app.Slug, f.bucket.ID, api.ObjectDeletionRequest{ID: deletionID, Key: key, VersionID: version})
	if e != nil || receipt.State != "dispatched" || calls.Load() != 1 {
		t.Fatal("pending receipt replay", receipt, e, calls.Load())
	}
	if _, e = f.pool.Exec(ctx, `UPDATE object_deletions SET lease_until=CASE WHEN lease_token='' THEN NULL ELSE now()-interval '1 second' END,retry_at=now() WHERE id=$1`, deletionID); e != nil {
		t.Fatal(e)
	}
	recovery := newServer(state.NewPgStore(f.pool), slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithObjectStorage(f.registry)
	f.enabled.Store(false)
	if e = recovery.reconcileObjectDeletions(ctx, nil); e != nil {
		t.Fatal(e)
	}
	receipt, e = client.GetObjectDeletion(ctx, f.app.Slug, f.bucket.ID, deletionID)
	if e != nil || receipt.State != "completed" || receipt.VersionID != version || calls.Load() != 2 {
		t.Fatal("worker recovery with ingress disabled", receipt, e, calls.Load())
	}
	f.enabled.Store(true)
	result, err := client.DeleteObjectBucketVersion(ctx, f.app.Slug, f.bucket.ID, key, version)
	if err != nil || result.VersionID != version || calls.Load() != 3 {
		t.Fatal("restart retry", result, err, calls.Load())
	}
	for _, tc := range []struct{ key, version string }{{"wrong-key", version}, {key, uuid.NewString()}, {key, "null"}, {key, ""}} {
		if _, err = client.DeleteObjectBucketVersion(ctx, f.app.Slug, f.bucket.ID, tc.key, tc.version); err == nil || calls.Load() != 3 {
			t.Fatal("invalid selector dispatched", tc, err, calls.Load())
		}
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodDelete, srv.URL+"/v1/apps/"+f.app.Slug+"/buckets/"+f.bucket.ID+"/objects?key="+url.QueryEscape(key), nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := srv.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 409 || calls.Load() != 3 {
		t.Fatal("control DELETE bypassed native marker admission", response.StatusCode, calls.Load())
	}
	request.URL.Path += "/versions"
	request.URL.RawQuery = url.Values{"key": {key}, "version_id": {version}}.Encode()
	request.Header.Set("If-Match", `"predicate"`)
	response, err = srv.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 501 || calls.Load() != 3 {
		t.Fatal("control DELETE ignored predicate", response.StatusCode, calls.Load())
	}
	// Storage scope alone cannot cross the bucket grant boundary.
	scopedToken, scopedHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	scopedKey, err := f.st.CreateAPIKey(ctx, f.account.ID, scopedHash, "bucket-writer", []string{api.ScopeStorageWrite})
	if err != nil {
		t.Fatal(err)
	}
	scopedClient := api.NewClient(srv.URL, scopedToken)
	if _, err = scopedClient.DeleteObjectBucketVersion(ctx, f.app.Slug, f.bucket.ID, key, version); err == nil || calls.Load() != 3 {
		t.Fatal("ungranted writer deleted version", err)
	}
	if _, err = client.SetObjectBucketAccessGrant(ctx, f.app.Slug, f.bucket.ID, scopedKey.ID, api.SetObjectBucketAccessGrantRequest{Permission: api.ObjectBucketPermissionWrite}); err != nil {
		t.Fatal(err)
	}
	if _, err = scopedClient.DeleteObjectBucketVersion(ctx, f.app.Slug, f.bucket.ID, key, version); err != nil || calls.Load() != 4 {
		t.Fatal("granted writer", err, calls.Load())
	}
	// Cleanup is still available when object ingress is disabled.
	f.enabled.Store(false)
	if _, err = scopedClient.DeleteObjectBucketVersion(ctx, f.app.Slug, f.bucket.ID, key, version); err != nil || calls.Load() != 5 {
		t.Fatal("disabled ingress blocked cleanup", err, calls.Load())
	}
	other, err := f.st.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, otherHash, _ := api.GenerateAPIKey()
	if _, err = f.st.CreateAPIKey(context.Background(), other.ID, otherHash, "other", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	if _, err = api.NewClient(srv.URL, otherToken).DeleteObjectBucketVersion(ctx, f.app.Slug, f.bucket.ID, key, version); err == nil || calls.Load() != 5 {
		t.Fatal("tenant isolation", err, calls.Load())
	}
}
