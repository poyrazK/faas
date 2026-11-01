package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func captureStderr(t *testing.T) *bytes.Buffer {
	t.Helper()
	var stderr bytes.Buffer
	old := *main.OsStderr
	*main.OsStderr = &stderr
	t.Cleanup(func() { *main.OsStderr = old })
	main.ResetServerNotices()
	t.Cleanup(main.ResetServerNotices)
	return &stderr
}

// Requests named only Go-http-client/1.1, so nothing told the server, or
// support reading its logs, which CLI release sent them.
func TestCLIRequestsNameTheRelease(t *testing.T) {
	agents := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agents <- r.UserAgent()
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	want := main.CLIUserAgent()
	if !strings.HasPrefix(want, "gregale/") || !strings.HasSuffix(want, "("+runtime.GOOS+"/"+runtime.GOARCH+")") {
		t.Fatalf("user agent %q, want gregale/<version> (<os>/<arch>)", want)
	}
	for _, client := range []*main.Client{main.NewClient(server.URL, "fp_test_ua"), main.NewClientWithDeployTimeout(server.URL, "fp_test_ua", 0)} {
		client.SetCompletionCache(nil)
		if _, err := client.GetUsage(context.Background(), "2026-10"); err != nil {
			t.Fatal(err)
		}
		if got := <-agents; got != want {
			t.Fatalf("User-Agent = %q, want %q", got, want)
		}
	}
}

// apid marks retired routes with Deprecation/Sunset and explains changes in
// Warning; the CLI dropped all three.
func TestCLIRelaysServerNoticesOnce(t *testing.T) {
	cases := []struct {
		name   string
		header http.Header
		want   string
	}{
		{
			name: "warning",
			// U+009B is the C1 control sequence introducer; HTTP passes it as
			// obs-text, so it must not reach the terminal.
			header: http.Header{"Deprecation": {"true"}, "Warning": {`299 - "operator keys cannot read customer data after this release` + "\u009b2J" + `; sign in with \"gregalectl auth login\""`}},
			want:   `Gregale API: operator keys cannot read customer data after this release 2J; sign in with "gregalectl auth login"`,
		},
		{
			name:   "deprecation",
			header: http.Header{"Deprecation": {"@1789603200"}, "Sunset": {"Thu, 01 Oct 2026 00:00:00 GMT"}},
			want:   "Gregale API: GET /v1/usage is deprecated and stops working after Thu, 01 Oct 2026 00:00:00 GMT; a newer gregale may use its replacement",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stderr := captureStderr(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				for key, values := range tc.header {
					w.Header()[key] = values
				}
				_, _ = w.Write([]byte(`[]`))
			}))
			defer server.Close()
			client := main.NewClient(server.URL, "fp_test_notice").SetCompletionCache(nil)
			for range 3 {
				if _, err := client.GetUsage(context.Background(), "2026-10"); err != nil {
					t.Fatal(err)
				}
			}
			if got := stderr.String(); strings.Count(got, "Gregale API:") != 1 || !strings.Contains(got, tc.want) || strings.Contains(got, "\u009b") {
				t.Fatalf("stderr = %q, want one line containing %q", got, tc.want)
			}
		})
	}
}

// A route the server does not offer answered "Not found: no such API route",
// indistinguishable from a typo or a missing app.
func TestMissingRouteHintNamesVersionSkew(t *testing.T) {
	missingRoute := main.DiagnosticProblem(api.Problem{Status: http.StatusNotFound, Code: api.CodeNotFound, Title: "Not found", Detail: "no such API route"})
	for _, want := range []string{"newer or older than the server", "gregale version", "not enabled for your account"} {
		if !strings.Contains(missingRoute.Hint, want) {
			t.Fatalf("hint %q lacks %q", missingRoute.Hint, want)
		}
	}
	if missingApp := main.DiagnosticProblem(api.Problem{Status: http.StatusNotFound, Code: api.CodeNotFound, Title: "Not found", Detail: "app not found"}); strings.Contains(missingApp.Hint, "newer or older") {
		t.Fatalf("a missing app got the version-skew hint: %q", missingApp.Hint)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(api.Problem{Status: http.StatusNotFound, Code: api.CodeNotFound, Title: "Not found", Detail: "no such API route"})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_route")
	code, _, stderr := runCLI(t, "billing", "usage", "summary")
	if code != 4 || !strings.Contains(stderr, "newer or older than the server") {
		t.Fatalf("exit = %d, stderr = %q; want 4 and the version-skew hint", code, stderr)
	}
}
