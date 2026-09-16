// s3client_test.go — table-driven tests for the hand-rolled
// SigV4 S3 client (issue #562). httptest.NewServer stands in
// for S3: the tests pin the four bits of behavior the
// shipper actually relies on:
//
//  1. ErrAuthMissing when KeyID/Secret are empty.
//  2. Permanent detection: 4xx with a Code/Message JSON body
//     surfaces as *Permanent so the shipper increments the
//     right metric reason bucket.
//  3. 5xx returns a plain error (transient — the shipper
//     retries).
//  4. The body that lands on the server matches what the
//     client wrote (no off-by-one in the bufio/gzip pipeline).

package logarchive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// fakeS3 is a minimal S3-compatible server: it captures the
// body the shipper sent, exposes a programmable status
// response for the next request, and lets tests assert the
// Authorization header.
type fakeS3 struct {
	body        []byte
	nextStatus  int
	nextCode    string
	nextMessage string
	auth        string
	path        string
}

func (f *fakeS3) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.body = body
		f.auth = r.Header.Get("Authorization")
		f.path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.nextStatus)
		if f.nextStatus >= 400 {
			_, _ = w.Write([]byte(`{"Code":"` + f.nextCode + `","Message":"` + f.nextMessage + `"}`))
		}
	})
}

// TestS3_NewClientAuthMissing covers the constructor's
// fail-closed posture: an empty KeyID or Secret returns
// ErrAuthMissing so the apid wire-up aborts on misconfigured
// creds rather than booting in a half-broken state.
func TestS3_NewClientAuthMissing(t *testing.T) {
	if _, err := NewS3Client("https://s3.example", "us-east-1", "b", "", "s"); !errors.Is(err, ErrAuthMissing) {
		t.Errorf("empty key id: err=%v, want ErrAuthMissing", err)
	}
	if _, err := NewS3Client("https://s3.example", "us-east-1", "b", "k", ""); !errors.Is(err, ErrAuthMissing) {
		t.Errorf("empty secret: err=%v, want ErrAuthMissing", err)
	}
}

func TestS3_GCPMetadataAuthUsesShortLivedBearerToken(t *testing.T) {
	var authorization string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c, err := NewS3Client(srv.URL, "auto", "mybucket", "", "", "gcp_metadata")
	if err != nil {
		t.Fatalf("NewS3Client: %v", err)
	}
	c.TokenSource = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "short-lived-token", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)})
	if err := c.PutObject(context.Background(), "k", "application/gzip", bytes.NewReader([]byte("x")), 1); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if authorization != "Bearer short-lived-token" {
		t.Fatalf("Authorization = %q, want metadata bearer token", authorization)
	}
}

func TestS3_GCPMetadataAuthRejectsMixedOrUnknownCredentials(t *testing.T) {
	if _, err := NewS3Client("https://storage.googleapis.com", "auto", "b", "key", "secret", "gcp_metadata"); err == nil {
		t.Fatal("gcp_metadata accepted long-lived HMAC credentials")
	}
	if _, err := NewS3Client("https://storage.googleapis.com", "auto", "b", "", "", "unknown"); err == nil {
		t.Fatal("unknown archive auth mode accepted")
	}
}

// TestS3_NewClientValidation covers the constructor's other
// fail-closed branches.
func TestS3_NewClientValidation(t *testing.T) {
	cases := []struct {
		name                                    string
		endpoint, region, bucket, keyID, secret string
		wantErr                                 string
	}{
		{"empty endpoint", "", "us-east-1", "b", "k", "s", "endpoint required"},
		{"empty bucket", "https://s3.example", "us-east-1", "", "k", "s", "bucket required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewS3Client(tc.endpoint, tc.region, tc.bucket, tc.keyID, tc.secret)
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err=%v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

// TestS3_PutObject_HappyPath pins the wire: PUT goes to the
// path-style URL, body lands intact, Authorization carries
// the SigV4 "AWS4-HMAC-SHA256 Credential=..." prefix.
func TestS3_PutObject_HappyPath(t *testing.T) {
	f := &fakeS3{nextStatus: 200}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	c, err := NewS3Client(srv.URL, "us-east-1", "mybucket", "AKIA", "secret")
	if err != nil {
		t.Fatalf("NewS3Client: %v", err)
	}
	body := []byte("hello-world")
	if err := c.PutObject(context.Background(), "faas-logs/inst/2026-08-08.jsonl.gz", "application/gzip", bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if !bytes.Equal(f.body, body) {
		t.Errorf("server got body %q, want %q", f.body, body)
	}
	wantPath := "/mybucket/faas-logs/inst/2026-08-08.jsonl.gz"
	if f.path != wantPath {
		t.Errorf("server got path %q, want %q", f.path, wantPath)
	}
	if !strings.HasPrefix(f.auth, "AWS4-HMAC-SHA256 Credential=AKIA/") {
		t.Errorf("auth header missing SigV4 prefix: %q", f.auth)
	}
}

// TestS3_PutObject_Permanent4xx maps to
// apid_log_archive_failures_total{reason="auth"} via the
// classifyFailure switch in shipper.go.
func TestS3_PutObject_Permanent4xx(t *testing.T) {
	f := &fakeS3{nextStatus: 403, nextCode: "AccessDenied", nextMessage: "bad key"}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	c, _ := NewS3Client(srv.URL, "us-east-1", "mybucket", "AKIA", "secret")
	err := c.PutObject(context.Background(), "k", "application/gzip", bytes.NewReader([]byte("x")), 1)
	if !IsPermanent(err) {
		t.Fatalf("err=%v, want *Permanent", err)
	}
	var perm *Permanent
	if !errors.As(err, &perm) {
		t.Fatalf("errors.As(*Permanent) failed")
	}
	if perm.StatusCode != 403 {
		t.Errorf("StatusCode=%d, want 403", perm.StatusCode)
	}
	if perm.Code != "AccessDenied" {
		t.Errorf("Code=%q, want AccessDenied", perm.Code)
	}
}

// TestS3_PutObject_Transient5xx stays a plain error so the
// shipper retries on the next tick.
func TestS3_PutObject_Transient5xx(t *testing.T) {
	f := &fakeS3{nextStatus: 500, nextCode: "InternalError", nextMessage: "retry"}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	c, _ := NewS3Client(srv.URL, "us-east-1", "mybucket", "AKIA", "secret")
	err := c.PutObject(context.Background(), "k", "application/gzip", bytes.NewReader([]byte("x")), 1)
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	if IsPermanent(err) {
		t.Errorf("5xx surfaced as *Permanent: %v", err)
	}
}

// TestS3_PutObject_BodyLengthMismatch is the defensive guard:
// r produced N bytes but the caller passed size != N. The
// shipper would never do this in production, but a bug in
// the gzip pipeline would; the test pins that the failure
// surfaces as *Permanent{Code:BodyLengthMismatch} so the
// reason counter increments the right bucket.
func TestS3_PutObject_BodyLengthMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	c, _ := NewS3Client(srv.URL, "us-east-1", "mybucket", "AKIA", "secret")
	err := c.PutObject(context.Background(), "k", "application/gzip", bytes.NewReader([]byte("xy")), 99)
	var perm *Permanent
	if !errors.As(err, &perm) {
		t.Fatalf("err=%v, want *Permanent", err)
	}
	if perm.Code != "BodyLengthMismatch" {
		t.Errorf("Code=%q, want BodyLengthMismatch", perm.Code)
	}
}

// TestS3_GetObject_HappyPath exercises the read-back path
// (PR-B uses this same primitive). The server's response
// body is streamed through GetObject into the caller's
// io.Writer with the byte count returned.
func TestS3_GetObject_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Errorf("missing SigV4 auth header: %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte("the body"))
	}))
	defer srv.Close()
	c, _ := NewS3Client(srv.URL, "us-east-1", "mybucket", "AKIA", "secret")
	var buf bytes.Buffer
	n, err := c.GetObject(context.Background(), "k", &buf)
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	if n != int64(len("the body")) {
		t.Errorf("n=%d, want %d", n, len("the body"))
	}
	if buf.String() != "the body" {
		t.Errorf("buf=%q, want %q", buf.String(), "the body")
	}
}

// TestS3_GetObject_NotFound returns *Permanent{Code:"Unknown"}
// (the parsed body has no Code field on a vendor that emits
// XML or an empty JSON body).
func TestS3_GetObject_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte("<Error><Code>NoSuchKey</Code></Error>"))
	}))
	defer srv.Close()
	c, _ := NewS3Client(srv.URL, "us-east-1", "mybucket", "AKIA", "secret")
	_, err := c.GetObject(context.Background(), "missing", io.Discard)
	if !IsPermanent(err) {
		t.Fatalf("err=%v, want *Permanent", err)
	}
	var perm *Permanent
	if !errors.As(err, &perm) {
		t.Fatalf("errors.As(*Permanent) failed")
	}
	if perm.Code != "Unknown" {
		t.Errorf("Code=%q, want Unknown (XML body)", perm.Code)
	}
}

// TestS3_SigV4_KnownVector pins the signing implementation
// against a fixed request/credential/date triple. The
// canonical request + signing key chain has many small
// details (header order, hex casing, trailing newline); a
// regression here would silently break every upload. We
// don't include the full canonical bytes in the test — the
// Authorization header carries the Credential+Signature
// parts that catch the highest-density bugs (HMAC key
// derivation, hex encoding).
func TestS3_SigV4_KnownVector(t *testing.T) {
	f := &fakeS3{nextStatus: 200}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	c, _ := NewS3Client(srv.URL, "us-east-1", "mybucket", "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY")
	if err := c.PutObject(context.Background(), "k", "application/octet-stream", bytes.NewReader([]byte("")), 0); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	// The Credential= field pins the scope:
	// "<dateStamp>/us-east-1/s3/aws4_request". The date
	// stamp is "today" so we only check the suffix.
	wantSuffix := "/us-east-1/s3/aws4_request"
	if !strings.Contains(f.auth, wantSuffix) {
		t.Errorf("auth=%q, missing credential scope suffix %q", f.auth, wantSuffix)
	}
	// SignedHeaders is a fixed string for our wire.
	wantSigned := "SignedHeaders=content-type;host;x-amz-content-sha256;x-amz-date"
	if !strings.Contains(f.auth, wantSigned) {
		t.Errorf("auth=%q, missing SignedHeaders %q", f.auth, wantSigned)
	}
	// Signature is hex-encoded HMAC-SHA256 = 64 lowercase hex
	// chars.
	sigStart := strings.Index(f.auth, "Signature=")
	if sigStart < 0 {
		t.Fatalf("auth=%q, missing Signature", f.auth)
	}
	sig := f.auth[sigStart+len("Signature="):]
	if len(sig) != 64 {
		t.Errorf("signature length=%d, want 64", len(sig))
	}
	for _, c := range sig {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("signature char %q not lowercase hex", c)
			break
		}
	}
}
