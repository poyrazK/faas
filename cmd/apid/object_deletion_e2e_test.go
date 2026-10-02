package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 405
func TestObjectDeletionRecoveryMissingPlacementPG(t *testing.T) {
	for _, phase := range []string{"prepared", "dispatched"} {
		t.Run(phase, func(t *testing.T) {
			f := newGatewayRecoveryFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("missing placement dispatched a provider request", r.Method, r.URL)
				w.WriteHeader(http.StatusInternalServerError)
			}), 0)
			ctx := t.Context()
			j, created, e := f.st.BeginObjectDeletion(ctx, state.ObjectDeletion{ObjectDeletion: api.ObjectDeletion{ID: uuid.NewString(), BucketID: f.bucket.ID, Key: "key"}, AccountID: f.account.ID, AppID: f.app.ID, Token: uuid.NewString()}, f.policy)
			if e != nil || !created {
				t.Fatal(j, created, e)
			}
			if phase == "dispatched" {
				if j, e = f.st.DispatchObjectDeletion(ctx, j.ID, j.Token, "", nil); e != nil {
					t.Fatal(j, e)
				}
			}
			if _, e = f.pool.Exec(ctx, `UPDATE object_deletions SET lease_until=now()-interval '1 second',retry_at=now() WHERE id=$1`, j.ID); e != nil {
				t.Fatal(e)
			}
			// An empty catalog models removal of this placement, without
			// changing the bucket's immutable placement identity.
			s := newServer(state.NewPgStore(f.pool), slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithObjectStorage(&objectstorage.Registry{Accounting: f.policy})
			if e = s.reconcileObjectDeletions(ctx, nil); e != nil {
				t.Fatal(e)
			}
			j, e = f.st.GetObjectDeletion(ctx, f.account.ID, f.bucket.ID, j.ID)
			if e != nil {
				t.Fatal(e)
			}
			if phase == "prepared" && (j.State != "failed" || j.LastErrorCode != "preparation_expired") || phase == "dispatched" && (j.State != "dispatched" || j.LastErrorCode != "configuration") {
				t.Fatal("missing placement was not settled or durably deferred", j)
			}
			rows, e := f.st.DueObjectDeletions(ctx, api.ObjectDeletionBatch)
			if e != nil || len(rows) != 0 {
				t.Fatal("missing placement monopolized the next recovery batch", rows, e)
			}
		})
	}
}

// adr: 405
func TestObjectDeletionControlRecoveryPG(t *testing.T) {
	var mu sync.Mutex
	deleted := false
	var deletes atomic.Int32
	f := newGatewayRecoveryFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/xml")
		if q.Has("versioning") {
			_, _ = io.WriteString(w, `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`)
			return
		}
		if q.Has("versions") {
			_, _ = io.WriteString(w, `<ListVersionsResult><EncodingType>url</EncodingType><IsTruncated>false</IsTruncated>`)
			if deleted {
				_, _ = io.WriteString(w, `<DeleteMarker><Key>key</Key><VersionId>private-created-marker</VersionId><IsLatest>true</IsLatest><LastModified>2026-10-02T12:00:00Z</LastModified></DeleteMarker>`)
			}
			_, _ = fmt.Fprintf(w, `<Version><Key>key</Key><VersionId>private-before</VersionId><IsLatest>%t</IsLatest><Size>1</Size><ETag>&quot;data&quot;</ETag><LastModified>2026-10-02T11:00:00Z</LastModified></Version></ListVersionsResult>`, !deleted)
			return
		}
		if r.Method != "DELETE" || q.Get("versionId") != "" {
			t.Error("unsafe selector", r.URL)
			w.WriteHeader(500)
			return
		}
		deleted = true
		deletes.Add(1)
		conn, _, e := w.(http.Hijacker).Hijack()
		if e != nil {
			t.Error(e)
			return
		}
		_ = conn.Close()
	}), 0)
	ctx := t.Context()
	v := f.st
	if _, e := v.ObserveObjectBucketVersioning(ctx, f.account.ID, f.app.ID, f.bucket.ID, "Enabled"); e != nil {
		t.Fatal(e)
	}
	if _, e := f.pool.Exec(ctx, `UPDATE object_bucket_versioning SET retry_at=now(),propagation_until=now()-interval '1 second'`); e != nil {
		t.Fatal(e)
	}
	j, e := v.ClaimObjectBucketVersioning(ctx, f.bucket.ID, "configure")
	if e != nil {
		t.Fatal(e)
	}
	j, e = v.AdvanceObjectBucketVersioning(ctx, f.bucket.ID, j.Token, "Enabled")
	if e != nil {
		t.Fatal(e)
	}
	c, e := v.ClaimObjectCapacityReconciliation(ctx, j.CapacityJobID, "inventory")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = v.StageObjectVersionInventoryPage(ctx, c.ID, c.Token, "", []state.ObjectVersionInventoryRecord{{Identity: strings.Repeat("a", 64), Bytes: 1}}); e != nil {
		t.Fatal(e)
	}
	if _, e = f.pool.Exec(ctx, `UPDATE object_bucket_versioning SET retry_at=now()`); e != nil {
		t.Fatal(e)
	}
	j, e = v.ClaimObjectBucketVersioning(ctx, f.bucket.ID, "verify")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = v.AdvanceObjectBucketVersioning(ctx, f.bucket.ID, j.Token, "Enabled"); e != nil {
		t.Fatal(e)
	}
	token, hash, _ := api.GenerateAPIKey()
	if _, e = f.st.CreateAPIKey(ctx, f.account.ID, hash, "owner", api.ScopesAdminOnly); e != nil {
		t.Fatal(e)
	}
	s := newServer(f.st, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithObjectStorage(f.registry)
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)
	client := api.NewClient(srv.URL, token)
	id := uuid.NewString()
	request := api.ObjectDeletionRequest{ID: id, Key: "key"}
	receipt, e := client.CreateObjectDeletion(ctx, f.app.Slug, f.bucket.ID, request)
	if e != nil || receipt.State != "dispatched" {
		t.Fatal(receipt, e)
	}
	receipt, e = client.CreateObjectDeletion(ctx, f.app.Slug, f.bucket.ID, request)
	if e != nil || receipt.State != "dispatched" || deletes.Load() != 1 {
		t.Fatal("replay dispatched", receipt, e, deletes.Load())
	}
	// Reconstruct the store and server; recovery works with customer ingress off.
	f.enabled.Store(false)
	srv.Close()
	s = newServer(state.NewPgStore(f.pool), slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithObjectStorage(f.registry)
	if s.objectStorageEnabled() {
		t.Fatal("recovery fixture unexpectedly enabled ingress")
	}
	srv = httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)
	client = api.NewClient(srv.URL, token)
	if _, e = f.pool.Exec(ctx, `UPDATE object_deletions SET retry_at=now() WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	if e = s.reconcileObjectDeletions(ctx, nil); e != nil {
		t.Fatal(e)
	}
	receipt, e = client.GetObjectDeletion(ctx, f.app.Slug, f.bucket.ID, id)
	if e != nil || receipt.State != "completed" || !receipt.DeleteMarker || !state.ValidObjectVersionID(receipt.VersionID) || deletes.Load() != 1 {
		t.Fatal(receipt, e, deletes.Load())
	}
	// A credential with storage scope needs the bucket write grant for receipts.
	limited, limitedHash, _ := api.GenerateAPIKey()
	credential, e := f.st.CreateAPIKey(ctx, f.account.ID, limitedHash, "writer", []string{api.ScopeStorageWrite})
	if e != nil {
		t.Fatal(e)
	}
	limitedClient := api.NewClient(srv.URL, limited)
	if _, e = limitedClient.GetObjectDeletion(ctx, f.app.Slug, f.bucket.ID, id); e == nil {
		t.Fatal("ungranted key read receipt")
	}
	if _, e = client.SetObjectBucketAccessGrant(ctx, f.app.Slug, f.bucket.ID, credential.ID, api.SetObjectBucketAccessGrantRequest{Permission: state.ObjectBucketPermissionWrite}); e != nil {
		t.Fatal(e)
	}
	if _, e = limitedClient.GetObjectDeletion(ctx, f.app.Slug, f.bucket.ID, id); e != nil {
		t.Fatal(e)
	}
	other, e := f.st.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if e != nil {
		t.Fatal(e)
	}
	foreign, foreignHash, _ := api.GenerateAPIKey()
	if _, e = f.st.CreateAPIKey(context.Background(), other.ID, foreignHash, "other", api.ScopesAdminOnly); e != nil {
		t.Fatal(e)
	}
	if _, e = api.NewClient(srv.URL, foreign).GetObjectDeletion(ctx, f.app.Slug, f.bucket.ID, id); e == nil {
		t.Fatal("foreign account read receipt")
	}
}
