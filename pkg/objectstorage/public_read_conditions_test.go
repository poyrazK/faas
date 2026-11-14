package objectstorage

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 151 — public asset reads retain HTTP conditional and range semantics.
func TestPublicReadPreservesConditionFailures(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, status := range []int{http.StatusPreconditionFailed, http.StatusRequestedRangeNotSatisfiable} {
			t.Run(method+"/"+http.StatusText(status), func(t *testing.T) {
				f := newUploadFixture(t)
				client := &http.Client{Transport: metadataRoundTripper(func(r *http.Request) (*http.Response, error) {
					if status == http.StatusPreconditionFailed && r.Header.Get("If-Match") != `"old"` || status == http.StatusRequestedRangeNotSatisfiable && r.Header.Get("Range") != "bytes=99-" {
						t.Error("read condition was not forwarded")
					}
					return &http.Response{StatusCode: status, ContentLength: 100, Header: http.Header{
						"Content-Range": {"bytes */3"}, "Content-Length": {"100"}, "Content-Encoding": {"gzip"},
						"Content-Type": {"application/xml"}, "Cache-Control": {"public, max-age=3600"},
						"Etag": {`"current"`}, "Last-Modified": {"Thu, 05 Nov 2026 00:00:00 GMT"}, "Accept-Ranges": {"bytes"},
						"X-Amz-Request-Id": {"private-provider-request"},
					}, Body: io.NopCloser(strings.NewReader("private provider error document"))}, nil
				})}
				backend := f.registry.backends["test"]
				backend.Provider = publicReadTestProvider{endpoint: "https://storage.test/file"}
				f.registry.backends["test"] = backend
				h, err := NewPublicReadHandler(PublicReadConfig{Store: f.store, Registry: f.registry, RequestMetrics: f.store, Accounting: f.accounting, AppsDomain: "apps.test", Next: http.NotFoundHandler(), HTTPClient: client})
				if err != nil {
					t.Fatal(err)
				}
				b, err := f.store.GetObjectBucket(t.Context(), f.account.ID, f.app.ID, f.route.BucketID)
				if err != nil {
					t.Fatal(err)
				}
				b.PublicRead, b.ServeAt = true, "/assets"
				h.(*publicReadHandler).mounts.put("upload-test.apps.test", []state.ObjectBucket{b}, h.(*publicReadHandler).now())
				r := httptest.NewRequest(method, "https://upload-test.apps.test/assets/file", nil)
				if status == http.StatusPreconditionFailed {
					r.Header.Set("If-Match", `"old"`)
				} else {
					r.Header.Set("Range", "bytes=99-")
				}
				res := httptest.NewRecorder()
				h.ServeHTTP(res, r)
				if res.Code != status || res.Body.Len() != 0 || res.Header().Get("ETag") != `"current"` || res.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("condition response = %d %v %q", res.Code, res.Header(), res.Body.String())
				}
				if status == http.StatusRequestedRangeNotSatisfiable && res.Header().Get("Content-Range") != "bytes */3" {
					t.Fatal("range refusal lost the representation length")
				}
				for _, header := range []string{"Content-Length", "Content-Encoding", "Content-Type", "X-Amz-Request-Id"} {
					if res.Header().Get(header) != "" {
						t.Errorf("provider error header escaped: %s", header)
					}
				}
			})
		}
	}
}
