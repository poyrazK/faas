package s3gateway

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// adr: 1034
// Golden signatures calculated independently with Python stdlib HMAC/SHA256,
// not the Go SDK signer (which omits explicit zero) or our compatibility helper.
func TestSigV4ExplicitZeroContentLength(t *testing.T) {
	for _, presigned := range []bool{false, true} {
		t.Run(map[bool]string{false: "header", true: "query"}[presigned], func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPut, "https://s3.gregale.dev/assets/empty%20file?tag=b&tag=a&x-id=PutObject", bytes.NewReader(nil))
			r.Header.Set("Content-Length", "0")
			now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
			var parsed sigV4Request
			var err error
			if presigned {
				r.URL.RawQuery = "X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=GRGAAAAAAAAAAAAAAAAA%2F20261011%2Fus-east-1%2Fs3%2Faws4_request&X-Amz-Date=20261011T120000Z&X-Amz-Expires=60&X-Amz-SignedHeaders=content-length%3Bhost&tag=a%20b%2F%2B~" + "&X-Amz-Signature=c224968194152a9bfb64a009148a1f5036f50ed87dbcce81caa53e2450d9989f"
				parsed, err = parsePresignedSigV4(r, "us-east-1", now)
			} else {
				r.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")
				r.Header.Set("X-Amz-Date", "20261011T120000Z")
				r.Header["X-Amz-Meta-Note"] = []string{"  hello   world ", "two  values"}
				r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+testAccess+"/20261011/us-east-1/s3/aws4_request, SignedHeaders=content-length;host;x-amz-content-sha256;x-amz-date;x-amz-meta-note, Signature=4527ea6b4f8a4b18998dae2fec958dbaee711c0f8ac87df56f899cbf595ecc33")
				parsed, err = parseSigV4(r, "us-east-1", now)
			}
			if err != nil || !strings.Contains(parsed.SignedHeader, "content-length") {
				t.Fatal("invalid independent signature fixture", err)
			}
			verify := verifySigV4
			if presigned {
				verify = verifyPresignedSigV4
			}
			if err = verify(t.Context(), r, parsed, testSecret, "us-east-1"); err != nil {
				t.Fatal("valid explicit-zero signature rejected", err)
			}
			for _, length := range []int64{1, -1} {
				r.ContentLength = length
				if err = verify(t.Context(), r, parsed, testSecret, "us-east-1"); err == nil {
					t.Fatal("tampered signed length accepted", length)
				}
			}
			r.ContentLength = 0
			r.URL, _ = url.Parse("https://s3.gregale.dev/assets/other?" + r.URL.RawQuery)
			if err = verify(t.Context(), r, parsed, testSecret, "us-east-1"); err == nil {
				t.Fatal("tampered path accepted")
			}
		})
	}
}
