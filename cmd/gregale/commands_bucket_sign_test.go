package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// `gregale bucket sign` maps its flags onto the signed-URL API and refuses
// what the API would, naming the flag.
func TestBucketSignRequest(t *testing.T) {
	get, err := bucketSignRequest("v1/a.gz", "get", 15*time.Minute, -1, "")
	if err != nil || get.Method != "GET" || get.ExpiresIn != 900 || get.SizeBytes != nil {
		t.Fatalf("GET = %+v, %v", get, err)
	}
	put, err := bucketSignRequest("a.png", "PUT", 0, 18231, "image/png")
	if err != nil || put.SizeBytes == nil || *put.SizeBytes != 18231 || put.ContentType != "image/png" || put.ExpiresIn != 0 {
		t.Fatalf("PUT = %+v, %v", put, err)
	}
	for name, tc := range map[string]struct {
		method  string
		expires time.Duration
		size    int64
		ctype   string
		want    string
	}{
		"put without size":   {"PUT", 0, -1, "", "--size-bytes"},
		"get with size":      {"GET", 0, 10, "", "apply to --method PUT"},
		"too long":           {"GET", 16 * time.Minute, -1, "", "--expires"},
		"fractional seconds": {"GET", 1500 * time.Millisecond, -1, "", "--expires"},
		"delete":             {"DELETE", 0, -1, "", "--method must be GET or PUT"},
	} {
		if _, err := bucketSignRequest("k", tc.method, tc.expires, tc.size, tc.ctype); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}

func TestRenderBucketSignedPrintsACurlWithTheHeaders(t *testing.T) {
	var out bytes.Buffer
	renderBucketSigned(&out, api.ObjectSignedRequest{
		URL: "https://s3.example.test/b/a.png?X-Amz-Signature=abc&x=1", Method: "PUT",
		Headers:   map[string]string{"Content-Type": "image/png", "Content-Length": "18231"},
		ExpiresAt: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC),
	})
	text := out.String()
	for _, want := range []string{"PUT https://s3.example.test/b/a.png", "Expires: 2026-11-06T12:00:00Z", "-H 'Content-Length: 18231'", "--data-binary @FILE", "'https://s3.example.test/b/a.png?X-Amz-Signature=abc&x=1'"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
}
