package faas

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestGregaleReleaseMiddlewarePropagatesOnlyToManagedServiceCalls(t *testing.T) {
	const release = "release-182"
	transport := NewGregaleReleaseTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get(GregaleReleaseHeader); got != release {
			t.Errorf("downstream release = %q, want %q", got, release)
		}
		if got := req.Header.Get(GregaleRevisionHeader); got != "" {
			t.Errorf("downstream revision = %q, want it stripped", got)
		}
		return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	}))

	handler := GregaleReleaseMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, ok := GregaleReleaseFromContext(r.Context()); !ok || got != release {
			t.Errorf("request release = %q, %v; want %q, true", got, ok, release)
		}
		outbound, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "http://billing.svc.gregale:10080/health", nil)
		if err != nil {
			t.Fatal(err)
		}
		outbound.Header.Set(GregaleRevisionHeader, "api-deployment-id")
		resp, err := transport.RoundTrip(outbound)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/checkout", nil)
	req.Header.Set(GregaleReleaseHeader, release)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestGregaleReleaseTransportDoesNotModifyExternalRequests(t *testing.T) {
	transport := NewGregaleReleaseTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get(GregaleReleaseHeader); got != "caller-choice" {
			t.Errorf("external release = %q, want original value", got)
		}
		if got := req.Header.Get(GregaleRevisionHeader); got != "caller-revision" {
			t.Errorf("external revision = %q, want original value", got)
		}
		return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	}))

	req, err := http.NewRequestWithContext(WithGregaleRelease(context.Background(), "request-release"), http.MethodGet, "https://example.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(GregaleReleaseHeader, "caller-choice")
	req.Header.Set(GregaleRevisionHeader, "caller-revision")
	if _, err := transport.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
}

func TestGregaleReleaseMiddlewareIgnoresAmbiguousHeader(t *testing.T) {
	handler := GregaleReleaseMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if got, ok := GregaleReleaseFromContext(r.Context()); ok {
			t.Errorf("ambiguous release propagated as %q", got)
		}
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Add(GregaleReleaseHeader, "release-a")
	req.Header.Add(GregaleReleaseHeader, "release-b")
	handler.ServeHTTP(httptest.NewRecorder(), req)
}

func TestGregaleReleaseTransportPreservesExplicitRelease(t *testing.T) {
	transport := NewGregaleReleaseTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get(GregaleReleaseHeader); got != "explicit-release" {
			t.Errorf("release = %q, want explicit value", got)
		}
		return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	}))
	req, err := http.NewRequestWithContext(WithGregaleRelease(context.Background(), "context-release"), http.MethodGet, "http://billing.svc.gregale/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(GregaleReleaseHeader, "explicit-release")
	if _, err := transport.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
}
