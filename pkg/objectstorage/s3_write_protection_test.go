package objectstorage

import (
	"context"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func testWriteProtection() state.ObjectWriteProtectionSnapshot {
	now := time.Now().UTC().Truncate(time.Millisecond)
	until := now.Add(72 * time.Hour)
	return state.ObjectWriteProtectionSnapshot{Enabled: true, Revision: 1, CapturedAt: &now, Requested: api.ObjectWriteProtection{Retention: &api.ObjectVersionRetention{Mode: "COMPLIANCE", RetainUntilDate: &until}, LegalHold: &api.ObjectVersionLegalHold{Status: "ON"}}}
}
func protectedHeadHeaders(w http.ResponseWriter, p state.ObjectWriteProtectionSnapshot, receiptKey, receipt, version string, size int64) {
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("ETag", `"proof"`)
	w.Header().Set("X-Amz-Version-Id", version)
	w.Header().Set("X-Amz-Meta-"+receiptKey, receipt)
	w.Header().Set("X-Amz-Meta-"+ReservedObjectProtectionMetadataKey, p.Proof())
	r := p.MinimumRetention()
	if !r.Empty() {
		w.Header().Set("X-Amz-Object-Lock-Mode", r.Mode)
		w.Header().Set("X-Amz-Object-Lock-Retain-Until-Date", r.RetainUntilDate.Format(time.RFC3339Nano))
	}
	if p.Requested.LegalHold != nil {
		w.Header().Set("X-Amz-Object-Lock-Legal-Hold", p.Requested.LegalHold.Status)
	}
}

// adr: 592
func TestS3ProtectedWriteExactReadback(t *testing.T) {
	for _, bad := range []string{"", "missing hold", "short retention", "duplicate date", "missing proof", "null version", "wrong version"} {
		t.Run(bad, func(t *testing.T) {
			snapshot := testWriteProtection()
			receipt := uuid.NewString()
			puts, heads := 0, 0
			provider := protectionTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut {
					puts++
					if r.Header.Get("X-Amz-Object-Lock-Mode") != "COMPLIANCE" || r.Header.Get("X-Amz-Object-Lock-Legal-Hold") != "ON" || r.Header.Get("X-Amz-Meta-"+ReservedObjectProtectionMetadataKey) != snapshot.Proof() {
						t.Error("policy not dispatched", r.Header)
					}
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("ETag", `"proof"`)
					w.Header().Set("X-Amz-Version-Id", "native-proof")
					return
				}
				heads++
				if r.URL.Query().Get("versionId") != "native-proof" {
					t.Error("mutable readback", r.URL)
				}
				protectedHeadHeaders(w, snapshot, ReservedUploadReceiptMetadataKey, receipt, "native-proof", 3)
				switch bad {
				case "missing hold":
					w.Header().Del("X-Amz-Object-Lock-Legal-Hold")
				case "short retention":
					w.Header().Set("X-Amz-Object-Lock-Retain-Until-Date", snapshot.Requested.Retention.RetainUntilDate.Add(-time.Second).Format(time.RFC3339Nano))
				case "duplicate date":
					w.Header().Add("X-Amz-Object-Lock-Retain-Until-Date", snapshot.Requested.Retention.RetainUntilDate.Format(time.RFC3339Nano))
				case "missing proof":
					w.Header().Del("X-Amz-Meta-" + ReservedObjectProtectionMetadataKey)
				case "null version":
					w.Header().Set("X-Amz-Version-Id", "null")
				case "wrong version":
					w.Header().Set("X-Amz-Version-Id", "native-other")
				}
			}))
			billed := 0
			ctx, err := WithObjectWriteProtection(t.Context(), provider, snapshot, func(context.Context) error { billed++; return nil })
			if err != nil {
				t.Fatal(err)
			}
			out, err := provider.(TrackedObjectWriter).WriteTrackedObject(ctx, "bucket", "key", receipt, strings.NewReader("abc"), 3, ObjectMetadata{})
			if puts != 1 || heads != 1 || billed != 1 || bad == "" && (err != nil || out.VerifiedProtection != snapshot.Proof()) || bad != "" && (!errors.Is(err, ErrUnavailable) || errors.Is(err, ErrWriteRejected)) {
				t.Fatal(out, err, puts, heads, billed)
			}
		})
	}
}
func TestS3ProtectionOnCopyAndMultipart(t *testing.T) {
	for _, kind := range []string{"copy", "multipart"} {
		t.Run(kind, func(t *testing.T) {
			snapshot := testWriteProtection()
			receipt := uuid.NewString()
			mutations, heads := 0, 0
			provider := protectionTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					heads++
					key := ReservedUploadReceiptMetadataKey
					if kind == "multipart" {
						key = ReservedMultipartSessionMetadataKey
					}
					protectedHeadHeaders(w, snapshot, key, receipt, "native-proof", 3)
					return
				}
				if r.Method == http.MethodGet {
					_, _ = io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
					return
				}
				mutations++
				w.Header().Set("Content-Type", "application/xml")
				w.Header().Set("X-Amz-Version-Id", "native-proof")
				if kind == "copy" {
					if r.Header.Get("X-Amz-Object-Lock-Legal-Hold") != "ON" {
						t.Error("copy lost protection")
					}
					_, _ = io.WriteString(w, `<CopyObjectResult><ETag>&quot;proof&quot;</ETag><LastModified>2026-10-05T00:00:00Z</LastModified></CopyObjectResult>`)
					return
				}
				if r.URL.Query().Has("uploads") {
					if r.Header.Get("X-Amz-Object-Lock-Legal-Hold") != "ON" || r.Header.Get("X-Amz-Meta-"+ReservedObjectProtectionMetadataKey) != snapshot.Proof() {
						t.Error("initiation lost policy")
					}
					_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><UploadId>native-upload</UploadId></InitiateMultipartUploadResult>`)
					return
				}
				if r.Header.Get("X-Amz-Object-Lock-Mode") != "" {
					t.Error("completion replaced initiation policy")
				}
				_, _ = io.WriteString(w, `<CompleteMultipartUploadResult><ETag>&quot;proof&quot;</ETag></CompleteMultipartUploadResult>`)
			}))
			ctx, err := WithObjectWriteProtection(t.Context(), provider, snapshot, nil)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "copy" {
				out, e := provider.(TrackedObjectCopier).CopyTrackedObject(ctx, "bucket", receipt, CopyObjectRequest{SourceKey: "source", DestinationKey: "dest", MetadataDirective: "REPLACE", TaggingDirective: "REPLACE"}, CopySourceSnapshot{ETag: `"source"`, SizeBytes: 3})
				if e != nil || out.VerifiedProtection != snapshot.Proof() {
					t.Fatal(out, e)
				}
			} else {
				id, e := provider.(Provider).EnsureMultipartUpload(ctx, "bucket", MultipartCreateRequest{SessionID: receipt, Key: "key", SizeBytes: 3, BeforeRequest: func(context.Context) error { return nil }})
				if e != nil {
					t.Fatal(e)
				}
				out, e := provider.(MultipartResultCompleter).CompleteMultipartWithResult(ctx, "bucket", MultipartCompleteRequest{SessionID: receipt, Key: "key", ProviderUploadID: id, SizeBytes: 3, Parts: []CompletedPart{{PartNumber: 1, ETag: `"part"`}}}, ObjectWriteConditions{})
				if e != nil || out.VerifiedProtection != snapshot.Proof() {
					t.Fatal(out, e)
				}
			}
			if heads != 1 || mutations != map[string]int{"copy": 1, "multipart": 2}[kind] {
				t.Fatal(mutations, heads)
			}
		})
	}
}
func TestS3ProtectedWriteHistoryRecovery(t *testing.T) {
	snapshot := testWriteProtection()
	receipt := uuid.NewString()
	mutations := 0
	provider := protectionTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = fmt.Fprint(w, `<ListVersionsResult><IsTruncated>false</IsTruncated><Version><Key>key</Key><VersionId>retained-proof</VersionId><IsLatest>false</IsLatest><Size>3</Size><ETag>&quot;proof&quot;</ETag></Version></ListVersionsResult>`)
			return
		}
		if r.Method != http.MethodHead {
			mutations++
			w.WriteHeader(500)
			return
		}
		protectedHeadHeaders(w, snapshot, ReservedUploadReceiptMetadataKey, receipt, "retained-proof", 3)
	}))
	ctx, err := WithObjectWriteProtection(t.Context(), provider, snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	page, err := provider.(HistoricalObjectWriteConfirmer).ConfirmTrackedObjectHistory(ctx, "bucket", ObjectHistoryProofRequest{Key: "key", Receipt: receipt, SizeBytes: 3, BeforeRequest: func(context.Context) error { return nil }})
	if err != nil || page.ProviderVersionID != "retained-proof" || page.VerifiedProtection != snapshot.Proof() || mutations != 0 {
		t.Fatal(page, err, mutations)
	}
}

func protectionTestProvider(t *testing.T, handler http.Handler) HistoricalObjectWriteConfirmer {
	t.Helper()
	upstream := httptest.NewTLSServer(handler)
	t.Cleanup(upstream.Close)
	cfg := testBackend()
	cfg.Endpoint = upstream.URL
	raw, err := NewS3(cfg, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	p := raw.(*S3)
	opts := p.client.Options()
	opts.HTTPClient = upstream.Client()
	p.client = s3.New(opts)
	p.signer = s3.NewPresignClient(p.client)
	return p
}
