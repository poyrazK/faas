package objectstorage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 939
func TestGCSConditionalPutCompetingWriters(t *testing.T) {
	for _, condition := range []ObjectWriteConditions{{IfNoneMatch: "*"}, {IfMatch: `"old"`}, {IfMatch: "*"}} {
		t.Run(condition.IfMatch+condition.IfNoneMatch, func(t *testing.T) {
			var mu sync.Mutex
			var heads, puts, meters atomic.Int32
			generation := int64(0)
			if condition.IfMatch != "" {
				generation = 11
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.URL.Path != "/physical/目录 /+%.txt" {
					t.Error("key changed", r.URL.Path)
				}
				if r.Method == http.MethodHead {
					heads.Add(1)
					w.Header().Set("ETag", `"old"`)
					w.Header().Set("X-Goog-Generation", strconv.FormatInt(generation, 10))
					w.Header().Set("Content-Length", "3")
					return
				}
				puts.Add(1)
				if r.Method != http.MethodPut || r.Header.Get("If-Match") != "" || r.Header.Get("If-None-Match") != "" || r.Header.Get("X-Goog-Meta-"+ReservedUploadReceiptMetadataKey) == "" {
					t.Error("wrong native conditional write")
				}
				if r.Header.Get("X-Goog-If-Generation-Match") != strconv.FormatInt(generation, 10) {
					w.WriteHeader(http.StatusPreconditionFailed)
					return
				}
				body, _ := io.ReadAll(r.Body)
				if string(body) != "new" {
					t.Error("wrong payload")
				}
				generation++
				w.Header().Set("ETag", `"new"`)
				w.Header().Set("X-Goog-Generation", strconv.FormatInt(generation, 10))
			}))
			defer server.Close()
			p := testGCS(server.URL, &fakeGCSStore{})
			ctx := WithConditionalWriteRequestRecorder(t.Context(), func(context.Context) error { meters.Add(1); return nil })
			size := int64(3)
			var requests []SignedRequest
			for range 2 {
				signed, err := p.PresignTrackedPut(ctx, "physical", SignRequest{Method: "PUT", Key: "目录 /+%.txt", SizeBytes: &size}, condition, uuid.NewString())
				if err != nil {
					t.Fatal(err)
				}
				u, _ := url.Parse(signed.URL)
				if !strings.Contains(u.Query().Get("X-Goog-SignedHeaders"), "x-goog-if-generation-match") {
					t.Fatal("condition not covered by native signature")
				}
				requests = append(requests, signed)
			}
			var wg sync.WaitGroup
			var wins, rejections atomic.Int32
			for _, signed := range requests {
				wg.Go(func() {
					r, _ := http.NewRequestWithContext(ctx, "PUT", signed.URL, strings.NewReader("new"))
					for name, value := range signed.Headers {
						r.Header.Set(name, value)
					}
					response, err := server.Client().Do(r)
					if err != nil {
						t.Error(err)
						return
					}
					_ = response.Body.Close()
					switch response.StatusCode {
					case http.StatusOK:
						wins.Add(1)
					case http.StatusPreconditionFailed:
						rejections.Add(1)
					default:
						t.Error("unexpected result", response.StatusCode)
					}
				})
			}
			wg.Wait()
			if wins.Load() != 1 || rejections.Load() != 1 || puts.Load() != 2 || meters.Load() != heads.Load() || condition.IfMatch == "" && heads.Load() != 0 || condition.IfMatch != "" && heads.Load() != 2 {
				t.Fatal("lost update or unmetered observation", wins.Load(), rejections.Load(), puts.Load(), heads.Load(), meters.Load())
			}
		})
	}
}

// adr: 939
func TestGCSConditionalPutProbeFailuresNeverSign(t *testing.T) {
	for _, tc := range []struct {
		name, generation, etag string
		status                 int
		meterErr, want         error
	}{
		{"mismatch", "11", `"other"`, 200, nil, ErrPreconditionFailed},
		{"missing", "", "", 404, nil, ErrNotFound},
		{"malformed generation", "011", `"old"`, 200, nil, ErrUnavailable},
		{"missing generation", "", `"old"`, 200, nil, ErrUnavailable},
		{"provider failure", "", "", 503, nil, ErrUnavailable},
		{"budget rejection", "11", `"old"`, 200, ErrConflict, ErrConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls, signs atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "HEAD" {
					t.Error("mutated during observation")
				}
				w.Header().Set("ETag", tc.etag)
				w.Header().Set("X-Goog-Generation", tc.generation)
				w.Header().Set("Content-Length", "3")
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			p := testGCS(server.URL, &fakeGCSStore{})
			p.sign = func(context.Context, []byte) ([]byte, error) { signs.Add(1); return []byte("signature"), nil }
			size := int64(3)
			r := SignRequest{Method: "PUT", Key: "key", SizeBytes: &size}
			c := ObjectWriteConditions{IfMatch: `"old"`}
			if _, err := p.PresignConditionalPut(t.Context(), "physical", r, c); !errors.Is(err, ErrConfiguration) || calls.Load() != 0 {
				t.Fatal("unmetered conditional observation", err)
			}
			ctx := WithConditionalWriteRequestRecorder(t.Context(), func(context.Context) error { return tc.meterErr })
			_, err := p.PresignConditionalPut(ctx, "physical", r, c)
			wantCalls := int32(1)
			if tc.meterErr != nil {
				wantCalls = 0
			}
			if !errors.Is(err, tc.want) || calls.Load() != wantCalls || signs.Load() != 0 {
				t.Fatal("unsafe preflight result", err, calls.Load(), signs.Load())
			}
		})
	}
}

// adr: 939
func TestGCSConditionalPutPreservesReceiptAndEncryption(t *testing.T) {
	p := testGCS(gcsDefaultEndpoint, &fakeGCSStore{})
	p.encryption = EncryptionConfig{Algorithms: []string{"AES256"}}
	e, err := p.encryption.Resolve(uuid.NewString(), api.ObjectEncryption{Algorithm: "AES256"})
	if err != nil {
		t.Fatal(err)
	}
	receipt := uuid.NewString()
	size := int64(0)
	r := SignRequest{Method: "PUT", Key: "key", SizeBytes: &size, IfNoneMatch: "*", Metadata: map[string]string{"color": "blue"}}
	out, err := p.PresignEncryptedPut(t.Context(), "physical", r, ObjectWriteConditions{}, receipt, e)
	if err != nil {
		t.Fatal(err)
	}
	headers := make(http.Header)
	for k, v := range out.Headers {
		headers.Set(k, v)
	}
	if headers.Get("X-Goog-If-Generation-Match") != "0" || headers.Get("X-Goog-Meta-"+ReservedUploadReceiptMetadataKey) != receipt || headers.Get("X-Goog-Meta-"+ReservedObjectEncryptionMetadataKey) != e.Proof() || headers.Get("X-Goog-Meta-Color") != "blue" {
		t.Fatal("conditional encryption/receipt binding lost")
	}
	if _, err = p.PresignTrackedPut(t.Context(), "physical", r, ObjectWriteConditions{IfMatch: `"old"`}, receipt); !errors.Is(err, ErrInvalid) {
		t.Fatal("saved condition weakened", err)
	}
	if _, err = p.PresignConditionalPut(t.Context(), "physical", SignRequest{Method: "PUT", Key: "key", SizeBytes: &size}, ObjectWriteConditions{IfMatch: `W/"old"`}); !errors.Is(err, ErrUnsupported) {
		t.Fatal("weak ETag accepted", err)
	}
}
