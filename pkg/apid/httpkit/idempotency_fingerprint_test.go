package httpkit

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var testFingerprintKey = []byte("0123456789abcdef0123456789abcdef")

type fingerprintRequest struct {
	method, target, contentType, body string
	header                            map[string]string
}

func (f fingerprintRequest) build() *http.Request {
	method := f.method
	if method == "" {
		method = http.MethodPost
	}
	target := f.target
	if target == "" {
		target = "/v1/apps/a/queues/send"
	}
	r := httptest.NewRequest(method, target, strings.NewReader(f.body))
	if f.contentType != "" {
		r.Header.Set("Content-Type", f.contentType)
	}
	for k, v := range f.header {
		r.Header.Set(k, v)
	}
	return r
}

func multipartBody(t *testing.T, boundary string, fields [][2]string) (string, string) {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	if err := w.SetBoundary(boundary); err != nil {
		t.Fatal(err)
	}
	for _, f := range fields {
		if err := w.WriteField(f[0], f[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return w.FormDataContentType(), b.String()
}

// TestIdempotencyFingerprint pins H12-26: one Idempotency-Key reused with a
// new payload replayed the first response. Requests that mean the same thing
// share a fingerprint; requests that differ in body, query or precondition do
// not.
func TestIdempotencyFingerprint(t *testing.T) {
	mpA, bodyA := multipartBody(t, "boundary-one", [][2]string{{"slug", "a"}, {"runtime", "node22"}})
	mpB, bodyB := multipartBody(t, "boundary-two", [][2]string{{"slug", "a"}, {"runtime", "node22"}})
	mpC, bodyC := multipartBody(t, "boundary-one", [][2]string{{"slug", "a"}, {"runtime", "python312"}})
	for _, tc := range []struct {
		name      string
		a, b      fingerprintRequest
		wantEqual bool
	}{
		{"identical json", fingerprintRequest{contentType: "application/json", body: `{"body":"one"}`}, fingerprintRequest{contentType: "application/json", body: `{"body":"one"}`}, true},
		{"different payload", fingerprintRequest{contentType: "application/json", body: `{"body":"one"}`}, fingerprintRequest{contentType: "application/json", body: `{"body":"two"}`}, false},
		{"json key order and whitespace", fingerprintRequest{contentType: "application/json", body: `{"a":1,"b":[1,2]}`}, fingerprintRequest{contentType: "application/json; charset=utf-8", body: "{ \"b\": [1, 2],\n \"a\": 1 }\n"}, true},
		{"json number spelling is kept", fingerprintRequest{contentType: "application/json", body: `{"n":1}`}, fingerprintRequest{contentType: "application/json", body: `{"n":1.0}`}, false},
		{"missing content type still canonical json", fingerprintRequest{body: `{"a":1,"b":2}`}, fingerprintRequest{body: `{"b":2,"a":1}`}, true},
		{"octet-stream compares bytes", fingerprintRequest{contentType: "application/octet-stream", body: `{"a":1,"b":2}`}, fingerprintRequest{contentType: "application/octet-stream", body: `{"b":2,"a":1}`}, false},
		{"multipart boundary ignored", fingerprintRequest{contentType: mpA, body: bodyA}, fingerprintRequest{contentType: mpB, body: bodyB}, true},
		{"multipart field value differs", fingerprintRequest{contentType: mpA, body: bodyA}, fingerprintRequest{contentType: mpC, body: bodyC}, false},
		{"query order ignored", fingerprintRequest{target: "/v1/x?a=1&b=2"}, fingerprintRequest{target: "/v1/x?b=2&a=1"}, true},
		{"query value differs", fingerprintRequest{target: "/v1/x?dry_run=true"}, fingerprintRequest{target: "/v1/x?dry_run=false"}, false},
		{"precondition differs", fingerprintRequest{header: map[string]string{"If-Match": `"v1"`}}, fingerprintRequest{header: map[string]string{"If-Match": `"v2"`}}, false},
		{"transport headers ignored", fingerprintRequest{header: map[string]string{"User-Agent": "gregale/1", "X-Request-Id": "r1"}}, fingerprintRequest{header: map[string]string{"User-Agent": "gregale/2", "X-Request-Id": "r2"}}, true},
		{"media type differs", fingerprintRequest{contentType: "text/plain", body: "x"}, fingerprintRequest{contentType: "application/octet-stream", body: "x"}, false},
		{"method differs", fingerprintRequest{method: http.MethodPut}, fingerprintRequest{method: http.MethodPost}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := IdempotencyFingerprint(tc.a.build(), testFingerprintKey)
			b := IdempotencyFingerprint(tc.b.build(), testFingerprintKey)
			if len(a) != 32 || len(b) != 32 {
				t.Fatalf("fingerprint lengths = %d, %d; want 32", len(a), len(b))
			}
			if got := bytes.Equal(a, b); got != tc.wantEqual {
				t.Fatalf("fingerprints equal = %v, want %v", got, tc.wantEqual)
			}
		})
	}
}

func TestIdempotencyFingerprintIsKeyed(t *testing.T) {
	req := fingerprintRequest{contentType: "application/json", body: `{"value":"1234"}`}
	a := IdempotencyFingerprint(req.build(), testFingerprintKey)
	b := IdempotencyFingerprint(req.build(), []byte("another-host-key-another-host-ke"))
	if bytes.Equal(a, b) {
		t.Fatal("fingerprint did not depend on the key: a stored fingerprint could be brute-forced to the body")
	}
	if got := IdempotencyFingerprint(req.build(), nil); got != nil {
		t.Fatalf("fingerprint without a key = %x, want nil (unbound)", got)
	}
}

// The handler must still read the whole body after the fingerprint read it.
func TestIdempotencyFingerprintPreservesBody(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      string
		chunked   bool
		wantBound bool
	}{
		{"small", `{"body":"one"}`, false, true},
		{"empty", "", false, true},
		{"declared too large", strings.Repeat("x", int(IdempotencyFingerprintMaxBodyBytes)+1), false, false},
		{"chunked too large", strings.Repeat("x", int(IdempotencyFingerprintMaxBodyBytes)+1), true, false},
		{"chunked at the limit", strings.Repeat("x", int(IdempotencyFingerprintMaxBodyBytes)), true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader(tc.body))
			if tc.chunked {
				r.ContentLength = -1
				r.Body = io.NopCloser(strings.NewReader(tc.body))
			}
			got := IdempotencyFingerprint(r, testFingerprintKey)
			if (got != nil) != tc.wantBound {
				t.Fatalf("bound = %v, want %v", got != nil, tc.wantBound)
			}
			read, err := io.ReadAll(r.Body)
			if err != nil || string(read) != tc.body {
				t.Fatalf("handler read %d bytes (err=%v), want the original %d", len(read), err, len(tc.body))
			}
		})
	}
}
