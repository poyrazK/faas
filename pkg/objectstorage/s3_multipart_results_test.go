package objectstorage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 402
func TestS3MultipartActualResult(t *testing.T) {
	for _, tc := range []struct {
		name, etag, version string
		duplicate           bool
		want                error
	}{
		{"unversioned", `"provider-checksum-3"`, "", false, nil},
		{"native", `"provider-checksum-3"`, "private/+?version", false, nil},
		{"null", `"provider-checksum-3"`, "null", false, nil},
		{"missing etag", "", "native", false, ErrUnavailable},
		{"empty etag", "   ", "native", false, ErrUnavailable},
		{"oversized etag", strings.Repeat("a", api.MaxObjectWriteETagBytes+1), "native", false, ErrUnavailable},
		{"duplicate version", `"etag"`, "native", true, ErrUnavailable},
		{"oversized version", `"etag"`, strings.Repeat("v", api.ObjectProviderVersionIDMaxBytes+1), false, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, billed := 0, 0
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || r.URL.Query().Get("uploadId") != "upload" || billed != calls {
					t.Error("unexpected or unmetered completion", r.Method, r.URL, billed, calls)
				}
				w.Header().Set("Content-Type", "application/xml")
				w.Header().Set("X-Amz-Version-Id", tc.version)
				if tc.duplicate {
					w.Header().Add("X-Amz-Version-Id", "another")
				}
				_, _ = fmt.Fprintf(w, `<CompleteMultipartUploadResult><ETag>%s</ETag></CompleteMultipartUploadResult>`, tc.etag)
			})).(MultipartResultCompleter)
			r := MultipartCompleteRequest{SessionID: uuid.NewString(), Key: "key", ProviderUploadID: "upload", SizeBytes: 10, Parts: []CompletedPart{{PartNumber: 3, ETag: `"part"`}}, BeforeRequest: func(context.Context) error { billed++; return nil }}
			out, err := p.CompleteMultipartWithResult(t.Context(), "bucket", r, ObjectWriteConditions{})
			if !errors.Is(err, tc.want) || calls != 1 || err == nil && (out.ETag != tc.etag || out.ProviderVersionID != tc.version) {
				t.Fatal(out, err, calls)
			}
			if tc.version == "native" && !out.VersionsObserved {
				t.Fatal("lost native observation on an uncertain acknowledgment", out)
			}
		})
	}
}

func TestS3MultipartTruncatedNativeAcknowledgmentObservesHistory(t *testing.T) {
	p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Header().Set("X-Amz-Version-Id", "native")
		_, _ = io.WriteString(w, `<CompleteMultipartUploadResult><ETag>`)
	})).(MultipartResultCompleter)
	r := MultipartCompleteRequest{SessionID: uuid.NewString(), Key: "key", ProviderUploadID: "upload", SizeBytes: 10, Parts: []CompletedPart{{PartNumber: 1, ETag: `"part"`}}}
	out, err := p.CompleteMultipartWithResult(t.Context(), "bucket", r, ObjectWriteConditions{})
	if !errors.Is(err, ErrUnavailable) || !out.VersionsObserved {
		t.Fatal("uncertain response forgot its native observation", out, err)
	}
}

func TestS3MultipartHistoricalCompletionProof(t *testing.T) {
	for _, tc := range []struct {
		name, responseCode, historicalReceipt string
		currentMarker, recovering, alive      bool
		want                                  error
	}{
		{"hidden after overwrite", "NoSuchUpload", "exact", false, true, false, nil},
		{"hidden after delete", "NoSuchUpload", "exact", true, true, false, nil},
		{"conditional replay after overwrite", "PreconditionFailed", "exact", false, true, false, nil},
		{"conditional replay race", "ConditionalRequestConflict", "exact", true, true, false, nil},
		{"same size and etag foreign receipt", "NoSuchUpload", "foreign", false, true, false, ErrUnavailable},
		{"missing upload is uncertain initially", "NoSuchUpload", "foreign", false, false, false, ErrUnavailable},
		{"live rejected upload", "PreconditionFailed", "exact", false, true, true, ErrPreconditionFailed},
		{"first conditional rejection", "PreconditionFailed", "exact", false, false, false, ErrPreconditionFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receipt := uuid.NewString()
			calls, billed, listings := 0, 0, 0
			// Virtual size verifies multipart recovery supports objects beyond a single PUT.
			size := api.MaxObjectSinglePutBytes + 1
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls != billed {
					t.Error("unmetered recovery attempt", calls, billed)
				}
				w.Header().Set("Content-Type", "application/xml")
				switch {
				case r.Method == http.MethodPost:
					w.WriteHeader(412)
					_, _ = fmt.Fprintf(w, `<Error><Code>%s</Code></Error>`, tc.responseCode)
				case r.Method == http.MethodGet && r.URL.Query().Has("uploadId"):
					if tc.alive {
						_, _ = io.WriteString(w, `<ListPartsResult><IsTruncated>false</IsTruncated></ListPartsResult>`)
					} else {
						w.WriteHeader(404)
						_, _ = io.WriteString(w, `<Error><Code>NoSuchUpload</Code></Error>`)
					}
				case r.Method == http.MethodGet && r.URL.Query().Has("versions"):
					listings++
					_, _ = fmt.Fprintf(w, `<ListVersionsResult><IsTruncated>false</IsTruncated><Version><Key>key</Key><VersionId>private-old</VersionId><Size>%d</Size></Version></ListVersionsResult>`, size)
				case r.Method == http.MethodHead:
					w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
					w.Header().Set("ETag", `"same-etag"`)
					w.Header().Set("X-Amz-Version-Id", "private-current")
					w.Header().Set("X-Amz-Meta-"+ReservedMultipartSessionMetadataKey, "foreign")
					if r.URL.Query().Get("versionId") == "private-old" {
						w.Header().Set("X-Amz-Version-Id", "private-old")
						if tc.historicalReceipt == "exact" {
							w.Header().Set("X-Amz-Meta-"+ReservedMultipartSessionMetadataKey, receipt)
						}
					} else if tc.currentMarker {
						w.Header().Set("X-Amz-Delete-Marker", "true")
						w.WriteHeader(404)
					}
				default:
					t.Error("unexpected request", r.Method, r.URL)
				}
			})).(MultipartResultCompleter)
			r := MultipartCompleteRequest{SessionID: receipt, Key: "key", ProviderUploadID: "upload", SizeBytes: size, Parts: []CompletedPart{{PartNumber: 1, ETag: `"part"`}}, Recovering: tc.recovering, BeforeRequest: func(context.Context) error { billed++; return nil }}
			out, err := p.CompleteMultipartWithResult(t.Context(), "bucket", r, ObjectWriteConditions{IfNoneMatch: "*"})
			if !errors.Is(err, tc.want) || !out.VersionsObserved || err == nil && (out.ProviderVersionID != "private-old" || out.ETag != `"same-etag"`) {
				t.Fatal(out, err, calls)
			}
			if errors.Is(tc.want, ErrPreconditionFailed) && listings != 0 || !errors.Is(tc.want, ErrPreconditionFailed) && listings != 1 {
				t.Fatal("incorrect rejection proof/history traversal", listings)
			}
		})
	}
}

func TestS3MultipartRecoveryCursorBindingAndRequestFailure(t *testing.T) {
	receipt := uuid.NewString()
	calls := 0
	p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/xml")
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `<Error><Code>NoSuchUpload</Code></Error>`)
		case http.MethodHead:
			w.WriteHeader(404)
		case http.MethodGet:
			if r.URL.Query().Get("key-marker") == "" {
				_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>true</IsTruncated><NextKeyMarker>key</NextKeyMarker><NextVersionIdMarker>page</NextVersionIdMarker></ListVersionsResult>`)
			} else {
				_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>false</IsTruncated></ListVersionsResult>`)
			}
		}
	})).(MultipartResultCompleter)
	r := MultipartCompleteRequest{SessionID: receipt, Key: "key", ProviderUploadID: "upload", SizeBytes: 10, Parts: []CompletedPart{{PartNumber: 1, ETag: `"part"`}}, Recovering: true}
	out, err := p.CompleteMultipartWithResult(t.Context(), "bucket", r, ObjectWriteConditions{})
	if !errors.Is(err, ErrUnavailable) || out.RecoveryCursor == "" || calls != 3 {
		t.Fatal(out, err, calls)
	}
	r.RecoveryCursor = out.RecoveryCursor
	for _, mutate := range []func(*MultipartCompleteRequest){func(r *MultipartCompleteRequest) { r.SessionID = uuid.NewString() }, func(r *MultipartCompleteRequest) { r.Key = "other" }, func(r *MultipartCompleteRequest) { r.SizeBytes++ }} {
		bad := r
		mutate(&bad)
		if _, err = p.CompleteMultipartWithResult(t.Context(), "bucket", bad, ObjectWriteConditions{}); !errors.Is(err, ErrInvalid) || calls != 3 {
			t.Fatal("unbound cursor dispatched", err, calls)
		}
	}
	regular := multipartHistoryRequest(r)
	regular.MultipartSession = false
	if _, err = decodeHistoryCursor("bucket", regular); !errors.Is(err, ErrInvalid) {
		t.Fatal("multipart cursor crossed receipt kind", err)
	}
	sentinel := errors.New("accounting unavailable")
	for failAt := 1; failAt <= 3; failAt++ {
		billed, before := 0, calls
		r.BeforeRequest = func(context.Context) error {
			billed++
			if billed == failAt {
				return sentinel
			}
			return nil
		}
		_, err = p.CompleteMultipartWithResult(t.Context(), "bucket", r, ObjectWriteConditions{})
		if !errors.Is(err, sentinel) || calls-before != failAt-1 {
			t.Fatal("request passed failed admission", err, calls-before, failAt)
		}
	}
	r.BeforeRequest = nil
	out, err = p.CompleteMultipartWithResult(t.Context(), "bucket", r, ObjectWriteConditions{})
	if !errors.Is(err, ErrUnavailable) || out.RecoveryCursor != "" {
		t.Fatal("full sweep fabricated failure or retained exhausted cursor", out, err)
	}
}
