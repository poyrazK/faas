package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 398
func TestGatewayVersionInventoryEndToEndPG(t *testing.T) {
	var mu sync.Mutex
	writes, lists, failedPages, deletes := 0, 0, 0, 0
	f := newGatewayRecoveryFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodPut:
			writes++
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("ETag", fmt.Sprintf(`"etag-%d"`, writes))
			w.Header().Set("X-Amz-Version-Id", fmt.Sprintf("native-%d", writes))
		case http.MethodGet:
			lists++
			q := r.URL.Query()
			if !q.Has("versions") || q.Has("prefix") || q.Get("max-keys") != "1000" {
				t.Error("used current-object or unbounded inventory", r.URL)
			}
			w.Header().Set("Content-Type", "application/xml")
			if q.Get("key-marker") == "" {
				_, _ = fmt.Fprint(w, `<ListVersionsResult><IsTruncated>true</IsTruncated><NextKeyMarker>key</NextKeyMarker><NextVersionIdMarker>marker</NextVersionIdMarker><DeleteMarker><Key>key</Key><VersionId>marker</VersionId></DeleteMarker>`)
				for i := 1; i <= writes; i++ {
					_, _ = fmt.Fprintf(w, `<Version><Key>key</Key><VersionId>native-%d</VersionId><Size>5</Size></Version>`, i)
				}
				_, _ = fmt.Fprint(w, `</ListVersionsResult>`)
			} else {
				if q.Get("key-marker") != "key" || q.Get("version-id-marker") != "marker" {
					t.Error("lost version cursor", q)
				}
				if failedPages == 0 {
					failedPages++
					w.WriteHeader(500)
					_, _ = fmt.Fprint(w, `<Error><Code>InternalError</Code></Error>`)
					return
				}
				_, _ = fmt.Fprint(w, `<ListVersionsResult><IsTruncated>false</IsTruncated><Version><Key>key</Key><VersionId>old-retained</VersionId><Size>85</Size></Version></ListVersionsResult>`)
			}
		case http.MethodDelete:
			deletes++
			w.WriteHeader(204)
		default:
			t.Error("unexpected provider dispatch", r.Method, r.URL)
			w.WriteHeader(500)
		}
	}), 0)
	ctx := t.Context()
	put := func() error {
		_, err := f.client.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("assets"), Key: aws.String("key"), Body: strings.NewReader("hello")})
		return err
	}
	if err := put(); err != nil {
		t.Fatal(err)
	}
	j, err := f.st.RequestObjectCapacityReconciliation(ctx, f.account.ID, f.app.ID, f.bucket.ID)
	if err != nil {
		t.Fatal(err)
	}
	restarted := func() *server {
		return newServer(state.NewPgStore(f.pool), slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithObjectStorage(f.registry)
	}
	if err = restarted().reconcileObjectCapacity(ctx, nil); err != nil {
		t.Fatal(err)
	}
	j, err = f.st.GetObjectCapacityReconciliation(ctx, f.account.ID, f.bucket.ID, j.ID)
	if err != nil || j.State != "waiting" || j.InventoryScope != state.ObjectInventoryAllVersions || j.ScannedPages != 1 || j.ScannedBytes != 8 || j.InventoryCursor == "" {
		t.Fatal("partial page was not retained", j, err)
	}

	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.st.CreateAPIKey(ctx, f.account.ID, hash, "native-inventory", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	management := httptest.NewServer(restarted().handler())
	t.Cleanup(management.Close)
	customer, err := api.NewClient(management.URL, plaintext).GetObjectCapacityReconciliation(ctx, f.app.Slug, f.bucket.ID, j.ID)
	if err != nil || customer.InventoryScope != state.ObjectInventoryAllVersions || customer.ScannedBytes != 8 || customer.ScannedVersions != 2 || customer.ScannedPages != 1 {
		t.Fatal("customer progress lost", customer, err)
	}

	snap, err := f.st.ObjectUsage(ctx, f.account.ID, time.Now())
	if err != nil || snap.Buckets[0].BaselineBytes != 0 || snap.Buckets[0].GrantedBytes != 5 {
		t.Fatal("partial inventory rebased quota", snap, err)
	}
	if err = put(); err == nil {
		t.Fatal("write escaped inventory fence")
	}
	if _, err = f.pool.Exec(ctx, `UPDATE object_storage_capacity_reconciliations SET retry_at=now()-interval '1 second' WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	if err = restarted().reconcileObjectCapacity(ctx, nil); err != nil {
		t.Fatal(err)
	}
	j, err = f.st.GetObjectCapacityReconciliation(ctx, f.account.ID, f.bucket.ID, j.ID)
	if err != nil || j.State != "completed" || j.AfterBytes != 93 || j.AfterKeys != 3 || j.ScannedPages != 2 {
		t.Fatal("native inventory did not complete after restart", j, err)
	}
	if err = put(); err != nil {
		t.Fatal(err)
	}
	if err = put(); err == nil {
		t.Fatal("same-key overwrite reused native capacity")
	}
	if _, err = f.client.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String("assets"), Key: aws.String("key")}); err == nil {
		t.Fatal("unadmitted delete marker created")
	}
	if _, err = f.client.DeleteObjects(ctx, &awss3.DeleteObjectsInput{Bucket: aws.String("assets"), Delete: &types.Delete{Objects: []types.ObjectIdentifier{{Key: aws.String("key")}}}}); err == nil {
		t.Fatal("bulk delete created unadmitted markers")
	}
	snap, err = f.st.ObjectUsage(ctx, f.account.ID, time.Now())
	usage := state.SummarizeObjectUsage(snap, f.policy, time.Now())
	if err != nil || usage.CapacityBytes != 98 || usage.CapacityKeys != 4 || snap.Authorizations != 2 {
		t.Fatal("per-attempt native quota", usage, snap.Authorizations, err)
	}
	// Periodic inventory must enqueue the durable native scan without a current listing.
	if _, err = f.pool.Exec(ctx, `UPDATE object_storage_bucket_usage SET attempt_at=now()-interval '10 minutes' WHERE bucket_id=$1`, f.bucket.ID); err != nil {
		t.Fatal(err)
	}
	if err = restarted().reconcileObjectInventories(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err = restarted().reconcileObjectCapacity(ctx, nil); err != nil {
		t.Fatal(err)
	}
	snap, err = f.st.ObjectUsage(ctx, f.account.ID, time.Now())
	if err != nil || snap.Buckets[0].BaselineBytes != 98 || snap.Buckets[0].GrantedBytes != 0 || snap.Buckets[0].BaselineKeys != 4 {
		t.Fatal("native refresh lost version accounting", snap, err)
	}
	mu.Lock()
	gotWrites, gotLists, gotDeletes := writes, lists, deletes
	mu.Unlock()
	if gotWrites != 2 || gotLists != 5 || gotDeletes != 0 {
		t.Fatal("unsafe replay or delete", gotWrites, gotLists, gotDeletes)
	}
	metrics, err := f.st.ListObjectStorageProviderRequestMetrics(ctx, f.bucket.BackendID, f.bucket.BackendFingerprint, state.ObjectStoragePeriod(time.Now()))
	if err != nil || len(metrics) != 1 || metrics[0].RequestCount != int64(gotWrites+gotLists) {
		t.Fatal("unmetered native attempts", metrics, err)
	}
}

func TestNativePeriodicBlockedInventoryCadencePG(t *testing.T) {
	f := newGatewayRecoveryFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Error("blocked inventory dispatched a provider request", r.Method)
			w.WriteHeader(500)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("ETag", `"etag"`)
		w.Header().Set("X-Amz-Version-Id", "native")
	}), 0)
	ctx := t.Context()
	if err := f.st.AdmitObjectURL(ctx, f.account.ID, f.bucket.ID, "untracked", 1, true, f.policy); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("assets"), Key: aws.String("key"), Body: strings.NewReader("hello")}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE object_storage_bucket_usage SET attempt_at=now()-interval '10 minutes' WHERE bucket_id=$1`, f.bucket.ID); err != nil {
		t.Fatal(err)
	}
	s := newServer(state.NewPgStore(f.pool), slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithObjectStorage(f.registry)
	if err := s.reconcileObjectInventories(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileObjectCapacity(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileObjectInventories(ctx, nil); err != nil {
		t.Fatal(err)
	}
	var count int
	var status string
	if err := f.pool.QueryRow(ctx, `SELECT count(*),min(state) FROM object_storage_capacity_reconciliations WHERE bucket_id=$1`, f.bucket.ID).Scan(&count, &status); err != nil || count != 1 || status != "blocked" {
		t.Fatal("periodic refresh ignored cadence", count, status, err)
	}
}
