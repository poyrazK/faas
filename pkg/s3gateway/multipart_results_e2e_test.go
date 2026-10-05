package s3gateway

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/onebox-faas/faas/pkg/state"
)

type multipartResultHTTPFixture struct {
	mu                        sync.Mutex
	parts                     multipartCopyHTTPProvider
	receipt, native, failure  string
	calls, completions, pages int
	committed, deleted        bool
}

func (p *multipartResultHTTPFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	q := r.URL.Query()
	w.Header().Set("Content-Type", "application/xml")
	switch {
	case r.Method == http.MethodPost && q.Has("uploads"):
		p.receipt = r.Header.Get("X-Amz-Meta-Gregale-Upload-Id")
		p.parts.serve(t, w, r)
	case r.Method == http.MethodPost && q.Has("uploadId"):
		p.completions++
		if p.committed {
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `<Error><Code>NoSuchUpload</Code></Error>`)
			return
		}
		p.committed = true
		delete(p.parts.uploads, q.Get("uploadId"))
		w.Header().Set("X-Amz-Version-Id", p.native)
		switch p.failure {
		case "lost_ack":
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
		case "truncated":
			_, _ = io.WriteString(w, `<CompleteMultipartUploadResult><ETag>`)
		case "duplicate_version":
			w.Header().Add("X-Amz-Version-Id", "private-other")
			_, _ = io.WriteString(w, `<CompleteMultipartUploadResult><ETag>&quot;actual-provider-1&quot;</ETag></CompleteMultipartUploadResult>`)
		case "missing_etag":
			_, _ = io.WriteString(w, `<CompleteMultipartUploadResult/>`)
		default:
			_, _ = io.WriteString(w, `<CompleteMultipartUploadResult><ETag>&quot;actual-provider-1&quot;</ETag></CompleteMultipartUploadResult>`)
		}
	case r.Method == http.MethodGet && q.Has("versions"):
		p.pages++
		if q.Get("key-marker") == "" {
			_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>true</IsTruncated><NextKeyMarker>destination</NextKeyMarker><NextVersionIdMarker>page</NextVersionIdMarker><DeleteMarker><Key>destination</Key><VersionId>private-marker</VersionId></DeleteMarker></ListVersionsResult>`)
		} else {
			if q.Get("key-marker") != "destination" || q.Get("version-id-marker") != "page" {
				t.Error("lost persisted continuation", q)
			}
			_, _ = fmt.Fprintf(w, `<ListVersionsResult><IsTruncated>false</IsTruncated><Version><Key>destination</Key><VersionId>%s</VersionId><Size>10</Size></Version></ListVersionsResult>`, p.native)
		}
	case r.Method == http.MethodHead && strings.HasSuffix(r.URL.Path, "/destination"):
		w.Header().Set("Content-Length", "10")
		w.Header().Set("ETag", `"actual-provider-1"`)
		if q.Get("versionId") == p.native && p.native != "" {
			w.Header().Set("X-Amz-Version-Id", p.native)
			w.Header().Set("X-Amz-Meta-Gregale-Upload-Id", p.receipt)
		} else {
			w.Header().Set("X-Amz-Version-Id", "private-later")
			w.Header().Set("X-Amz-Meta-Gregale-Upload-Id", "foreign")
			if p.deleted {
				w.Header().Set("X-Amz-Delete-Marker", "true")
				w.WriteHeader(404)
			}
		}
	case r.Method == http.MethodGet && !q.Has("uploadId") && strings.HasSuffix(r.URL.Path, "/destination"):
		if q.Get("versionId") != p.native {
			t.Error("customer read selected wrong native version", q)
		}
		w.Header().Set("ETag", `"actual-provider-1"`)
		w.Header().Set("X-Amz-Version-Id", p.native)
		_, _ = io.WriteString(w, "abcdefghij")
	default:
		p.parts.serve(t, w, r)
	}
}

func newMultipartResultIntegration(t *testing.T, st multipartCopyIntegrationStore, native, failure string) (*multipartCopyIntegration, *multipartResultHTTPFixture, *awss3.CompleteMultipartUploadInput) {
	t.Helper()
	p := &multipartResultHTTPFixture{native: native, failure: failure, parts: multipartCopyHTTPProvider{uploads: map[string]map[string]int{}}}
	f := newMultipartCopyIntegrationWithProvider(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }))
	id := f.initiate(t, "destination")
	part, err := f.copyPart(t, "destination", id, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	in := &awss3.CompleteMultipartUploadInput{Bucket: aws.String("assets"), Key: aws.String("destination"), UploadId: aws.String(id), IfNoneMatch: aws.String("*"), MultipartUpload: &types.CompletedMultipartUpload{Parts: []types.CompletedPart{{PartNumber: aws.Int32(1), ETag: part.CopyPartResult.ETag}}}}
	return f, p, in
}

// adr: 544
func TestMultipartActualResultsEndToEndMem(t *testing.T) {
	multipartActualResultsEndToEnd(t, func(t *testing.T) multipartCopyIntegrationStore { return state.NewMemStore() })
}
func TestMultipartActualResultsEndToEndPG(t *testing.T) {
	multipartActualResultsEndToEnd(t, func(t *testing.T) multipartCopyIntegrationStore { st, _ := multipartCopyPGStore(t); return st })
}

func multipartActualResultsEndToEnd(t *testing.T, store func(*testing.T) multipartCopyIntegrationStore) {
	for _, native := range []string{"", "null", "private-old"} {
		t.Run("native="+native, func(t *testing.T) {
			f, p, in := newMultipartResultIntegration(t, store(t), native, "")
			out, err := f.client.CompleteMultipartUpload(t.Context(), in)
			if err != nil || aws.ToString(out.ETag) != `"actual-provider-1"` {
				t.Fatal(out, err)
			}
			public := aws.ToString(out.VersionId)
			if native == "" && public != "" || native == "null" && public != "null" || native == "private-old" && (!state.ValidObjectVersionID(public) || public == native) {
				t.Fatal("private or incorrect completion ID", out)
			}
			p.mu.Lock()
			p.deleted = true
			before := p.calls
			p.mu.Unlock()
			replay, err := f.client.CompleteMultipartUpload(t.Context(), in)
			p.mu.Lock()
			after := p.calls
			p.mu.Unlock()
			if err != nil || aws.ToString(replay.ETag) != aws.ToString(out.ETag) || aws.ToString(replay.VersionId) != public || before != after {
				t.Fatal("replay changed result or called provider after overwrite", replay, err, before, after)
			}
			if public != "" {
				read, err := f.client.GetObject(t.Context(), &awss3.GetObjectInput{Bucket: in.Bucket, Key: in.Key, VersionId: aws.String(public)})
				if err != nil {
					t.Fatal(err)
				}
				body, e := io.ReadAll(read.Body)
				_ = read.Body.Close()
				if e != nil || string(body) != "abcdefghij" || aws.ToString(read.VersionId) != public {
					t.Fatal(string(body), read.VersionId, e)
				}
			}
			changed := *in
			changed.IfNoneMatch = nil
			_, err = f.client.CompleteMultipartUpload(t.Context(), &changed)
			assertSDKErrorCode(t, err, "OperationAborted")
			changed = *in
			changed.MultipartUpload = &types.CompletedMultipartUpload{Parts: []types.CompletedPart{{PartNumber: aws.Int32(1), ETag: aws.String(`"different"`)}}}
			_, err = f.client.CompleteMultipartUpload(t.Context(), &changed)
			assertSDKErrorCode(t, err, "InvalidPart")
		})
	}
}

func TestMultipartHistoricalResultRecoveryRestartPG(t *testing.T) {
	for _, failure := range []string{"lost_ack", "truncated", "duplicate_version", "missing_etag"} {
		t.Run(failure, func(t *testing.T) {
			st, pool := multipartCopyPGStore(t)
			f, p, in := newMultipartResultIntegration(t, st, "private-old", failure)
			_, err := f.client.CompleteMultipartUpload(t.Context(), in)
			assertSDKErrorCode(t, err, "ServiceUnavailable")
			id := aws.ToString(in.UploadId)
			p.mu.Lock()
			p.deleted = true
			p.mu.Unlock()
			for page := 1; page <= 2; page++ {
				restarted := state.NewPgStore(pool)
				u, e := restarted.GetObjectMultipartUpload(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, id)
				if e != nil || !u.CompletionDispatched || u.CompletionErrorCode != "" || u.State != state.ObjectMultipartCompletingConditional || page == 2 && u.CompletionRecoveryCursor == "" {
					t.Fatal("lost pending intent or cursor", u, e)
				}
				usage, e := restarted.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
				if e != nil || usage.Buckets[0].MultipartBytes != 10 {
					t.Fatal("uncertain completion released parts", usage, e)
				}
				if _, e = pool.Exec(t.Context(), `UPDATE object_storage_multipart_uploads SET retry_at=now()-interval '1 second' WHERE id=$1`, id); e != nil {
					t.Fatal(e)
				}
				f.handler.store, f.handler.multipartStore, f.handler.requestMetrics = restarted, restarted, restarted
				out, e := f.client.CompleteMultipartUpload(t.Context(), in)
				if page == 1 {
					assertSDKErrorCode(t, e, "ServiceUnavailable")
					continue
				}
				if e != nil || aws.ToString(out.ETag) != `"actual-provider-1"` || !state.ValidObjectVersionID(aws.ToString(out.VersionId)) {
					t.Fatal(out, e)
				}
				u, e = restarted.GetObjectMultipartUpload(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, id)
				if e != nil || u.State != state.ObjectMultipartCompleted || u.CompletionRecoveryCursor != "" || !u.CompletionVersionsObserved || u.CompletionVersionID != aws.ToString(out.VersionId) {
					t.Fatal(u, e)
				}
				p.mu.Lock()
				calls, completions, pages := p.calls, p.completions, p.pages
				p.mu.Unlock()
				again, e := f.client.CompleteMultipartUpload(t.Context(), in)
				p.mu.Lock()
				after := p.calls
				p.mu.Unlock()
				if e != nil || aws.ToString(again.VersionId) != u.CompletionVersionID || calls != after || completions != 3 || pages != 2 {
					t.Fatal("recovery repeated admission or changed result", again, e, calls, after, completions, pages)
				}
			}
		})
	}
}
