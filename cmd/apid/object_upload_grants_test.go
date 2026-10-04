// adr:531
package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/s3gateway"
)

type uploadGrantRoundTripper func(*http.Request) (*http.Response, error)

func (f uploadGrantRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func brokerGrantRequest(g api.ObjectSignedRequest, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPut, g.URL, strings.NewReader(body))
	for name, value := range g.Headers {
		r.Header.Set(name, value)
	}
	return r
}

func brokerGrantGateway(t *testing.T, e testEnv, transport uploadGrantRoundTripper) *s3gateway.Handler {
	t.Helper()
	h, err := s3gateway.New(s3gateway.Config{Registry: e.s.objectStorage, Store: e.store, SpoolDir: t.TempDir(), MinSpoolFreeBytes: 1,
		OpenSecret: func([]byte) (string, error) {
			t.Fatal("broker grants must not resolve customer credential secrets")
			return "", nil
		}, HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func mintAPIPutGrant(t *testing.T, e testEnv, path string) api.ObjectSignedRequest {
	t.Helper()
	response := e.do(t, "POST", path+"/signed-url", map[string]any{"method": "PUT", "key": "folder/a b%2F.txt", "size_bytes": 3, "content_type": "text/plain", "cache_control": "private", "metadata": map[string]string{"custom": "frozen"}, "tags": map[string]string{"tag": "value"}}, nil)
	var grant api.ObjectSignedRequest
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &grant) != nil {
		t.Fatalf("mint = %d %s", response.Code, response.Body.String())
	}
	u, err := url.Parse(grant.URL)
	if err != nil || u.Host != "s3.gregale.dev" || len(u.Query().Get(s3gateway.UploadGrantQueryParameter)) != 43 {
		t.Fatal("API exposed an upstream capability")
	}
	return grant
}

func TestObjectUploadGrantAPIAndGatewayDrainSynchronousWrite(t *testing.T) {
	e, provider, b, path := mutationAPIFixture(t)
	g := mintAPIPutGrant(t, e, path)
	if len(provider.accessed) != 0 {
		t.Fatal("minting broker grant contacted storage")
	}
	token := uuid.NewString()
	calls := 0
	h := brokerGrantGateway(t, e, func(r *http.Request) (*http.Response, error) {
		calls++
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != "abc" || r.ContentLength != 3 {
			t.Fatal("provider upload bytes changed", err)
		}
		fence, err := e.store.AcquireObjectBucketWriteFence(context.Background(), b, token)
		if err != nil || fence.Requests != 1 || fence.NativeGrants != 0 {
			t.Fatalf("active broker writer absent: %+v %v", fence, err)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Etag": []string{"etag"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	out := httptest.NewRecorder()
	h.ServeHTTP(out, brokerGrantRequest(g, "abc"))
	if out.Code != 200 || calls != 1 {
		t.Fatalf("redeem = %d %s", out.Code, out.Body.String())
	}
	fence, err := e.store.ReadObjectBucketWriteFence(context.Background(), b, token)
	if err != nil || fence.Requests != 0 || fence.NativeGrants != 0 {
		t.Fatalf("completed broker request failed to drain: %+v %v", fence, err)
	}
	// The same URL was issued before the checkpoint began. Replay cannot send
	// another provider request after the source's writer fence was acquired.
	out = httptest.NewRecorder()
	h.ServeHTTP(out, brokerGrantRequest(g, "abc"))
	if out.Code != 503 || calls != 1 {
		t.Fatalf("earlier URL bypassed fence: %d %s", out.Code, out.Body.String())
	}
}

func TestObjectUploadGrantAPIAndGatewayRejectCapabilitySubstitution(t *testing.T) {
	e, _, _, path := mutationAPIFixture(t)
	g := mintAPIPutGrant(t, e, path)
	h := brokerGrantGateway(t, e, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid grant reached provider IO")
		return nil, nil
	})
	for _, test := range []struct {
		name   string
		change func(*http.Request)
	}{
		{"key", func(r *http.Request) { r.URL.Path = "/assets/another"; r.URL.RawPath = "" }},
		{"bucket", func(r *http.Request) { r.URL.Path = "/other/folder/a b%2F.txt"; r.URL.RawPath = "" }},
		{"method", func(r *http.Request) { r.Method = http.MethodDelete }},
		{"size", func(r *http.Request) { r.ContentLength = 4 }},
		{"metadata", func(r *http.Request) { r.Header.Set("X-Amz-Meta-Custom", "changed") }},
		{"extra-metadata", func(r *http.Request) { r.Header.Set("X-Amz-Meta-Injected", "extra") }},
		{"tags", func(r *http.Request) { r.Header.Set("X-Amz-Tagging", "tag=changed") }},
		{"copy", func(r *http.Request) { r.Header.Set("X-Amz-Copy-Source", "/assets/file") }},
		{"mixed-auth", func(r *http.Request) { r.Header.Set("Authorization", "AWS4-HMAC-SHA256 invalid") }},
		{"extra-query", func(r *http.Request) { q := r.URL.Query(); q.Set("uploadId", "other"); r.URL.RawQuery = q.Encode() }},
		{"duplicate-token", func(r *http.Request) {
			q := r.URL.Query()
			q.Add(s3gateway.UploadGrantQueryParameter, q.Get(s3gateway.UploadGrantQueryParameter))
			r.URL.RawQuery = q.Encode()
		}},
		{"unknown-token", func(r *http.Request) {
			q := r.URL.Query()
			q.Set(s3gateway.UploadGrantQueryParameter, strings.Repeat("A", 43))
			r.URL.RawQuery = q.Encode()
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := brokerGrantRequest(g, "abc")
			test.change(r)
			out := httptest.NewRecorder()
			h.ServeHTTP(out, r)
			if out.Code != 403 {
				t.Fatalf("altered grant request = %d %s", out.Code, out.Body.String())
			}
		})
	}
}

func TestObjectUploadGrantAPIAndGatewayMultipartPart(t *testing.T) {
	e, provider, b, path := mutationAPIFixture(t)
	response := e.do(t, "POST", path+"/multipart-uploads", api.CreateObjectMultipartUploadRequest{Key: "parts/key", SizeBytes: 3}, nil)
	var upload api.ObjectMultipartUpload
	if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &upload) != nil {
		t.Fatalf("create = %d %s", response.Code, response.Body.String())
	}
	callsBefore := len(provider.accessed)
	response = e.do(t, "POST", path+"/multipart-uploads/"+upload.ID+"/parts/1/signed-url", api.ObjectMultipartPartSignRequest{}, nil)
	var grant api.ObjectSignedRequest
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &grant) != nil || len(provider.accessed) != callsBefore {
		t.Fatalf("part signing contacted upstream: %d %s", response.Code, response.Body.String())
	}
	calls := 0
	h := brokerGrantGateway(t, e, func(r *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(r.Body)
		if string(body) != "abc" {
			t.Fatal("part bytes changed")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Etag": []string{"part-etag"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	out := httptest.NewRecorder()
	h.ServeHTTP(out, brokerGrantRequest(grant, "abc"))
	if out.Code != 200 || out.Header().Get("ETag") != "part-etag" || calls != 1 {
		t.Fatalf("part = %d %s", out.Code, out.Body.String())
	}
	if _, err := e.store.AcquireObjectBucketWriteFence(context.Background(), b, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	out = httptest.NewRecorder()
	h.ServeHTTP(out, brokerGrantRequest(grant, "abc"))
	if out.Code != 503 || calls != 1 {
		t.Fatalf("earlier part URL bypassed fence: %d %s", out.Code, out.Body.String())
	}
}
