package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type headerTransport struct{ base http.RoundTripper }

func (t headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("User-Agent", "wrapped/1")
	return t.base.RoundTrip(req)
}

// WrapTransport reaches every HTTP client the SDK sends with, including the
// upload client NewClientWithDeployTimeout adds.
func TestWrapTransportWrapsEveryClient(t *testing.T) {
	var agent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent = r.UserAgent()
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	wraps := 0
	client := api.NewClientWithDeployTimeout(server.URL, "", 0).SetCompletionCache(nil)
	client.WrapTransport(func(base http.RoundTripper) http.RoundTripper {
		wraps++
		return headerTransport{base: base}
	})
	if wraps != 1 {
		t.Fatalf("wrapped %d clients without an upload client, want 1", wraps)
	}
	wraps = 0
	api.NewClientWithDeployTimeout(server.URL, "", 1).WrapTransport(func(base http.RoundTripper) http.RoundTripper {
		wraps++
		return base
	})
	if wraps != 2 {
		t.Fatalf("wrapped %d clients with an upload client, want 2", wraps)
	}
	if _, err := client.GetUsage(context.Background(), "2026-10"); err != nil {
		t.Fatal(err)
	}
	if agent != "wrapped/1" {
		t.Fatalf("User-Agent = %q, want the wrapper's", agent)
	}
}
