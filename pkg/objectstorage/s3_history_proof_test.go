package objectstorage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func historyTestProvider(t *testing.T, handler http.Handler) HistoricalObjectWriteConfirmer {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	config := testBackend()
	config.Endpoint = upstream.URL
	p, err := NewS3(config, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	return p.(HistoricalObjectWriteConfirmer)
}

// adr: 539
func TestS3HistoricalReceiptPagination(t *testing.T) {
	key, receipt, version := "key /+%.txt", uuid.NewString(), "private/+?%version"
	var authorized, requests atomic.Int32
	p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) != authorized.Load() {
			t.Error("unmetered provider attempt")
		}
		if r.Method == http.MethodGet {
			q := r.URL.Query()
			if _, ok := q["versions"]; !ok || q.Get("prefix") != key || q.Get("max-keys") != strconv.Itoa(api.ObjectUploadHistoryPageSize) || q.Get("encoding-type") != "url" {
				t.Error("unbounded or wrong inventory", q)
			}
			w.Header().Set("Content-Type", "application/xml")
			if q.Get("key-marker") == "" {
				_, _ = fmt.Fprintf(w, `<ListVersionsResult><EncodingType>url</EncodingType><IsTruncated>true</IsTruncated><NextKeyMarker>%s</NextKeyMarker><NextVersionIdMarker>page-1</NextVersionIdMarker>`, url.PathEscape(key))
				for i := range api.ObjectUploadHistoryPageSize - 3 {
					_, _ = fmt.Fprintf(w, `<Version><Key>%s</Key><VersionId>foreign-%d</VersionId><Size>3</Size></Version>`, url.PathEscape(key), i)
				}
				_, _ = fmt.Fprintf(w, `<DeleteMarker><Key>%s</Key><VersionId>deleted</VersionId></DeleteMarker><Version><Key>%s</Key><VersionId>null</VersionId><Size>3</Size></Version><Version><Key>%s</Key><VersionId>sibling</VersionId><Size>3</Size></Version></ListVersionsResult>`, url.PathEscape(key), url.PathEscape(key), url.PathEscape(key+"-sibling"))
			} else {
				if q.Get("key-marker") != key || q.Get("version-id-marker") != "page-1" {
					t.Error("lost paired continuation", q)
				}
				_, _ = fmt.Fprintf(w, `<ListVersionsResult><EncodingType>url</EncodingType><IsTruncated>false</IsTruncated><Version><Key>%s</Key><VersionId>%s</VersionId><Size>3</Size></Version></ListVersionsResult>`, url.PathEscape(key), version)
			}
			return
		}
		id := r.URL.Query().Get("versionId")
		if r.Method != http.MethodHead || r.URL.Path != "/bucket/"+key || id == "null" || id == "sibling" || id == "deleted" {
			t.Error("wrong version probe", r.Method, r.URL)
		}
		w.Header().Set("Content-Length", "3")
		w.Header().Set("ETag", `"historical"`)
		w.Header().Set("X-Amz-Version-Id", id)
		marker := "foreign-receipt"
		if id == version {
			marker = receipt
		}
		w.Header().Set("X-Amz-Meta-"+ReservedUploadReceiptMetadataKey, marker)
	}))
	r := ObjectHistoryProofRequest{Key: key, Receipt: receipt, SizeBytes: 3, BeforeRequest: func(_ctx context.Context) error { authorized.Add(1); return nil }}
	page, err := p.ConfirmTrackedObjectHistory(t.Context(), "bucket", r)
	if !errors.Is(err, ErrConflict) || page.Cursor == "" || !page.VersionsObserved || requests.Load() != api.ObjectUploadHistoryPageSize-2 {
		t.Fatal(page, err, requests.Load())
	}
	r.Cursor = page.Cursor
	page, err = p.ConfirmTrackedObjectHistory(t.Context(), "bucket", r)
	if err != nil || page.ETag != `"historical"` || page.ProviderVersionID != version || page.Cursor != "" || !page.VersionsObserved || requests.Load() != api.ObjectUploadHistoryPageSize {
		t.Fatal(page, err, requests.Load())
	}
	raw, err := json.Marshal(page)
	if err != nil || strings.Contains(string(raw), "private") || strings.Contains(string(raw), "Cursor") {
		t.Fatal("private recovery data serialized", string(raw), err)
	}
}

func TestS3HistoricalProofFailuresRetainCursor(t *testing.T) {
	receipt := uuid.NewString()
	for _, tc := range []struct {
		name, responseVersion, marker, etag string
		size, status                        int
		want                                error
	}{
		{"wrong receipt", "version", "other", `"etag"`, 3, 200, ErrConflict},
		{"wrong size", "version", receipt, `"etag"`, 4, 200, ErrConflict},
		{"missing version", "", receipt, `"etag"`, 3, 200, ErrUnavailable},
		{"different version", "other-version", receipt, `"etag"`, 3, 200, ErrUnavailable},
		{"missing etag", "version", receipt, "", 3, 200, ErrUnavailable},
		{"gone version", "", "", "", 0, 404, ErrConflict},
		{"timeout", "", "", "", 0, 408, ErrUnavailable},
		{"server failure", "", "", "", 0, 500, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method == http.MethodGet {
					w.Header().Set("Content-Type", "application/xml")
					_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>false</IsTruncated><Version><Key>key</Key><VersionId>version</VersionId><Size>3</Size></Version></ListVersionsResult>`)
					return
				}
				w.Header().Set("Content-Length", strconv.Itoa(tc.size))
				w.Header().Set("ETag", tc.etag)
				w.Header().Set("X-Amz-Version-Id", tc.responseVersion)
				w.Header().Set("X-Amz-Meta-"+ReservedUploadReceiptMetadataKey, tc.marker)
				w.WriteHeader(tc.status)
			}))
			r := ObjectHistoryProofRequest{Key: "key", Receipt: receipt, SizeBytes: 3, BeforeRequest: func(context.Context) error { return nil }}
			c := s3HistoryCursor{Binding: historyBinding("bucket", r), Key: "key", Version: "previous-page"}
			raw, _ := json.Marshal(c)
			r.Cursor = base64.RawURLEncoding.EncodeToString(raw)
			page, err := p.ConfirmTrackedObjectHistory(t.Context(), "bucket", r)
			wantCursor := r.Cursor
			if errors.Is(tc.want, ErrConflict) {
				wantCursor = ""
			}
			if !errors.Is(err, tc.want) || page.Cursor != wantCursor || !page.VersionsObserved || requests.Load() != 2 {
				t.Fatal(page, err, requests.Load())
			}
		})
	}
}

func TestS3HistoryCursorAndAdmissionFailClosed(t *testing.T) {
	var requests atomic.Int32
	p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>false</IsTruncated></ListVersionsResult>`)
	}))
	r := ObjectHistoryProofRequest{Key: "key", Receipt: uuid.NewString(), SizeBytes: 3, BeforeRequest: func(context.Context) error { return nil }}
	c := s3HistoryCursor{Binding: historyBinding("bucket", r), Key: "key", Version: "page"}
	raw, _ := json.Marshal(c)
	r.Cursor = base64.RawURLEncoding.EncodeToString(raw)
	for _, mutate := range []func(*ObjectHistoryProofRequest){func(r *ObjectHistoryProofRequest) { r.Receipt = uuid.NewString() }, func(r *ObjectHistoryProofRequest) { r.Key = "other" }, func(r *ObjectHistoryProofRequest) { r.SizeBytes = 4 }, func(r *ObjectHistoryProofRequest) { r.Cursor = "invalid" }, func(r *ObjectHistoryProofRequest) {
		r.Cursor = strings.Repeat("x", api.ObjectUploadHistoryCursorMaxBytes+1)
	}, func(r *ObjectHistoryProofRequest) { r.BeforeRequest = nil }} {
		bad := r
		mutate(&bad)
		if _, err := p.ConfirmTrackedObjectHistory(t.Context(), "bucket", bad); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	if _, err := p.ConfirmTrackedObjectHistory(t.Context(), "other-bucket", r); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	recordErr := errors.New("ledger unavailable")
	r.BeforeRequest = func(context.Context) error { return recordErr }
	if _, err := p.ConfirmTrackedObjectHistory(t.Context(), "bucket", r); !errors.Is(err, recordErr) || requests.Load() != 0 {
		t.Fatal(err, requests.Load())
	}
}

func TestS3HistoryInvalidPagesAndUnsupportedProvider(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       error
	}{
		{"missing continuation", `<ListVersionsResult><IsTruncated>true</IsTruncated></ListVersionsResult>`, 200, ErrUnavailable},
		{"outside prefix", `<ListVersionsResult><IsTruncated>true</IsTruncated><NextKeyMarker>foreign</NextKeyMarker></ListVersionsResult>`, 200, ErrUnavailable},
		{"malformed XML", `<ListVersionsResult><IsTruncated>`, 200, ErrUnavailable},
		{"unsupported", `<Error><Code>NotImplemented</Code><Message>private provider identity</Message></Error>`, 501, ErrUnsupported},
		{"denied", `<Error><Code>AccessDenied</Code><Message>private provider identity</Message></Error>`, 403, ErrConfiguration},
		{"unavailable", `<Error><Code>InternalError</Code><Message>private provider identity</Message></Error>`, 500, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			r := ObjectHistoryProofRequest{Key: "key", Receipt: uuid.NewString(), SizeBytes: 3, BeforeRequest: func(context.Context) error { return nil }}
			page, err := p.ConfirmTrackedObjectHistory(t.Context(), "bucket", r)
			if !errors.Is(err, tc.want) || page.Cursor != "" || page.ETag != "" || requests.Load() != 1 || strings.Contains(err.Error(), "private") {
				t.Fatal(page, err, requests.Load())
			}
		})
	}
}

func TestS3HistoryCursorSupportsLongEscapedKeys(t *testing.T) {
	r := ObjectHistoryProofRequest{Key: strings.Repeat("<", 1024), Receipt: uuid.NewString(), SizeBytes: 3}
	old := s3HistoryCursor{Binding: historyBinding("bucket", r)}
	version := strings.Repeat("\\", api.ObjectProviderVersionIDMaxBytes)
	cursor, err := nextHistoryCursor(old, &s3.ListObjectVersionsOutput{IsTruncated: aws.Bool(true), EncodingType: types.EncodingTypeUrl, NextKeyMarker: aws.String(url.PathEscape(r.Key)), NextVersionIdMarker: aws.String(version)}, r.Key)
	if err != nil || len(cursor) > api.ObjectUploadHistoryCursorMaxBytes {
		t.Fatal("valid escaped key exceeded cursor budget", len(cursor), err)
	}
	r.Cursor = cursor
	decoded, err := decodeHistoryCursor("bucket", r)
	if err != nil || decoded.Key != r.Key || decoded.Version != version {
		t.Fatal("cursor changed native markers", decoded, err)
	}
}
