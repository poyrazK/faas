package s3gateway

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func presignedQueryPut(t *testing.T, target, body string, now time.Time) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, target, strings.NewReader(body))
	signer := awsv4.NewSigner(func(o *awsv4.SignerOptions) { o.DisableURIPathEscaping = true })
	signed, headers, err := signer.PresignHTTP(t.Context(), aws.Credentials{AccessKeyID: testAccess, SecretAccessKey: testSecret}, r, "UNSIGNED-PAYLOAD", "s3", "us-east-1", now)
	if err != nil {
		t.Fatal(err)
	}
	incoming := httptest.NewRequest(http.MethodPut, signed, strings.NewReader(body))
	incoming.Header = headers
	return incoming
}

// Stock @aws-sdk/s3-request-presigner 3.1149.0 puts metadata and checksums in
// the query. The fixture uses the same synthetic credentials and frozen clock
// as newGatewayTestHandler; CI needs neither Node nor an external service.
func TestGatewayJavaScriptPresignedPutFields(t *testing.T) {
	data, err := os.ReadFile("testdata/presigned-put-js.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"good", "evil"} {
		t.Run(body, func(t *testing.T) {
			calls := 0
			h, _, p := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Etag": {`"etag"`}}, Body: http.NoBody, Request: r}, nil
			})
			r := httptest.NewRequest(http.MethodPut, fixture.URL, strings.NewReader(body))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if body == "evil" {
				if w.Code != http.StatusBadRequest || calls != 0 || !strings.Contains(w.Body.String(), "BadDigest") {
					t.Fatalf("mismatched checksum: status=%d calls=%d body=%s", w.Code, calls, w.Body.String())
				}
				return
			}
			if w.Code != http.StatusOK || calls != 1 || len(p.presignRequests) != 1 || p.presignRequests[0].Metadata["owner"] != "customer" {
				t.Fatalf("signed fields lost: status=%d calls=%d requests=%+v", w.Code, calls, p.presignRequests)
			}
		})
	}
}

func TestPresignedObjectHeadersRejectAmbiguity(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		headers     http.Header
		wantError   bool
	}{
		{"metadata and empty value", "x-amz-meta-owner=customer&x-amz-meta-empty=", nil, false},
		{"tags", "x-amz-tagging=team%3Dstorage", nil, false},
		{"duplicate values", "x-amz-meta-owner=one&x-amz-meta-owner=two", nil, true},
		{"case aliases", "x-amz-meta-owner=one&X-Amz-Meta-Owner=one", nil, true},
		{"conflicting header", "x-amz-meta-owner=one", http.Header{"X-Amz-Meta-Owner": {"two"}}, true},
		{"matching header", "x-amz-meta-owner=one", http.Header{"X-Amz-Meta-Owner": {"one"}}, false},
		{"duplicate header", "x-amz-meta-owner=one", http.Header{"X-Amz-Meta-Owner": {"one", "one"}}, true},
		{"newline", "x-amz-meta-owner=one%0D%0Atwo", nil, true},
		{"invalid header name", "x-amz-meta-bad%20name=value", nil, true},
		{"empty checksum", "x-amz-checksum-sha256=", nil, true},
		{"malformed query", "x-amz-meta-owner=%zz", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPut, "https://s3.gregale.dev/assets/key?"+tc.query, nil)
			if tc.headers != nil {
				r.Header = tc.headers
			}
			err := applyPresignedObjectHeaders(r)
			if (err != nil) != tc.wantError {
				t.Fatalf("apply signed query: %v", err)
			}
		})
	}
}

func TestGatewayPresignedQueryValidationPrecedesProvider(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		tamper      bool
		wantStatus  int
	}{
		{"duplicates", "x-amz-meta-owner=one&x-amz-meta-owner=two", false, http.StatusBadRequest},
		{"reserved metadata", "x-amz-meta-" + objectstorage.ReservedUploadReceiptMetadataKey + "=forged", false, http.StatusBadRequest},
		{"invalid checksum", "x-amz-checksum-sha256=bad", false, http.StatusBadRequest},
		{"unsigned query change", "x-amz-meta-owner=customer", true, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			h, _, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(*http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("unexpected provider request")
			})
			r := presignedQueryPut(t, "https://s3.gregale.dev/assets/key?X-Amz-Expires=60&"+tc.query, "good", h.now())
			if tc.tamper {
				q := r.URL.Query()
				q.Set("x-amz-meta-owner", "attacker")
				r.URL.RawQuery = q.Encode()
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.wantStatus || calls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, calls, w.Body.String())
			}
		})
	}
}

func TestGatewayQueryChecksumProtectsOwnedNodeWrite(t *testing.T) {
	store, node := diskFixture(t)
	if err := store.CreateBucket("physical"); err != nil {
		t.Fatal(err)
	}
	f := newMultipartCopyIntegrationWithTransfer(t, state.NewMemStore(), node, objectstorage.Config{}, 0)
	sum := sha256.Sum256([]byte("good"))
	q := url.Values{"X-Amz-Expires": {"60"}, "x-amz-meta-owner": {"customer"}, "x-amz-checksum-sha256": {base64.StdEncoding.EncodeToString(sum[:])}}
	for _, body := range []string{"evil", "good"} {
		r := presignedQueryPut(t, *f.client.Options().BaseEndpoint+"/assets/query.txt?"+q.Encode(), body, time.Now())
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		if body == "evil" {
			if w.Code != http.StatusBadRequest {
				t.Fatalf("mismatched checksum: %d %s", w.Code, w.Body.String())
			}
			if _, file, err := store.Get("physical", "query.txt"); !errors.Is(err, objectnode.ErrNoObject) {
				if file != nil {
					_ = file.Close()
				}
				t.Fatalf("rejected content was published: %v", err)
			}
			continue
		}
		if w.Code != http.StatusOK {
			t.Fatalf("valid upload: %d %s", w.Code, w.Body.String())
		}
		object, file, err := store.Get("physical", "query.txt")
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(file)
		_ = file.Close()
		if readErr != nil || string(data) != "good" || object.Headers.Get("X-Amz-Meta-Owner") != "customer" {
			t.Fatal("stored bytes or metadata differ", string(data), object.Headers, readErr)
		}
	}
}
