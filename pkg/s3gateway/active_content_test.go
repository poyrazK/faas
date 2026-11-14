package s3gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// Object storage audit OS-9 (2026-11-10): a GET signed with a customer's own
// S3 key returned the stored type, so an uploaded HTML or SVG page ran script
// on s3.gregale.dev, a host every tenant shares. Active types are sandboxed
// downloads; every object response carries nosniff and a type.
func TestGatewaySandboxesActiveObjectsOnTheSharedHost(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, disposition     string
		wantType, wantDisposition, wantCSP string
	}{
		{name: "html", contentType: "text/html; charset=utf-8", wantType: "text/html; charset=utf-8", wantDisposition: "attachment", wantCSP: activeObjectSandboxCSP},
		{name: "svg keeps its filename", contentType: "image/svg+xml", disposition: `inline; filename="logo.svg"`, wantType: "image/svg+xml", wantDisposition: `attachment; filename=logo.svg`, wantCSP: activeObjectSandboxCSP},
		{name: "xml subtype", contentType: "application/rss+xml", wantType: "application/rss+xml", wantDisposition: "attachment", wantCSP: activeObjectSandboxCSP},
		{name: "javascript", contentType: "text/javascript", wantType: "text/javascript", wantDisposition: "attachment", wantCSP: activeObjectSandboxCSP},
		{name: "unparsable type is not trusted", contentType: "text/html;;", wantType: "text/html;;", wantDisposition: "attachment", wantCSP: activeObjectSandboxCSP},
		{name: "image stays inline", contentType: "image/png", disposition: "inline", wantType: "image/png", wantDisposition: "inline"},
		{name: "json unchanged", contentType: "application/json", wantType: "application/json"},
		{name: "missing type is binary", wantType: "application/octet-stream"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler, _, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(r *http.Request) (*http.Response, error) {
				header := http.Header{"Content-Length": {"5"}}
				if tc.contentType != "" {
					header.Set("Content-Type", tc.contentType)
				}
				if tc.disposition != "" {
					header.Set("Content-Disposition", tc.disposition)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader("hello")), Request: r}, nil
			})
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, signedGatewayRequest(t, method, "https://s3.gregale.dev/assets/page", nil, "UNSIGNED-PAYLOAD"))
				if recorder.Code != http.StatusOK {
					t.Fatalf("%s = %d %s", method, recorder.Code, recorder.Body.String())
				}
				got := recorder.Header()
				if got.Get("X-Content-Type-Options") != "nosniff" {
					t.Fatalf("%s X-Content-Type-Options = %q", method, got.Get("X-Content-Type-Options"))
				}
				if got.Get("Content-Type") != tc.wantType || got.Get("Content-Disposition") != tc.wantDisposition || got.Get("Content-Security-Policy") != tc.wantCSP {
					t.Fatalf("%s headers: type %q disposition %q csp %q; want %q %q %q", method,
						got.Get("Content-Type"), got.Get("Content-Disposition"), got.Get("Content-Security-Policy"), tc.wantType, tc.wantDisposition, tc.wantCSP)
				}
			}
		})
	}
}
