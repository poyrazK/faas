// adr: 553
package s3gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// Hunt #9 H9-S161: behind Cloudflare (the proxied profile) a stored text/html
// object came back with the edge's bot-detection script appended. The edge
// leaves Cache-Control: no-transform responses alone.
func TestGatewayMarksHTMLReadsNoTransformBehindTheEdge(t *testing.T) {
	for _, tc := range []struct {
		name, profile, contentType, stored, want string
	}{
		{"html without cache control", "proxied", "text/html; charset=utf-8", "", "no-transform"},
		{"html keeps stored directives", "proxied", "text/html", "public, max-age=60", "public, max-age=60, no-transform"},
		{"xhtml", "proxied", "application/xhtml+xml", "", "no-transform"},
		{"already no-transform", "proxied", "TEXT/HTML", "private, No-Transform", "private, No-Transform"},
		{"non-html unchanged", "proxied", "application/json", "max-age=60", "max-age=60"},
		{"direct profile unchanged", "direct", "text/html", "max-age=60", "max-age=60"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler, _, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(r *http.Request) (*http.Response, error) {
				header := http.Header{"Content-Length": {"5"}, "Content-Type": {tc.contentType}}
				if tc.stored != "" {
					header.Set("Cache-Control", tc.stored)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader("hello")), Request: r}, nil
			})
			handler.registry.Transfer.Profile = tc.profile
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, signedGatewayRequest(t, method, "https://s3.gregale.dev/assets/page.html", nil, "UNSIGNED-PAYLOAD"))
				if recorder.Code != http.StatusOK {
					t.Fatalf("%s = %d %s", method, recorder.Code, recorder.Body.String())
				}
				if got := recorder.Header().Get("Cache-Control"); got != tc.want {
					t.Fatalf("%s Cache-Control = %q, want %q", method, got, tc.want)
				}
			}
		})
	}
}
