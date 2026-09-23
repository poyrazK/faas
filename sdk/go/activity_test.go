package faas

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestListOrgActivityEncodesFilters(t *testing.T) {
	t.Parallel()
	var gotRequestURI string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequestURI = r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = client.ListOrgActivity(context.Background(), "acme", "cursor+/=", "deploy.", "github",
		"11111111-1111-4111-8111-111111111111", 25)
	if err != nil {
		t.Fatalf("ListOrgActivity: %v", err)
	}
	want := "/v1/orgs/acme/activity?actor_type=github&app_id=11111111-1111-4111-8111-111111111111&before=" +
		url.QueryEscape("cursor+/=") + "&kind_prefix=deploy.&limit=25"
	if gotRequestURI != want {
		t.Fatalf("RequestURI = %q, want %q", gotRequestURI, want)
	}
}
