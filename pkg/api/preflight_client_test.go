package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The source is caller-supplied text — a pasted URL — so it has to reach the
// server percent-encoded rather than splicing extra query parameters into the
// request.
func TestGetPreflight_EncodesSourceAndRef(t *testing.T) {
	var gotPath, gotSource, gotRef string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotSource = r.URL.Query().Get("source")
		gotRef = r.URL.Query().Get("ref")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"commit_sha":"abc","verdict":{"level":"green"}}`))
	}))
	defer srv.Close()

	report, err := NewClient(srv.URL, "t").GetPreflight(
		context.Background(), "https://github.com/gregale/api?x=1&y=2", "main")
	if err != nil {
		t.Fatalf("GetPreflight: %v", err)
	}

	if gotPath != "/v1/preflight" {
		t.Errorf("path = %q, want /v1/preflight", gotPath)
	}
	if gotSource != "https://github.com/gregale/api?x=1&y=2" {
		t.Errorf("source = %q, want the input verbatim", gotSource)
	}
	if gotRef != "main" {
		t.Errorf("ref = %q, want main", gotRef)
	}
	if report.Verdict.Level != PreflightGreen {
		t.Errorf("Level = %q, want green", report.Verdict.Level)
	}
}

// An omitted ref means the default branch; sending an empty parameter would
// ask the server to resolve a ref named "".
func TestGetPreflight_OmitsEmptyRef(t *testing.T) {
	var hadRef bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadRef = r.URL.Query()["ref"]
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if _, err := NewClient(srv.URL, "t").GetPreflight(context.Background(), "gregale/api", ""); err != nil {
		t.Fatalf("GetPreflight: %v", err)
	}
	if hadRef {
		t.Error("ref parameter sent for an empty ref, want it omitted")
	}
}
