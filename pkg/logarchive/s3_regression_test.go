package logarchive

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// Issue #562: verify the signature against the AWS implementation, not just
// the Authorization header's shape. The signed URI includes the bucket and
// endpoint prefix, and must match the escaped path/query actually sent.
func TestS3_SignatureMatchesAWSForActualURL(t *testing.T) {
	for _, endpoint := range []string{"https://s3.example", "https://s3.example/prefix%20dir?z=last&a=two+words&a=first"} {
		t.Run(endpoint, func(t *testing.T) {
			c, err := NewS3Client(endpoint, "us-east-1", "mybucket", "AKIA", "test-secret")
			if err != nil {
				t.Fatal(err)
			}
			key := "faas-logs/instance/2026/09/a b.jsonl.gz"
			req, err := http.NewRequest(http.MethodPut, c.objectURL(key), nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/gzip")
			body := []byte("archive")
			if err := c.sign(req, hexSHA256(body)); err != nil {
				t.Fatal(err)
			}
			got := req.Header.Get("Authorization")
			stamp, err := time.Parse("20060102T150405Z", req.Header.Get("x-amz-date"))
			if err != nil {
				t.Fatal(err)
			}
			expected := req.Clone(context.Background())
			expected.Header.Del("Authorization")
			expected.Header.Del("Host") // host is supplied by the request URL
			creds := aws.Credentials{AccessKeyID: c.KeyID, SecretAccessKey: c.Secret}
			if err := v4.NewSigner().SignHTTP(context.Background(), creds, expected, hexSHA256(body), "s3", c.Region, stamp, func(o *v4.SignerOptions) {
				o.DisableURIPathEscaping = true // S3 signs the escaped URI once
			}); err != nil {
				t.Fatal(err)
			}
			_, gotSignature, _ := strings.Cut(got, "Signature=")
			_, wantSignature, _ := strings.Cut(expected.Header.Get("Authorization"), "Signature=")
			if gotSignature != wantSignature {
				t.Fatalf("signature does not match AWS for %s", req.URL)
			}
		})
	}
}

type countedSeeker struct {
	*strings.Reader
	readBytes int64
}

func (r *countedSeeker) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.readBytes += int64(n)
	return n, err
}

func (r *countedSeeker) WriteTo(w io.Writer) (int64, error) {
	return io.Copy(w, struct{ io.Reader }{r})
}

// Seekable archives must be hashed and rewound for streaming, rather than
// copied wholesale onto the daemon heap. The reader may start at an offset.
func TestS3_PutObjectStreamsSeekableBody(t *testing.T) {
	const payload = "the archive body"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != payload || r.ContentLength != int64(len(payload)) {
			t.Errorf("body/length = %q/%d, err = %v", body, r.ContentLength, err)
		}
		if r.Header.Get("x-amz-content-sha256") != hexSHA256([]byte(payload)) {
			t.Error("body hash does not cover exactly the streamed bytes")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c, err := NewS3Client(srv.URL, "us-east-1", "bucket", "AKIA", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	r := &countedSeeker{Reader: strings.NewReader("skip" + payload)}
	if _, err := r.Seek(4, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if err := c.PutObject(context.Background(), "key", "application/gzip", r, int64(len(payload))); err != nil {
		t.Fatal(err)
	}
	if r.readBytes != int64(2*len(payload)) {
		t.Fatalf("reader supplied %d bytes, want two bounded passes (%d), not a buffered copy", r.readBytes, 2*len(payload))
	}
}

func TestS3_PutObjectBoundsMismatchedBody(t *testing.T) {
	c, err := NewS3Client("https://s3.example", "us-east-1", "bucket", "AKIA", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int64{-1, 0, 1} {
		r := &countedSeeker{Reader: strings.NewReader(strings.Repeat("x", 4096))}
		// Hide Seek to exercise the fallback for a plain io.Reader.
		err := c.PutObject(context.Background(), "key", "application/gzip", struct{ io.Reader }{r}, size)
		var perm *Permanent
		if !errors.As(err, &perm) || perm.Code != "BodyLengthMismatch" {
			t.Fatalf("size %d: error = %v, want BodyLengthMismatch", size, err)
		}
		if r.readBytes > size+1 {
			t.Errorf("size %d: read %d bytes, want at most declared size plus one", size, r.readBytes)
		}
	}
}
