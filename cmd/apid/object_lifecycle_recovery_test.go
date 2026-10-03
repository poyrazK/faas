package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 408
func TestObjectLifecycleRecoveryEndToEndPG(t *testing.T) {
	var deleted atomic.Bool
	var requests, deletes atomic.Int32
	modified := time.Now().UTC().AddDate(0, 0, -10).Truncate(time.Millisecond)
	f := newGatewayRecoveryFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/xml")
		if q.Has("versioning") {
			_, _ = io.WriteString(w, `<VersioningConfiguration/>`)
			return
		}
		if q.Has("versions") {
			if q.Get("prefix") == "" && (q.Get("max-keys") != "1" || q.Get("version-id-marker") != "") {
				t.Error("invalid lifecycle discovery", q)
			}
			_, _ = io.WriteString(w, `<ListVersionsResult><EncodingType>url</EncodingType><IsTruncated>false</IsTruncated>`)
			if !deleted.Load() && q.Get("key-marker") == "" {
				_, _ = fmt.Fprintf(w, `<Version><Key>key</Key><VersionId>null</VersionId><IsLatest>true</IsLatest><LastModified>%s</LastModified><Size>1</Size><ETag>&quot;data&quot;</ETag></Version>`, modified.Format(time.RFC3339Nano))
			}
			_, _ = io.WriteString(w, `</ListVersionsResult>`)
			return
		}
		if r.Method != http.MethodDelete || !strings.HasSuffix(r.URL.Path, "/key") || q.Get("versionId") != "" {
			t.Error("unexpected lifecycle request", r.Method, q)
			w.WriteHeader(500)
			return
		}
		deleted.Store(true)
		deletes.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}), 1)
	ctx := t.Context()
	days := int32(1)
	_, client := lifecycleCustomerClient(t, f)
	if _, err := client.PutObjectBucketLifecycle(ctx, f.app.Slug, f.bucket.ID, api.ObjectBucketLifecycleRequest{Rules: []api.ObjectLifecycleRule{{ID: "expire", Status: "Enabled", Expiration: &api.ObjectLifecycleExpiration{Days: &days}}}}); err != nil {
		t.Fatal(err)
	}
	setFlag := func(enabled bool) {
		t.Helper()
		if _, err := f.st.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{Key: runtimeConfigS3, Scope: state.RuntimeConfigScopeGlobal, DesiredValue: boolJSON(enabled), ApplyMode: state.RuntimeConfigApplyHot, Reason: "lifecycle test"}); err != nil {
			t.Fatal(err)
		}
	}
	server := func() *server {
		t.Helper()
		runtime := newRuntimeConfigManager(nil)
		if err := runtime.reconcile(ctx, f.st); err != nil {
			t.Fatal(err)
		}
		return newServer(state.NewPgStore(f.pool), slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithObjectStorage(f.registry).WithRuntimeConfigManager(runtime)
	}
	setFlag(false)
	s := server()
	if err := s.reconcileObjectLifecycle(ctx, nil); err != nil || requests.Load() != 0 {
		t.Fatal("disabled storage dispatched lifecycle", err, requests.Load())
	}
	setFlag(true)
	missing := server().WithObjectStorage(&objectstorage.Registry{Accounting: f.policy})
	missingOutcome := ""
	if err := missing.reconcileObjectLifecycle(ctx, func(_, outcome string) { missingOutcome = outcome }); err != nil || requests.Load() != 0 || missingOutcome != "deferred" {
		t.Fatal("missing placement was not deferred", err, requests.Load(), missingOutcome)
	}
	if rows, err := f.st.DueObjectLifecyclePolicies(ctx, api.ObjectLifecycleBatch); err != nil || len(rows) != 0 {
		t.Fatal("missing placement monopolized the next batch", rows, err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE object_lifecycle_scans SET retry_at=clock_timestamp() WHERE bucket_id=$1`, f.bucket.ID); err != nil {
		t.Fatal(err)
	}
	s = server()
	outcomes := []string{}
	observe := func(operation, outcome string) { outcomes = append(outcomes, operation+":"+outcome) }
	if err := s.reconcileObjectLifecycle(ctx, observe); err != nil || deletes.Load() != 1 {
		t.Fatal("wired expiration", err, deletes.Load())
	}
	s = server()
	if err := s.reconcileObjectLifecycle(ctx, observe); err != nil || deletes.Load() != 1 {
		t.Fatal("restart repeated expiration", err, deletes.Load())
	}
	if strings.Join(outcomes, ",") != "lifecycle:scanning,lifecycle:completed" {
		t.Fatal("incorrect lifecycle recovery progress", outcomes)
	}
	usage, err := f.st.ObjectUsage(ctx, f.account.ID, time.Now())
	if err != nil || usage.Buckets[0].BaselineBytes != 1 || usage.Buckets[0].BaselineKeys != 1 || usage.Buckets[0].GrantedBytes != 0 {
		t.Fatal("lifecycle accounting", usage, err)
	}
	metrics, err := f.st.ListObjectStorageProviderRequestMetrics(ctx, f.bucket.BackendID, f.bucket.BackendFingerprint, state.ObjectStoragePeriod(time.Now()))
	if err != nil || len(metrics) != 1 || metrics[0].RequestCount != int64(requests.Load()) {
		t.Fatal("lifecycle provider metrics", metrics, err, requests.Load())
	}
}
