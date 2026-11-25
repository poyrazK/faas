package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type catalogPreviewRoundTripper func(*http.Request) (*http.Response, error)

func (f catalogPreviewRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPreviewDataAPICatalogSendsNoSQL(t *testing.T) {
	client := NewClient("https://api.example", "test-token")
	client.http = &http.Client{Transport: catalogPreviewRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/postgres/databases/db-id/data-api-catalog-preview" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Body != nil {
			body, err := io.ReadAll(r.Body)
			if err != nil || len(body) != 0 {
				t.Fatalf("unexpected request body: %q %v", body, err)
			}
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"database_id":"db-id","columns":[],"rows":[],"row_count":0}`)), Request: r}, nil
	})}
	response, err := client.PreviewDataAPICatalog(context.Background(), "db-id")
	if err != nil || response.DatabaseID != "db-id" {
		t.Fatalf("response: %+v, %v", response, err)
	}
}
