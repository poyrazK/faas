package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 550
func TestObjectLifecycleMultipartEndToEndPG(t *testing.T) {
	for _, lostACK := range []bool{false, true} {
		name := "acknowledged"
		if lostACK {
			name = "lost-acknowledgment-and-rule-removal"
		}
		t.Run(name, func(t *testing.T) { objectLifecycleMultipartEndToEnd(t, lostACK) })
	}
}

func objectLifecycleMultipartEndToEnd(t *testing.T, lostACK bool) {
	t.Helper()
	const key = "tmp/目录 /+%.bin"
	const native = "private-upload/+%?"
	var aborts, lists, writes, requests atomic.Int32
	var gone, incompleteListing atomic.Bool
	f := newGatewayRecoveryFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/xml")
		if q.Has("uploads") && r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
			return
		}
		if r.URL.Path != "/physical/"+key || r.Header.Get("Authorization") == "" && q.Get("X-Amz-Signature") == "" {
			t.Error("incorrect or unsigned provider request", r.Method, r.URL)
			w.WriteHeader(500)
			return
		}
		if q.Has("uploads") && r.Method == http.MethodPost {
			_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><UploadId>`+native+`</UploadId></InitiateMultipartUploadResult>`)
			return
		}
		if q.Get("uploadId") != native {
			t.Error("wrong native upload", r.URL)
			w.WriteHeader(500)
			return
		}
		switch r.Method {
		case http.MethodPut:
			body, err := io.ReadAll(r.Body)
			if err != nil || string(body) != "0123456789" || q.Get("partNumber") != "1" {
				t.Error("incorrect provider part", string(body), err, q)
				w.WriteHeader(500)
				return
			}
			writes.Add(1)
			w.Header().Set("ETag", `"part"`)
		case http.MethodDelete:
			n := aborts.Add(1)
			if lostACK && n <= 2 {
				// Lose both permitted SDK attempts; the durable owner must retry.
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = conn.Close()
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			lists.Add(1)
			if q.Get("max-parts") != "1" {
				t.Error("unbounded abort verification", r.URL)
			}
			if gone.Load() {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `<Error><Code>NoSuchUpload</Code></Error>`)
				return
			}
			if incompleteListing.Load() {
				_, _ = io.WriteString(w, `<ListPartsResult/>`)
				return
			}
			_, _ = io.WriteString(w, `<ListPartsResult><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><ETag>"part"</ETag><Size>10</Size></Part></ListPartsResult>`)
		default:
			t.Error("unexpected provider call", r.Method, r.URL)
			w.WriteHeader(500)
		}
	}), 7)
	ctx := t.Context()
	u, err := f.client.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{Bucket: aws.String("assets"), Key: aws.String(key)})
	if err != nil || u.UploadId == nil {
		t.Fatal("S3 initiate", u, err)
	}
	input := &awss3.UploadPartInput{Bucket: aws.String("assets"), Key: aws.String(key), UploadId: u.UploadId, PartNumber: aws.Int32(1), ContentLength: aws.Int64(10), Body: strings.NewReader("0123456789")}
	if _, err = f.client.UploadPart(ctx, input); err != nil || writes.Load() != 1 {
		t.Fatal("S3 part", err, writes.Load())
	}
	if _, err = f.pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET created_at=created_at-interval '4 days' WHERE id=$1`, *u.UploadId); err != nil {
		t.Fatal(err)
	}
	days := int32(1)
	if _, err = f.client.PutBucketLifecycleConfiguration(ctx, &awss3.PutBucketLifecycleConfigurationInput{Bucket: aws.String("assets"), LifecycleConfiguration: &types.BucketLifecycleConfiguration{Rules: []types.LifecycleRule{{ID: aws.String("abandoned"), Status: types.ExpirationStatusEnabled, Filter: &types.LifecycleRuleFilter{Prefix: aws.String("tmp/")}, AbortIncompleteMultipartUpload: &types.AbortIncompleteMultipartUpload{DaysAfterInitiation: &days}}}}}); err != nil {
		t.Fatal(err)
	}
	owner := func(enabled bool) *server {
		t.Helper()
		s := newServer(state.NewPgStore(f.pool), slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithObjectStorage(f.registry)
		if e := s.runtimeConfig.apply(runtimeConfigS3, boolJSON(enabled)); e != nil {
			t.Fatal(e)
		}
		return s
	}
	s := owner(true)
	before := requests.Load()
	if err = s.reconcileObjectLifecycle(ctx, nil); err != nil || requests.Load() != before {
		t.Fatal("abort discovery called the provider", err, requests.Load(), before)
	}
	stored, err := f.st.GetObjectMultipartUpload(ctx, f.account.ID, f.app.ID, f.bucket.ID, *u.UploadId)
	if err != nil || stored.State != state.ObjectMultipartAborting || stored.LifecycleAbort.RuleID != "abandoned" || stored.LifecycleAbort.ExpectedProviderUploadID != native {
		t.Fatal("durable admission", stored, err)
	}
	if raw, e := json.Marshal(stored); e != nil || strings.Contains(string(raw), "scan_token") || strings.Contains(string(raw), "expected_provider_upload_id") {
		t.Fatal("private lifecycle binding leaked", string(raw), e)
	}
	assertUsage := func(want int64) {
		t.Helper()
		usage, e := state.NewPgStore(f.pool).ObjectUsage(ctx, f.account.ID, time.Now())
		if e != nil || len(usage.Buckets) != 1 || usage.Buckets[0].MultipartBytes != want || usage.Buckets[0].BaselineBytes != 7 {
			t.Fatal("cleanup accounting", usage, e)
		}
	}
	assertUsage(10)
	input.Body = strings.NewReader("0123456789")
	if _, err = f.client.UploadPart(ctx, input); err == nil || writes.Load() != 1 {
		t.Fatal("part crossed lifecycle cutoff", err, writes.Load())
	}
	if lostACK {
		if _, err = f.client.DeleteBucketLifecycle(ctx, &awss3.DeleteBucketLifecycleInput{Bucket: aws.String("assets")}); err != nil {
			t.Fatal(err)
		}
	} else {
		if err = owner(true).reconcileObjectLifecycle(ctx, nil); err != nil {
			t.Fatal(err)
		}
		j, e := f.st.GetObjectLifecycleScan(ctx, f.account.ID, f.bucket.ID, stored.LifecycleAbort.ScanID)
		if e != nil || j.State != "completed" || j.ScannedUploads != 1 {
			t.Fatal("scan did not finish discovery", j, e)
		}
	}
	if err = owner(false).reconcileObjectMultipartUploads(ctx, nil); err != nil {
		t.Fatal(err)
	}
	assertUsage(10)
	if lostACK && (aborts.Load() != 2 || lists.Load() != 0) || !lostACK && (aborts.Load() != 1 || lists.Load() != 1) {
		t.Fatal("uncertain/remaining cleanup", aborts.Load(), lists.Load())
	}
	retry := func() {
		t.Helper()
		if _, e := f.pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET retry_at=clock_timestamp() WHERE id=$1`, *u.UploadId); e != nil {
			t.Fatal(e)
		}
		if e := owner(false).reconcileObjectMultipartUploads(ctx, nil); e != nil {
			t.Fatal(e)
		}
	}
	if lostACK {
		retry()
		assertUsage(10)
		if aborts.Load() != 3 || lists.Load() != 1 {
			t.Fatal("restart lost abort verification", aborts.Load(), lists.Load())
		}
	}
	// A reconstructed owner must retain quota when a provider's empty listing
	// omits completeness, even after the abort acknowledgment succeeded.
	incompleteListing.Store(true)
	retry()
	assertUsage(10)
	got, err := state.NewPgStore(f.pool).GetObjectMultipartUpload(ctx, f.account.ID, f.app.ID, f.bucket.ID, *u.UploadId)
	if err != nil || got.State != state.ObjectMultipartAborting || got.LeaseToken != "" {
		t.Fatal("incomplete listing released cleanup fence", got, err)
	}
	incompleteListing.Store(false)
	gone.Store(true)
	retry()
	assertUsage(0)
	got, err = state.NewPgStore(f.pool).GetObjectMultipartUpload(ctx, f.account.ID, f.app.ID, f.bucket.ID, *u.UploadId)
	if err != nil || got.State != state.ObjectMultipartAborted || got.LifecycleAbort != stored.LifecycleAbort {
		t.Fatal("restart lost terminal receipt", got, err)
	}
	finalRequests := requests.Load()
	if err = owner(false).reconcileObjectMultipartUploads(ctx, nil); err != nil || requests.Load() != finalRequests {
		t.Fatal("terminal cleanup replay", err, requests.Load())
	}
}
