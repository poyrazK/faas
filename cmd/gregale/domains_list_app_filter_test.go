package main_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Production-us rc.294: `gregale domains list --app h14-py` ignored --app
// (the list case parsed no flags) and printed nothing when the account had no
// domains (Hunt 14 H14-50).
func TestDomainsListFiltersByAppAndSaysWhenEmpty(t *testing.T) {
	domains := `[{"domain":"api.example.com","app_id":"app-1","verified":true},{"domain":"web.example.com","app_id":"app-2"}]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/domains":
			_, _ = w.Write([]byte(domains))
		case "/v1/apps/api":
			_, _ = w.Write([]byte(`{"id":"app-1","slug":"api"}`))
		case "/v1/apps":
			_, _ = w.Write([]byte(`[{"id":"app-1","slug":"api"},{"id":"app-2","slug":"web"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	main.ResetJSONOut(t)
	run := func(args ...string) (int, string) {
		var out bytes.Buffer
		previous := *main.OsStdout
		*main.OsStdout = &out
		defer func() { *main.OsStdout = previous }()
		return main.Run(append([]string{"domains", "list"}, args...)), out.String()
	}

	if code, got := run("--app", "api"); code != 0 || !strings.Contains(got, "api.example.com") || strings.Contains(got, "web.example.com") {
		t.Fatalf("--app api: exit %d, want only api's domain:\n%s", code, got)
	}
	if code, got := run(); code != 0 || !strings.Contains(got, "web.example.com") {
		t.Fatalf("no filter: exit %d, want every domain:\n%s", code, got)
	}
	domains = `[]`
	if code, got := run("--app", "api"); code != 0 || !strings.Contains(got, "No custom domains for app api") {
		t.Fatalf("empty: exit %d, want the no-domains line:\n%q", code, got)
	}
	domains = `[{"domain":"api.example.com","app_id":"app-1","verified":true},{"domain":"web.example.com","app_id":"app-2"}]`
	if code, got := run("api"); code != 0 || !strings.Contains(got, "api.example.com") || strings.Contains(got, "web.example.com") {
		t.Fatalf("positional slug: exit %d, want only api's domain:\n%s", code, got)
	}
}
