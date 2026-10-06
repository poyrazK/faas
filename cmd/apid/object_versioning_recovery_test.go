package main

import (
	"encoding/xml"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// SDK and typed control client -> real adapter -> local HTTP provider -> PG,
// including lost configuration ack, paged inventory and a rebuilt worker.
func TestBucketVersioningRecoveryEndToEndPG(t *testing.T) {
	var mu sync.Mutex
	status := ""
	configPuts, pages, writes, requests := 0, 0, 0, 0
	pageFailed := false
	f := newGatewayRecoveryFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		w.Header().Set("Content-Type", "application/xml")
		q := r.URL.Query()
		switch {
		case q.Has("versioning") && r.Method == http.MethodGet:
			if status == "" {
				_, _ = io.WriteString(w, `<VersioningConfiguration/>`)
			} else {
				_, _ = fmt.Fprintf(w, `<VersioningConfiguration><Status>%s</Status></VersioningConfiguration>`, status)
			}
		case q.Has("versioning") && r.Method == http.MethodPut:
			var in struct{ Status string }
			if xml.NewDecoder(r.Body).Decode(&in) != nil {
				t.Error("bad provider configuration")
			}
			status = in.Status
			configPuts++
			if configPuts == 1 {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = conn.Close()
			}
		case q.Has("versions") && r.Method == http.MethodGet:
			pages++
			if q.Get("key-marker") == "" {
				_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>true</IsTruncated><NextKeyMarker>key</NextKeyMarker><NextVersionIdMarker>private-marker</NextVersionIdMarker><Version><Key>key</Key><VersionId>null</VersionId><Size>5</Size></Version><DeleteMarker><Key>key</Key><VersionId>private-marker</VersionId></DeleteMarker></ListVersionsResult>`)
			} else if !pageFailed {
				pageFailed = true
				w.WriteHeader(500)
				_, _ = io.WriteString(w, `<Error><Code>InternalError</Code></Error>`)
			} else {
				if q.Get("version-id-marker") != "private-marker" {
					t.Error("lost paired cursor")
				}
				_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>false</IsTruncated><Version><Key>key</Key><VersionId>private-old</VersionId><Size>85</Size></Version></ListVersionsResult>`)
			}
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/key"):
			writes++
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("ETag", `"new"`)
			w.Header().Set("X-Amz-Version-Id", fmt.Sprint("private-new-", writes))
		default:
			t.Errorf("unsafe provider request %s %s", r.Method, r.URL)
			w.WriteHeader(500)
		}
	}), 0)
	ctx := t.Context()
	restarted := func() *server {
		return newServer(state.NewPgStore(f.pool), slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithObjectStorage(f.registry)
	}
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.st.CreateAPIKey(ctx, f.account.ID, hash, "versioning", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	management := httptest.NewServer(restarted().handler())
	t.Cleanup(management.Close)
	client := api.NewClient(management.URL, plaintext)
	j, err := client.PutObjectBucketVersioning(ctx, f.app.Slug, f.bucket.ID, "Enabled")
	if err != nil || j.State != "waiting" {
		t.Fatal(j, err)
	}
	f.enabled.Store(false) // Recovery must still converge with data ingress disabled.
	if err = restarted().reconcileObjectBucketVersioning(ctx, nil); err != nil {
		t.Fatal(err)
	}
	durable, err := f.st.GetObjectBucketVersioning(ctx, f.account.ID, f.app.ID, f.bucket.ID)
	if err != nil || !durable.Dispatched || !durable.VersionsRequired || durable.LastErrorCode != "provider_failed" {
		t.Fatal(durable, err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE object_bucket_versioning SET retry_at=now() WHERE bucket_id=$1`, f.bucket.ID); err != nil {
		t.Fatal(err)
	}
	if err = restarted().reconcileObjectBucketVersioning(ctx, nil); err != nil {
		t.Fatal(err)
	}
	j, err = client.GetObjectBucketVersioning(ctx, f.app.Slug, f.bucket.ID)
	if err != nil || j.State != "propagating" || j.ObservedStatus != "Enabled" {
		t.Fatal(j, err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE object_bucket_versioning SET retry_at=now(),propagation_until=now()-interval '1 second' WHERE bucket_id=$1`, f.bucket.ID); err != nil {
		t.Fatal(err)
	}
	if err = restarted().reconcileObjectBucketVersioning(ctx, nil); err != nil {
		t.Fatal(err)
	}
	j, err = client.GetObjectBucketVersioning(ctx, f.app.Slug, f.bucket.ID)
	if err != nil || j.State != "inventory" || j.CapacityJobID == "" {
		t.Fatal(j, err)
	}
	if err = restarted().reconcileObjectCapacity(ctx, nil); err != nil {
		t.Fatal(err)
	}
	c, err := f.st.GetObjectCapacityReconciliation(ctx, f.account.ID, f.bucket.ID, j.CapacityJobID)
	if err != nil || c.State != "waiting" || c.ScannedPages != 1 || c.ScannedBytes != 8 {
		t.Fatal(c, err)
	}
	snap, err := f.st.ObjectUsage(ctx, f.account.ID, time.Now())
	if err != nil || snap.Buckets[0].BaselineBytes != 0 {
		t.Fatal("partial cutover rebased quota", snap, err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE object_storage_capacity_reconciliations SET retry_at=now() WHERE id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	if err = restarted().reconcileObjectCapacity(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE object_bucket_versioning SET retry_at=now() WHERE bucket_id=$1`, f.bucket.ID); err != nil {
		t.Fatal(err)
	}
	if err = restarted().reconcileObjectBucketVersioning(ctx, nil); err != nil {
		t.Fatal(err)
	}
	j, err = client.GetObjectBucketVersioning(ctx, f.app.Slug, f.bucket.ID)
	if err != nil || j.State != "ready" {
		t.Fatal(j, err)
	}
	f.enabled.Store(true)
	out, err := f.client.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("assets"), Key: aws.String("key"), Body: strings.NewReader("hello")})
	if err != nil || aws.ToString(out.VersionId) == "" || strings.Contains(aws.ToString(out.VersionId), "private") {
		t.Fatal(out, err)
	}
	if _, err = f.client.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("assets"), Key: aws.String("key"), Body: strings.NewReader("hello")}); err == nil {
		t.Fatal("overwrite reused retained capacity")
	}
	_, err = f.client.PutBucketVersioning(ctx, &awss3.PutBucketVersioningInput{Bucket: aws.String("assets"), VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusSuspended}})
	if err != nil {
		t.Fatal(err)
	}
	outConfig, err := f.client.GetBucketVersioning(ctx, &awss3.GetBucketVersioningInput{Bucket: aws.String("assets")})
	if err != nil || outConfig.Status != types.BucketVersioningStatusSuspended {
		t.Fatal(outConfig, err)
	}
	j, err = client.GetObjectBucketVersioning(ctx, f.app.Slug, f.bucket.ID)
	if err != nil || !j.VersionsRequired || j.State != "propagating" || j.Revision != 2 {
		t.Fatal(j, err)
	}
	metrics, err := f.st.ListObjectStorageProviderRequestMetrics(ctx, f.bucket.BackendID, f.bucket.BackendFingerprint, state.ObjectStoragePeriod(time.Now()))
	if err != nil || len(metrics) != 1 {
		t.Fatal(metrics, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if configPuts != 2 || pages != 3 || writes != 1 || metrics[0].RequestCount != int64(requests) {
		t.Fatal("replayed or unmetered calls", configPuts, pages, writes, requests, metrics)
	}
}
