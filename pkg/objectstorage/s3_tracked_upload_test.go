package objectstorage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

func TestS3TrackedUploadSingleAttemptAndReceipt(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		etag     string
		rejected bool
	}{
		{"accepted", 200, `"etag"`, false}, {"server failure", 500, "", false}, {"rejected", 403, "", true}, {"timeout", 408, "", false}, {"missing etag", 200, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receipt := uuid.NewString()
			var puts atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut {
					puts.Add(1)
					if r.Header.Get("X-Amz-Meta-"+ReservedUploadReceiptMetadataKey) != receipt || r.ContentLength != 3 {
						t.Error("missing write identity or size", r.Header, r.ContentLength)
					}
					data, e := io.ReadAll(r.Body)
					if e != nil || string(data) != "abc" {
						t.Error(string(data), e)
					}
					w.Header().Set("ETag", tc.etag)
					w.Header().Set("Content-Type", "application/xml")
					w.WriteHeader(tc.status)
					if tc.status >= 400 {
						_, _ = io.WriteString(w, `<Error><Code>AccessDenied</Code><Message>private detail</Message></Error>`)
					}
					return
				}
				w.Header().Set("Content-Length", "3")
				w.Header().Set("ETag", `"etag"`)
				w.Header().Set("X-Amz-Meta-"+ReservedUploadReceiptMetadataKey, receipt)
			}))
			defer upstream.Close()
			config := testBackend()
			config.Endpoint = upstream.URL
			provider, e := NewS3(config, testCredentials)
			if e != nil {
				t.Fatal(e)
			}
			writer := provider.(TrackedObjectWriter)
			result, e := writer.WriteTrackedObject(context.Background(), "bucket", "key", receipt, io.LimitReader(strings.NewReader("abcdef"), 3), 3, ObjectMetadata{ContentType: "image/png"})
			if tc.status == 200 && tc.etag != "" {
				if e != nil || result.ETag != tc.etag {
					t.Fatal(result, e)
				}
			} else if e == nil {
				t.Fatal("unconfirmed success")
			}
			if errors.Is(e, ErrWriteRejected) != tc.rejected {
				t.Fatal("wrong rejection classification", e)
			}
			if puts.Load() != 1 {
				t.Fatal("provider write attempts", puts.Load(), e)
			}
			result, e = writer.ConfirmTrackedObject(context.Background(), "bucket", "key", receipt, 3)
			if e != nil || result.ETag != `"etag"` {
				t.Fatal("receipt recovery", result, e)
			}
		})
	}
}
func TestS3TrackedUploadRequiresExactProof(t *testing.T) {
	receipt := uuid.NewString()
	for _, tc := range []struct {
		name, marker, etag string
		size, status       int
	}{
		{name: "absent", status: 404}, {name: "foreign receipt", marker: uuid.NewString(), etag: `"etag"`, size: 3},
		{name: "wrong size", marker: receipt, etag: `"etag"`, size: 4}, {name: "missing etag", marker: receipt, size: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", strconv.Itoa(tc.size))
				w.Header().Set("ETag", tc.etag)
				w.Header().Set("X-Amz-Meta-"+ReservedUploadReceiptMetadataKey, tc.marker)
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
			}))
			defer upstream.Close()
			config := testBackend()
			config.Endpoint = upstream.URL
			provider, e := NewS3(config, testCredentials)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = provider.(TrackedObjectWriter).ConfirmTrackedObject(context.Background(), "bucket", "key", receipt, 3); e == nil {
				t.Fatal("accepted incomplete proof")
			}
		})
	}
	if e := ValidateObjectMetadata(ObjectMetadata{Metadata: map[string]string{strings.ToUpper(ReservedUploadReceiptMetadataKey): receipt}}); !errors.Is(e, ErrInvalid) {
		t.Fatal("client can forge receipt", e)
	}
}

// adr: 393
func TestS3TrackedGatewayPresign(t *testing.T) {
	receipt := uuid.NewString()
	provider, err := NewS3(testBackend(), testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	signer := provider.(TrackedObjectPresigner)
	size := int64(3)
	r := SignRequest{Method: http.MethodPut, Key: "key", SizeBytes: &size, ExpiresIn: 60, ContentType: "text/plain", Metadata: map[string]string{"owner": "customer"}, Tags: map[string]string{"kind": "test"}}
	signed, err := signer.PresignTrackedPut(t.Context(), "bucket", r, ObjectWriteConditions{IfNoneMatch: "*"}, receipt)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPut, signed.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range signed.Headers {
		request.Header.Set(k, v)
	}
	marker := request.Header.Get("X-Amz-Meta-" + ReservedUploadReceiptMetadataKey)
	if marker == "" {
		marker = request.URL.Query().Get("X-Amz-Meta-" + ReservedUploadReceiptMetadataKey)
	}
	if marker != receipt || request.Header.Get("If-None-Match") != "*" {
		t.Fatal("receipt/condition binding missing")
	}
	if _, ok := r.Metadata[ReservedUploadReceiptMetadataKey]; ok {
		t.Fatal("caller metadata mutated")
	}
	forged := r
	forged.Metadata = map[string]string{ReservedUploadReceiptMetadataKey: receipt}
	if _, err = signer.PresignTrackedPut(t.Context(), "bucket", forged, ObjectWriteConditions{}, receipt); !errors.Is(err, ErrInvalid) {
		t.Fatal("caller marker accepted", err)
	}
	if _, err = provider.Presign(t.Context(), "bucket", forged); !errors.Is(err, ErrInvalid) {
		t.Fatal("ordinary signing allowed reserved marker", err)
	}
	if _, err = signer.PresignTrackedPut(t.Context(), "bucket", r, ObjectWriteConditions{}, "bad-id"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestS3TrackedZeroByteConfirmationRequiresSize(t *testing.T) {
	receipt := uuid.NewString()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Transfer-Encoding", "chunked")
		w.Header().Set("ETag", `"etag"`)
		w.Header().Set("X-Amz-Meta-"+ReservedUploadReceiptMetadataKey, receipt)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	config := testBackend()
	config.Endpoint = upstream.URL
	p, err := NewS3(config, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.(ObjectWriteConfirmer).ConfirmTrackedObject(t.Context(), "bucket", "key", receipt, 0); err == nil {
		t.Fatal("missing size treated as zero-byte proof")
	}
}
