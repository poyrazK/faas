//go:build !no_pg

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 620
func TestLifecycleProtectionCustomerRecoveryPG(t *testing.T) {
	var held, deleted atomic.Bool
	held.Store(true)
	var deletes, requests atomic.Int32
	modified := time.Now().UTC().AddDate(0, 0, -10).Truncate(time.Millisecond)
	f := newGatewayRecoveryFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/xml")
		switch {
		case q.Has("object-lock"):
			_, _ = io.WriteString(w, `<ObjectLockConfiguration><ObjectLockEnabled>Enabled</ObjectLockEnabled></ObjectLockConfiguration>`)
		case q.Has("versioning"):
			_, _ = io.WriteString(w, `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`)
		case q.Has("legal-hold"):
			if q.Get("versionId") != "private-target" {
				t.Error("hold selector changed", q)
			}
			status := "OFF"
			if held.Load() {
				status = "ON"
			}
			w.Header().Set("X-Amz-Version-Id", "private-target")
			_, _ = fmt.Fprintf(w, `<LegalHold><Status>%s</Status></LegalHold>`, status)
		case q.Has("retention"):
			w.Header().Set("X-Amz-Version-Id", "private-target")
			_, _ = io.WriteString(w, `<Retention/>`)
		case q.Has("versions"):
			_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>false</IsTruncated>`)
			if q.Get("key-marker") == "" {
				_, _ = fmt.Fprintf(w, `<Version><Key>key</Key><VersionId>private-current</VersionId><IsLatest>true</IsLatest><LastModified>%s</LastModified><Size>1</Size><ETag>&quot;current&quot;</ETag></Version>`, modified.AddDate(0, 0, 5).Format(time.RFC3339Nano))
				if q.Get("prefix") != "" && !deleted.Load() {
					_, _ = fmt.Fprintf(w, `<Version><Key>key</Key><VersionId>private-target</VersionId><IsLatest>false</IsLatest><LastModified>%s</LastModified><Size>1</Size><ETag>&quot;target&quot;</ETag></Version>`, modified.Format(time.RFC3339Nano))
				}
			}
			_, _ = io.WriteString(w, `</ListVersionsResult>`)
		case r.Method == http.MethodDelete && q.Get("versionId") == "private-target" && !held.Load():
			if r.Header.Get("X-Amz-Bypass-Governance-Retention") != "" {
				t.Error("lifecycle bypassed governance")
			}
			deleted.Store(true)
			deletes.Add(1)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
		default:
			t.Error("unexpected lifecycle mutation", r.Method, q)
			w.WriteHeader(500)
		}
	}), 8)
	ctx := t.Context()
	lock := f.st
	cfg := api.ObjectBucketObjectLockConfiguration{Enabled: true}
	if _, err := lock.RequestObjectBucketObjectLock(ctx, f.account.ID, f.app.ID, f.bucket.ID, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.ObserveObjectBucketVersioning(ctx, f.account.ID, f.app.ID, f.bucket.ID, "Enabled"); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if _, err := f.pool.Exec(ctx, `UPDATE object_bucket_versioning SET retry_at=clock_timestamp(),propagation_until=CASE WHEN state IN ('waiting','propagating') THEN clock_timestamp()-interval '1 second' ELSE propagation_until END WHERE bucket_id=$1`, f.bucket.ID); err != nil {
			t.Fatal(err)
		}
		v, err := f.st.ClaimObjectBucketVersioning(ctx, f.bucket.ID, fmt.Sprint("versioning", i))
		if err != nil {
			t.Fatal(err)
		}
		v, err = f.st.AdvanceObjectBucketVersioning(ctx, f.bucket.ID, v.Token, "Enabled")
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			c, err := f.st.ClaimObjectCapacityReconciliation(ctx, v.CapacityJobID, "inventory")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.st.StageObjectVersionInventoryPage(ctx, c.ID, c.Token, "", []state.ObjectVersionInventoryRecord{{Identity: strings.Repeat("a", 64), Bytes: 4}, {Identity: strings.Repeat("b", 64), Bytes: 4}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	j, err := f.st.ClaimObjectBucketObjectLock(ctx, f.bucket.ID, "lock")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.st.FinishObjectBucketObjectLock(ctx, f.bucket.ID, j.Token, cfg); err != nil {
		t.Fatal(err)
	}
	s, client := lifecycleCustomerClient(t, f)
	if _, err = client.PutObjectBucketLifecycle(ctx, f.app.Slug, f.bucket.ID, api.ObjectBucketLifecycleRequest{Rules: []api.ObjectLifecycleRule{{ID: "expire", Status: "Enabled", NoncurrentVersionExpiration: &api.ObjectLifecycleNoncurrentExpiration{NoncurrentDays: 1}}}}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = s.reconcileObjectLifecycle(ctx, nil); err != nil {
			t.Fatal(err)
		}
	}
	if deletes.Load() != 0 {
		t.Fatal("held version expired")
	}
	held.Store(false)
	if _, err = f.pool.Exec(ctx, `UPDATE object_bucket_lifecycle SET next_scan_at=clock_timestamp() WHERE bucket_id=$1`, f.bucket.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.reconcileObjectLifecycle(ctx, nil); err != nil || deletes.Load() != 1 {
		t.Fatal("released version not dispatched", err, deletes.Load())
	}
	var id string
	if err = f.pool.QueryRow(ctx, `SELECT id::text FROM object_deletions WHERE bucket_id=$1 AND state='dispatched'`, f.bucket.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE object_deletions SET lease_until=NULL,lease_token='',retry_at=clock_timestamp() WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	// A reconstructed daemon with new ingress and Object Lock enrollment off
	// still settles accepted custody from exact native absence, without DELETE.
	s = newServer(state.NewPgStore(f.pool), slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithObjectStorage(f.registry)
	if err = s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("false")); err != nil {
		t.Fatal(err)
	}
	if err = s.reconcileObjectDeletions(ctx, nil); err != nil || deletes.Load() != 1 {
		t.Fatal("disabled recovery repeated delete", err, deletes.Load())
	}
	receipt, err := client.GetObjectDeletion(ctx, f.app.Slug, f.bucket.ID, id)
	if err != nil || receipt.State != "completed" || strings.Contains(receipt.VersionID, "private") {
		t.Fatal("owned progress did not expose recovered receipt", receipt, err)
	}
	usage, err := f.st.ObjectUsage(ctx, f.account.ID, time.Now())
	if err != nil || usage.Buckets[0].BaselineBytes != 8 || usage.Buckets[0].BaselineKeys != 2 {
		t.Fatal("receipt prematurely refunded baseline", usage, err)
	}
	metrics, err := f.st.ListObjectStorageProviderRequestMetrics(ctx, f.bucket.BackendID, f.bucket.BackendFingerprint, state.ObjectStoragePeriod(time.Now()))
	if err != nil || len(metrics) != 1 || metrics[0].RequestCount != int64(requests.Load()) {
		t.Fatal("protection reads escaped metering", metrics, err, requests.Load())
	}
}
