package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Production-us rc.294: `gregale add postgres --app h14-py --env default`
// printed only "usage: gregale add postgres --app APP [flags]" because no
// database existed yet and --region was missing, a flag the compacted usage
// line does not show (Hunt 14 H14-41).
func TestAddPostgresNamesTheMissingRegionWhenCreating(t *testing.T) {
	var created bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api":
			_, _ = w.Write([]byte(`{"id":"app-1","slug":"api"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case r.Method == http.MethodPost:
			created = true
			http.Error(w, "unexpected create", http.StatusTeapot)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	main.WithCwd(t, t.TempDir())
	stderr, restore := main.CaptureStderr(t)
	code := main.CmdAddPostgres([]string{"--app", "api", "--env", "default"})
	restore()
	got := stderr.String()
	if code != 1 || created || !strings.Contains(got, "--region") || !strings.Contains(got, `"api-postgres"`) || strings.Contains(got, "usage:") {
		t.Fatalf("exit = %d, created = %v; want the missing --region named for api-postgres, not a bare usage: %s", code, created, got)
	}
}
