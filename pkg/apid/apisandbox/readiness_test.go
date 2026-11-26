package apisandbox

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSandboxCheckRequiresPublicResponsesFromExactDeployment(t *testing.T) {
	const artifact = "sandbox-release"
	const doc = `{"openapi":"3.1.0","info":{"title":"Notes","version":"1"},"paths":{"/notes":{"get":{}}}}`
	for _, test := range []struct {
		name, path, body, revision string
		status                     int
		wantCode                   string
		retry                      bool
		calls                      int
	}{
		{name: "document and GET smoke route", path: "/notes", body: doc, revision: artifact, status: 204, calls: 2},
		{name: "document only", body: doc, revision: artifact, status: 200, calls: 1},
		{name: "wrong deployment", body: doc, revision: "source", status: 200, wantCode: "openapi_revision", retry: true, calls: 1},
		{name: "invalid OpenAPI", body: `{"openapi":"3.1.0"}`, revision: artifact, wantCode: "openapi_invalid", calls: 1},
		{name: "oversized document", body: doc + strings.Repeat(" ", maxOpenAPISize), revision: artifact, wantCode: "openapi_too_large", calls: 1},
		{name: "missing smoke route", path: "/notes", body: doc, revision: artifact, status: 404, wantCode: "smoke_http_status", calls: 2},
		{name: "transient smoke error", path: "/notes", body: doc, revision: artifact, status: 503, wantCode: "smoke_http_status", retry: true, calls: 2},
		{name: "no redirects", path: "/notes", body: doc, revision: artifact, status: 302, wantCode: "smoke_http_status", calls: 2},
		{name: "reject authority in path", path: "@other.example/notes", wantCode: "smoke_path_invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Scheme != "https" || r.URL.Host != "sandbox.example" || r.Method != "GET" ||
					r.Header.Get("Authorization") != "Bearer verification-key" {
					t.Fatalf("unexpected probe %s %s", r.Method, r.URL)
				}
				status, body := http.StatusOK, test.body
				wantPath := OpenAPIPath
				if calls == 2 {
					wantPath, status, body = test.path, test.status, ""
				}
				if r.URL.Path != wantPath {
					t.Fatalf("probe = %q, want %q", r.URL.Path, wantPath)
				}
				header := make(http.Header)
				header.Set(api.RevisionHeader, test.revision)
				header.Set("Location", "https://elsewhere.example/")
				return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			got, failure, retry := Check(t.Context(), client, "https://sandbox.example", "verification-key", artifact, test.path)
			if retry != test.retry || calls != test.calls {
				t.Fatalf("retry=%v, probes=%d, failure=%+v", retry, calls, failure)
			}
			if test.wantCode != "" {
				if got != nil || failure == nil || failure.Code != test.wantCode {
					t.Fatalf("receipt=%+v failure=%+v, want %q", got, failure, test.wantCode)
				}
				return
			}
			if got == nil || failure != nil || got.OpenAPIStatus != http.StatusOK || got.DeploymentID != artifact ||
				got.CheckedAt.IsZero() || (test.path != "" && got.SmokeStatus != test.status) ||
				(test.path == "" && got.SmokeStatus != 0) {
				t.Fatalf("receipt=%+v failure=%+v", got, failure)
			}
		})
	}
}
