package objectstorage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"github.com/google/uuid"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/api/option"
)

func TestGCSMultipartActualResult(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       error
	}{
		{"actual", `<CompleteMultipartUploadResult><ETag>&quot;actual-gcs&quot;</ETag></CompleteMultipartUploadResult>`, nil},
		{"missing", `<CompleteMultipartUploadResult/>`, ErrUnavailable},
		{"wrong root", `<CopyObjectResult><ETag>&quot;actual-gcs&quot;</ETag></CopyObjectResult>`, ErrUnavailable},
		{"truncated", `<CompleteMultipartUploadResult><ETag>`, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, billed := 0, 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || calls != billed {
					t.Error("unmetered completion", r.Method, calls, billed)
				}
				w.Header().Set("Content-Type", "application/xml")
				_, _ = io.WriteString(w, tc.body)
			}))
			defer upstream.Close()
			p := testGCS(upstream.URL, &fakeGCSStore{})
			p.httpClient = upstream.Client()
			r := MultipartCompleteRequest{SessionID: uuid.NewString(), Key: "key", ProviderUploadID: "upload", SizeBytes: 10, Parts: []CompletedPart{{PartNumber: 1, ETag: `"part"`}}, BeforeRequest: func(context.Context) error { billed++; return nil }}
			out, err := p.CompleteMultipartWithResult(t.Context(), "bucket", r, ObjectWriteConditions{})
			if !errors.Is(err, tc.want) || calls != 1 || err == nil && (out.ETag != `"actual-gcs"` || out.ProviderVersionID != "") {
				t.Fatal(out, err, calls)
			}
		})
	}
}

func TestGCSObjectStateProbeDoesNotHideSDKRetries(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(503)
		_, _ = io.WriteString(w, `{"error":{"code":503,"message":"temporarily unavailable"}}`)
	}))
	defer upstream.Close()
	client, err := storage.NewClient(t.Context(), option.WithEndpoint(upstream.URL), option.WithoutAuthentication(), option.WithHTTPClient(upstream.Client()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	client.SetRetry(storage.WithPolicy(storage.RetryAlways), storage.WithBackoff(gax.Backoff{Initial: time.Millisecond, Max: time.Millisecond, Multiplier: 1}))
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, err = (&googleGCSStore{client: client}).ObjectState(ctx, "bucket", "key")
	if err == nil || calls.Load() != 1 {
		t.Fatal("proof probe issued hidden SDK attempts", err, calls.Load())
	}
}

func TestS3MultipartAmbiguousHistoricalProof(t *testing.T) {
	for _, field := range []string{"X-Amz-Version-Id", "ETag", "X-Amz-Meta-Gregale-Upload-Id", "accounting"} {
		t.Run(field, func(t *testing.T) {
			receipt := uuid.NewString()
			calls, billed := 0, 0
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/xml")
				switch {
				case r.Method == http.MethodPost:
					w.WriteHeader(404)
					_, _ = io.WriteString(w, `<Error><Code>NoSuchUpload</Code></Error>`)
				case r.Method == http.MethodGet:
					_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>false</IsTruncated><Version><Key>key</Key><VersionId>native</VersionId><Size>10</Size></Version></ListVersionsResult>`)
				case r.URL.Query().Get("versionId") == "native":
					w.Header().Set("Content-Length", "10")
					w.Header().Set("ETag", `"actual"`)
					w.Header().Set("X-Amz-Version-Id", "native")
					w.Header().Set("X-Amz-Meta-Gregale-Upload-Id", receipt)
					w.Header().Add(field, "ambiguous")
				default:
					w.WriteHeader(404)
				}
			})).(MultipartResultCompleter)
			sentinel := fmt.Errorf("accounting unavailable")
			r := MultipartCompleteRequest{SessionID: receipt, Key: "key", ProviderUploadID: "upload", SizeBytes: 10, Parts: []CompletedPart{{PartNumber: 1, ETag: `"part"`}}, Recovering: true, BeforeRequest: func(context.Context) error {
				billed++
				if field == "accounting" && billed == 4 {
					return sentinel
				}
				return nil
			}}
			out, err := p.CompleteMultipartWithResult(t.Context(), "bucket", r, ObjectWriteConditions{})
			want, attempts := ErrUnavailable, 4
			if field == "accounting" {
				want, attempts = sentinel, 3
			}
			if !errors.Is(err, want) || !out.VersionsObserved || calls != attempts {
				t.Fatal("ambiguous proof completed", out, err, calls)
			}
		})
	}
}
