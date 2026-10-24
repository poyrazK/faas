package main_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Log lines print an 8-character instance prefix; --archive --instance with
// that prefix reported a false "archive gap" because archives are keyed by
// the full id (Hunt 12 H12-8).
func TestResolveArchiveInstanceExpandsPrefixes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/web/instances" || r.URL.Query().Get("history") != "true" {
			http.NotFound(w, r)
			return
		}
		main.WriteJSONTest(w, []api.InstanceResponse{
			{ID: "e3998828-9cff-49d1-801d-27ba0c6fc7ca"},
			{ID: "69ec8c91-1932-490b-8a52-826da957ba07"},
			{ID: "69ec0000-0000-4000-8000-000000000000"},
		})
	}))
	t.Cleanup(srv.Close)
	client := api.NewClient(srv.URL, "fp_test_x")
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"unique prefix", "e3998828", "e3998828-9cff-49d1-801d-27ba0c6fc7ca", false},
		{"full id unchanged", "e3998828-9cff-49d1-801d-27ba0c6fc7ca", "e3998828-9cff-49d1-801d-27ba0c6fc7ca", false},
		{"no match unchanged", "deadbeef", "deadbeef", false},
		{"non-uuid id unchanged", "instance-123", "instance-123", false},
		{"ambiguous prefix", "69ec", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := main.ResolveArchiveInstance(context.Background(), client, "web", tt.in)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("ResolveArchiveInstance(%q) = %q, %v; want %q, err=%t", tt.in, got, err, tt.want, tt.wantErr)
			}
		})
	}
}
