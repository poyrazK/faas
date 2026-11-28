package s3gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// Reusing a completed signed PUT URL with another body returned the first
// write's 200 and ETag while nothing was stored (prod hunt r79).
func TestCompletedURLPutMatches(t *testing.T) {
	t.Parallel()
	md5abc := `"900150983cd24fb0d6963f7d28e17f72"`
	for _, tc := range []struct {
		name, body, etag string
		bytes            int64
		header           map[string]string
		want             bool
	}{
		{name: "same body", body: "abc", etag: md5abc, bytes: 3, want: true},
		{name: "other body same size", body: "xyz", etag: md5abc, bytes: 3},
		{name: "other size", body: "abcd", etag: md5abc, bytes: 3},
		{name: "opaque etag same size", body: "xyz", etag: `"opaque-etag"`, bytes: 3, want: true},
		{name: "opaque etag other size", body: "xyzw", etag: `"opaque-etag"`, bytes: 3},
		{name: "chunked decoded size", body: "chunks", etag: md5abc, bytes: 3, header: map[string]string{"Content-Encoding": "aws-chunked", "X-Amz-Decoded-Content-Length": "3"}, want: true},
		{name: "chunked other size", body: "chunks", etag: md5abc, bytes: 3, header: map[string]string{"Content-Encoding": "aws-chunked", "X-Amz-Decoded-Content-Length": "4"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPut, "/b/k", strings.NewReader(tc.body))
			for k, v := range tc.header {
				r.Header.Set(k, v)
			}
			got := completedURLPutMatches(r, state.ObjectUploadCompletion{Bytes: tc.bytes, ETag: tc.etag})
			if got != tc.want {
				t.Fatalf("completedURLPutMatches = %v, want %v", got, tc.want)
			}
		})
	}
}
