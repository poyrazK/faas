package s3gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func nativeCopyBaseline(t *testing.T, f *multipartCopyIntegration, size int64) {
	t.Helper()
	ctx := t.Context()
	writes := f.store.(state.ObjectTrackedGatewayUploadStore)
	c, err := writes.BeginTrackedGatewayUpload(ctx, state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: f.bucket.AccountID, AppID: f.bucket.AppID, BucketID: f.bucket.ID, SubjectID: "existing", Key: "source", Bytes: 0, Status: "pending"}, f.handler.registry.Accounting)
	if err != nil {
		t.Fatal(err)
	}
	c, err = writes.DispatchTrackedObjectUpload(ctx, c.AccountID, c.BucketID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.Status = "completed"
	c.ETag = `"source"`
	c.RecoveryVersionsObserved = true
	if _, err = writes.FinishTrackedObjectUpload(ctx, c); err != nil {
		t.Fatal(err)
	}
	capacity := f.store.(state.ObjectCapacityStore)
	j, err := capacity.RequestObjectCapacityReconciliation(ctx, f.bucket.AccountID, f.bucket.AppID, f.bucket.ID)
	if err != nil {
		t.Fatal(err)
	}
	j, err = capacity.ClaimObjectCapacityReconciliation(ctx, j.ID, "native-source")
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("source\x00" + f.provider.sourceVersion))
	j, err = f.store.(state.ObjectVersionInventoryStore).StageObjectVersionInventoryPage(ctx, j.ID, j.Token, "", []state.ObjectVersionInventoryRecord{{Identity: hex.EncodeToString(hash[:]), Bytes: size}})
	if err != nil || j.State != "completed" {
		t.Fatal(j, err)
	}
}

// adr: 399
func TestVersionedSourceCopiesEndToEndMem(t *testing.T) {
	versionedSourceCopiesEndToEnd(t, func(t *testing.T) multipartCopyIntegrationStore { return state.NewMemStore() })
}
func TestVersionedSourceCopiesEndToEndPG(t *testing.T) {
	versionedSourceCopiesEndToEnd(t, func(t *testing.T) multipartCopyIntegrationStore { st, _ := multipartCopyPGStore(t); return st })
}

func versionedSourceCopiesEndToEnd(t *testing.T, store func(*testing.T) multipartCopyIntegrationStore) {
	modified := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	before, after := modified.Add(-time.Hour), modified.Add(time.Hour)
	for _, part := range []bool{false, true} {
		for _, tc := range []struct {
			name                 string
			match, none          string
			modified, unmodified *time.Time
			code                 string
			failure              string
			copies               int
			baseline             bool
		}{
			{name: "native overwrite race", copies: 1, baseline: true},
			{name: "modified", modified: &before, copies: 1, baseline: true},
			{name: "modified date fails", modified: &after, code: "PreconditionFailed", copies: 1, baseline: true},
			{name: "unmodified", unmodified: &after, copies: 1, baseline: true},
			{name: "unmodified date fails", unmodified: &before, code: "PreconditionFailed", copies: 1, baseline: true},
			{name: "matching ETag overrides stale date", match: `"source"`, unmodified: &before, copies: 1, baseline: true},
			{name: "excluded ETag defeats modified date", none: `"source"`, modified: &before, code: "PreconditionFailed", baseline: true},
			{name: "both date bounds", modified: &before, unmodified: &after, copies: 1, baseline: true},
			{name: "selected version deleted", failure: "source_deleted", code: "NoSuchKey", copies: 1, baseline: true},
			{name: "native baseline required", code: "NotImplemented"},
		} {
			name := "object/"
			if part {
				name = "part/"
			}
			t.Run(name+tc.name, func(t *testing.T) {
				f := newMultipartCopyIntegration(t, store(t))
				size := int64(9)
				if part {
					size = 6 << 20
				}
				f.provider.mu.Lock()
				f.provider.sourceSize = size
				f.provider.sourceVersion = "native/+%&=?"
				f.provider.sourceModified = modified
				f.provider.latestChanged = true
				f.provider.failure = tc.failure
				f.provider.mu.Unlock()
				if tc.baseline {
					nativeCopyBaseline(t, f, size)
				}
				var err error
				var id string
				if part {
					id = f.initiate(t, "destination")
					out, e := f.client.UploadPartCopy(t.Context(), &awss3.UploadPartCopyInput{Bucket: aws.String("assets"), Key: aws.String("destination"), UploadId: aws.String(id), PartNumber: aws.Int32(1), CopySource: aws.String("assets/source"), CopySourceRange: aws.String("bytes=0-9"), CopySourceIfMatch: stringPtr(tc.match), CopySourceIfNoneMatch: stringPtr(tc.none), CopySourceIfModifiedSince: tc.modified, CopySourceIfUnmodifiedSince: tc.unmodified})
					err = e
					if tc.code == "" && (out == nil || out.CopyPartResult == nil || aws.ToString(out.CopyPartResult.ETag) != `"part"` || out.CopySourceVersionId != nil) {
						t.Fatal("invalid or leaking copy result", out)
					}
				} else {
					out, e := f.client.CopyObject(t.Context(), &awss3.CopyObjectInput{Bucket: aws.String("assets"), Key: aws.String("destination"), CopySource: aws.String("assets/source"), CopySourceIfMatch: stringPtr(tc.match), CopySourceIfNoneMatch: stringPtr(tc.none), CopySourceIfModifiedSince: tc.modified, CopySourceIfUnmodifiedSince: tc.unmodified})
					err = e
					if tc.code == "" && (out == nil || out.CopyObjectResult == nil || aws.ToString(out.CopyObjectResult.ETag) != `"copied"` || out.CopySourceVersionId != nil) {
						t.Fatal("invalid or leaking copy result", out)
					}
				}
				if tc.code != "" {
					assertSDKErrorCode(t, err, tc.code)
				} else if err != nil {
					t.Fatal(err)
				}
				f.provider.mu.Lock()
				copies := f.provider.copies
				f.provider.mu.Unlock()
				if copies != tc.copies {
					t.Fatal("unexpected provider dispatch count", copies)
				}
				usage, e := f.store.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
				if e != nil {
					t.Fatal(e)
				}
				if part {
					want := int64(0)
					if tc.copies > 0 {
						want = 10
					}
					if usage.Buckets[0].MultipartBytes != want {
						t.Fatal("incorrect copied-byte reservation", usage)
					}
					// Date rejection is definitive: it releases the transfer fence
					// so a fresh valid attempt can reuse the same reserved part.
					if tc.code == "PreconditionFailed" && tc.copies == 1 {
						if _, e = f.copyPart(t, "destination", id, 1, ""); e != nil {
							t.Fatal("date rejection retained part fence", e)
						}
					}
				} else if usage.Buckets[0].GrantedBytes != int64(tc.copies)*size || usage.Buckets[0].GrantedKeys != int64(tc.copies) {
					t.Fatal("native copy did not reserve full version", usage)
				}
			})
		}
	}
}

func TestVersionedCopyRepeatedDestinationQuotaPG(t *testing.T) {
	st, _ := multipartCopyPGStore(t)
	f := newMultipartCopyIntegration(t, st)
	f.provider.mu.Lock()
	f.provider.sourceSize = 9
	f.provider.sourceVersion = "native"
	f.provider.latestChanged = true
	f.provider.mu.Unlock()
	nativeCopyBaseline(t, f, 9)
	f.handler.registry.Accounting.MaxAccountBytes = 27
	f.handler.registry.Accounting.MaxBucketBytes = 27
	f.handler.registry.Accounting.MaxAccountKeys = 3
	in := &awss3.CopyObjectInput{Bucket: aws.String("assets"), Key: aws.String("same-key"), CopySource: aws.String("assets/source")}
	for range 2 {
		if _, err := f.client.CopyObject(t.Context(), in); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.client.CopyObject(t.Context(), in); err != nil {
		assertSDKErrorCode(t, err, "OperationAborted")
	} else {
		t.Fatal("overwrite escaped retained-version quota")
	}
	usage, err := st.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
	f.provider.mu.Lock()
	copies := f.provider.copies
	f.provider.mu.Unlock()
	if err != nil || usage.Buckets[0].GrantedBytes != 18 || usage.Buckets[0].GrantedKeys != 2 || copies != 2 {
		t.Fatal(usage, err, copies)
	}
}

func TestVersionedCopyUncertainAcknowledgmentPG(t *testing.T) {
	st, pool := multipartCopyPGStore(t)
	f := newMultipartCopyIntegration(t, st)
	f.provider.mu.Lock()
	f.provider.sourceSize = 9
	f.provider.sourceVersion = "native"
	f.provider.failure = "lost_ack"
	f.provider.mu.Unlock()
	nativeCopyBaseline(t, f, 9)
	date := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	_, err := f.client.CopyObject(t.Context(), &awss3.CopyObjectInput{Bucket: aws.String("assets"), Key: aws.String("destination"), CopySource: aws.String("assets/source"), CopySourceIfUnmodifiedSince: &date})
	assertSDKErrorCode(t, err, "ServiceUnavailable")
	restarted := state.NewPgStore(pool)
	receipts, err := restarted.ListObjectWriteReceipts(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, "pending", 10, "")
	if err != nil || len(receipts.Items) != 1 || receipts.Items[0].Operation != "copy" || receipts.Items[0].Bytes != 9 {
		t.Fatal("restart lost uncertain copy", receipts, err)
	}
	var phase string
	if err = pool.QueryRow(t.Context(), `SELECT write_phase FROM object_upload_completions WHERE id=$1`, receipts.Items[0].ID).Scan(&phase); err != nil || phase != state.ObjectUploadDispatched {
		t.Fatal(phase, err)
	}
	usage, err := restarted.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
	if err != nil || usage.Buckets[0].GrantedBytes != 9 || usage.Buckets[0].GrantedKeys != 1 {
		t.Fatal("uncertain native copy refunded quota", usage, err)
	}
	f.provider.mu.Lock()
	copies := f.provider.copies
	f.provider.mu.Unlock()
	if copies != 1 {
		t.Fatal("uncertain copy retried", copies)
	}
}

func TestVersionedPartCopyUncertainAcknowledgmentPG(t *testing.T) {
	st, pool := multipartCopyPGStore(t)
	f := newMultipartCopyIntegration(t, st)
	f.provider.mu.Lock()
	f.provider.sourceSize = 6 << 20
	f.provider.sourceVersion = "native"
	f.provider.failure = "lost_ack"
	f.provider.mu.Unlock()
	nativeCopyBaseline(t, f, 6<<20)
	id := f.initiate(t, "destination")
	_, err := f.copyPart(t, "destination", id, 1, "")
	assertSDKErrorCode(t, err, "ServiceUnavailable")
	restarted := state.NewPgStore(pool)
	if err = restarted.BeginObjectMultipartPart(t.Context(), f.bucket.AccountID, f.bucket.ID, id, "restart", 1, 10, f.handler.registry.MaxUploadBytes, f.handler.registry.Accounting); !errors.Is(err, state.ErrConflict) {
		t.Fatal("restart lost native transfer fence", err)
	}
	usage, err := restarted.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
	if err != nil || usage.Buckets[0].MultipartBytes != 10 {
		t.Fatal("uncertain native part refunded quota", usage, err)
	}
	f.provider.mu.Lock()
	copies := f.provider.copies
	f.provider.mu.Unlock()
	if copies != 1 {
		t.Fatal("uncertain native part replayed", copies)
	}
}

func TestGatewayCopyDateHeaderValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []string
		code   int
	}{
		{"invalid", []string{"not-a-date"}, 400},
		{"empty", []string{""}, 400},
		{"oversized", []string{strings.Repeat("x", 129)}, 400},
		{"duplicate", []string{"Fri, 02 Oct 2026 10:00:00 GMT", "Fri, 02 Oct 2026 11:00:00 GMT"}, 400},
		{"old adapter", []string{"Fri, 02 Oct 2026 10:00:00 GMT"}, 501},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _ := newGatewayReceiptHandler(t, func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid or unsupported date reached provider")
				return nil, nil
			})
			r := signedCopyTestRequest(t)
			r.Header["X-Amz-Copy-Source-If-Unmodified-Since"] = tc.values
			if err := awsv4.NewSigner(func(o *awsv4.SignerOptions) { o.DisableURIPathEscaping = true }).SignHTTP(context.Background(), aws.Credentials{AccessKeyID: testAccess, SecretAccessKey: testSecret}, r, "UNSIGNED-PAYLOAD", "s3", "us-east-1", h.now()); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.code {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestGatewayCopyDateMustBeSigned(t *testing.T) {
	h, _, _ := newGatewayReceiptHandler(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("unsigned date reached provider")
		return nil, nil
	})
	r := signedCopyTestRequest(t)
	r.Header.Set("X-Amz-Copy-Source-If-Modified-Since", "Fri, 02 Oct 2026 10:00:00 GMT")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal(w.Code, w.Body.String())
	}
}
