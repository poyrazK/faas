package objectstorage

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/smithy-go/middleware"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type metadataBody struct {
	remaining, read int64
	closed          bool
	err             error
}

func (b *metadataBody) Read(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	if b.remaining == 0 {
		return 0, io.EOF
	}
	n := int(min(int64(len(p)), b.remaining))
	for i := range n {
		p[i] = ' '
	}
	b.remaining -= int64(n)
	b.read += int64(n)
	return n, nil
}
func (b *metadataBody) Close() error { b.closed = true; return nil }

type metadataRoundTripper func(*http.Request) (*http.Response, error)

func (f metadataRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestS3MetadataResponseBoundBeforeDecode(t *testing.T) {
	limit := api.MaxObjectProviderMetadataResponseBytes
	for _, tc := range []struct {
		name, operation, method string
		length, bytes, read     int64
		status                  int
		readErr                 error
		wantErr, streaming      bool
	}{
		{name: "declared oversized", length: limit + 1, bytes: limit + 1, status: 200, wantErr: true},
		{name: "chunked oversized", length: -1, bytes: limit + 10, read: limit + 1, status: 200, wantErr: true},
		{name: "understated oversized", length: 1, bytes: limit + 10, read: limit + 1, status: 200, wantErr: true},
		{name: "exact boundary", length: limit, bytes: limit, read: limit, status: 200},
		{name: "interrupted body", length: -1, status: 200, readErr: io.ErrUnexpectedEOF, wantErr: true},
		{name: "large head object", method: "HEAD", length: limit + 10, status: 200},
		{name: "streaming get", operation: "GetObject", length: limit + 10, bytes: limit + 10, status: 200, streaming: true},
		{name: "streaming range", operation: "GetObject", length: limit + 10, bytes: limit + 10, status: 206, streaming: true},
		{name: "get error bounded", operation: "GetObject", length: -1, bytes: limit + 10, read: limit + 1, status: 503, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &metadataBody{remaining: tc.bytes, err: tc.readErr}
			transport := s3ResponseTransport{base: metadataRoundTripper(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, ContentLength: tc.length, Body: body}, nil
			})}
			method := tc.method
			if method == "" {
				method = "GET"
			}
			request, err := http.NewRequestWithContext(middleware.WithOperationName(t.Context(), tc.operation), method, "https://provider.test/bucket", nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := transport.RoundTrip(request)
			if errors.Is(err, ErrUnavailable) != tc.wantErr || body.read != tc.read || body.closed == tc.streaming {
				t.Fatalf("response=%v err=%v read=%d closed=%v", response != nil, err, body.read, body.closed)
			}
			if response != nil {
				defer response.Body.Close()
				if tc.streaming && response.Body != body {
					t.Fatal("GET body stopped streaming")
				}
			}
		})
	}
}

func TestS3SDKMetadataBoundPreservesUnknownMutations(t *testing.T) {
	for _, operation := range []string{"listing", "put", "multipart"} {
		t.Run(operation, func(t *testing.T) {
			var calls atomic.Int32
			p := listingFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "application/xml")
				w.Header().Set("ETag", `"ack"`)
				w.Header().Set("X-Amz-Version-Id", "native-version")
				w.Header().Set("Content-Length", strconv.FormatInt(api.MaxObjectProviderMetadataResponseBytes+1, 10))
				w.WriteHeader(200)
				_, _ = io.CopyN(w, &metadataBody{remaining: api.MaxObjectProviderMetadataResponseBytes + 1}, api.MaxObjectProviderMetadataResponseBytes+1)
			})
			var err error
			switch operation {
			case "listing":
				_, err = p.ListObjects(t.Context(), "bucket", "", "", 1000)
			case "put":
				_, err = p.(TrackedObjectWriter).WriteTrackedObject(t.Context(), "bucket", "key", uuid.NewString(), strings.NewReader("abc"), 3, ObjectMetadata{})
			case "multipart":
				r := MultipartCompleteRequest{SessionID: uuid.NewString(), Key: "key", ProviderUploadID: "upload", SizeBytes: 3, Parts: []CompletedPart{{PartNumber: 1, ETag: `"part"`}}}
				var result MultipartCompletionResult
				result, err = p.(MultipartResultCompleter).CompleteMultipartWithResult(t.Context(), "bucket", r, ObjectWriteConditions{})
				if !result.VersionsObserved || result.ETag != "" {
					t.Fatal("lost conservative version observation or accepted oversize ACK", result)
				}
			}
			if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrWriteRejected) || operation != "listing" && calls.Load() != 1 {
				t.Fatal("incorrect uncertain-response handling", err, calls.Load())
			}
		})
	}
}

func TestS3SDKLargeObjectRemainsStreaming(t *testing.T) {
	size := api.MaxObjectProviderMetadataResponseBytes + 1
	p := listingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		if r.Method != http.MethodHead {
			_, _ = io.CopyN(w, &metadataBody{remaining: size}, size)
		}
	}).(*S3)
	if got, err := p.ObjectSize(t.Context(), "bucket", "key"); err != nil || got != size {
		t.Fatal("object length confused with metadata size", got, err)
	}
	body, err := p.ReadObject(t.Context(), "bucket", "key")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	if got, err := io.Copy(io.Discard, body); err != nil || got != size {
		t.Fatal("large GET truncated", got, err)
	}
}
