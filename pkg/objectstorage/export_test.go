package objectstorage

// adr: 712

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"cloud.google.com/go/storage"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
)

// NewGCSConditionalFixtureForTest connects the production SDK to a local wire
// fixture. It is exported only in the test build for external engine conformance
// tests; production constructors continue to require application credentials.
func NewGCSConditionalFixtureForTest(t *testing.T, handler http.Handler) *GCS {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer state-fixture" {
			t.Error("GCS state request lost OAuth identity")
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	httpClient := newGCSHTTPClient(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "state-fixture"}))
	client, err := storage.NewClient(t.Context(), option.WithHTTPClient(httpClient), option.WithEndpoint(server.URL+"/"), storage.WithJSONReads(), storage.WithDisabledClientMetrics())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return testGCS(server.URL, &googleGCSStore{client: client})
}
