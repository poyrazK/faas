package main_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Prod hunt r46: `metric requests --by status` printed an empty Method column.
func TestRequestAnalyticsOmitsEmptyMethodColumn(t *testing.T) {
	for _, tc := range []struct {
		name   string
		groups []api.RequestAnalyticsGroup
		want   bool
	}{
		{"by status", []api.RequestAnalyticsGroup{{Value: "200", Requests: 2}}, false},
		{"by route", []api.RequestAnalyticsGroup{{Value: "/x", Method: "GET", Requests: 2}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			main.RenderRequestAnalytics(&buf, api.RequestAnalyticsResponse{Slug: "demo", Groups: tc.groups})
			if got := strings.Contains(buf.String(), "\tMethod\t"); got != tc.want {
				t.Fatalf("Method column = %v, want %v:\n%s", got, tc.want, buf.String())
			}
		})
	}
}

// Prod hunt r46: `edge cache set GET /c for 60s` was refused as missing
// --method although the usage reads as that sentence.
func TestEdgeCacheSetTakesMethodWord(t *testing.T) {
	got, err := main.RewriteCliTree([]string{"edge", "cache", "set", "get", "/c", "for", "60s", "--app", "demo"})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if want := []string{"cache", "GET", "/c", "for", "60s", "--app", "demo"}; !slices.Equal(got, want) {
		t.Fatalf("rewrite = %q, want %q", got, want)
	}
	if _, err := main.RewriteCliTree([]string{"edge", "cache", "set", "/c", "for", "60s"}); err == nil {
		t.Fatal("edge cache set without a method was accepted")
	}
}

// Prod hunt r46: `alert check status my-api root` printed usage although
// `check list my-api` and `check create my-api` take the app first.
func TestSyntheticCheckStatusTakesAppArgument(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got == "" {
			got = r.URL.Path
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"title":"not found","status":404,"code":"not_found"}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Chdir(t.TempDir())
	_, stdout, stderr := runCLI(t, "alert", "check", "status", "demo", "root")
	if !strings.Contains(got, "/apps/demo/") {
		t.Fatalf("first request %q, want app demo\n%s%s", got, stdout, stderr)
	}
	if code, _, stderr := runCLI(t, "alert", "check", "status", "--app", "other", "demo", "root"); code != 1 {
		t.Fatalf("two different apps = %d %q, want the usage exit 1", code, stderr)
	}
}
